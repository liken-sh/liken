package main

// One INDI device can serve several resources. INDI puts every
// interface of one driver on one device: a QHY camera's filter wheel is
// on the camera's device, and a Flip-Flat's dust cap and flat panel are
// on one device. So a device resource either runs a driver, or names in
// spec.device the resource whose driver serves it. The resource that
// runs the driver owns the device: it gets the pod, the claim, the
// power, and the finalizer, and its pod's life covers every resource it
// serves.
//
// A served resource acts through its owner's handle: the same server,
// the same INDI device. Its lifecycle follows its owner's:
//
//   - It gets the finalizer when its owner does (startDevices), so a
//     delete of either waits for the deactivation.
//   - Its activation runs after its owner connects, and its
//     deactivation runs before its owner's (deviceTiers), so a
//     Flip-Flat's cap closes before the device stops.
//   - When a person deletes it or its owner during a session, it runs
//     its deactivation through its owner's driver before that driver
//     stops (leaveServed). A spec edit that would move it takes effect
//     when the session ends, as for any device (tree.server).
//
// The tree refuses a served resource that cannot share its owner's
// device, and the refusal is its status's fault: an owner that does not
// exist or runs no driver, an owner on another server, because one INDI
// device runs on one server, and a second served resource of one kind
// on one owner. A served resource whose owner connects and does not
// define its interface reports Error, as any device does (detect).

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// runsDriver reports whether a device runs a driver of its own, and so
// owns its INDI device.
func (d *device) runsDriver() bool { return d.object.Spec.Device == nil }

// named answers the device resource that a served device's spec.device
// names, while it exists and runs a driver of its own. The answer
// ignores the servers, so a served device that left its server still
// finds the driver that served it there.
func (t *tree) named(d *device) (*device, bool) {
	ref := d.object.Spec.Device
	if ref == nil {
		return nil, false
	}
	kind, ok := observatory.KindNamed(ref.Kind)
	if !ok {
		return nil, false
	}
	owner, ok := t.device(kind, ref.Name)
	if !ok || !owner.runsDriver() {
		return nil, false
	}
	return owner, true
}

// runnerOf answers the device whose driver serves a device on a server:
// the device itself when it runs a driver, and the resource that its
// spec.device names otherwise. The INDI device, the pod, and the
// connection of a device are its runner's.
func (t *tree) runnerOf(d *device) (*device, bool) {
	if d.runsDriver() {
		return d, true
	}
	return t.named(d)
}

// podOf answers the name of the pod that runs a device's driver, and
// the pod while it exists. A served device has the pod only while it
// shares its owner's device (shares): a served device on the shelf
// that let go of its owner has no pod, though its spec.device still
// names the owner.
func (t *tree) podOf(d *device) (string, *pod, bool) {
	runner, ok := t.runnerOf(d)
	if !ok {
		return "", nil, false
	}
	name, err := objectName(runner.kind, runner.name())
	if err != nil {
		return "", nil, false
	}
	p, ok := t.pods[name]
	if !d.runsDriver() && !t.shares(d) {
		return name, nil, false
	}
	return name, p, ok
}

// shares reports whether a served device shares the INDI device of the
// resource that its spec.device names: the tree serves it on its
// server, or it left that server and still holds the finalizer, while
// its deactivation runs there (leaveServed).
func (t *tree) shares(d *device) bool {
	return t.verdict(d).owner != nil || holdsFinalizer(d.object.Metadata)
}

// serving is the tree's verdict on one served device that is placed on
// a server: the owner that serves it, or why none does.
type serving struct {
	owner   *device
	refusal string
}

// servedVerdicts holds the verdict of each served device that is
// placed on a server. The status writer asks for every device on each
// pass, so a tree computes them once.
type servedVerdicts struct {
	once     sync.Once
	verdicts map[*device]serving
}

// verdict answers the owner of a served device that is placed on a
// server, or the refusal that keeps it from one. A device that runs a
// driver, and a served device on no server, have neither.
func (t *tree) verdict(d *device) serving {
	t.served.once.Do(func() { t.served.verdicts = t.judgeServed() })
	return t.served.verdicts[d]
}

