package main

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// A full outbox stops the exposures with a Warning on the reservation.
// When the outbox has free space again and a change wakes the runner,
// it exposes.
func TestAFullOutboxWaitsForSpace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.worker.setFree(0)
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
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

// With promotion AfterReservation, the Event says that nothing frees
// the outbox before the reservation ends.
func TestAFullOutboxWithPromotionAfterTheReservation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.worker.setFree(0)
		x := target("m42", lights(1))
		x.Spec.Archive.Promotion = astrophotography.PromotionAfterReservation
		w.put(targetsPath, x)
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False OutboxFull" })
		events := w.runnerEvents("Target", "m42")
		if last := events[len(events)-1]; !strings.Contains(last.Message, "AfterReservation") {
			t.Errorf("the Event says %q, want the message that names AfterReservation", last.Message)
		}
	})
}

// When the worker cannot report the outbox's free space, the runner
// does not expose. When the worker responds again, the night continues.
func TestAWorkerThatCannotReportTheSpaceStopsTheExposures(t *testing.T) {
	cases := []struct {
		name    string
		fault   func(*fakeWorker, bool)
		message string
	}{
		{"a worker that is not ready", func(f *fakeWorker, on bool) { f.set(func() { f.down = on }) },
			"503 Service Unavailable"},
		{"a worker that returns no JSON", func(f *fakeWorker, on bool) { f.set(func() { f.garbled = on }) },
			"decoding the outbox response of the worker of east-tonight"},
		{"a worker that is gone", func(f *fakeWorker, on bool) { f.server.SetDown(on) },
			"requesting the outbox of the worker of east-tonight"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newWorld(t)
				c.fault(w.worker, true)
				w.put(targetsPath, target("m42", lights(1)))
				w.put(reservationsPath, reservationIn(observatory.ReservationReady))
				w.playCamera(t, saves)
				w.start(t)
				waitUntil(t, func() bool { return w.imaging(t, "m42") == "False ExposureFailed" })
				events := w.runnerEvents("Target", "m42")
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

// When the API server rejects the write of the OutboxFull condition,
// the next pass checks the space again and writes the condition.
func TestAFullOutboxWhoseConditionWriteFailsIsCheckedAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.worker.setFree(0)
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "True Slewing" })
		w.failStatusWrites(1)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False OutboxFull" })
	})
}
