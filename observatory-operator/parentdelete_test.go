package main

// A Telescope or an Observatory that a person deletes while a
// reservation holds it ends that reservation, as spec.end does. The
// finalizer keeps it in the tree until the reservation is Released, so
// the deactivation steps still find it and its devices.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func TestATelescopeDeletedDuringASessionEndsItsReservation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.api.deleteNamed(kindCollection(observatory.TelescopeKind), "east")
		r := w.phase("east-tonight", observatory.ReservationReleased, 10*time.Minute)
		if abort := stepOf(r, observatory.StepAbort); !strings.HasPrefix(abort.Summary, "Telescope east was deleted") {
			t.Errorf("Abort = %q, want it to name the delete", abort.Summary)
		}
		w.gone(observatory.TelescopeKind, "east", time.Minute)
		if !slices.Contains(w.indi.changes(), parkCap) {
			t.Errorf("changes = %q, want %q", w.indi.changes(), parkCap)
		}
		if pods := w.api.names(podsCollection); len(pods) != 0 {
			t.Errorf("pods after the release: %v", pods)
		}
		deactivating := "Deactivating: Deactivating Telescope east: Telescope east was deleted"
		if n := countOf(eventMessages(w.api), deactivating); n != 1 {
			t.Errorf("%d Events %q in %q, want one", n, deactivating, eventMessages(w.api))
		}
	})
}

// countOf counts the items of a list that equal one value.
func countOf(items []string, value string) int {
	n := 0
	for _, item := range items {
		if item == value {
			n++
		}
	}
	return n
}

// An Observatory deleted during a session ends the reservation of each
// of its telescopes, and goes once both are Released.
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
			if abort := stepOf(r, observatory.StepAbort); !strings.HasPrefix(abort.Summary, "Observatory lab was deleted") {
				t.Errorf("the Abort of %s = %q, want it to name the delete", telescope, abort.Summary)
			}
		}
		w.gone(observatory.ObservatoryKind, "lab", time.Minute)
		park := "lab-observatory Dome Simulator.DOME_PARK PARK=On UNPARK=Off"
		if !slices.Contains(w.indi.changes(), park) {
			t.Errorf("changes = %q, want %q", w.indi.changes(), park)
		}
		if pods := w.api.names(podsCollection); len(pods) != 0 {
			t.Errorf("pods after the release: %v", pods)
		}
	})
}

// holdWith adds another controller's finalizer to a stored object, so
// a delete leaves it with a deletionTimestamp.
func (a *fakeAPI) holdWith(collection, name, finalizer string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	next := clone(a.objects[collection][name])
	next["metadata"].(map[string]any)["finalizers"] = []any{finalizer}
	a.store(collection, name, next, "MODIFIED")
}

// A reservation of a telescope that is being deleted, or whose
// observatory is, waits in its Wait step, as it waits for a telescope
// that does not exist.
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
				w.api.holdWith(kindCollection(c.kind), c.object, "example.com/hold")
				w.api.deleteNamed(kindCollection(c.kind), c.object)
				w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
				w.until(time.Minute, "the Wait step does not name the delete", func() bool {
					r, _ := w.reservation("east-tonight")
					return stepOf(r, observatory.StepWait).Summary == c.summary
				})
				time.Sleep(10 * time.Minute)
				r, _ := w.reservation("east-tonight")
				if wait := stepOf(r, observatory.StepWait); wait.State != observatory.StepRunning || wait.Summary != c.summary {
					t.Errorf("Wait = %s %q, want Running %q", wait.State, wait.Summary, c.summary)
				}
			})
		})
	}
}

// A Telescope or an Observatory that no reservation holds carries no
// finalizer, so a delete removes it at once, also after a session.
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
					t.Errorf("%s %s stays after its delete", c.kind.Name, c.object)
				}
			})
		})
	}
}