// judgeServed answers the verdict of each served device on a server.
// Of two served devices of one kind on one owner, one is served: the
// one that holds the finalizer, so a device that waits for the next
// session never displaces one that its owner serves already, then the
// older resource, then the one whose name sorts first. A served device
// that waits for the next session of its server (tree.waits) is judged
// there too, so its status names a refusal now, and it has no owner
// until that session starts it.
func (t *tree) judgeServed() map[*device]serving {
	verdicts := map[*device]serving{}
	type slot struct {
		owner *device
		kind  observatory.Kind
	}
	winners := map[slot]*device{}
	waiting := map[*device]bool{}
	for _, d := range t.devices {
		if d.runsDriver() {
			continue
		}
		ref, placed := t.server(d)
		if !placed && t.waits(d) {
			ref, placed = t.declared(d)
			waiting[d] = true
		}
		if !placed {
			continue
		}
		v := t.judge(d, ref)
		verdicts[d] = v
		if v.refusal != "" {
			continue
		}
		at := slot{v.owner, d.kind}
		if other, taken := winners[at]; !taken || servedBefore(d, other) {
			winners[at] = d
		}
	}
	for d, v := range verdicts {
		if v.refusal != "" {
			continue
		}
		switch winner := winners[slot{v.owner, d.kind}]; {
		case winner != d:
			verdicts[d] = serving{refusal: fmt.Sprintf("%s %s serves %s %s already: one INDI device serves one resource of each kind",
				v.owner.kind.Name, v.owner.name(), winner.kind.Name, winner.name())}
		case waiting[d]:
			verdicts[d] = serving{}
		}
	}
	return verdicts
}

// judge answers the verdict of one served device on a server, before
// the choice between two served devices of one kind.
func (t *tree) judge(d *device, ref serverRef) serving {
	names := d.object.Spec.Device.Kind + " " + d.object.Spec.Device.Name
	kind, _ := observatory.KindNamed(d.object.Spec.Device.Kind)
	target, exists := t.device(kind, d.object.Spec.Device.Name)
	switch {
	case !exists:
		return serving{refusal: "spec.device names " + names + ", which does not exist"}
	case !target.runsDriver():
		return serving{refusal: "spec.device names " + names + ", which runs no driver of its own: name the resource that runs the driver"}
	case target.kind == d.kind:
		return serving{refusal: "spec.device names " + names + ", which is a " + d.kind.Name + " itself: one INDI device serves one resource of each kind"}
	}
	at, placed := t.server(target)
	switch {
	case !placed:
		return serving{refusal: "spec.device names " + names + ", which runs on no server"}
	case at != ref:
		return serving{refusal: fmt.Sprintf("spec.device names %s, which runs on server %s, and %s %s is on server %s: one INDI device runs on one server",
			names, at, d.kind.Name, d.name(), ref)}
	}
	if holder, taken := t.driverTakenBy(target); taken {
		return serving{refusal: "spec.device names " + names + ", which does not run: " + sharedDriver(holder, ref)}
	}
	return serving{owner: target}
}

// servedBefore reports whether a served device keeps its owner's
// device rather than another served device of its kind.
func servedBefore(a, b *device) bool {
	if ha, hb := holdsFinalizer(a.object.Metadata), holdsFinalizer(b.object.Metadata); ha != hb {
		return ha
	}
	ca, cb := createdAt(a), createdAt(b)
	if !ca.Equal(cb) {
		return ca.Before(cb)
	}
	return a.key() < b.key()
}

// ownerOf answers the device that a device's server runs a driver for:
// the device itself when it runs one, and its owner when the tree
// serves it.
func (t *tree) ownerOf(d *device) (*device, bool) {
	if d.runsDriver() {
		return d, true
	}
	v := t.verdict(d)
	return v.owner, v.owner != nil
}

// servedBy answers the devices that an owner serves, in the order of
// t.devices.
func (t *tree) servedBy(owner *device) []*device {
	var out []*device
	for _, d := range t.devices {
		if !d.runsDriver() && t.verdict(d).owner == owner {
			out = append(out, d)
		}
	}
	return out
}

