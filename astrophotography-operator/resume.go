package main

// Finishing a frame that was in flight when the operator restarted or
// lost its INDI connection. The runner sends every setting of an
// exposure before it starts the exposure, so the INDI driver finishes
// the exposure and writes the frame without the operator. Before a
// runner sends anything else, it waits until the camera's CCD_EXPOSURE
// is no longer Busy for each target whose status.exposure belongs to
// its reservation, and asks the worker whether the frame's file is in
// the outbox. It counts the frame only if the file is there, and it
// clears status.exposure either way, so no frame counts twice.

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/outbox"
	"github.com/liken-sh/liken/observatory-operator/indi"
)

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

func (r *runner) finish(ctx context.Context, t *astrophotography.Target) error {
	e := t.Status.Exposure
	frame, err := outbox.Parse(e.File)
	if err != nil {
		r.logf("target %s: status.exposure names %s: %v; the frame does not count", targetName(t), e.File, err)
		_, err := r.count(t, e, false, "", "")
		return err
	}
	// When the Camera object is gone or has no status.indiDevice, the
	// runner has no device to read CCD_EXPOSURE from, so it does not wait.
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
	wrote, err := r.count(t, e, counted, r.res.Status.Session.Outbox.ClaimName, "")
	// The work that started this exposure ended before it counted the
	// frame, so it did not increment framesExposed either. Only the write
	// that counts the frame increments it, so it counts once.
	if wrote && counted {
		r.o.metrics.runner.framesExposed.WithLabelValues(t.Metadata.Namespace, t.Metadata.Name).Inc()
	}
	return err
}

// exposureEnd returns the latest time a frame in flight can finish: its
// start time plus the exposure duration plus readoutAllowance. When an
// edit removed the frame's sequence, the duration is unknown and is
// left out.
func exposureEnd(t *astrophotography.Target, e *astrophotography.Exposure) time.Time {
	end := e.StartTime.Add(readoutAllowance)
	if i := int(e.Sequence); i < len(t.Spec.Sequences) {
		if d, err := t.Spec.Sequences[i].ExposureDuration(); err == nil {
			end = end.Add(d)
		}
	}
	return end
}

// waitForExposure waits until the camera defines CCD_EXPOSURE and the
// property is not Busy, until end or readoutAllowance from now,
// whichever is later. At that deadline it returns nil: a camera that is
// still Busy is running an exposure that the runner did not start, such
// as one a person started in KStars, and a camera that never defines the
// property has no exposure to finish.
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
