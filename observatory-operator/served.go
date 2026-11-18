package main

// INDI puts every interface of one driver on one INDI device. A QHY
// camera's filter wheel is on the camera's device, and a Flip-Flat's
// dust cap and flat panel are on one device. So a device resource
// either runs a driver, or sets spec.device to the resource whose
// driver serves it. The resource that runs the driver is the owner. It
// gets the pod, the claim, the power, and the finalizer, and the
// resources that it serves use its pod.
//
// A served resource uses its owner's handle, so its changes go to the
// same server and INDI device, and its lifecycle follows its owner's:
//
//   - It gets the finalizer when its owner does (startDevices), so a
//     delete of either waits for the deactivation.
//   - Its activation runs after its owner connects, and its
//     deactivation runs before its owner's (deviceTiers), so a
//     Flip-Flat's cap closes before the device disconnects.
//   - When a person deletes it or its owner during a session, its
//     deactivation runs through the owner's driver before that driver
//     stops (leaveServed). A spec edit that would move it takes effect
//     when the session ends, as for any device (tree.server).
//
// The tree refuses a served resource that cannot share its owner's
// INDI device, such as one whose owner runs on another server, and
// writes the reason in the resource's status. When the owner connects
// and its INDI device does not define the served resource's interface,
// the resource reports Error (detect).

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// runsDriver reports whether a device runs its own driver, which makes
// it the owner of its INDI device.
func (d *device) runsDriver() bool { return d.object.Spec.Device == nil }

// named returns the resource that a served device's spec.device refers
// to, if that resource exists and runs its own driver. It does not
// compare servers, so a served device that left its server can still
// find the owner that served it there.
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

// runnerOf returns the device whose driver serves d: d itself when it
// runs a driver, and the resource named by its spec.device otherwise.
// A device uses its runner's INDI device, pod, and connection.
func (t *tree) runnerOf(d *device) (*device, bool) {
	if d.runsDriver() {
		return d, true
	}
	return t.named(d)
}

// podOf returns the name of the pod that runs a device's driver, and
// the pod if it exists. For a served device it returns no pod unless
// shares reports true, even though spec.device still names the owner.
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

// shares reports whether a served device uses its owner's INDI device:
// while the tree serves it on a server, and after it leaves that server
// while it keeps the finalizer, because leaveServed runs its
// deactivation through the owner's driver.
func (t *tree) shares(d *device) bool {
	return t.ownershipOf(d).owner != nil || hasOperatorFinalizer(d.object.Metadata)
}

// ownership is the result of checking one served device on a server:
// the owner that serves it, or the reason why none does.
type ownership struct {
	owner   *device
	refusal string
}

// servedOwnerships caches the ownership of each served device on a
// server. The status writer reads every device on each pass, so the
// tree computes them once.
type servedOwnerships struct {
	once     sync.Once
	byDevice map[*device]ownership
}

// ownershipOf returns the ownership of a served device on a server. A
// device that runs a driver, and a served device on no server, get the
// zero value.
func (t *tree) ownershipOf(d *device) ownership {
	t.served.once.Do(func() { t.served.byDevice = t.ownerships() })
	return t.served.byDevice[d]
}

// ownerships checks each served device on a server. When two served
// devices of one kind name one owner, servedBefore picks the one that
// is served, and the other is refused.
//
// A served device that waits for its server's next session
// (tree.waits) is checked against the server that its spec declares,
// so a refusal shows in its status at once. When it would be served,
// it gets no owner and no refusal until the next session starts it.
func (t *tree) ownerships() map[*device]ownership {
	owners := map[*device]ownership{}
	type ownerKind struct {
		owner *device
		kind  observatory.Kind
	}
	winners := map[ownerKind]*device{}
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
		v := t.checkOwner(d, ref)
		owners[d] = v
		if v.refusal != "" {
			continue
		}
		at := ownerKind{v.owner, d.kind}
		if other, taken := winners[at]; !taken || servedBefore(d, other) {
			winners[at] = d
		}
	}
	for d, v := range owners {
		if v.refusal != "" {
			continue
		}
		switch winner := winners[ownerKind{v.owner, d.kind}]; {
		case winner != d:
			owners[d] = ownership{refusal: fmt.Sprintf("%s %s serves %s %s already: one INDI device serves one resource of each kind",
				v.owner.kind.Name, v.owner.name(), winner.kind.Name, winner.name())}
		case waiting[d]:
			owners[d] = ownership{}
		}
	}
	return owners
}

