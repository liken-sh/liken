package main

// The frame in flight after a restart (plan 01, "Restarts"). Every
// setting of an exposure goes out before the exposure begins, so the
// driver keeps exposing and writes the frame while the operator
// restarts or its connection is down. Before it writes anything, a
// runner finishes each frame that a target's status.exposure names for
// its reservation: it reads CCD_EXPOSURE, waits while it is Busy, and
// then asks the worker whether the file is in the outbox. It counts the
// frame if it is, and clears status.exposure either way, so the frame
// counts once.

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/outbox"
	"github.com/liken-sh/liken/observatory-operator/indi"
)

// resume finishes every frame in flight of the runner's reservation.
func (r *runner) resume(ctx context.Context) error {
	targets, err := r.o.listTargets(r.namespace)
	if err != nil {
		return err
	}
	for i := range targets {
		t := &targets[i]
		if e := t.Status.Exposure; e != nil && e.Reservation == r.name {
			if err := r.finish(ctx, t); err != nil {
				return err
			}
		}
	}
	return nil
}

// finish waits for one frame in flight to end, and counts it when the
// outbox holds its file.
func (r *runner) finish(ctx context.Context, t *astrophotography.Target) error {
	e := t.Status.Exposure
	frame, err := outbox.Parse(e.File)
	if err != nil {
		r.logf("target %s: status.exposure names %s: %v; the frame does not count", targetName(t), e.File, err)
		return r.count(t, e, false, "", "")
	}
	// A camera that its driver does not serve holds no exposure, so the
	// runner waits only on a camera it can name on the server.
	if device, err := cameraDevice(r.o, r.namespace, frame.Camera); err == nil {
		if err := r.waitForExposure(ctx, device, exposureEnd(t, e)); err != nil {
			return err
		}
	}
	box, err := r.o.outboxOf(ctx, r.namespace, r.name)
	if err != nil {
		return err
	}
	counted := slices.Contains(box.Frames, e.File)
	r.logf("target %s: the frame %s in flight before the restart is %s", targetName(t), e.FrameID, map[bool]string{true: "in the outbox", false: "not in the outbox"}[counted])
	return r.count(t, e, counted, r.res.Status.Session.Outbox.ClaimName, "")
}

// exposureEnd answers when a frame in flight ends at the latest: its
// start, its exposure, and the readout. A sequence that an edit removed
// gives the start alone.
func exposureEnd(t *astrophotography.Target, e *astrophotography.Exposure) time.Time {
	end := e.StartTime.Add(readoutAllowance)
	if i := int(e.Sequence); i < len(t.Spec.Sequences) {
		if d, err := t.Spec.Sequences[i].ExposureDuration(); err == nil {
			end = end.Add(d)
		}
	}
	return end
}

// waitForExposure waits until the client reads the camera's
// CCD_EXPOSURE and it is not Busy, until the frame's end at the latest,
// and for at least the readout. A camera still Busy after that holds an
// exposure that is not the runner's, such as one a person started in
// KStars, and a camera the server does not reach in that time holds
// none.
func (r *runner) waitForExposure(ctx context.Context, device string, end time.Time) error {
	ctx, cancel := context.WithDeadline(ctx, later(end, time.Now().Add(readoutAllowance)))
	defer cancel()
	err := r.client.WaitFor(ctx, func(s *indi.Store) bool {
		p, ok := s.Property(device, "CCD_EXPOSURE")
		return ok && p.State != indi.Busy
	})
	if errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return err
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
