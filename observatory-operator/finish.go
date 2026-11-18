package main

import (
	"context"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// failed waits while a step of the reservation is Failed. A failed
// activation step waits only until spec.end, and then stage() starts
// deactivation to stop what activation started. A failed deactivation
// step waits with the finalizer in place, because the devices may not
// be safe to power off.
//
// A person retries with the annotation observatory.liken.sh/retry.
// failed then removes the annotation, sets each Failed step to
// Pending, records the session again from the current specs, and
// returns.
func (r *runner) failed(ctx context.Context) {
	for ctx.Err() == nil {
		wake := r.o.structure.wait()
		if !r.refresh() {
			return
		}
		if _, retry := r.res.Metadata.Annotations[annotationRetry]; retry {
			if err := r.o.send(ctx, nil, "removing the retry annotation of the Reservation "+r.name, func() error {
				r.refresh()
				return r.o.clearRetry(r.res)
			}); err != nil {
				continue
			}
			for i := range r.status.Steps {
				if s := &r.status.Steps[i]; s.State == observatory.StepFailed {
					s.State, s.StartTime, s.StopTime, s.Summary = observatory.StepPending, nil, nil, ""
				}
			}
			r.status.Phase = phaseOf(r.status.Step)
			notes, err := r.recordAgain(ctx)
			if err != nil {
				return
			}
			r.save(ctx)
			r.o.record(r.res, "Retry", strings.Join(append([]string{"Retrying " + string(r.status.Step)}, notes...), "; "))
			r.awaitStore(ctx, func(res *observatory.Reservation) bool {
				_, still := res.Metadata.Annotations[annotationRetry]
				return !still
			})
			return
		}
		if r.stage() != stageFailed {
			return
		}
		r.observe(ctx)
		r.waitForChange(ctx, wake)
	}
}

// released frees the telescope for other reservations and removes the
// finalizer, so a reservation that a person deleted goes away. A
// reservation that reached spec.end stays, Released, until a person
// deletes it.
func (r *runner) released(ctx context.Context) {
	r.o.claims.release(r.res)
	r.o.structure.notify()
	_ = r.o.send(ctx, nil, "removing the finalizer of the Reservation "+r.name, func() error {
		if !r.refresh() || !hasFinalizer(r.res) {
			return nil
		}
		return r.o.setFinalizer(r.res, false)
	})
}

// awaitStore waits until the operator's cached copy of the reservation
// satisfies check, or the reservation is gone. Call it after a write,
// so the next read of the cache does not return the copy from before
// the write.
func (r *runner) awaitStore(ctx context.Context, check func(*observatory.Reservation) bool) {
	_ = r.o.waitFor(ctx, nil, func(t *tree) (bool, string, error) {
		res, ok := t.reservations[r.name]
		return !ok || check(res), "", nil
	})
}