// checkOwner checks one served device on the server ref against the
// resource that its spec.device names. ownerships then picks one served
// device of each kind for each owner.
func (t *tree) checkOwner(d *device, ref serverRef) ownership {
	names := d.object.Spec.Device.Kind + " " + d.object.Spec.Device.Name
	kind, _ := observatory.KindNamed(d.object.Spec.Device.Kind)
	target, exists := t.device(kind, d.object.Spec.Device.Name)
	switch {
	case !exists:
		return ownership{refusal: "spec.device names " + names + ", which does not exist"}
	case !target.runsDriver():
		return ownership{refusal: "spec.device names " + names + ", which runs no driver of its own: name the resource that runs the driver"}
	case target.kind == d.kind:
		return ownership{refusal: "spec.device names " + names + ", which is a " + d.kind.Name + " itself: one INDI device serves one resource of each kind"}
	}
	at, placed := t.server(target)
	switch {
	case !placed:
		return ownership{refusal: "spec.device names " + names + ", which runs on no server"}
	case at != ref:
		return ownership{refusal: fmt.Sprintf("spec.device names %s, which runs on server %s, and %s %s is on server %s: one INDI device runs on one server",
			names, at, d.kind.Name, d.name(), ref)}
	}
	if holder, taken := t.driverHolder(target); taken {
		return ownership{refusal: "spec.device names " + names + ", which does not run: " + sharedDriver(holder, ref)}
	}
	return ownership{owner: target}
}

// servedBefore reports whether a is served in preference to b, when
// both are served devices of one kind on one owner. The device that
// holds the finalizer wins, so a device that waits for the next session
// never displaces one that its owner already serves. Then the older
// resource wins, then the one whose key sorts first.
func servedBefore(a, b *device) bool {
	if ha, hb := hasOperatorFinalizer(a.object.Metadata), hasOperatorFinalizer(b.object.Metadata); ha != hb {
		return ha
	}
	ca, cb := createdAt(a), createdAt(b)
	if !ca.Equal(cb) {
		return ca.Before(cb)
	}
	return a.key() < b.key()
}

// ownerOf returns the device whose driver the server runs for d: d
// itself when it runs a driver, and its owner when the tree serves it.
func (t *tree) ownerOf(d *device) (*device, bool) {
	if d.runsDriver() {
		return d, true
	}
	v := t.ownershipOf(d)
	return v.owner, v.owner != nil
}

// servedBy returns the devices that an owner serves, in the order of
// t.devices.
func (t *tree) servedBy(owner *device) []*device {
	var out []*device
	for _, d := range t.devices {
		if !d.runsDriver() && t.ownershipOf(d).owner == owner {
			out = append(out, d)
		}
	}
	return out
}

// owners returns the handles of the devices that run a driver, one for
// each INDI device. Connecting and disconnecting apply to the INDI
// device, so they go through the owner's handle only.
func owners(handles []handle) []handle {
	return slices.DeleteFunc(slices.Clone(handles), func(h handle) bool { return !h.d.runsDriver() })
}

func driverDevices(devices []*device) []*device {
	return slices.DeleteFunc(slices.Clone(devices), func(d *device) bool { return !d.runsDriver() })
}

// deviceTiers splits groups of devices, listed in activation order,
// into tiers. Each group becomes two tiers: its devices that are not
// served, then its served devices. A served device whose owner is in a
// later group goes in the served tier of that later group, so its
// activation runs after its owner's. Deactivation runs the tiers in
// reverse, so it runs before its owner's.
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

// addServedFinalizers adds the finalizer to each device that owner serves, and
// waits until the stores show it. The finalizer keeps a deleted served
// device until leaveServed has run its deactivation through the owner's
// driver, so it must be in place before the owner's pod starts that
// driver. owner can come from an earlier read of the tree, so
// addServedFinalizers looks it up in t by kind and name. send retries a write
// that the API server refuses until ctx ends.
func (o *operator) addServedFinalizers(ctx context.Context, report func(string), t *tree, owner *device) error {
	var served []*device
	if own, ok := t.device(owner.kind, owner.name()); ok {
		served = t.servedBy(own)
	}
	for _, d := range served {
		if err := o.addDeviceFinalizer(ctx, report, d); err != nil {
			return err
		}
	}
	return o.waitFor(ctx, report, func(now *tree) (bool, string, error) {
		for _, d := range served {
			if held, ok := now.device(d.kind, d.name()); ok && !deleting(held.object.Metadata) && !hasOperatorFinalizer(held.object.Metadata) {
				return false, "waiting for the finalizer of " + d.kind.Name + " " + d.name(), nil
			}
		}
		return true, "", nil
	})
}

// servedByPodOn reports whether d is a served device that holds the
// finalizer and whose owner's pod runs on the server ref.
func servedByPodOn(t *tree, d *device, ref serverRef) bool {
	if d.runsDriver() || !hasOperatorFinalizer(d.object.Metadata) {
		return false
	}
	_, p, running := t.podOf(d)
	return running && p.Metadata.Labels[labelServer] == ref.String()
}

// leaveServed deactivates each served device whose owner's pod runs on
// ref, that still holds the finalizer, and that the tree no longer
// places on ref, such as after a person deletes it or its owner. The
// deactivation goes through the owner's driver, so it must run before
// setDrivers stops that driver. Then leaveServed removes the finalizer.
func (r *runner) leaveServed(ctx context.Context, t *tree, ref serverRef) error {
	on := map[*device]bool{}
	for _, d := range t.devicesOn(ref) {
		on[d] = true
	}
	var left []*device
	for _, d := range t.devices {
		if servedByPodOn(t, d, ref) && !on[d] {
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
		r.o.logf("Reservation %s: %s left server %s", r.name, d.key(), ref)
	}
	return joinErrors(problems)
}
