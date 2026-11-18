package main

// Tests of the example's triggers. When the weather turns unsafe, the
// mounts park and then the dome parks. After 20 minutes of safe
// weather, the dome unparks.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// startedRuns counts the runs of one trigger that started on one
// resource, from its ProcedureStarted Events.
func startedRuns(a *fakeAPI, kind observatory.Kind, name, trigger string) int {
	n := 0
	for _, e := range a.recorded.About(kind.Name, name) {
		if e.Reason == reasonProcedureStarted && strings.HasPrefix(e.Message, "Procedure "+trigger+" ") {
			n += int(e.Count)
		}
	}
	return n
}

// lastRun returns the record of one trigger's last run on a resource
// from the resource's stored status.
func lastRun(t *testing.T, w *world, kind observatory.Kind, name, trigger string) observatory.ProcedureRun {
	d, _ := decode[deviceObject](t, w.api, kindCollection(kind), name)
	for _, run := range d.Status.Procedures {
		if run.Trigger == trigger {
			return run
		}
	}
	return observatory.ProcedureRun{}
}

// weather sets the weather simulator's SAFETY_STATUS state. Ok makes
// the station Safe, and Alert makes it unsafe.
func (w *world) weather(state string) {
	w.indi.setState("lab-observatory", "Weather Simulator", "SAFETY_STATUS", state)
}

// runEnds waits until one trigger's last run on a resource has a stop
// time, and returns the run.
func (w *world) runEnds(kind observatory.Kind, name, trigger string, limit time.Duration) observatory.ProcedureRun {
	w.t.Helper()
	var run observatory.ProcedureRun
	w.until(limit, kind.Name+" "+name+" has not ended a run of "+trigger, func() bool {
		run = lastRun(w.t, w, kind, name, trigger)
		return run.StopTime != nil
	})
	return run
}

// The dome's park waits for the mount's run for the same transition.
// The west telescope is not reserved, so its mount runs nothing, and
// the dome does not wait for it.
func TestUnsafeWeatherParksTheMountsAndThenTheDome(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.weather("Alert")
		dome := w.runEnds(observatory.DomeKind, "lab", "triggers[0]", time.Minute)
		if dome.State != observatory.StepDone || dome.Summary != "When WeatherStation lab Safe=False: parked Dome lab" {
			t.Errorf("the dome's run is %s %q, want Done with the summary \"When WeatherStation lab Safe=False: parked Dome lab\"", dome.State, dome.Summary)
		}
		if !w.indi.sentBefore("east-telescope Telescope Simulator.TELESCOPE_PARK PARK=On", "lab-observatory Dome Simulator.DOME_PARK PARK=On") {
			t.Errorf("the INDI changes are %v, want the mount parked before the dome", w.indi.changes())
		}
		if got := eventsAbout(w.api, observatory.DomeKind, "lab", reasonDomeParkRefused); len(got) != 0 {
			t.Errorf("the dome has %d park refusals, want none: %v", len(got), got)
		}
		station, _ := decode[observatory.WeatherStation](t, w.api, kindCollection(observatory.WeatherStationKind), "lab")
		unsafe := conditionOf(station.Status.Conditions, observatory.ConditionSafe)
		if mount := lastRun(t, w, observatory.MountKind, "east", "triggers[0]"); mount.Since == nil || !mount.Since.Equal(unsafe.LastTransitionTime) {
			t.Errorf("the mount's run started for the transition at %v, want the transition at %v", mount.Since, unsafe.LastTransitionTime)
		}
	})
}

