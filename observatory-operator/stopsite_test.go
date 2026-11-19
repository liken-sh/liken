package main

// When the reservations of several telescopes of one observatory end
// together, exactly one of them stops the observatory: the one whose
// telescope sorts last waits until the others release their
// telescopes (sitestop.go).

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// holdFirstStopSite delays the status write that records the end of the
// first StopSite step by a minute, as a slow API server does. The first
// runner still holds its telescope during that minute, because
// claims.release runs after the write.
func holdFirstStopSite(api *fakeAPI) {
	var mu sync.Mutex
	held := false
	api.onEachRequest(func(r *http.Request) {
		path := r.URL.Path
		if r.Method == http.MethodGet || !strings.Contains(path, "/reservations/") || !strings.HasSuffix(path, "/status") {
			return
		}
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		var written observatory.Reservation
		_ = json.Unmarshal(body, &written)
		if step := stepOf(written, observatory.StepStopSite); step.State != observatory.StepDone && step.State != observatory.StepSkipped {
			return
		}
		// A sync.Once would block a second write on its mutex, which the
		// bubble does not count as durably blocked.
		mu.Lock()
		first := !held
		held = true
		mu.Unlock()
		if first {
			time.Sleep(time.Minute)
		}
	})
}

// endTogether gives each reservation the same spec.end, a minute from
// now. Each reservation holds the telescope of its name's first part.
func (w *world) endTogether(reservations ...string) {
	end := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
	for _, name := range reservations {
		telescope, _, _ := strings.Cut(name, "-")
		w.reserve(name, map[string]any{"telescope": telescope, "holder": "desktop", "end": end})
	}
}

// stopSites returns the StopSite step of each reservation, by name.
func (w *world) stopSites(reservations ...string) map[string]observatory.Step {
	out := map[string]observatory.Step{}
	for _, name := range reservations {
		r, _ := w.reservation(name)
		out[name] = stepOf(r, observatory.StepStopSite)
	}
	return out
}

// readyTogether reserves each telescope and waits until every
// reservation is Ready.
func (w *world) readyTogether(reservations ...string) {
	for _, name := range reservations {
		telescope, _, _ := strings.Cut(name, "-")
		w.reserve(name, map[string]any{"telescope": telescope, "holder": "desktop"})
	}
	for _, name := range reservations {
		w.phase(name, observatory.ReservationReady, 10*time.Minute)
	}
}

// When both reservations of the observatory's telescopes end together,
// one StopSite step runs: it disconnects the observatory's weather
// station, switches off the output that powers it, and then stops the
// observatory's server. The other step skips and names the reservation
// that stops the observatory.
func TestTwoReservationsThatEndTogetherStopTheSiteOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.put(observatory.SwitchKind, "lab", map[string]any{"observatory": "lab", "driver": map[string]any{"name": "indi_simulator_io"}})
		w.powerFrom(observatory.WeatherStationKind, "lab", "lab", 1)
		w.readyTogether("east-tonight", "west-tonight")
		holdFirstStopSite(w.api)
		w.endTogether("east-tonight", "west-tonight")
		w.phase("east-tonight", observatory.ReservationReleased, 10*time.Minute)
		w.phase("west-tonight", observatory.ReservationReleased, 10*time.Minute)

		steps := w.stopSites("east-tonight", "west-tonight")
		if east := steps["east-tonight"]; east.State != observatory.StepSkipped || east.Summary != "Reservation west-tonight stops Observatory lab" {
			t.Errorf("the StopSite step of east-tonight is %+v, want Skipped with \"Reservation west-tonight stops Observatory lab\"", east)
		}
		if west := steps["west-tonight"]; west.State != observatory.StepDone {
			t.Errorf("the StopSite step of west-tonight is %+v, want Done", west)
		}
		if !w.indi.sentBefore("lab-observatory Weather Simulator.CONNECTION CONNECT=Off", labOutput("1", "OFF")) {
			t.Errorf("the weather station's disconnect was not sent before its output switched off: %v", w.indi.changes())
		}
		w.until(time.Minute, "the observatory's server runs", func() bool {
			return !slices.Contains(w.api.names(podsCollection), "lab-observatory")
		})
	})
}

