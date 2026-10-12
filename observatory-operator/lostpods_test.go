package main

// What a session records outlives every pod of its server: an eviction
// that deletes them all, during the session or during its release,
// leaves the record in the reservation's status.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// podsOf answers the names of the pods of one server: its own pod and
// the pod of each device that it runs.
func (w *world) podsOf(server string) []string {
	var out []string
	for _, name := range w.api.names(podsCollection) {
		p, _ := w.api.object(podsCollection, name)
		if labels, _ := p["metadata"].(map[string]any)["labels"].(map[string]any); labels[labelServer] == server {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// evictServer deletes every pod of a server while the operator is
// down, as a node that fails does, and waits until the operator
// creates the server's pod again and its mount connects.
func (w *world) evictServer(server string) {
	w.t.Helper()
	w.halt()
	for _, name := range w.podsOf(server) {
		w.api.deleteNamed(podsCollection, name)
	}
	w.start()
	w.until(5*time.Minute, server+" is not back", func() bool {
		return slices.Contains(w.indi.connected(server), "Telescope Simulator")
	})
	w.settle()
}

// The focuser draws from output 1 of Switch lab and runs
// indi_simulator_focus when east-tonight starts. A person edits its
// spec, and then an eviction deletes every pod of east's server. The
// server comes back with the focuser as the session started it: the
// same driver, and the release switches off output 1 and not the
// output that the spec names now.
func TestAServerWhosePodsAllGoKeepsItsSessionsSpecs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		edit  func(w *world)
		check func(t *testing.T, w *world)
	}{
		{"another output", func(w *world) { w.powerFrom(observatory.FocuserKind, "east", "lab", 2) }, func(t *testing.T, w *world) {
			from := len(w.indi.changes())
			w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
			w.gone(observatory.ReservationKind, "east-tonight", 10*time.Minute)
			sent := w.indi.changes()[from:]
			if !slices.Contains(sent, labOutput("1", "OFF")) || slices.Contains(sent, labOutput("2", "OFF")) {
				t.Errorf("changes = %q, want %q and not %q", sent, labOutput("1", "OFF"), labOutput("2", "OFF"))
			}
		}},
		{"another driver", func(w *world) {
			w.amend(observatory.FocuserKind, "east", func(spec map[string]any) { spec["driver"] = map[string]any{"name": "indi_simulator_rotator"} })
		}, func(t *testing.T, w *world) {
			if p, _ := w.api.object(podsCollection, "east-focuser"); execOf(p) != "indi_simulator_focus" {
				t.Errorf("the focuser's pod runs %s, want indi_simulator_focus", execOf(p))
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := heldWorld(t, false)
				c.edit(w)
				w.settle()
				w.evictServer("east-telescope")
				c.check(t, w)
			})
		})
	}
}

// An eviction deletes every pod of east's server during its release,
// after StopDevices deleted the focuser's pod and before PowerOff. The
// focuser's pod terminates slowly, so StopDevices waits while the
// other pods go. PowerOff still switches off output 1 of Switch lab,
// which the session switched on for the focuser.
func TestAReleaseWhoseServerPodsAllGoSwitchesOffItsOutputs(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := heldWorld(t, false)
		w.holdPod("east-focuser")
		from := len(w.indi.changes())
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(5*time.Minute, "StopDevices does not wait for the focuser's pod", func() bool {
			r, _ := w.reservation("east-tonight")
			object, _ := w.api.object(podsCollection, "east-focuser")
			return stepOf(r, observatory.StepStopDevices).State == observatory.StepRunning &&
				object["metadata"].(map[string]any)["deletionTimestamp"] != nil
		})
		for _, name := range w.podsOf("east-telescope") {
			w.api.deleteNamed(podsCollection, name)
		}
		w.releasePod("east-focuser")
		w.gone(observatory.ReservationKind, "east-tonight", 10*time.Minute)

		if sent := w.indi.changes()[from:]; !slices.Contains(sent, labOutput("1", "OFF")) {
			t.Errorf("changes = %q, want %q", sent, labOutput("1", "OFF"))
		}
	})
}

// The reservations of east and west share the observatory's server,
// and each records the Dome as it started. A person gives the Dome
// another driver, one reservation ends, in either order, and then an
// eviction deletes every pod of the observatory's server. The server
// comes back with the Dome's recorded driver, because the reservation
// that still holds the server keeps its own record.
func TestAnObservatorysServerKeepsItsSessionsSpecsWhileAnyReservationHoldsIt(t *testing.T) {
	t.Parallel()
	for _, ended := range []string{"east-tonight", "west-tonight"} {
		t.Run(ended, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := heldWorld(t, true)
				w.amend(observatory.DomeKind, "lab", func(spec map[string]any) { spec["driver"] = map[string]any{"name": "indi_simulator_weather"} })
				w.api.deleteNamed(kindCollection(observatory.ReservationKind), ended)
				w.gone(observatory.ReservationKind, ended, 10*time.Minute)

				w.halt()
				for _, name := range w.podsOf("lab-observatory") {
					w.api.deleteNamed(podsCollection, name)
				}
				w.start()
				w.until(5*time.Minute, "lab-observatory is not back", func() bool {
					return slices.Contains(w.indi.connected("lab-observatory"), "Weather Simulator")
				})
				w.settle()

				if p, _ := w.api.object(podsCollection, "lab-dome"); execOf(p) != "indi_simulator_dome" {
					t.Errorf("the Dome's pod runs %s, want indi_simulator_dome", execOf(p))
				}
			})
		})
	}
}
