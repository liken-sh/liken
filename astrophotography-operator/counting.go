package main

// Counting a frame, and the frame that does not come (plan 01, step 7.8).
// A frame the driver saved counts as pending: it waits in the outbox
// claim that status.outboxes names, and the promotion of plan 03 moves
// it to the archive. That status is the whole hand-off to promotion:
// promotion's loop wakes on the target's change, finds frames pending
// in an outbox, and starts its Job. The runner launches nothing itself.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// failedExposureWait is the wait after an exposure that gave no frame,
// before the next one. A driver that fails once often fails again at
// once, and the wait keeps a broken camera from cycling all night.
const failedExposureWait = time.Minute

// count clears status.exposure and, when counted, adds the frame to the
// pending count of its sequence and of its outbox claim. A target whose
// status.exposure names another frame was counted already, by a runner
// before a restart, and count leaves it alone. imaging is the message of
// an Imaging that turns True with the reason Exposing, or empty to
// leave the condition alone.
func (r *runner) count(t *astrophotography.Target, e *astrophotography.Exposure, counted bool, claim, imaging string) error {
	_, err := r.o.writeTarget(t, func(fresh *astrophotography.Target, change *statusChange) bool {
		if fresh.Status.Exposure == nil || fresh.Status.Exposure.FrameID != e.FrameID {
			return false
		}
		fresh.Status.Exposure = nil
		if counted {
			addPending(fresh, int(e.Sequence), claim)
		}
		if imaging != "" && fresh.Status.Reservation == r.name {
			change.setImaging(fresh, true, astrophotography.ReasonExposing, imaging)
		}
		return true
	})
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

// addPending counts one frame of the sequence at index as pending in
// the outbox claim. A sequence that an edit removed since the exposure
// started gets no count; the promotion report counts the frame by its
// header.
func addPending(t *astrophotography.Target, index int, claim string) {
	alignSequences(t)
	if index < len(t.Status.Sequences) {
		t.Status.Sequences[index].Pending++
	}
	for i := range t.Status.Outboxes {
		if t.Status.Outboxes[i].ClaimName == claim {
			t.Status.Outboxes[i].Pending++
			return
		}
	}
	t.Status.Outboxes = append(t.Status.Outboxes, astrophotography.OutboxFrames{ClaimName: claim, Pending: 1})
}

// exposureFailed records an exposure that gave no frame: it clears
// status.exposure, sets Imaging False with ExposureFailed, and waits
// before the next frame. The target stays claimed, because the failure
// is the camera's, and the next target would meet the same camera.
func (w *work) exposureFailed(ctx context.Context, cause error) error {
	t := w.target
	w.r.logf("target %s: %v", targetName(t), cause)
	w.r.o.metrics.runner.exposureFailures.WithLabelValues(t.Metadata.Namespace, t.Metadata.Name).Inc()
	_, err := w.r.o.writeTarget(t, func(fresh *astrophotography.Target, change *statusChange) bool {
		if fresh.Status.Reservation != w.r.name {
			return false
		}
		fresh.Status.Exposure = nil
		change.setImaging(fresh, false, astrophotography.ReasonExposureFailed,
			fmt.Sprintf("Reservation %s: the last exposure gave no frame: %v. The next one starts in %s.", w.r.name, cause, failedExposureWait))
		return true
	})
	if err != nil {
		return err
	}
	sleep(ctx, failedExposureWait, nil)
	return nil
}