// eastHeldInDeactivation holds the east dust cap's park, so the
// Deactivation step of east-tonight keeps running, ends both
// reservations, and waits until west-tonight's StopSite waits for it.
func eastHeldInDeactivation(t *testing.T) *world {
	t.Helper()
	w := startWorld(t)
	w.readyTogether("east-tonight", "west-tonight")
	w.indi.hold("Dust Cover Simulator", "CAP_PARK")
	w.endTogether("east-tonight", "west-tonight")
	w.until(10*time.Minute, "west-tonight does not wait for east-tonight", func() bool {
		s := w.stopSites("west-tonight")["west-tonight"]
		return s.State == observatory.StepPending && s.Summary == waitingForEast
	})
	return w
}

const waitingForEast = "Stops Observatory lab when Reservation east-tonight releases Telescope east"

// closeEastCap lets the east dust cap close, as its driver does when the
// cap reaches its end.
func (w *world) closeEastCap() {
	w.indi.release("Dust Cover Simulator", "CAP_PARK")
	w.indi.report("Dust Cover Simulator", "CAP_PARK", "PARK", "On")
	w.indi.report("Dust Cover Simulator", "CAP_PARK", "UNPARK", "Off")
}

// The wait comes before the StopSite step starts, so a deactivation of
// another telescope that takes longer than the step's 5 minutes does
// not fail the waiting reservation.
func TestALongDeactivationOfAnotherTelescopeDoesNotFailTheWait(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := eastHeldInDeactivation(t)
		time.Sleep(7 * time.Minute)
		w.closeEastCap()
		w.phase("east-tonight", observatory.ReservationReleased, 10*time.Minute)
		west := w.phase("west-tonight", observatory.ReservationReleased, 10*time.Minute)

		if s := stepOf(west, observatory.StepStopSite); s.State != observatory.StepDone {
			t.Errorf("after a wait of 7 minutes the StopSite step of west-tonight is %+v, want Done", s)
		}
	})
}

// When the reservation that the last one waits for fails into Error,
// its telescope keeps the observatory, so the waiting step skips as in
// use.
func TestAReservationThatFailsWhileTheLastWaitsKeepsTheSite(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := eastHeldInDeactivation(t)
		w.phase("east-tonight", observatory.ReservationFailed, 20*time.Minute)
		west := w.phase("west-tonight", observatory.ReservationReleased, 10*time.Minute)

		if s := stepOf(west, observatory.StepStopSite); s.State != observatory.StepSkipped || s.Summary != "In use by Reservation east-tonight" {
			t.Errorf("the StopSite step of west-tonight is %+v, want Skipped with \"In use by Reservation east-tonight\"", s)
		}
	})
}

// A person deletes the waiting reservation. Its finalizer keeps it
// until its steps finish, so it still stops the observatory after the
// other reservation releases its telescope, and then goes.
func TestAWaitingReservationThatIsDeletedStillStopsTheSite(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := eastHeldInDeactivation(t)
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "west-tonight")
		w.closeEastCap()
		w.gone(observatory.ReservationKind, "west-tonight", 10*time.Minute)

		want := []string{"Normal StopSite: Done in 0 s: stopped lab-dome, lab-observatory, lab-weatherstation x1"}
		if got := w.eventsWith(observatory.ReservationKind, "west-tonight", "StopSite"); !slices.Equal(got, want) {
			t.Errorf("the StopSite Events of west-tonight are %q, want %q", got, want)
		}
	})
}

// An operator that stops while a reservation waits leaves the step
// Pending, and the next operator waits again and stops the observatory.
func TestAWaitThatTheOperatorEndsWaitsAgainAfterARestart(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := eastHeldInDeactivation(t)
		w.halt()
		if s := w.stopSites("west-tonight")["west-tonight"]; s.State != observatory.StepPending {
			t.Errorf("after the operator stopped the StopSite step of west-tonight is %+v, want Pending", s)
		}
		w.start()
		w.closeEastCap()
		west := w.phase("west-tonight", observatory.ReservationReleased, 15*time.Minute)

		if s := stepOf(west, observatory.StepStopSite); s.State != observatory.StepDone {
			t.Errorf("after the restart the StopSite step of west-tonight is %+v, want Done", s)
		}
	})
}

