package main

// A sequence or a centering refers to a filter by its name in the
// wheel's FILTER_NAME property, which is also the name that the driver
// writes into each frame's FILTER keyword. The runner checks the names
// before it slews, so a target with a filter that the wheel does not
// have fails with NoFilter before the mount moves, not after the slew
// and the centering. selectFilter looks the name up again before each
// frame, so a filter renamed during the night fails the target at the
// next frame that uses the old name.

import (
	"cmp"
	"context"
	"slices"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/observatory-operator/indi"
)

// checkFilters fails the target with NoFilter when the wheel has no
// filter for the work that remains: the filter of each sequence that
// still has frames to take, and the centering filter. A finished
// sequence is not checked, so a person can remove its filter from the
// wheel once its frames are done. With no filter wheel the check
// passes, and selectFilter fails the target at the first frame that
// names a filter.
func (w *work) checkFilters() error {
	if w.wheelDevice == "" {
		return nil
	}
	t := aligned(w.target)
	var needed []string
	if c := t.Spec.Centering; c != nil {
		// centeringRun uses the first sequence's filter when the
		// centering has none.
		needed = append(needed, cmp.Or(c.Filter, t.Spec.Sequences[0].Filter))
	}
	for _, p := range t.Status.Sequences {
		if workLeft(p) > 0 {
			needed = append(needed, p.Filter)
		}
	}
	for _, filter := range needed {
		if filter == "" {
			continue
		}
		if _, err := w.filterSlot(filter); err != nil {
			return err
		}
	}
	return nil
}

// filterSlot returns the wheel's slot number for a filter, counting from
// 1, or a NoFilter failure when FILTER_NAME has no such name.
func (w *work) filterSlot(filter string) (int, error) {
	names, _ := w.r.client.Property(w.wheelDevice, "FILTER_NAME")
	var known []string
	for _, m := range names.Members {
		known = append(known, m.Text)
	}
	if i := slices.Index(known, filter); i >= 0 {
		return i + 1, nil
	}
	return 0, fail(astrophotography.ReasonNoFilter, "filter wheel %s has no filter named %q, and its FILTER_NAME lists %q", w.wheelDevice, filter, known)
}

// selectFilter turns the filter wheel to a frame's filter. A frame with
// no filter leaves the wheel where it is.
func (w *work) selectFilter(ctx context.Context, filter string) error {
	if filter == "" {
		return nil
	}
	if w.wheelDevice == "" {
		return fail(astrophotography.ReasonNoFilter, "the sequence names the filter %q, and optical train %q has no FilterWheel", filter, w.camera.train)
	}
	slot, err := w.filterSlot(filter)
	if err != nil {
		return err
	}
	_, err = w.r.settle(ctx, filterTimeout, func() (indi.Sent, error) {
		return w.r.client.SetNumbers(w.wheelDevice, "FILTER_SLOT", map[string]float64{"FILTER_SLOT_VALUE": float64(slot)})
	})
	return err
}
