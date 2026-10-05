package main

// The two ends of a reservation's record: a failed step, which waits
// for a person, and the release, which gives the telescope back.

import (
	"context"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// failed waits while a step is Failed. A failed activation step waits
// for the reservation to end, and deactivation then runs from Abort,
// which stops what activation started. A failed deactivation step
// keeps the finalizer, because the devices may not be safe to power
// off, and the Ready and SafeToPowerOff conditions name the step. In
// both cases the annotation observatory.liken.sh/retry runs the failed
// step again: the operator removes the annotation and sets the step
// Pending.
func (r *runner) failed(ctx context.Context) {
	for ctx.Err() == nil {
		wake := r.o.changed.wait()
		if !r.refresh() {
			return
		}
		if _, retry := r.res.Metadata.Annotations[annotationRetry]; retry {
			if err := r.o.clearRetry(r.res); err != nil {
				r.retryLater(ctx, "removing the retry annotation", err)
				continue
			}
			for i := range r.status.Steps {
				if s := &r.status.Steps[i]; s.State == observatory.StepFailed {
					s.State, s.StartTime, s.FinishTime, s.Message = observatory.StepPending, nil, nil, ""
				}
			}
			r.status.Phase = phaseOf(r.status.Step)
			r.save(ctx)
			r.o.record(r.res, eventNormal, "Retry", "running "+string(r.status.Step)+" again")
			r.awaitStore(ctx, func(res *observatory.Reservation) bool {
				_, still := res.Metadata.Annotations[annotationRetry]
				return !still
			})
			return
		}
		if r.stage() != stageFailed {
			return
		}
		r.keepWaiting(ctx, wake)
	}
}

// released gives the telescope back and removes the finalizer, so a
// deleted reservation goes away. A reservation that reached spec.end
// stays, Released, until a person deletes it.
func (r *runner) released(ctx context.Context) {
	r.o.claims.release(r.res)
	r.o.changed.notify()
	for ctx.Err() == nil && r.refresh() && hasFinalizer(r.res) {
		err := r.o.setFinalizer(r.res, false)
		if err == nil {
			return
		}
		r.retryLater(ctx, "removing the finalizer", err)
	}
}

// awaitStore waits until the store's copy of the reservation meets
// check, after a write of the operator's own, so the next read does not
// act on the copy from before the write.
func (r *runner) awaitStore(ctx context.Context, check func(*observatory.Reservation) bool) {
	_ = r.o.waitFor(ctx, nil, func(t *tree) (bool, string, error) {
		res, ok := t.reservations[r.name]
		return !ok || check(res), "", nil
	})
}