// A trigger's run names its condition in its record and its Events, so
// a person can see what started it. The record uses the trigger's name
// as its key.
func TestATriggersRunNamesItsCondition(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.weather("Alert")
		run := w.runEnds(observatory.DomeKind, "lab", "triggers[0]", time.Minute)
		if run.Trigger != "triggers[0]" || run.Summary != "When WeatherStation lab Safe=False: parked Dome lab" {
			t.Errorf("the dome's run is trigger %q with summary %q, want triggers[0] and \"When WeatherStation lab Safe=False: parked Dome lab\"", run.Trigger, run.Summary)
		}
		got := w.eventsWith(observatory.DomeKind, "lab", reasonProcedureStarted, reasonProcedureDone)
		want := []string{
			"Normal ProcedureStarted: Procedure activation started: state: Unparked x1",
			"Normal ProcedureDone: Procedure activation done in 1 s: found Dome lab unparked and opened its shutter x1",
			"Normal ProcedureStarted: Procedure triggers[0] (WeatherStation lab Safe=False) started: state: Parked x1",
			"Normal ProcedureDone: Procedure triggers[0] (WeatherStation lab Safe=False) done in 0 s: parked Dome lab x1",
		}
		if !slices.Equal(got, want) {
			t.Errorf("the dome's Events =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	})
}

// A trigger runs once for each transition of its condition, however
// often the status writer writes the condition.
func TestATriggerRunsOnceForEachTransition(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.weather("Alert")
		first := w.runEnds(observatory.MountKind, "east", "triggers[0]", time.Minute)
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if n := startedRuns(w.api, observatory.MountKind, "east", "triggers[0]"); n != 1 {
			t.Errorf("the mount's trigger ran %d times for one transition, want once", n)
		}
		w.weather("Ok")
		time.Sleep(time.Minute)
		w.weather("Alert")
		w.until(time.Minute, "the second transition runs nothing", func() bool {
			return startedRuns(w.api, observatory.MountKind, "east", "triggers[0]") == 2
		})
		var second observatory.ProcedureRun
		w.until(time.Minute, "the second run does not end", func() bool {
			second = lastRun(t, w, observatory.MountKind, "east", "triggers[0]")
			return second.StopTime != nil && second.Since.After(*first.Since)
		})
		if second.Summary != "When WeatherStation lab Safe=False: found Mount east parked" {
			t.Errorf("the summary of the second run is %q, want \"When WeatherStation lab Safe=False: found Mount east parked\"", second.Summary)
		}
	})
}

// A trigger's for delays its run until the condition has kept its
// status that long, and a change of status before then cancels the
// wait. So the dome unparks only after 20 minutes of safe weather.
func TestForWaitsOutAFlap(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		const unpark = "Dome Simulator.DOME_PARK"
		w.weather("Alert")
		w.runEnds(observatory.DomeKind, "lab", "triggers[0]", time.Minute)
		parks := w.indi.count("lab-observatory", unpark)

		w.weather("Ok")
		time.Sleep(19 * time.Minute)
		w.weather("Alert")
		time.Sleep(time.Minute)
		synctest.Wait()
		if n := w.indi.count("lab-observatory", unpark); n != parks {
			t.Errorf("the dome received %d park changes during the flap, want none", n-parks)
		}

		w.weather("Ok")
		safe := time.Now()
		run := w.runEnds(observatory.DomeKind, "lab", "triggers[1]", 25*time.Minute)
		if run.State != observatory.StepDone || run.Summary != "When WeatherStation lab Safe=True for 20m: unparked Dome lab" {
			t.Errorf("the dome's unpark run is %s %q, want Done with the summary \"When WeatherStation lab Safe=True for 20m: unparked Dome lab\"", run.State, run.Summary)
		}
		if waited := run.StartTime.Sub(safe); waited < 20*time.Minute || waited > 21*time.Minute {
			t.Errorf("the dome unparked %v after the weather turned safe, want between 20m and 21m", waited)
		}
	})
}

