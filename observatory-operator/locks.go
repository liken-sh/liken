package main

// The park locks stop a dome and a mount from moving in a way that can
// damage the telescope. A dome must not park while a mount is unparked,
// because the slit could close on the telescope, and a mount must not
// unpark while the dome is parked. The locks apply whenever the
// observatory has a dome, and no field turns them off.
//
// INDI enforces both locks in its drivers. A mount whose DOME_POLICY
// is DOME_LOCKS refuses to unpark while the dome it snoops reports
// DOME_PARK as parked. A dome whose MOUNT_POLICY is MOUNT_LOCKS refuses
// to park while the mount it snoops reports TELESCOPE_PARK as unparked
// (libs/indibase/inditelescope.cpp and indidome.cpp). A driver snoops
// only devices on its own INDI server, but the dome runs on the
// observatory's server and each mount runs on its telescope's.
//
// So the operator copies each park report to the other servers. It
// sends the domes' park state to each mount's server, and the mounts'
// park state to the dome's server, as set*Vector messages that
// indiserver passes to the driver that snoops the device
// (indi.Client.Relay). The driver then refuses a move on its own,
// without waiting for the operator. The operator does not chain the
// servers to each other instead, because servers chained in both
// directions send getProperties around the loop without end, and a
// server whose chained server stops exits too.
//
// A dome snoops one mount, but an observatory can have several
// telescopes. So the operator relays one report for all the mounts,
// under the name that the dome snoops (ACTIVE_TELESCOPE). It reports
// unparked while any mount is not parked. A mount snoops one dome, so
// the operator relays one report for all the domes under the name that
// the mount snoops (ACTIVE_DOME). It reports parked while any dome is
// not unparked. A device that is moving counts as neither parked nor
// unparked, so it keeps the lock in force. A device that reports no
// park state also keeps it in force, because it may be in any position.
// The exception is a mount whose reservation's Deactivation step ended
// before its driver stopped, because that step parks the mount.
//
// A driver keeps the last report it received. While the operator is
// down, each lock keeps the state that the operator relayed last, and
// later moves are not relayed. A driver that restarts loses the report,
// and the operator relays it again when the driver defines its
// properties again.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The reasons of the Warning Events that the operator posts on a dome
// or a mount when its driver refuses a park or an unpark because a lock
// is in force.
const (
	reasonMountUnparkRefused = "MountUnparkRefused"
	reasonDomeParkRefused    = "DomeParkRefused"
)

// The reasons of the LocksRelayed condition.
const (
	reasonLocksRelayed = "Relayed"
	reasonLocksWaiting = "Waiting"
	reasonLocksIdle    = "Idle"
)

// epochs counts the INDI events that can change which devices and
// properties a server defines: every event except an update or a
// message, and the opening of each connection. A driver that restarts
// defines its properties again and forgets the reports it received, so
// a new epoch on a server makes the operator relay to it again. All
// servers share the counter, so a new server never repeats an epoch
// that an earlier server had.
var epochs atomic.Uint64

// lockRelay is one park report that the operator sends to one server.
type lockRelay struct {
	server *indiServer
	report indi.Property
	// key changes whenever the driver may not have the current report:
	// with the server's epoch, with the reported state, and, for a
	// relay to a mount, with whether the mount is connected, because a
	// mount reads a snooped report only while it is connected. The
	// operator sends the report again when the key differs from the key
	// it sent last.
	key string
}

func (l lockRelay) id() string {
	return l.server.name + "/" + l.report.Device + "/" + l.report.Name
}

// lockWatch is one park property on one device. When the driver sets it
// to Alert while locked is true, the driver refused a move.
type lockWatch struct {
	h        handle
	property string
	locked   bool
	reason   string
	message  string
}

func (l lockWatch) id() string { return l.h.server.name + "/" + l.h.name + "/" + l.property }

// sitePlan is what the park locks of one observatory need: the reports
// to relay, the properties to watch for refusals, and the condition.
type sitePlan struct {
	relays  []lockRelay
	watches []lockWatch
	// condition is nil when the observatory has no dome.
	condition *observatory.Condition
}

// lockMemo records, from the last relayLocks call, the key of each
// relay that was sent and the state of each watched property.
type lockMemo struct {
	mu     sync.Mutex
	sent   map[string]string
	states map[string]indi.State
}

