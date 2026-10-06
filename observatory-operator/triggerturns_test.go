package main

// A resource runs one procedure at a time. A trigger's run that
// becomes due while another run of its resource goes on waits for it,
// and begins only while its condition still holds with the same
// transition time.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// parkingDome answers a world whose dome parks for the unsafe weather,
// slowly, while its unpark after 20 minutes of safe weather is due and
// waits for the park to end. The park's timeout of 30 minutes outlasts
// the unpark's for.
func parkingDome(t *testing.T) *world {
	w := startWorld(t)
	station := func(extra map[string]any) map[string]any {
		when := map[string]any{"kind": "WeatherStation", "name": "lab", "type": "Safe"}
		for k, v := range extra {
			when[k] = v
		}
		return when
	}
	w.amend(observatory.DomeKind, "lab", func(spec map[string]any) {
		spec["triggers"] = []any{
			map[string]any{"when": station(map[string]any{"status": "False"}),
				"run": []any{map[string]any{"state": "Parked", "after": []any{map[string]any{"kind": "Mount"}}, "timeout": "30m"}}},
			map[string]any{"when": station(map[string]any{"for": "20m"}),
				"run": []any{map[string]any{"state": "Unparked"}}},
		}
	})
	w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
	w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
	w.indi.move("Dome Simulator", "DOME_PARK")
	w.weather("Alert")
	w.until(time.Minute, "the dome does not park", func() bool {
		return w.indi.count("lab-observatory", "Dome Simulator.DOME_PARK") == 1
	})
	w.weather("Ok")
	time.Sleep(21 * time.Minute)
	synctest.Wait()
	if run := lastRun(t, w, observatory.DomeKind, "lab", "triggers[1]"); run.State != observatory.StepPending || run.Summary != "When WeatherStation lab Safe=True for 20m: waiting for the run of triggers[0] (WeatherStation lab Safe=False) to end" {
		t.Fatalf("the dome's unpark = %s %q", run.State, run.Summary)
	}
	return w
}

// The unpark begins when the park ends, so the dome never receives two
// targets at once.
func TestARunThatBecomesDueWaitsForTheRunOfItsResource(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := parkingDome(t)
		w.indi.setState("lab-observatory", "Dome Simulator", "DOME_PARK", "Ok")
		w.until(time.Minute, "the dome does not unpark", func() bool {
			return w.indi.count("lab-observatory", "Dome Simulator.DOME_PARK") == 2
		})
		w.indi.setState("lab-observatory", "Dome Simulator", "DOME_PARK", "Ok")
		park := w.runEnds(observatory.DomeKind, "lab", "triggers[0]", time.Minute)
		unpark := w.runEnds(observatory.DomeKind, "lab", "triggers[1]", time.Minute)
		if park.State != observatory.StepDone || unpark.State != observatory.StepDone || unpark.Summary != "When WeatherStation lab Safe=True for 20m: unparked Dome lab" {
			t.Errorf("the park = %s %q, the unpark = %s %q", park.State, park.Summary, unpark.State, unpark.Summary)
		}
		if unpark.StartTime.Before(*park.StopTime) {
			t.Errorf("the unpark began at %v, before the park ended at %v", unpark.StartTime, park.StopTime)
		}
	})
}

// The weather turns unsafe again while the unpark waits, so the unpark
// does not begin, and its record says why.
func TestARunWhoseConditionChangedWhileItWaitedDoesNotBegin(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := parkingDome(t)
		w.weather("Alert")
		w.settle()
		w.indi.setState("lab-observatory", "Dome Simulator", "DOME_PARK", "Ok")
		unpark := w.runEnds(observatory.DomeKind, "lab", "triggers[1]", time.Minute)
		if unpark.State != observatory.StepSkipped || unpark.Summary != "When WeatherStation lab Safe=True for 20m: WeatherStation lab Safe is no longer True, so the run did not begin" {
			t.Errorf("the unpark = %s %q", unpark.State, unpark.Summary)
		}
		if n := w.indi.count("lab-observatory", "Dome Simulator.DOME_PARK"); n != 1 {
			t.Errorf("the dome received %d park changes, want 1", n)
		}
	})
}

// The deactivation ends the run that goes on and the run that waits,
// and the dome's own deactivation then takes its turn.
func TestDeactivationEndsARunThatWaits(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := parkingDome(t)
		at := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "end": at})
		for _, c := range []struct{ trigger, summary string }{
			{"triggers[0]", "When WeatherStation lab Safe=False: the activity of Dome lab ended"},
			{"triggers[1]", "When WeatherStation lab Safe=True for 20m: the activity of Dome lab ended, so the run did not begin"},
		} {
			if run := w.runEnds(observatory.DomeKind, "lab", c.trigger, 5*time.Minute); run.State != observatory.StepSkipped || run.Summary != c.summary {
				t.Errorf("%s = %s %q", c.trigger, run.State, run.Summary)
			}
		}
		w.indi.setState("lab-observatory", "Dome Simulator", "DOME_PARK", "Ok")
		if run := w.runEnds(observatory.DomeKind, "lab", observatory.TriggerDeactivation, 15*time.Minute); run.State != observatory.StepDone {
			t.Errorf("the dome's deactivation = %s %q", run.State, run.Summary)
		}
	})
}