// When a trigger's condition already has its status at the end of the
// resource's activation, the trigger runs then. Here the cap's
// activation waits for unsafe weather, so the condition is already
// False when the activation ends.
func TestATriggerWhoseConditionHoldsAtActivationRunsThen(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		unsafe := map[string]any{"kind": "WeatherStation", "name": "lab", "type": "Safe", "status": "False"}
		w.amend(observatory.DustCapKind, "east", func(spec map[string]any) {
			spec["activation"] = []any{map[string]any{"state": "Open", "requires": []any{unsafe}}}
			spec["triggers"] = []any{map[string]any{"when": unsafe, "run": []any{map[string]any{"state": "Closed"}}}}
		})
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.until(time.Minute, "the cap does not wait", func() bool {
			r, _ := w.reservation("east-tonight")
			return stepOf(r, observatory.StepActivation).Summary == "DustCap east: waiting for WeatherStation lab Safe=False"
		})
		w.weather("Alert")
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		run := w.runEnds(observatory.DustCapKind, "east", "triggers[0]", time.Minute)
		activation := lastRun(t, w, observatory.DustCapKind, "east", observatory.TriggerActivation)
		if run.State != observatory.StepDone || run.Summary != "When WeatherStation lab Safe=False: closed DustCap east" || run.StartTime.Before(*activation.StopTime) {
			t.Errorf("the cap's trigger run is %+v, want Done with the summary \"When WeatherStation lab Safe=False: closed DustCap east\", started after its activation ended at %v", run, activation.StopTime)
		}
	})
}

// A run that has started continues to its end when its condition
// changes in the meantime. The dome's park waits for the mount's run
// for the same transition, and runs after it.
func TestARunThatStartedRunsToItsEnd(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.move("Telescope Simulator", "TELESCOPE_PARK")
		w.weather("Alert")
		w.until(time.Minute, "the mount does not park", func() bool {
			return lastRun(t, w, observatory.MountKind, "east", "triggers[0]").State == observatory.StepRunning
		})
		w.weather("Ok")
		time.Sleep(time.Minute)
		synctest.Wait()
		w.indi.setState("east-telescope", "Telescope Simulator", "TELESCOPE_PARK", "Ok")
		for _, d := range []struct {
			kind    observatory.Kind
			name    string
			summary string
		}{{observatory.MountKind, "east", "When WeatherStation lab Safe=False: parked Mount east"},
			{observatory.DomeKind, "lab", "When WeatherStation lab Safe=False: parked Dome lab"}} {
			run := w.runEnds(d.kind, d.name, "triggers[0]", time.Minute)
			if run.State != observatory.StepDone || run.Summary != d.summary {
				t.Errorf("the run of %s %s is %s %q, want Done %q", d.kind.Name, d.name, run.State, run.Summary, d.summary)
			}
		}
	})
}

// A run that is in progress when its resource's deactivation begins is
// canceled, and the deactivation's own procedure runs.
func TestDeactivationStopsATriggersRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.hold("Telescope Simulator", "TELESCOPE_PARK")
		w.weather("Alert")
		w.until(time.Minute, "the mount does not park", func() bool {
			return lastRun(t, w, observatory.MountKind, "east", "triggers[0]").State == observatory.StepRunning
		})
		// The fake driver answers no park until here. Releasing the hold
		// now lets the deactivation's own park finish, and the trigger's
		// run is still in progress when the deactivation begins.
		w.indi.release("Telescope Simulator", "TELESCOPE_PARK")
		at := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "end": at})
		run := w.runEnds(observatory.MountKind, "east", "triggers[0]", 5*time.Minute)
		if run.State != observatory.StepSkipped || run.Summary != "When WeatherStation lab Safe=False: the activity of Mount east ended" {
			t.Errorf("the mount's trigger run is %s %q, want Skipped with the summary \"When WeatherStation lab Safe=False: the activity of Mount east ended\"", run.State, run.Summary)
		}
		r := w.phase("east-tonight", observatory.ReservationReleased, 15*time.Minute)
		if text := stepText(stepOf(r, observatory.StepDeactivation)); !strings.Contains(text, "Mount east state: Parked Done: Parked Mount east") {
			t.Errorf("the Deactivation step reads\n%s\nwant it to contain \"Mount east state: Parked Done: Parked Mount east\"", text)
		}
	})
}