// relayLocks sends each relay whose key differs from the key sent last,
// and posts a Warning for each watched property that changed to Alert
// while its lock was in force. keepLocks calls it after each change. An
// action that parks or unparks calls it first, so the driver has the
// current park states when the move arrives: the move goes out on the
// same connection after the relay, and indiserver passes both to the
// driver in that order.
//
// It returns true when a relay failed. The next call sends that relay
// again, because sent does not record it.
func (o *operator) relayLocks(t *tree) (failed bool) {
	o.locks.mu.Lock()
	defer o.locks.mu.Unlock()
	sent, states := map[string]string{}, map[string]indi.State{}
	for _, name := range sortedNames(t.observatories) {
		plan := o.planLocks(t, t.observatories[name])
		for _, r := range plan.relays {
			id := r.id()
			if o.locks.sent[id] == r.key {
				sent[id] = r.key
				continue
			}
			if err := r.server.client.Relay(r.report); err != nil {
				o.logf("relaying %s.%s to %s: %v", r.report.Device, r.report.Name, r.server.name, err)
				failed = true
				continue
			}
			sent[id] = r.key
		}
		for _, w := range plan.watches {
			p, ok := w.h.client().Property(w.h.name, w.property)
			if !ok {
				continue
			}
			id := w.id()
			states[id] = p.State
			before, seen := o.locks.states[id]
			if seen && before != indi.Alert && p.State == indi.Alert && w.locked {
				o.recorder.Warning(reference(w.h.d.kind, w.h.d.object.Metadata), w.reason, w.message)
			}
		}
	}
	o.locks.sent, o.locks.states = sent, states
	return failed
}

// refusedMoves maps the reason of each refusal Warning to the kind and
// state of the action that the lock refuses.
var refusedMoves = map[string]string{
	reasonDomeParkRefused:    observatory.DomeKind.Name + "/" + string(observatory.StateParked),
	reasonMountUnparkRefused: observatory.MountKind.Name + "/" + string(observatory.StateUnparked),
}

// explainRefusal replaces the error of a park or an unpark that a driver
// set to Alert while its lock was in force with the text of the
// refusal's Warning, which names the devices that keep the lock in force. The
// actions of a trigger do not wait for the mounts to park, so for a
// dome's park in a trigger the error also says how to order it after
// the mounts. Any other error is returned unchanged.
func (o *operator) explainRefusal(c procCall, h handle, a action, err error) error {
	var alert *indi.AlertError
	if !errors.As(err, &alert) {
		return err
	}
	t := o.snapshot()
	// A device that has left the tree has no observatory there, so use
	// the observatory that the call ran for.
	name := c.res.observatory
	if name == "" {
		name = t.observatoryOf(h.d.kind, h.d.name())
	}
	site, ok := t.observatories[name]
	if !ok {
		return err
	}
	refused := lockWatch{h: h, property: alert.Property.Name}.id()
	for _, w := range o.planLocks(t, site).watches {
		if w.id() != refused || !w.locked || refusedMoves[w.reason] != h.d.kind.Name+"/"+a.State {
			continue
		}
		if c.condition != "" && w.reason == reasonDomeParkRefused {
			return errors.New(w.message + ". In a trigger, set after: [{kind: Mount}] on the dome's park so that it runs after the mounts park")
		}
		return errors.New(w.message)
	}
	return err
}

// keepLocks relays the park reports after each change, and writes each
// mount's DOME_POLICY to match its observatory's domes (domepolicy.go).
func (o *operator) keepLocks(ctx context.Context) {
	policies := &mountPolicies{tried: map[string]string{}}
	defer policies.group.Wait()
	for {
		wake := o.changed.wait()
		if o.stores.ready() {
			began := time.Now()
			failed := o.relayLocks(o.snapshot())
			o.keepDomePolicies(ctx, policies)
			o.series.m.observePass(observatory.ObservatoryKind.Name, time.Since(began), failed)
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		}
	}
}

// domesOf returns the domes on an observatory's server. The park locks
// apply while it returns at least one.
func domesOf(t *tree, site *observatory.Observatory) []*device {
	return lockDevices(t, serverRef{observatory.ObservatoryKind, site.Metadata.Name}, observatory.DomeKind)
}

// lockDevices returns the devices of one kind on one server, sorted by
// key: those that tree.devicesOf places on the server, and those whose
// driver pod runs on it. A dome or a mount that a person deleted, alone
// or with its reservation, can still move until its Deactivation step
// parks it and its driver stops, so a running driver pod keeps it in
// the lock.
func lockDevices(t *tree, s serverRef, kind observatory.Kind) []*device {
	out := t.devicesOf(s, kind)
	for _, d := range t.devices {
		if d.kind != kind || slices.Contains(out, d) {
			continue
		}
		if on, running := t.runningOn(d); running && on == s {
			out = append(out, d)
		}
	}
	return sortedDevices(out)
}

