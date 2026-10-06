package main

// The lock policies between an observatory's domes and its mounts.
//
// INDI enforces both policies in the drivers. A mount whose DOME_POLICY
// is DOME_LOCKS refuses to unpark while the dome it snoops reports
// DOME_PARK parked. A dome whose MOUNT_POLICY is MOUNT_LOCKS refuses to
// park while the mount it snoops reports TELESCOPE_PARK unparked
// (libs/indibase/inditelescope.cpp and indidome.cpp). A driver snoops
// through its own server, but the dome runs on the observatory's
// server and each mount on its telescope's.
//
// So the operator relays each report across the servers. It sends the
// domes' park state to each mount's server, and the mounts' park state
// to the domes' server, as set*Vector elements that indiserver hands to
// the driver that snoops them (indi.Client.Relay). The driver then
// refuses a move itself, at once. Plan 12 records why the operator does
// not chain the servers instead: a chain in both directions makes the
// servers send getProperties around the loop without end, and a server
// whose chained server stops exits too.
//
// A dome snoops one mount, but an observatory can have several
// telescopes. So the operator relays one report for all the mounts,
// under the name that the dome snoops (ACTIVE_TELESCOPE): unparked
// while any mount is not parked. A mount snoops one dome, and the
// operator relays one report for all the domes under the name that the
// mount snoops (ACTIVE_DOME): parked while any dome is not unparked. A
// device that moves counts as neither parked nor unparked, so it holds
// the lock. A device that reports no park state holds it too, because
// it may be anywhere, except a mount that the Secure step parked
// before its driver stopped.
//
// A driver keeps the last report it received. While the operator is
// down, each lock holds the state that the operator relayed last, and
// a later move is not relayed. A driver that restarts forgets the
// report, and the operator relays it again when the driver defines its
// properties again.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The Events of a refusal: a driver answered a move with Alert while
// its lock held. Each is a Warning on the device, because the move the
// person or the program asked for did not happen.
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

// epochs numbers the structural changes of every INDI connection: a new
// connection, and a property that a driver defines or deletes. A driver
// that restarts defines its properties again, so a new epoch on its
// server means that a report may need relaying again. One counter
// serves every server, so a new server never repeats an epoch of an
// old one.
var epochs atomic.Uint64

// lockRelay is one report for one server.
type lockRelay struct {
	server *indiServer
	report indi.Property
	// key holds what the driver's copy of the report depends on. The
	// operator sends the report again when the key changes.
	key string
}

func (l lockRelay) id() string {
	return l.server.name + "/" + l.report.Device + "/" + l.report.Name
}

// lockWatch is one park property whose Alert is a refusal while the
// lock holds.
type lockWatch struct {
	h        handle
	property string
	locked   bool
	reason   string
	message  string
}

func (l lockWatch) id() string { return l.h.server.name + "/" + l.h.name + "/" + l.property }

// sitePlan is what the lock policies of one observatory need now.
type sitePlan struct {
	relays  []lockRelay
	watches []lockWatch
	// condition is nil when the observatory sets no lock policy.
	condition *observatory.Condition
}

// lockMemo records what the operator relayed and the state of each
// watched property, between passes.
type lockMemo struct {
	mu     sync.Mutex
	sent   map[string]string
	states map[string]indi.State
}

// relayLocks relays each report whose key changed since the operator
// sent it last, and posts a Warning for each refusal. keepLocks calls
// it after each change, and a step calls it before a move that a lock
// decides, so the drivers hold the current state before the move
// arrives. The server's connection
// carries the report and then the move, and indiserver hands both to
// the driver in that order.
func (o *operator) relayLocks(t *tree) {
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
}

// keepLocks relays the locks after each change, until ctx ends.
func (o *operator) keepLocks(ctx context.Context) {
	for {
		wake := o.changed.wait()
		if o.stores.ready() {
			o.relayLocks(o.snapshot())
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		}
	}
}

