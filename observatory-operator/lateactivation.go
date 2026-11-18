package main

// A device can miss the activation that runs when its Telescope or
// Observatory turns Active. The Activation step skips a device whose
// driver has not yet defined the properties its interface needs, and a
// spec edit can add an activation to a device that is already running.
// In both cases the trigger controller runs the activation itself, with
// the time the parent turned Active as its since.
//
// It does this only while a reservation that governs the device is
// Ready, because before then the reservation's Activation step runs
// activations in order and a run here would race with it. The device's
// triggers start when this run ends (activePeriod). A failure posts
// ProcedureFailed, and the retry annotation on the device runs the
// activation again with its Done actions skipped. While the
// reservation is Ready, a Failed activation of the current Active
// period can only have come from this controller, because a failed
// Activation step takes the reservation out of Ready and the retry then
// goes through the reservation.

import (
	"context"
	"fmt"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// activeParent returns the key of the Telescope or Observatory whose Active
// condition starts and ends the resource's activity. It returns false
// for a device with no parent.
func activeParent(r resource) (string, bool) {
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

// activeSince returns when the resource's Telescope or Observatory
// turned Active, or the zero time while it is not Active.
func (o *operator) activeSince(r resource) time.Time {
	key, ok := activeParent(r)
	if !ok {
		return time.Time{}
	}
	if state := o.activity.get(key); state.active {
		return state.since
	}
	return time.Time{}
}

// lateActivation starts the activation of a device that its Active
// parent did not run, and cancels the run when the parent stops being
// Active. When retry is set, it runs a Failed activation of the
// current Active period again.
func (o *operator) lateActivation(ctx context.Context, t *tree, r resource, k *control, retry bool) {
	if r.device == nil || len(r.procedures.Activation) == 0 {
		return
	}
	id := r.record() + "/" + observatory.TriggerActivation
	f := k.inProgress[id]
	key, _ := activeParent(r)
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
	recorded := ok && isRunFor(run, state.since, time.Time{})
	rerun := recorded && retry && run.State == observatory.StepFailed
	if recorded && !rerun && run.State != observatory.StepRunning && run.State != observatory.StepPending {
		return
	}
	// A Running or Pending record is a run that an operator restart
	// interrupted, and it resumes whatever the device's phase is. A new
	// run or a rerun needs a connected device.
	resumed := recorded && !rerun
	if !o.governedByReady(t, r) || (!resumed && r.device.object.Status.Phase != observatory.DeviceConnected) {
		return
	}
	k.inProgress[id] = o.startRun(ctx, procCall{res: r, trigger: observatory.TriggerActivation,
		actions: r.procedures.Activation, since: state.since, rerun: rerun}, &k.group)
}

// governedByReady reports whether a Ready reservation governs the
// device: the reservation that holds the device's telescope, or for a
// device of an observatory, the reservation that holds any telescope
// in that observatory.
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
