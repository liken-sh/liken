package main

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// parkingDome returns a world in which the dome is parking because the
// weather was unsafe, and the unpark that 20 minutes of safe weather
// triggers waits for the park to end. A resource runs one procedure at
// a time, and a run that waits begins only if its trigger's condition
// still has the same transition time. The park's 30-minute timeout is
// longer than the unpark's 20 minutes, so the park is still running
// when the unpark becomes due.
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
		t.Fatalf("the dome's unpark is %s %q, want Pending until the park ends", run.State, run.Summary)
	}
	return w
}

// The unpark begins after the park ends, so the dome never receives two
// conflicting targets at once.
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
			t.Errorf("the park is %s %q and the unpark is %s %q, want both Done and the unpark to have unparked the Dome", park.State, park.Summary, unpark.State, unpark.Summary)
		}
		if unpark.StartTime.Before(*park.StopTime) {
			t.Errorf("the unpark began at %v, want it after the park ended at %v", unpark.StartTime, park.StopTime)
		}
	})
}

// The weather turns unsafe again while the unpark waits, so the unpark
// is Skipped with the reason in its summary.
func TestARunWhoseConditionChangedWhileItWaitedDoesNotBegin(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := parkingDome(t)
		w.weather("Alert")
		w.settle()
		w.indi.setState("lab-observatory", "Dome Simulator", "DOME_PARK", "Ok")
		unpark := w.runEnds(observatory.DomeKind, "lab", "triggers[1]", time.Minute)
		if unpark.State != observatory.StepSkipped || unpark.Summary != "When WeatherStation lab Safe=True for 20m: WeatherStation lab Safe is no longer True, so the run did not begin" {
			t.Errorf("the unpark is %s %q, want Skipped with the reason that the weather is no longer Safe", unpark.State, unpark.Summary)
		}
		if n := w.indi.count("lab-observatory", "Dome Simulator.DOME_PARK"); n != 1 {
			t.Errorf("the dome received %d park changes, want 1", n)
		}
	})
}

// Deactivation skips the run in progress and the run that waits, and
// then the dome's own deactivation procedure runs.
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
				t.Errorf("%s is %s %q, want Skipped %q", c.trigger, run.State, run.Summary, c.summary)
			}
		}
		w.indi.setState("lab-observatory", "Dome Simulator", "DOME_PARK", "Ok")
		if run := w.runEnds(observatory.DomeKind, "lab", observatory.TriggerDeactivation, 15*time.Minute); run.State != observatory.StepDone {
			t.Errorf("the dome's deactivation is %s %q, want Done", run.State, run.Summary)
		}
	})
}
