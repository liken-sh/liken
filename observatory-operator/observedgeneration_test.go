package main

// A reservation's status.observedGeneration follows each edit of its
// spec, in every phase where the runner runs no step, so a client such
// as `kubectl wait` can tell that the operator read the edit.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func TestAReservationObservesAnEditWhileItWaits(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		spec  map[string]any
		phase observatory.ReservationPhase
	}{
		{"Ready", map[string]any{}, observatory.ReservationReady},
		{"Scheduled for a later start", map[string]any{"start": "2100-01-01T00:00:00Z"}, observatory.ReservationScheduled},
		{"Failed", map[string]any{"outbox": map[string]any{"claimName": "nightly"}}, observatory.ReservationFailed},
		{"Released at its end", map[string]any{"end": "2000-01-01T00:00:00Z"}, observatory.ReservationReleased},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				c.spec["telescope"], c.spec["holder"] = "east", "desktop"
				w.reserve("east-tonight", c.spec)
				w.phase("east-tonight", c.phase, 15*time.Minute)
				w.settle()

				w.amend(observatory.ReservationKind, "east-tonight", func(spec map[string]any) {
					spec["holder"] = "laptop"
				})
				w.settle()

				r, _ := w.reservation("east-tonight")
				if r.Metadata.Generation != 2 || r.Status.ObservedGeneration != 2 {
					t.Errorf("generation %d, observedGeneration %d, want 2 and 2", r.Metadata.Generation, r.Status.ObservedGeneration)
				}
				if ready := conditionOf(r.Status.Conditions, observatory.ConditionReady); ready.ObservedGeneration != 2 {
					t.Errorf("Ready = %+v, want observedGeneration 2", ready)
				}
				if r.Status.Phase != c.phase {
					t.Errorf("the reservation is %s after the edit, want %s", r.Status.Phase, c.phase)
				}
			})
		})
	}
}
