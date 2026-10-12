package main

// The spec.requires of a session's resources: the Observatory's hold
// the Wait step, the Telescope's hold the Requires step, and dawn ends
// a wait that never ended.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// require sets the spec.requires of one of the example's resources.
func (w *world) require(kind observatory.Kind, name string, requires ...map[string]any) {
	w.t.Helper()
	w.amend(kind, name, func(spec map[string]any) {
		list := []any{}
		for _, r := range requires {
			list = append(list, r)
		}
		spec["requires"] = list
	})
}

// stepWaits waits until a reservation's step runs with a summary.
func (w *world) stepWaits(name string, step observatory.StepName, summary string) {
	w.t.Helper()
	var r observatory.Reservation
	w.until(10*time.Minute, "the Reservation "+name+" does not wait in "+string(step), func() bool {
		r, _ = w.reservation(name)
		s := stepOf(r, step)
		return s.State == observatory.StepRunning && s.Summary == summary
	})
}

// telescopeTouched reports whether the operator sent any change to the
// east telescope's INDI server, which runs only from PowerOn.
func (w *world) telescopeTouched() bool {
	return slices.ContainsFunc(w.indi.changes(), func(change string) bool { return strings.HasPrefix(change, "east-telescope ") })
}

var setUpRequired = map[string]any{"type": observatory.ConditionSetUp}

// A portable telescope's SetUp holds the Requires step: the
// observatory's server runs, and the telescope's stays off until a
// person confirms.
func TestATelescopesRequirementHoldsPowerOn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.require(observatory.TelescopeKind, "east", setUpRequired)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.stepWaits("east-tonight", observatory.StepRequires, "Waiting for Telescope east SetUp=True")
		time.Sleep(time.Hour)
		synctest.Wait()
		if w.telescopeTouched() {
			t.Errorf("the telescope's server ran before the person confirmed: %v", w.indi.changes())
		}
		if pods := w.api.names(podsCollection); slices.ContainsFunc(pods, func(p string) bool { return strings.HasPrefix(p, "east-") }) {
			t.Errorf("pods of the telescope = %v", pods)
		}

		w.setUp(observatory.TelescopeKind, "east", true, time.Time{})
		r := w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		if got := stepOf(r, observatory.StepRequires).Summary; got != "Found Telescope east SetUp=True" {
			t.Errorf("Requires = %q", got)
		}
	})
}

// The Observatory's requirement holds the Wait step, before the
// reservation takes its telescope, so nothing starts and the telescope
// stays Idle.
func TestAnObservatorysRequirementHoldsStartSite(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.require(observatory.ObservatoryKind, "lab", setUpRequired)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.stepWaits("east-tonight", observatory.StepWait, "Waiting for Observatory lab SetUp=True")
		time.Sleep(time.Hour)
		synctest.Wait()
		if pods := w.api.names(podsCollection); len(pods) != 0 {
			t.Errorf("pods = %v", pods)
		}
		if east, _ := decode[observatory.Telescope](t, w.api, kindCollection(observatory.TelescopeKind), "east"); east.Status.Phase != observatory.PhaseIdle {
			t.Errorf("the telescope is %s while its reservation waits", east.Status.Phase)
		}

		w.setUp(observatory.ObservatoryKind, "lab", true, time.Time{})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
	})
}

// A requirement on a WeatherStation's Safe holds the telescope while
// the station that the observatory's server runs reports bad weather,
// and each condition that the step waits for is in its message.
func TestAWeatherStationsSafeHoldsTheTelescope(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.require(observatory.TelescopeKind, "east", setUpRequired,
			map[string]any{"kind": "WeatherStation", "name": "lab", "type": "Safe"})
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.stepWaits("east-tonight", observatory.StepRequires, "Waiting for Telescope east SetUp=True")
		w.weather("Alert")
		w.stepWaits("east-tonight", observatory.StepRequires, "Waiting for Telescope east SetUp=True, WeatherStation lab Safe=True")
		w.setUp(observatory.TelescopeKind, "east", true, time.Time{})
		w.stepWaits("east-tonight", observatory.StepRequires, "Waiting for WeatherStation lab Safe=True")
		time.Sleep(time.Hour)
		synctest.Wait()
		if w.telescopeTouched() {
			t.Errorf("the telescope's server ran in bad weather: %v", w.indi.changes())
		}

		w.weather("Ok")
		r := w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		if got := stepOf(r, observatory.StepRequires).Summary; got != "Found Telescope east SetUp=True, WeatherStation lab Safe=True" {
			t.Errorf("Requires = %q", got)
		}
	})
}

// labDawn is the first astronomical dawn at the example's observatory
// after a time.
func labDawn(after time.Time) time.Time {
	return dawnAfter(after, observatory.Location{Latitude: -30.169, Longitude: -70.806, Elevation: 2207})
}

// A reservation that still waits at dawn ends with the telescope's
// equipment never powered on, from either gate, and its status and its
// Events say why.
func TestAReservationStillWaitingAtDawnEnds(t *testing.T) {
	cases := []struct {
		name     string
		kind     observatory.Kind
		resource string
		step     observatory.StepName
		waited   string
		event    string
	}{
		{"in the Requires step", observatory.TelescopeKind, "east", observatory.StepRequires,
			"Telescope east SetUp=True", string(observatory.ReservationDeactivating)},
		{"in the Wait step", observatory.ObservatoryKind, "lab", observatory.StepWait,
			"Observatory lab SetUp=True", string(observatory.ReservationReleased)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.require(c.kind, c.resource, setUpRequired)
				began := stamp()
				w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
				w.stepWaits("east-tonight", c.step, "Waiting for "+c.waited)
				dawn := labDawn(began)
				time.Sleep(time.Until(dawn) - time.Minute)
				if r, _ := w.reservation("east-tonight"); r.Status.Phase == observatory.ReservationReleased {
					t.Fatalf("the reservation ended before dawn: %v", stepStates(r))
				}

				r := w.phase("east-tonight", observatory.ReservationReleased, 10*time.Minute)
				want := "Dawn came at " + dawn.Format(time.RFC3339) + " while waiting for " + c.waited + ", so the session did not start"
				if s := stepOf(r, c.step); s.State != observatory.StepSkipped || s.Summary != want {
					t.Errorf("%s = %s %q, want Skipped %q", c.step, s.State, s.Summary, want)
				}
				if w.telescopeTouched() {
					t.Errorf("the telescope's server ran: %v", w.indi.changes())
				}
				if pods := w.api.names(podsCollection); len(pods) != 0 {
					t.Errorf("pods after the release = %v", pods)
				}
				if got := eventsAbout(w.api, observatory.ReservationKind, "east-tonight", c.event); !slices.ContainsFunc(got, func(m string) bool {
					return strings.HasSuffix(m, ": dawn came at "+dawn.Format(time.RFC3339)+" while waiting for "+c.waited+", so the session did not start")
				}) {
					t.Errorf("%s Events = %v", c.event, got)
				}
			})
		})
	}
}