// planLocks answers the relays, the watches, and the condition of one
// observatory's lock policies.
func (o *operator) planLocks(t *tree, site *observatory.Observatory) sitePlan {
	policies := site.Spec.Policies
	if policies == nil || (!policies.DomeLocksMount && !policies.MountLocksDome) {
		return sitePlan{}
	}
	siteRef := serverRef{observatory.ObservatoryKind, site.Metadata.Name}
	domeDevices := sortedDevices(t.devicesOf(siteRef, observatory.DomeKind))
	domeHandles := o.handlesOf(t, siteRef, domeDevices)
	// Only a held telescope's mount counts. A telescope that no
	// reservation holds has no server, and the Secure step of its last
	// reservation parked its mount.
	var mountDevices []*device
	var mountHandles []handle
	secured := map[string]bool{}
	for _, name := range sortedNames(t.telescopes) {
		holder, held := o.claims.holderOf(name)
		if t.telescopes[name].Spec.Observatory != site.Metadata.Name || !held {
			continue
		}
		ref := serverRef{observatory.TelescopeKind, name}
		devices := sortedDevices(t.devicesOf(ref, observatory.MountKind))
		mountDevices = append(mountDevices, devices...)
		mountHandles = append(mountHandles, o.handlesOf(t, ref, devices)...)
		for _, d := range devices {
			secured[d.key()] = parkedBySecure(t.reservations[holder])
		}
	}
	var plan sitePlan
	if len(domeHandles) == 0 && len(mountHandles) == 0 {
		plan.condition = locksCondition(observatory.ConditionFalse, reasonLocksIdle, "No dome or mount runs")
		return plan
	}
	// A dome that does not report holds the lock, because it may be
	// parked. A mount that does not report holds it too, unless the
	// Secure step parked it before its driver stopped.
	domes := parkStates(domeHandles, domeDevices, "DOME_PARK", "UNPARK", func(*device) bool { return true })
	mounts := parkStates(mountHandles, mountDevices, "TELESCOPE_PARK", "PARK", func(d *device) bool { return !secured[d.key()] })
	var done, waiting []string
	if policies.DomeLocksMount {
		d, w := plan.domeLocksMount(site, domes, mountHandles)
		done, waiting = append(done, d...), append(waiting, w...)
	}
	if policies.MountLocksDome {
		d, w := plan.mountLocksDome(site, domeHandles, mounts)
		done, waiting = append(done, d...), append(waiting, w...)
	}
	if len(waiting) > 0 {
		plan.condition = locksCondition(observatory.ConditionFalse, reasonLocksWaiting, "Waiting for "+strings.Join(waiting, ", and for "))
		return plan
	}
	message := sentence(strings.Join(done, "; ")) + ". While observatory-operator is down, each driver keeps the last state that the operator relayed"
	plan.condition = locksCondition(observatory.ConditionTrue, reasonLocksRelayed, message)
	return plan
}

// parkedBySecure reports whether a reservation's Secure step ended, so
// its telescope's mount is parked.
func parkedBySecure(r *observatory.Reservation) bool {
	if r == nil {
		return false
	}
	s := findStep(r.Status.Steps, observatory.StepSecure)
	return s != nil && (s.State == observatory.StepDone || s.State == observatory.StepSkipped)
}

