package main

// The filter wheel. A sequence and a centering name a filter by its
// name in the wheel's FILTER_NAME, the name that the driver writes into
// each frame's FILTER keyword. The runner reads FILTER_NAME before the
// slew, so a target that names a filter the wheel does not hold fails
// with NoFilter before the mount moves, and the runner goes on to the
// next target instead of spending a slew and a centering on one that
// cannot take a frame. Each frame selects its filter by name again, so
// a person who renames the wheel's filters during the night fails the
// target at the next frame that needs a missing name.

import (
	"cmp"
	"context"
	"slices"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/observatory-operator/indi"
)

// checkFilters fails the target with NoFilter when the wheel holds no
// filter of a name that the target's remaining work needs: the filter
// of each sequence with work left, and the filter of the centering
// frames. A sequence with no work left needs no filter, so a person can
// take a filter out of the wheel after its frames are done.
//
// A train with no filter wheel is left to selectFilter, which fails the
// target at the first frame that names a filter.
func (w *work) checkFilters() error {
	if w.wheelDevice == "" {
		return nil
	}
	t := aligned(w.target)
	var needed []string
	if c := t.Spec.Centering; c != nil {
		// centeringRun takes the first sequence's filter when the
		// centering names none.
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

// filterSlot answers the wheel's slot of a filter, counted from 1, or a
// NoFilter failure when FILTER_NAME does not name the filter.
func (w *work) filterSlot(filter string) (int, error) {
	names, _ := w.r.client.Property(w.wheelDevice, "FILTER_NAME")
	var known []string
	for _, m := range names.Members {
		known = append(known, m.Text)
	}
	if i := slices.Index(known, filter); i >= 0 {
		return i + 1, nil
	}
	return 0, fail(astrophotography.ReasonNoFilter, "filter wheel %s has no filter named %q; its FILTER_NAME holds %q", w.wheelDevice, filter, known)
}

// selectFilter turns the filter wheel to a frame's filter. A frame with
// no filter takes the light path as it is.
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
