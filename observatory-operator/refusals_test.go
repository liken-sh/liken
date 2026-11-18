package main

// When a park lock makes a driver refuse a park or an unpark, the action
// fails with the text of the refusal's Warning Event, so the run's
// record says why the driver set the property to Alert.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// In the first case, the fake server never completes the mount's park,
// so the dome's trigger parks the dome in bad weather while the mount is
// unparked. The actions of a trigger do not wait for the mounts to park,
// so the summary tells the user to add an after. In the second case,
// the dome and the mount start parked and the dome has no activation, so
// the mount's activation finds the dome parked.
func TestARefusedMoveExplainsTheLock(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		setup   func(w *world)
		kind    observatory.Kind
		device  string
		trigger string
		summary string
	}{
		{"a trigger's park", func(w *world) {
			w.amend(observatory.DomeKind, "lab", func(spec map[string]any) {
				spec["triggers"] = []any{map[string]any{
					"when": map[string]any{"kind": "WeatherStation", "name": "lab", "type": "Safe", "status": "False"},
					"run":  []any{map[string]any{"state": "Parked"}}}}
			})
			w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
			w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
			w.indi.hold("Telescope Simulator", "TELESCOPE_PARK")
			w.weather("Alert")
		}, observatory.DomeKind, "lab", "triggers[0]",
			"When WeatherStation lab Safe=False: state: Parked: Dome lab refused to park, because Mount east is unparked or moving. " +
				"In Observatory lab, a dome does not park until every mount parks. " +
				"In a trigger, set after: [{kind: Mount}] on the dome's park so that it runs after the mounts park"},
		{"an activation's unpark", func(w *world) {
			w.indi.preset("Dome Simulator", "DOME_PARK", "PARK")
			w.indi.preset("Telescope Simulator", "TELESCOPE_PARK", "PARK")
			w.amend(observatory.DomeKind, "lab", func(spec map[string]any) {
				delete(spec, "activation")
				delete(spec, "triggers")
			})
			w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		}, observatory.MountKind, "east", observatory.TriggerActivation,
			"state: Unparked: Mount east refused to unpark, because Dome lab is parked or moving. " +
				"In Observatory lab, a mount does not unpark until every dome unparks"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				c.setup(w)
				run := w.runEnds(c.kind, c.device, c.trigger, 15*time.Minute)
				if run.State != observatory.StepFailed || run.Summary != c.summary {
					t.Errorf("the run is %s %q, want %s %q", run.State, run.Summary, observatory.StepFailed, c.summary)
				}
			})
		})
	}
}