// domeLocksMount relays the domes' park state to each mount. It answers
// what it relays and what it waits for, for the condition.
func (plan *sitePlan) domeLocksMount(site *observatory.Observatory, domes []parkState, mounts []handle) (done, waiting []string) {
	if len(domes) == 0 {
		return []string{"found no dome to relay"}, nil
	}
	holding, silent := holdingAndSilent(domes, "DOME_PARK")
	waiting = silent
	parked := len(holding) > 0
	state := "every dome is unparked: " + stateNames(domes)
	if parked {
		state = "a dome is parked or moving: " + stateNames(holding)
	}
	var to []string
	for _, h := range mounts {
		plan.watches = append(plan.watches, lockWatch{h: h, property: "TELESCOPE_PARK", locked: parked, reason: reasonMountUnparkRefused,
			message: fmt.Sprintf("%s refused to unpark: %s is parked or moving, and Observatory %s sets domeLocksMount, so the mount stays parked until the dome unparks",
				h, stateNames(holding), site.Metadata.Name)})
		dome, ok := snooped(h, "ACTIVE_DOME")
		if !ok {
			waiting = append(waiting, h.String()+" to name a dome in ACTIVE_DEVICES")
			continue
		}
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

// mountLocksDome relays the mounts' park state to each dome.
func (plan *sitePlan) mountLocksDome(site *observatory.Observatory, domes []handle, mounts []parkState) (done, waiting []string) {
	holding, silent := holdingAndSilent(mounts, "TELESCOPE_PARK")
	waiting = silent
	unparked := len(holding) > 0
	state := "no mount runs"
	switch {
	case unparked:
		state = "a mount is unparked or moving: " + stateNames(holding)
	case len(mounts) > 0:
		state = "every mount is parked: " + stateNames(mounts)
	}
	for _, h := range domes {
		plan.watches = append(plan.watches, lockWatch{h: h, property: "DOME_PARK", locked: unparked, reason: reasonDomeParkRefused,
			message: fmt.Sprintf("%s refused to park: %s is unparked or moving, and Observatory %s sets mountLocksDome, so the dome stays unparked until every mount parks",
				h, stateNames(holding), site.Metadata.Name)})
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

// parkState is one dome or one mount, as a lock reads it.
type parkState struct {
	d *device
	// holds is true when the device holds the lock: a dome that is not
	// unparked, or a mount that is not parked.
	holds bool
	// silent is true when the device reports no park state, and holds
	// is then what the operator assumes.
	silent bool
}

func (s parkState) String() string {
	if s.silent {
		return s.d.kind.Name + " " + s.d.name() + " (no report)"
	}
	return s.d.kind.Name + " " + s.d.name()
}

// parkStates reads the park property of each device. A device holds
// the lock unless it reports free On and does not move. A device whose
// driver reports no park property holds it when silentHolds says so.
func parkStates(handles []handle, devices []*device, property, free string, silentHolds func(*device) bool) []parkState {
	var out []parkState
	for _, d := range devices {
		state := parkState{d: d, silent: true, holds: silentHolds(d)}
		for _, h := range handles {
			if h.d.key() != d.key() {
				continue
			}
			if p, ok := h.client().Property(h.name, property); ok {
				state.silent = false
				state.holds = p.State == indi.Busy || !isOn(p, free)
			}
		}
		out = append(out, state)
	}
	return out
}

// holdingAndSilent answers the devices that hold the lock, and what the
// condition waits for: a report from each silent device that holds it.
func holdingAndSilent(states []parkState, property string) (holding []parkState, waiting []string) {
	for _, s := range states {
		if !s.holds {
			continue
		}
		holding = append(holding, s)
		if s.silent {
			waiting = append(waiting, s.d.kind.Name+" "+s.d.name()+" to report "+property)
		}
	}
	return holding, waiting
}

func stateNames(states []parkState) string {
	var out []string
	for _, s := range states {
		out = append(out, s.String())
	}
	return strings.Join(out, ", ")
}

func isOn(p indi.Property, member string) bool {
	m, ok := p.Member(member)
	return ok && m.Switch
}

// snooped answers the device that a driver snoops for one role of its
// ACTIVE_DEVICES, such as ACTIVE_DOME.
func snooped(h handle, role string) (string, bool) {
	p, ok := h.client().Property(h.name, "ACTIVE_DEVICES")
	if !ok {
		return "", false
	}
	m, ok := p.Member(role)
	return m.Text, ok && m.Text != ""
}

// parkReport is a park property as a driver reports it once the device
// has parked or unparked. A driver reads a snooped park state only
// when its state is Ok.
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