// vanish removes a reservation from the store, as when a person removes
// its finalizer during the deactivation.
func (w *world) vanish(name string) {
	collection := kindCollection(observatory.ReservationKind)
	object, _ := w.api.object(collection, name)
	w.api.mu.Lock()
	w.api.store(collection, name, object, "DELETED")
	w.api.mu.Unlock()
}

// A reservation that the last one waits for goes from the store. Its
// telescope then reads Idle, and an Idle telescope does not hold up the
// waiting reservation, which stops the observatory.
func TestAReservationGoneFromTheStoreDoesNotHoldUpTheWait(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := eastHeldInDeactivation(t)
		w.vanish("east-tonight")
		west := w.phase("west-tonight", observatory.ReservationReleased, 10*time.Minute)

		if s := stepOf(west, observatory.StepStopSite); s.State != observatory.StepDone {
			t.Errorf("the StopSite step of west-tonight is %+v, want Done", s)
		}
	})
}

// Three telescopes end together. The reservation of the last by name
// stops the observatory, and the other two name it.
func TestThreeReservationsThatEndTogetherStopTheSiteOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.put(observatory.TelescopeKind, "south", map[string]any{"observatory": "lab"})
		w.put(observatory.MountKind, "south", map[string]any{"telescope": "south", "driver": map[string]any{"name": "indi_simulator_telescope"},
			"deactivation": []any{map[string]any{"state": "Parked"}}})
		reservations := []string{"east-tonight", "south-tonight", "west-tonight"}
		w.readyTogether(reservations...)
		w.endTogether(reservations...)
		for _, name := range reservations {
			w.phase(name, observatory.ReservationReleased, 10*time.Minute)
		}

		steps := w.stopSites(reservations...)
		for _, name := range []string{"east-tonight", "south-tonight"} {
			if s := steps[name]; s.State != observatory.StepSkipped || s.Summary != "Reservation west-tonight stops Observatory lab" {
				t.Errorf("the StopSite step of %s is %+v, want Skipped with \"Reservation west-tonight stops Observatory lab\"", name, s)
			}
		}
		if s := steps["west-tonight"]; s.State != observatory.StepDone {
			t.Errorf("the StopSite step of west-tonight is %+v, want Done", s)
		}
	})
}

// holdReleasedWrite delays the status write that records the phase
// Released of one reservation by 5 minutes, as a slow API server does.
// The reservation keeps its telescope during those minutes, because
// claims.release runs after the write.
func holdReleasedWrite(api *fakeAPI, reservation string) {
	var mu sync.Mutex
	held := false
	api.onEachRequest(func(r *http.Request) {
		if r.Method == http.MethodGet || !strings.HasSuffix(r.URL.Path, "/reservations/"+reservation+"/status") {
			return
		}
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		var written observatory.Reservation
		_ = json.Unmarshal(body, &written)
		if written.Status.Phase != observatory.ReservationReleased {
			return
		}
		mu.Lock()
		first := !held
		held = true
		mu.Unlock()
		if first {
			time.Sleep(5 * time.Minute)
		}
	})
}

// West's StopSite skips while east is Ready, and west still holds its
// telescope when east ends and reaches its own StopSite. West's step has
// finished, so it does not stop the observatory, and east does.
func TestAReservationWhoseStepSkippedLeavesTheSiteToTheOther(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.readyTogether("east-tonight", "west-tonight")
		holdReleasedWrite(w.api, "west-tonight")
		w.endTogether("west-tonight")
		w.until(10*time.Minute, "the StopSite step of west-tonight does not skip", func() bool {
			return w.stopSites("west-tonight")["west-tonight"].State == observatory.StepSkipped
		})
		w.endTogether("east-tonight")
		east := w.phase("east-tonight", observatory.ReservationReleased, 10*time.Minute)
		w.phase("west-tonight", observatory.ReservationReleased, 10*time.Minute)

		if s := stepOf(east, observatory.StepStopSite); s.State != observatory.StepDone {
			t.Errorf("the StopSite step of east-tonight is %+v, want Done, because west-tonight's step skipped while east was Ready", s)
		}
	})
}