func (o *operator) planLocks(t *tree, site *observatory.Observatory) sitePlan {
	domeDevices := domesOf(t, site)
	if len(domeDevices) == 0 {
		return sitePlan{}
	}
	siteRef := serverRef{observatory.ObservatoryKind, site.Metadata.Name}
	domeHandles := o.handlesOf(t, siteRef, domeDevices)
	// Only the mounts of reserved telescopes count. A telescope that no
	// reservation holds has no server, and the Deactivation step of its
	// last reservation already parked its mount.
	var mountDevices []*device
	var mountHandles []handle
	deactivated := map[string]bool{}
	for _, name := range sortedNames(t.telescopes) {
		holder, held := o.claims.holderOf(name)
		if t.telescopes[name].Spec.Observatory != site.Metadata.Name || !held {
			continue
		}
		ref := serverRef{observatory.TelescopeKind, name}
		devices := lockDevices(t, ref, observatory.MountKind)
		mountDevices = append(mountDevices, devices...)
		mountHandles = append(mountHandles, o.handlesOf(t, ref, devices)...)
		for _, d := range devices {
			deactivated[d.key()] = parkedByDeactivation(t.reservations[holder])
		}
	}
	var plan sitePlan
	if len(domeHandles) == 0 && len(mountHandles) == 0 {
		plan.condition = locksCondition(observatory.ConditionFalse, reasonLocksIdle, "No dome or mount runs")
		return plan
	}
	domes := parkStates(domeHandles, domeDevices, "DOME_PARK", "UNPARK", func(*device) bool { return true })
	mounts := parkStates(mountHandles, mountDevices, "TELESCOPE_PARK", "PARK", func(d *device) bool { return !deactivated[d.key()] })
	done, waiting := plan.domeLocksMount(site, domes, mountHandles)
	d, w := plan.mountLocksDome(site, domeHandles, mounts)
	done, waiting = append(done, d...), append(waiting, w...)
	if len(waiting) > 0 {
		plan.condition = locksCondition(observatory.ConditionFalse, reasonLocksWaiting, "Waiting for "+strings.Join(waiting, ", and for "))
		return plan
	}
	message := sentence(strings.Join(done, "; ")) + ". While observatory-operator is down, each driver keeps the last state that the operator relayed"
	plan.condition = locksCondition(observatory.ConditionTrue, reasonLocksRelayed, message)
	return plan
}

// parkedByDeactivation reports whether the Deactivation step of a
// reservation ended. That step parks the telescope's mount, so a mount
// that reports no park state afterward is parked.
func parkedByDeactivation(r *observatory.Reservation) bool {
	if r == nil {
		return false
	}
	s := findStep(r.Status.Steps, observatory.StepDeactivation)
	return s != nil && (s.State == observatory.StepDone || s.State == observatory.StepSkipped)
}

// domeLocksMount adds to plan the relay of the domes' park state to
// each mount, and the watch for a refused unpark. It returns what it
// relays and what it waits for, as text for the LocksRelayed condition.
func (plan *sitePlan) domeLocksMount(site *observatory.Observatory, domes []parkState, mounts []handle) (done, waiting []string) {
	locking, silent := lockingAndSilent(domes, "DOME_PARK")
	waiting = silent
	parked := len(locking) > 0
	state := "every dome is unparked: " + stateNames(domes)
	if parked {
		state = "a dome is parked or moving: " + stateNames(locking)
	}
	var to []string
	for _, h := range mounts {
		plan.watches = append(plan.watches, lockWatch{h: h, property: "TELESCOPE_PARK", locked: parked, reason: reasonMountUnparkRefused,
			message: fmt.Sprintf("%s refused to unpark, because %s parked or moving. In Observatory %s, a mount does not unpark until every dome unparks",
				h, subject(locking), site.Metadata.Name)})
		dome, ok := snooped(h, "ACTIVE_DOME")
		if !ok {
			waiting = append(waiting, h.String()+" to name a dome in ACTIVE_DEVICES")
			continue
		}
		// The snooped device is on the same server, so the driver
		// receives its reports with no relay.
		if _, local := h.client().Property(dome, "DRIVER_INFO"); local {
			continue
		}
		plan.relays = append(plan.relays, lockRelay{
			server: h.server,
			report: parkReport(dome, "DOME_PARK", parked),
			key:    fmt.Sprintf("%d %t %t", h.server.epoch.Load(), h.connected(), parked),
		})
		to = append(to, h.String())
	}
	if len(to) > 0 {
		done = append(done, fmt.Sprintf("relayed to %s that %s", strings.Join(to, ", "), state))
	}
	return done, waiting
}

