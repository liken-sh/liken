package main

// A device that joins a running server during a Ready reservation
// (serverdrivers.go) missed the transition of Active that ran every
// other activation. A dust cap added to a train during a session would
// stay closed, and a dome created again would stay parked. So the
// trigger controller runs the activation of each such device as an
// answer to the same transition: the run's since is the time its
// Telescope or Observatory turned Active.
//
// The controller acts only while a reservation that governs the device
// is Ready. Before that, the reservation's Activation step runs the
// device's activation in the tree's order, and a run here would race
// it. No step waits on the run, and its after waits for nothing: the
// step already ended every other run of the transition. A failure
// posts ProcedureFailed, as a trigger's does. The device's triggers
// start when the run ends (activePeriod).
//
// A device that leaves while its parent is Active runs no
// deactivation. The person who removes it takes it out of the
// operator's care, and its driver stops as it leaves.

import (
	"context"
	"fmt"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// governor answers the key of the Telescope or the Observatory whose
// Active condition starts and ends a resource's activity, and false
// for a device with no parent.
func governor(r resource) (string, bool) {
	switch {
	case r.kind == observatory.ObservatoryKind:
		return observatoryKey(r.name()), true
	case r.kind == observatory.TelescopeKind:
		return telescopeKey(r.name()), true
	case r.telescope != "":
		return telescopeKey(r.telescope), true
	case r.observatory != "":
		return observatoryKey(r.observatory), true
	}
	return "", false
}

// joinActivation starts the activation of a device that joined an
// Active parent, and stops it when the parent's activity ends.
func (o *operator) joinActivation(ctx context.Context, t *tree, r resource, k *control) {
	if r.device == nil || len(r.procedures.Activation) == 0 {
		return
	}
	id := r.record() + "/" + observatory.TriggerActivation
	f := k.flights[id]
	key, _ := governor(r)
	state := o.activity.get(key)
	if !state.active {
		if f != nil {
			f.cancel(fmt.Errorf("the activity of %s ended", r))
		}
		return
	}
	if f != nil {
		return
	}
	run, ok := o.runs.get(r.record(), observatory.TriggerActivation)
	answered := ok && answers(run, state.since, time.Time{})
	if answered && run.State != observatory.StepRunning && run.State != observatory.StepPending {
		return
	}
	// A run that an operator restart interrupted resumes whatever the
	// device reads now, and a new run needs a connected device.
	if !o.governedByReady(t, r) || (!answered && r.device.object.Status.Phase != observatory.DeviceConnected) {
		return
	}
	k.flights[id] = o.fly(ctx, procCall{res: r, trigger: observatory.TriggerActivation,
		actions: r.procedures.Activation, since: state.since}, &k.group)
}

// governedByReady reports whether a reservation that governs a device
// is Ready: the holder of the device's telescope, or of any telescope
// in the device's observatory.
func (o *operator) governedByReady(t *tree, r resource) bool {
	for _, name := range sortedNames(t.telescopes) {
		if r.telescope != "" && name != r.telescope {
			continue
		}
		if r.telescope == "" && t.telescopes[name].Spec.Observatory != r.observatory {
			continue
		}
		holder, ok := o.claims.holderOf(name)
		if res := t.reservations[holder]; ok && res != nil && res.Status.Phase == observatory.ReservationReady {
			return true
		}
	}
	return false
}
