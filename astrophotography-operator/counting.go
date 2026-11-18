package main

// The runner counts each frame that the camera driver saved as pending
// in the Target's status. A frame is pending until a promotion Job moves
// it from the outbox claim to the archive. The runner only writes the
// count, and the promotion loop in promotion.go starts the Job.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// failedExposureWait is how long the runner waits after an exposure
// that produced no frame. A driver that fails once often fails again
// immediately, so without the wait a broken camera would retry in a
// tight loop all night.
const failedExposureWait = time.Minute

// count clears status.exposure and, when counted is true, adds the
// frame to the pending count of its sequence and of its outbox claim.
// When status.exposure is not this frame, a runner before a restart
// already counted it, and count writes nothing. When imaging is not
// empty and status.reservation is still this runner's, count also sets
// the Imaging condition to True with the reason Exposing and imaging as
// the message. count returns whether it wrote the Target, and returns
// false with no error when the Target is gone.
func (r *runner) count(t *astrophotography.Target, e *astrophotography.Exposure, counted bool, claim, imaging string) (bool, error) {
	wrote, err := r.o.writeTarget(t, func(fresh *astrophotography.Target, change *statusChange) bool {
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
		return false, nil
	}
	return wrote, err
}

// addPending adds one pending frame to the sequence at index and to
// the outbox claim. When an edit of the spec removed that sequence after
// the exposure started, only the claim's count goes up. The promotion
// Job later reads the frame's FITS header to find its sequence.
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

// exposureFailed records an exposure that produced no frame: it clears
// status.exposure, sets Imaging to False with the reason
// ExposureFailed, and waits failedExposureWait. The runner keeps the
// target, because the next target would use the same failed camera.
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
			fmt.Sprintf("Reservation %s took no frame from the last exposure, and starts the next one in %s: %v", w.r.name, failedExposureWait, cause))
		return true
	})
	if err != nil {
		return err
	}
	sleep(ctx, failedExposureWait, nil)
	return nil
}
