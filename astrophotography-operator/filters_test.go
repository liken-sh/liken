package main

// The filters a target names, checked against the filter wheel's
// FILTER_NAME before the mount moves.

import (
	"slices"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// imageBoth images a target that change edits, and then the target
// b-next, and answers the night when b-next is done.
func imageBoth(t *testing.T, change func(*astrophotography.Target)) *night {
	t.Helper()
	w := newWorld(t)
	first := target("a-first", lights(1))
	change(first)
	w.put(targetsPath, first)
	w.put(targetsPath, target("b-next", lights(1)))
	w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
	w.playCamera(t, saves)
	w.start(t)
	waitUntil(t, func() bool { return w.imaging(t, "b-next") == "False SequencesDone" })
	return w
}

// A target that names a filter the wheel does not hold fails with
// NoFilter before the slew: no Slewing Event, no write to the mount,
// and status.failed names the reservation. The runner goes on to the
// next target, which slews once.
func TestAFilterTheWheelDoesNotHoldFailsBeforeTheSlew(t *testing.T) {
	cases := []struct {
		name   string
		change func(*astrophotography.Target)
	}{
		{"a sequence's filter", func(x *astrophotography.Target) {
			x.Spec.Sequences[0].Filter = "H_Beta"
		}},
		{"the centering filter", func(x *astrophotography.Target) {
			c := centeringOn("lab")
			c.Filter = "Methane"
			x.Spec.Centering = &c
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := imageBoth(t, c.change)
				if got := w.imaging(t, "a-first"); got != "False "+astrophotography.ReasonNoFilter {
					t.Errorf("Imaging %q, want False %s", got, astrophotography.ReasonNoFilter)
				}
				if got := w.eventReasons("Target", "a-first"); slices.Contains(got, astrophotography.ReasonSlewing) {
					t.Errorf("Events on the target %q, want no %s", got, astrophotography.ReasonSlewing)
				}
				if got := slews(w); got != 1 {
					t.Errorf("%d slews, want only the one of b-next", got)
				}
				if got := failedReservation(w.targetNow(t, "a-first")); got != tonight {
					t.Errorf("status.failed names %q, want %q", got, tonight)
				}
			})
		})
	}
}

// A sequence with no work left needs no filter, so a filter that left
// the wheel after its frames does not fail the target.
func TestADoneSequenceNeedsNoFilter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := imageBoth(t, func(x *astrophotography.Target) {
			done := astrophotography.Sequence{Filter: "H_Beta", Exposure: "10s", Count: 1}
			x.Spec.Sequences = append([]astrophotography.Sequence{done}, x.Spec.Sequences...)
			x.Status.Sequences = []astrophotography.SequenceProgress{{Sequence: done, Archived: 1}, {Sequence: lights(1)}}
		})
		if got := w.imaging(t, "a-first"); got != "False "+astrophotography.ReasonSequencesDone {
			t.Errorf("Imaging %q, want False %s", got, astrophotography.ReasonSequencesDone)
		}
	})
}
