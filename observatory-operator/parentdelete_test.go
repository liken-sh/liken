package main

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// A Telescope that a person deletes during a reservation ends that
// reservation, as spec.end does. The finalizer keeps the Telescope
// until the reservation is Released, so the deactivation steps can
// still find it and its devices.
func TestATelescopeDeletedDuringASessionEndsItsReservation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.api.deleteNamed(kindCollection(observatory.TelescopeKind), "east")
		r := w.phase("east-tonight", observatory.ReservationReleased, 10*time.Minute)
		if abort := stepOf(r, observatory.StepStopWork); !strings.HasPrefix(abort.Summary, "Telescope east was deleted") {
			t.Errorf("the StopWork summary is %q, want it to begin \"Telescope east was deleted\"", abort.Summary)
		}
		w.gone(observatory.TelescopeKind, "east", time.Minute)
		if !slices.Contains(w.indi.changes(), parkCap) {
			t.Errorf("the INDI changes are %q, want them to include %q", w.indi.changes(), parkCap)
		}
		if pods := w.api.names(podsCollection); len(pods) != 0 {
			t.Errorf("after the release, the pods are %v, want none", pods)
		}
		deactivating := "Deactivating: Deactivating Telescope east: Telescope east was deleted"
		if n := countOf(eventMessages(w.api), deactivating); n != 1 {
			t.Errorf("%d Events have the message %q in %q, want 1", n, deactivating, eventMessages(w.api))
		}
	})
}

func countOf(items []string, value string) int {
	n := 0
	for _, item := range items {
		if item == value {
			n++
		}
	}
	return n
}

// An Observatory that a person deletes during a session ends the
// reservation of each of its telescopes, and is removed once they are
// all Released.
func TestAnObservatoryDeletedDuringASessionEndsEveryReservationOfIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		for _, telescope := range []string{"east", "west"} {
			w.reserve(telescope+"-tonight", map[string]any{"telescope": telescope, "holder": "desktop"})
			w.phase(telescope+"-tonight", observatory.ReservationReady, 10*time.Minute)
		}
		w.api.deleteNamed(kindCollection(observatory.ObservatoryKind), "lab")
		for _, telescope := range []string{"east", "west"} {
			r := w.phase(telescope+"-tonight", observatory.ReservationReleased, 10*time.Minute)
			if abort := stepOf(r, observatory.StepStopWork); !strings.HasPrefix(abort.Summary, "Observatory lab was deleted") {
				t.Errorf("the StopWork summary of %s is %q, want it to begin \"Observatory lab was deleted\"", telescope, abort.Summary)
			}
		}
		w.gone(observatory.ObservatoryKind, "lab", time.Minute)
		park := "lab-observatory Dome Simulator.DOME_PARK PARK=On UNPARK=Off"
		if !slices.Contains(w.indi.changes(), park) {
			t.Errorf("the INDI changes are %q, want them to include %q", w.indi.changes(), park)
		}
		if pods := w.api.names(podsCollection); len(pods) != 0 {
			t.Errorf("after the release, the pods are %v, want none", pods)
		}
	})
}

// addFinalizer adds a finalizer to a stored object, so that a delete
// sets its deletionTimestamp and keeps it.
func (a *fakeAPI) addFinalizer(collection, name, finalizer string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	next := clone(a.objects[collection][name])
	next["metadata"].(map[string]any)["finalizers"] = []any{finalizer}
	a.store(collection, name, next, "MODIFIED")
}

// A reservation of a telescope that is being deleted, or whose
// observatory is being deleted, stays in its Wait step, as it does for
// a telescope that does not exist.
func TestAReservationOfADeletingTelescopeWaits(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		kind    observatory.Kind
		object  string
		summary string
	}{
		{"a deleting telescope", observatory.TelescopeKind, "east", "Telescope east is being deleted"},
		{"a deleting observatory", observatory.ObservatoryKind, "lab", "Observatory lab is being deleted"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.api.addFinalizer(kindCollection(c.kind), c.object, "example.com/hold")
				w.api.deleteNamed(kindCollection(c.kind), c.object)
				w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
				w.until(time.Minute, "the Wait step does not name the delete", func() bool {
					r, _ := w.reservation("east-tonight")
					return stepOf(r, observatory.StepWait).Summary == c.summary
				})
				time.Sleep(10 * time.Minute)
				r, _ := w.reservation("east-tonight")
				if wait := stepOf(r, observatory.StepWait); wait.State != observatory.StepRunning || wait.Summary != c.summary {
					t.Errorf("the Wait step is %s %q, want Running %q", wait.State, wait.Summary, c.summary)
				}
			})
		})
	}
}

// A Telescope or an Observatory that no reservation uses has no
// finalizer, so a delete removes it at once. That includes one whose
// session has ended.
func TestAParentWithNoReservationIsDeletedAtOnce(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		session bool
		kind    observatory.Kind
		object  string
	}{
		{"a telescope never reserved", false, observatory.TelescopeKind, "west"},
		{"a telescope after its session", true, observatory.TelescopeKind, "east"},
		{"an observatory after its session", true, observatory.ObservatoryKind, "lab"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				if c.session {
					end := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
					w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "end": end})
					w.phase("east-tonight", observatory.ReservationReleased, 2*time.Hour)
				}
				w.until(time.Minute, "the finalizer stays", func() bool {
					object, _ := decode[struct{ Metadata observatory.ObjectMeta }](t, w.api, kindCollection(c.kind), c.object)
					return len(object.Metadata.Finalizers) == 0
				})
				w.api.deleteNamed(kindCollection(c.kind), c.object)
				if _, ok := w.api.object(kindCollection(c.kind), c.object); ok {
					t.Errorf("%s %s exists after its delete, want it gone", c.kind.Name, c.object)
				}
			})
		})
	}
}