// owners answers the handles of the devices that run a driver, one for
// each INDI device. A connection or a disconnection belongs to the
// INDI device, so it goes through its owner's handle alone.
func owners(handles []handle) []handle {
	return slices.DeleteFunc(slices.Clone(handles), func(h handle) bool { return !h.d.runsDriver() })
}

// driverDevices answers the devices that run a driver of their own.
func driverDevices(devices []*device) []*device {
	return slices.DeleteFunc(slices.Clone(devices), func(d *device) bool { return !d.runsDriver() })
}

// deviceTiers splits groups of devices, in the order that activation
// runs them, into tiers: each group's owners, then the served devices
// of that group. A served device whose owner is in a later group waits
// for that group, so its activation runs after its owner's, and in the
// reversed tiers of deactivation its deactivation runs before its
// owner's.
func deviceTiers(t *tree, groups [][]*device) [][]*device {
	group := map[*device]int{}
	for i, devices := range groups {
		for _, d := range devices {
			group[d] = i
		}
	}
	tiers := make([][]*device, 2*len(groups))
	for i, devices := range groups {
		for _, d := range devices {
			owner, served := t.named(d)
			if !served {
				tiers[2*i] = append(tiers[2*i], d)
				continue
			}
			at := i
			if j, listed := group[owner]; listed && j > at {
				at = j
			}
			tiers[2*at+1] = append(tiers[2*at+1], d)
		}
	}
	return tiers
}

// holdServed gives each device that an owner serves the finalizer,
// with its owner's, and waits until the stores show it. The finalizer
// keeps a deleted served device until its deactivation ran through its
// owner's driver (leaveServed), so the served device holds it before
// its owner's pod starts that driver. The owner
// can come from another reading of the tree, so t finds it by kind and
// name. A write that the API server refuses is sent again (send), and
// the step's deadline bounds the tries.
func (o *operator) holdServed(ctx context.Context, report func(string), t *tree, owner *device) error {
	var served []*device
	if own, ok := t.device(owner.kind, owner.name()); ok {
		served = t.servedBy(own)
	}
	for _, d := range served {
		if err := o.holdDevice(ctx, report, d); err != nil {
			return err
		}
	}
	return o.waitFor(ctx, report, func(now *tree) (bool, string, error) {
		for _, d := range served {
			if held, ok := now.device(d.kind, d.name()); ok && !deleting(held.object.Metadata) && !holdsFinalizer(held.object.Metadata) {
				return false, "waiting for the finalizer of " + d.kind.Name + " " + d.name(), nil
			}
		}
		return true, "", nil
	})
}

// served reports whether a device holds the finalizer for a server: it
// holds the finalizer, and the pod of the driver that its spec.device
// names runs on the server.
func served(t *tree, d *device, ref serverRef) bool {
	if d.runsDriver() || !holdsFinalizer(d.object.Metadata) {
		return false
	}
	_, p, running := t.podOf(d)
	return running && p.Metadata.Labels[labelServer] == ref.String()
}

// leaveServed runs the deactivation of each served device that left a
// running server and still holds the finalizer: a person deleted it or
// its owner, so the tree no longer serves it there.
// The run acts through the owner's driver, which still runs, so it
// comes before setDrivers stops that driver. The finalizer goes once
// the run ends, as a device's goes once its pod is gone.
func (r *runner) leaveServed(ctx context.Context, t *tree, ref serverRef) error {
	on := map[*device]bool{}
	for _, d := range t.devicesOn(ref) {
		on[d] = true
	}
	var left []*device
	for _, d := range t.devices {
		if served(t, d, ref) && !on[d] {
			left = append(left, d)
		}
	}
	if len(left) == 0 {
		return nil
	}
	var resources []resource
	for _, d := range left {
		resources = append(resources, leaver(t, d, ref))
	}
	r.o.deactivateLeft(ctx, ref, resources)
	var problems []error
	for _, d := range left {
		problems = append(problems, r.o.setFinalizerOf(d.kind, d.name(), false))
		r.o.logf("Reservation %s: %s left %s", r.name, d.key(), ref)
	}
	return joinErrors(problems)
}
