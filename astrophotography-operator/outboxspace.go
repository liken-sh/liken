package main

// Before each exposure, the runner reads the outbox's free space from
// the worker. When less than the size of three frames is free, the
// runner does not expose. It sets Imaging to False with the reason
// OutboxFull, posts a Warning on the reservation, and waits. A promotion
// Job frees space by moving frames to the archive, and the end of the
// Job wakes the runner to check again.

import (
	"context"
	"fmt"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
)

type outboxFull struct {
	free, need int64
}

func (f *outboxFull) Error() string {
	return fmt.Sprintf("the outbox has %d bytes free, and three frames need %d", f.free, f.need)
}

// checkSpace returns an *outboxFull when the outbox has less free space
// than three frames need.
func (w *work) checkSpace(ctx context.Context, s sequenceSettings) error {
	size := w.frameSize(s)
	box, err := w.r.o.outboxOf(ctx, w.r.namespace, w.r.name)
	if err != nil {
		return err
	}
	if need := 3 * size; box.FreeBytes < need {
		return &outboxFull{free: box.FreeBytes, need: need}
	}
	return nil
}

// frameSize returns the size in bytes of one frame in the outbox. It is
// width × height × bytes per pixel / binning², with the sizes read from
// the camera's CCD_INFO property, plus headerBytes for the FITS
// header. A camera that has no CCD_INFO is counted as the header alone.
func (w *work) frameSize(s sequenceSettings) int64 {
	info, _ := w.r.client.Property(w.cameraDevice, "CCD_INFO")
	number := func(name string) int64 {
		m, _ := info.Member(name)
		return int64(m.Number)
	}
	bin := int64(1)
	if s.binning != nil && *s.binning > 0 {
		bin = int64(*s.binning)
	}
	bytesPerPixel := (number("CCD_BITSPERPIXEL") + 7) / 8
	return number("CCD_MAX_X")*number("CCD_MAX_Y")*bytesPerPixel/(bin*bin) + headerBytes
}

// waitForSpace sets Imaging to False with OutboxFull and waits for any
// change in the cluster, such as the end of a promotion Job. It posts
// the Warning only when Imaging does not already have the reason
// OutboxFull, so a long wait posts it once.
func (w *work) waitForSpace(ctx context.Context, full *outboxFull) error {
	wake := w.r.o.changed.wait()
	message := fmt.Sprintf("Reservation %s waits for a promotion to free space in the outbox: %v", w.r.name, full)
	if w.target.Spec.Archive.Promotion == astrophotography.PromotionAfterReservation {
		message = fmt.Sprintf("Reservation %s takes no more frames, because spec.archive.promotion is AfterReservation and no promotion frees space before the reservation ends: %v", w.r.name, full)
	}
	if !imagingReason(w.target, astrophotography.ReasonOutboxFull) {
		w.r.o.recorder.Warning(reservationRef(w.res), reasonOutboxFull, message)
	}
	if err := w.setImaging(false, astrophotography.ReasonOutboxFull, message); err != nil {
		return err
	}
	select {
	case <-wake:
	case <-ctx.Done():
	}
	return nil
}
