package main

// The outbox's space (plans 01 and 03). Before each exposure the runner
// asks the worker for the free bytes of the outbox, and with less than
// three frames' size free it does not expose: Imaging turns False with
// the reason OutboxFull, the reservation gets the Warning
// AstrophotographyOutboxFull, and the runner waits. A promotion frees
// the space, and the end of its Job wakes the runner, which asks again.

import (
	"context"
	"fmt"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/observatory-operator/indi"
)

// outboxFull is the error of an outbox with too little space for the
// next frame.
type outboxFull struct {
	free, need int64
}

func (f *outboxFull) Error() string {
	return fmt.Sprintf("the outbox has %d bytes free, and three frames need %d", f.free, f.need)
}

// checkSpace answers an *outboxFull when the outbox has less than three
// frames' size free.
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

// frameSize answers the size of one frame in the outbox: the camera's
// width × height × bytes per pixel / binning², from its CCD_INFO, and
// three FITS blocks for the header. A camera that reports no CCD_INFO
// counts as the header alone.
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
	return number("CCD_MAX_X")*number("CCD_MAX_Y")*bytesPerPixel/(bin*bin) + headerBlocks
}

// waitForSpace sets Imaging False with OutboxFull, and waits for a
// change, such as the end of a promotion Job. The Warning on the
// reservation goes out when the target first finds the outbox full.
func (w *work) waitForSpace(ctx context.Context, full *outboxFull) error {
	wake := w.r.o.changed.wait()
	message := fmt.Sprintf("Reservation %s does not expose: %v. A promotion frees the space.", w.r.name, full)
	if w.target.Spec.Archive.Promotion == astrophotography.PromotionAfterReservation {
		message = fmt.Sprintf("Reservation %s does not expose: %v. The target's promotion is AfterReservation, so nothing frees the space before the reservation ends.", w.r.name, full)
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

// selectFilter turns the filter wheel to the sequence's filter, by its
// name in FILTER_NAME, the name that the driver writes into FILTER. A
// sequence with no filter takes the light path as it is.
func (w *work) selectFilter(ctx context.Context, filter string) error {
	if filter == "" {
		return nil
	}
	if w.wheelDevice == "" {
		return fail(astrophotography.ReasonNoFilter, "the sequence names the filter %q, and optical train %q has no FilterWheel", filter, w.camera.train)
	}
	names, _ := w.r.client.Property(w.wheelDevice, "FILTER_NAME")
	slot := 0
	var known []string
	for i, m := range names.Members {
		known = append(known, m.Text)
		if m.Text == filter && slot == 0 {
			slot = i + 1
		}
	}
	if slot == 0 {
		return fail(astrophotography.ReasonNoFilter, "filter wheel %s has no filter named %q; its FILTER_NAME holds %q", w.wheelDevice, filter, known)
	}
	_, err := w.r.settle(ctx, filterTimeout, func() (indi.Sent, error) {
		return w.r.client.SetNumbers(w.wheelDevice, "FILTER_SLOT", map[string]float64{"FILTER_SLOT_VALUE": float64(slot)})
	})
	return err
}
