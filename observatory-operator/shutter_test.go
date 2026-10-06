package main

// A dome's target state includes its shutter. The driver moves the
// shutter through DOME_SHUTTER_PARK_POLICY only when the park state
// changes, so a dome that starts unparked with its shutter closed, as
// the simulator does, stays closed after an unpark that finds it
// unparked. The action then opens the shutter itself.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The second case starts the dome parked with its shutter open, and
// nothing unparks the dome or a mount, so the dome's deactivation finds
// it parked.
func TestADomesStateIncludesItsShutter(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, trigger string
		setup         func(w *world)
		end           observatory.ReservationPhase
		shutter       string
		summary       string
	}{
		{"unparked opens", observatory.TriggerActivation, func(*world) {}, observatory.ReservationReady,
			"lab-observatory Dome Simulator.DOME_SHUTTER SHUTTER_OPEN=On SHUTTER_CLOSE=Off", "Found Dome lab unparked and opened its shutter"},
		{"parked closes", observatory.TriggerDeactivation, func(w *world) {
			w.indi.preset("Dome Simulator", "DOME_PARK", "PARK")
			w.indi.preset("Dome Simulator", "DOME_SHUTTER", "SHUTTER_OPEN")
			w.amend(observatory.DomeKind, "lab", func(spec map[string]any) {
				delete(spec, "activation")
				delete(spec, "triggers")
			})
			w.amend(observatory.MountKind, "east", func(spec map[string]any) { delete(spec, "activation") })
		}, observatory.ReservationReleased, "lab-observatory Dome Simulator.DOME_SHUTTER SHUTTER_OPEN=Off SHUTTER_CLOSE=On", "Found Dome lab parked and closed its shutter"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				c.setup(w)
				end := time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339)
				w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "end": end})
				w.phase("east-tonight", c.end, time.Hour)
				run := w.runEnds(observatory.DomeKind, "lab", c.trigger, time.Minute)
				if run.State != observatory.StepDone || run.Summary != c.summary {
					t.Errorf("the dome's %s = %s %q, want %q", c.trigger, run.State, run.Summary, c.summary)
				}
				sent := w.indi.changes()
				if !slices.ContainsFunc(sent, func(s string) bool { return s == c.shutter }) {
					t.Errorf("the dome received no %q: %v", c.shutter, sent)
				}
			})
		})
	}
}
