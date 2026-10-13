package main

// The outbox's space before each exposure, and the worker that answers
// for it.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// A full outbox stops the exposures: Imaging turns False with
// OutboxFull, and the reservation gets a Warning. When the outbox has
// space again and a change wakes the runner, it exposes.
func TestAFullOutboxWaitsForSpace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.worker.setFree(0)
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False OutboxFull" })
		if got := w.eventReasons("Reservation", tonight); !slices.Equal(got, []string{reasonOutboxFull}) {
			t.Errorf("Events on the reservation %q, want %q", got, reasonOutboxFull)
		}
		w.worker.setFree(1 << 40)
		// A promotion Job that ends is a change that wakes the runner.
		w.put(jobsPath, object(ns, "m42-promote", map[string]any{labelManagedBy: managedBy}, map[string]any{}))
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		if got := w.eventReasons("Target", "m42"); !slices.Contains(got, astrophotography.ReasonExposing) {
			t.Errorf("Events on the target %q, want Exposing after OutboxFull", got)
		}
	})
}

// With promotion AfterReservation, nothing frees the outbox before the
// reservation ends, and the condition says so.
func TestAFullOutboxWithPromotionAfterTheReservation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.worker.setFree(0)
		x := target("m42", lights(1))
		x.Spec.Archive.Promotion = astrophotography.PromotionAfterReservation
		w.put(targetsPath, x)
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False OutboxFull" })
		events := w.api.recorded.About("Target", "m42")
		if last := events[len(events)-1]; !strings.Contains(last.Message, "nothing frees the space before the reservation ends") {
			t.Errorf("the Event says %q", last.Message)
		}
	})
}

// A worker that cannot answer stops the exposures, because the runner
// cannot tell whether the outbox has space. When it answers, the night
// goes on, and Imaging posts its recovery.
func TestAWorkerThatCannotAnswerHoldsTheExposures(t *testing.T) {
	cases := []struct {
		name    string
		fault   func(*fakeWorker, bool)
		message string
	}{
		{"a worker that is not ready", func(f *fakeWorker, on bool) { f.set(func() { f.down = on }) },
			"503 Service Unavailable"},
		{"a worker that answers no JSON", func(f *fakeWorker, on bool) { f.set(func() { f.garbled = on }) },
			"reading the worker's answer"},
		{"a worker that is gone", func(f *fakeWorker, on bool) { f.server.SetDown(on) },
			"asking the worker of east-tonight for its outbox"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newWorld(t)
				c.fault(w.worker, true)
				w.put(targetsPath, target("m42", lights(1)))
				w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
				w.playCamera(t, saves)
				w.start(t)
				waitUntil(t, func() bool { return w.imaging(t, "m42") == "False ExposureFailed" })
				events := w.api.recorded.About("Target", "m42")
				if last := events[len(events)-1]; !strings.Contains(last.Message, c.message) {
					t.Errorf("the Event says %q, want %q in it", last.Message, c.message)
				}
				c.fault(w.worker, false)
				waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
				got := w.eventReasons("Target", "m42")
				want := []string{"Slewing", "ExposureFailed", "Exposing", "SequencesDone"}
				if !slices.Equal(got, want) {
					t.Errorf("Events %q, want %q", got, want)
				}
			})
		})
	}
}

// A full outbox whose condition the API server refuses to record ends
// the pass, and the next pass finds the outbox full again.
func TestAFullOutboxThatCannotBeRecordedIsFoundAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.worker.setFree(0)
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "True Slewing" })
		w.failStatusWrites(1)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False OutboxFull" })
	})
}