// An operator that stops during a trigger's run leaves the run
// Running, and the next copy resumes it without starting it again.
func TestANewOperatorResumesATriggersRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.hold("Telescope Simulator", "TELESCOPE_PARK")
		w.weather("Alert")
		w.until(time.Minute, "the mount's run is not recorded", func() bool {
			return lastRun(t, w, observatory.MountKind, "east", "triggers[0]").State == observatory.StepRunning
		})
		w.restart()
		w.indi.release("Telescope Simulator", "TELESCOPE_PARK")
		if run := w.runEnds(observatory.MountKind, "east", "triggers[0]", 15*time.Minute); run.State != observatory.StepDone {
			t.Errorf("after the restart, the mount's run is %s %q, want Done", run.State, run.Summary)
		}
		if n := startedRuns(w.api, observatory.MountKind, "east", "triggers[0]"); n != 1 {
			t.Errorf("the mount's run started %d times, want once", n)
		}
		if n := w.indi.count("east-telescope", "Telescope Simulator.TELESCOPE_PARK"); n != 2 {
			t.Errorf("after the restart, the mount received %d parks, want 2: the held one and one from the new operator", n)
		}
	})
}

// A run that an operator restart interrupted resumes even when its
// condition changed while no copy of the operator was running. The new
// copy waits for the park in progress and sends no second park.
func TestANewOperatorResumesARunWhoseConditionChanged(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.move("Telescope Simulator", "TELESCOPE_PARK")
		w.weather("Alert")
		w.until(time.Minute, "the mount's run is not recorded", func() bool {
			return lastRun(t, w, observatory.MountKind, "east", "triggers[0]").State == observatory.StepRunning
		})
		w.halt()
		w.weather("Ok")
		w.start()
		w.until(time.Minute, "the restarted operator does not report the weather safe", func() bool {
			return strings.HasPrefix(w.stateOf(observatory.WeatherStationKind, "lab", observatory.ConditionSafe), "True")
		})
		w.indi.setState("east-telescope", "Telescope Simulator", "TELESCOPE_PARK", "Ok")
		if run := w.runEnds(observatory.MountKind, "east", "triggers[0]", time.Minute); run.State != observatory.StepDone || run.Summary != "When WeatherStation lab Safe=False: parked Mount east" {
			t.Errorf("after the restart, the mount's run is %s %q, want Done with the summary \"When WeatherStation lab Safe=False: parked Mount east\"", run.State, run.Summary)
		}
		if n := w.indi.count("east-telescope", "Telescope Simulator.TELESCOPE_PARK"); n != 1 {
			t.Errorf("after the restart, the mount received %d parks, want 1", n)
		}
	})
}

// A trigger whose condition refers to a resource that does not exist
// records one failed run and posts one Warning.
func TestATriggerOnAConditionOfNothingFailsOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.amend(observatory.MountKind, "east", func(spec map[string]any) {
			spec["triggers"] = []any{map[string]any{"when": map[string]any{"kind": "WeatherStation", "name": "roof", "type": "Safe"},
				"run": []any{map[string]any{"state": "Parked"}}}}
		})
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		time.Sleep(time.Minute)
		synctest.Wait()
		run := lastRun(t, w, observatory.MountKind, "east", "triggers[0]")
		if run.State != observatory.StepFailed || run.Summary != "triggers[0].when: no WeatherStation roof" {
			t.Errorf("the mount's trigger run is %s %q, want Failed with the summary \"triggers[0].when: no WeatherStation roof\"", run.State, run.Summary)
		}
		if n := procedureEvents(w.api, observatory.MountKind, "east", reasonProcedureFailed); n != 1 {
			t.Errorf("the mount posted %d ProcedureFailed Events, want 1", n)
		}
	})
}