// mountLocksDome adds to plan the relay of the mounts' park state to
// each dome, and the watch for a refused park. It returns the same
// lists as domeLocksMount.
func (plan *sitePlan) mountLocksDome(site *observatory.Observatory, domes []handle, mounts []parkState) (done, waiting []string) {
	locking, silent := lockingAndSilent(mounts, "TELESCOPE_PARK")
	waiting = silent
	unparked := len(locking) > 0
	state := "no mount runs"
	switch {
	case unparked:
		state = "a mount is unparked or moving: " + stateNames(locking)
	case len(mounts) > 0:
		state = "every mount is parked: " + stateNames(mounts)
	}
	for _, h := range domes {
		plan.watches = append(plan.watches, lockWatch{h: h, property: "DOME_PARK", locked: unparked, reason: reasonDomeParkRefused,
			message: fmt.Sprintf("%s refused to park, because %s unparked or moving. In Observatory %s, a dome does not park until every mount parks",
				h, subject(locking), site.Metadata.Name)})
		mount, ok := snooped(h, "ACTIVE_TELESCOPE")
		if !ok {
			waiting = append(waiting, h.String()+" to name a mount in ACTIVE_DEVICES")
			continue
		}
		if _, local := h.client().Property(mount, "DRIVER_INFO"); local {
			continue
		}
		plan.relays = append(plan.relays, lockRelay{
			server: h.server,
			report: parkReport(mount, "TELESCOPE_PARK", !unparked),
			key:    fmt.Sprintf("%d %t", h.server.epoch.Load(), unparked),
		})
		done = append(done, fmt.Sprintf("relayed to %s that %s", h, state))
	}
	return done, waiting
}

type parkState struct {
	d *device
	// locks is true when the device keeps the lock in force: a dome that
	// is not unparked, or a mount that is not parked.
	locks bool
	// silent is true when the device reports no park state, so locks
	// comes from parkStates' silentLocks.
	silent bool
}

func (s parkState) String() string {
	if s.silent {
		return s.d.kind.Name + " " + s.d.name() + " (no report)"
	}
	return s.d.kind.Name + " " + s.d.name()
}

// parkStates reads the park property of each device. A device keeps the
// lock in force unless the property's member unlocking is On and the
// property is not Busy. For a device with no park property, silentLocks
// decides.
func parkStates(handles []handle, devices []*device, property, unlocking string, silentLocks func(*device) bool) []parkState {
	var out []parkState
	for _, d := range devices {
		state := parkState{d: d, silent: true, locks: silentLocks(d)}
		for _, h := range handles {
			if h.d.key() != d.key() {
				continue
			}
			if p, ok := h.client().Property(h.name, property); ok {
				state.silent = false
				state.locks = p.State == indi.Busy || !isOn(p, unlocking)
			}
		}
		out = append(out, state)
	}
	return out
}

// lockingAndSilent returns the devices that keep the lock in force, and
// the condition's text for each of them that reports no park state.
func lockingAndSilent(states []parkState, property string) (locking []parkState, waiting []string) {
	for _, s := range states {
		if !s.locks {
			continue
		}
		locking = append(locking, s)
		if s.silent {
			waiting = append(waiting, s.d.kind.Name+" "+s.d.name()+" to report "+property)
		}
	}
	return locking, waiting
}

func stateNames(states []parkState) string {
	var out []string
	for _, s := range states {
		out = append(out, s.String())
	}
	return strings.Join(out, ", ")
}

// subject returns the devices as the subject of a sentence, with its
// verb: "Dome lab is", or "Mount east and Mount west are".
func subject(states []parkState) string {
	var names []string
	for _, s := range states {
		names = append(names, s.String())
	}
	switch len(names) {
	case 0:
		return "no device is"
	case 1:
		return names[0] + " is"
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1] + " are"
}

func isOn(p indi.Property, member string) bool {
	m, ok := p.Member(member)
	return ok && m.Switch
}

// snooped returns the device that the driver of h snoops for one role
// of its ACTIVE_DEVICES property, such as ACTIVE_DOME, and false when
// the role is missing or empty.
func snooped(h handle, role string) (string, bool) {
	p, ok := h.client().Property(h.name, "ACTIVE_DEVICES")
	if !ok {
		return "", false
	}
	m, ok := p.Member(role)
	return m.Text, ok && m.Text != ""
}

// parkReport builds the park property that a driver sends after its
// device parks or unparks. The state is Ok because a driver ignores a
// snooped park property in any other state.
func parkReport(device, property string, parked bool) indi.Property {
	return indi.Property{
		Device: device, Name: property, Type: indi.SwitchType, State: indi.Ok,
		Members: []indi.Member{{Name: "PARK", Switch: parked}, {Name: "UNPARK", Switch: !parked}},
	}
}

func locksCondition(status observatory.ConditionStatus, reason, message string) *observatory.Condition {
	c := condition(observatory.ConditionLocksRelayed, status, reason, message)
	return &c
}

func sortedDevices(devices []*device) []*device {
	sort.Slice(devices, func(i, j int) bool { return devices[i].key() < devices[j].key() })
	return devices
}
