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
// posts ProcedureFailed, as a trigger's does, and the retry annotation
// on the device runs it again, with its Done actions skipped. The
// device's triggers start when the run ends (activePeriod).
//
// While a governing reservation is Ready, a Failed activation of the
// current transition is a run that this controller started. A failure
// in the reservation's Activation step fails the reservation, which
// is then not Ready, and that run retries through the reservation.
//
// A device that leaves while its parent is Active runs its
// deactivation before its driver stops (leaves.go), and drops the
// record of its activation, so a device that joins again activates
// again. Each run's record answers the transition, and the times of
// two runs in the same second cannot order them, so the record of the
// other trigger goes instead.

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
// Active parent, and stops it when the parent's activity ends. retry
// runs a Failed activation of the current transition again.
func (o *operator) joinActivation(ctx context.Context, t *tree, r resource, k *control, retry bool) {
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
	rerun := answered && retry && run.State == observatory.StepFailed
	if answered && !rerun && run.State != observatory.StepRunning && run.State != observatory.StepPending {
		return
	}
	// A run that an operator restart interrupted resumes whatever the
	// device reads now. A new run, or one run again, needs a connected
	// device.
	resumed := answered && !rerun
	if !o.governedByReady(t, r) || (!resumed && r.device.object.Status.Phase != observatory.DeviceConnected) {
		return
	}
	if !answered {
		// A device that left ran its deactivation of this transition,
		// and dropped its activation's record (leaves.go). It joins
		// again, so its next leave must run its deactivation again.
		o.runs.drop(r.record(), observatory.TriggerDeactivation)
	}
	k.flights[id] = o.fly(ctx, procCall{res: r, trigger: observatory.TriggerActivation,
		actions: r.procedures.Activation, since: state.since, rerun: rerun}, &k.group)
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
