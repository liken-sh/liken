package main

// The triggers of the example: the mounts park and then the dome
// parks when the weather turns unsafe, and the dome unparks after 20
// minutes of safe weather.

import (
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
		if e.Reason == reasonProcedureStarted && strings.HasPrefix(e.Message, "Procedure "+trigger+" started") {
			n += int(e.Count)
		}
	}
	return n
}

// lastRun answers the record of one trigger's last run on a resource,
// as the status writer wrote it.
func lastRun(t *testing.T, w *world, kind observatory.Kind, name, trigger string) observatory.ProcedureRun {
	d, _ := decode[deviceObject](t, w.api, kindCollection(kind), name)
	for _, run := range d.Status.Procedures {
		if run.Trigger == trigger {
			return run
		}
	}
	return observatory.ProcedureRun{}
}

// weather sets the weather station's verdict, as the simulator reports
// it: Ok is Safe, and Alert is Danger.
func (w *world) weather(state string) {
	w.indi.setState("lab-observatory", "Weather Simulator", "SAFETY_STATUS", state)
}

// runEnds waits until one trigger's last run on a resource ended.
func (w *world) runEnds(kind observatory.Kind, name, trigger string, limit time.Duration) observatory.ProcedureRun {
	w.t.Helper()
	var run observatory.ProcedureRun
	w.until(limit, kind.Name+" "+name+" has not ended a run of "+trigger, func() bool {
		run = lastRun(w.t, w, kind, name, trigger)
		return run.StopTime != nil
	})
	return run
}

// The dome's park waits for the mount's run on the same transition,
// so the park lock lets the dome park. The west telescope is not
// reserved, so its mount runs nothing, and the dome does not wait for
// it.
func TestUnsafeWeatherParksTheMountsAndThenTheDome(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.weather("Alert")
		dome := w.runEnds(observatory.DomeKind, "lab", "triggers[0]", time.Minute)
		if dome.State != observatory.StepDone || dome.Summary != "Parked Dome lab" {
			t.Errorf("the dome's run = %s %q", dome.State, dome.Summary)
		}
		if !w.indi.sentBefore("east-telescope Telescope Simulator.TELESCOPE_PARK PARK=On", "lab-observatory Dome Simulator.DOME_PARK PARK=On") {
			t.Errorf("the dome did not park after the mount: %v", w.indi.changes())
		}
		if got := eventsAbout(w.api, observatory.DomeKind, "lab", reasonDomeParkRefused); len(got) != 0 {
			t.Errorf("refusals = %v", got)
		}
		station, _ := decode[observatory.WeatherStation](t, w.api, kindCollection(observatory.WeatherStationKind), "lab")
		unsafe := conditionOf(station.Status.Conditions, observatory.ConditionSafe)
		if mount := lastRun(t, w, observatory.MountKind, "east", "triggers[0]"); mount.Since == nil || !mount.Since.Equal(unsafe.LastTransitionTime) {
			t.Errorf("the mount's run answers %v, want the transition at %v", mount.Since, unsafe.LastTransitionTime)
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
			t.Errorf("the mount's trigger ran %d times for one transition", n)
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
		if second.Summary != "Found Mount east parked" {
			t.Errorf("the second run = %q", second.Summary)
		}
	})
}

// for delays a run until the status has held that long, and a change
// of the status before then cancels it. So the dome unparks only after
// 20 minutes of safe weather.
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
		if run.State != observatory.StepDone || run.Summary != "Unparked Dome lab" {
			t.Errorf("the dome's unpark = %s %q", run.State, run.Summary)
		}
		if waited := run.StartTime.Sub(safe); waited < 20*time.Minute || waited > 21*time.Minute {
			t.Errorf("the dome unparked %v after the weather turned safe, want 20 min", waited)
		}
	})
}

// A condition that holds when a resource's activation ends runs its
// trigger then. The cap's activation waits for unsafe weather here, so
// the condition holds when the activation ends.
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
		if run.State != observatory.StepDone || run.Summary != "Closed DustCap east" || run.StartTime.Before(*activation.StopTime) {
			t.Errorf("the cap's trigger = %+v, after its activation ended at %v", run, activation.StopTime)
		}
	})
}

// A run whose condition changes before it ends stops, and its record
// says why. The dome's park, which waits for the mount's run, stops
// too, and parks nothing.
func TestARunStopsWhenItsConditionChanges(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.hold("Telescope Simulator", "TELESCOPE_PARK")
		w.weather("Alert")
		w.until(time.Minute, "the mount does not park", func() bool {
			return lastRun(t, w, observatory.MountKind, "east", "triggers[0]").State == observatory.StepRunning
		})
		w.weather("Ok")
		for _, d := range []struct {
			kind observatory.Kind
			name string
		}{{observatory.MountKind, "east"}, {observatory.DomeKind, "lab"}} {
			run := w.runEnds(d.kind, d.name, "triggers[0]", time.Minute)
			if run.State != observatory.StepSkipped || run.Summary != "WeatherStation lab Safe is no longer False" {
				t.Errorf("the run of %s %s = %s %q", d.kind.Name, d.name, run.State, run.Summary)
			}
		}
		if n := w.indi.count("lab-observatory", "Dome Simulator.DOME_PARK"); n != 0 {
			t.Errorf("the dome received %d parks", n)
		}
	})
}

// A run that goes on when its resource's deactivation begins stops,
// and the deactivation's own procedure runs.
func TestDeactivationStopsATriggersRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.hold("Telescope Simulator", "TELESCOPE_PARK")
		w.weather("Alert")
		w.until(time.Minute, "the mount does not park", func() bool {
			return lastRun(t, w, observatory.MountKind, "east", "triggers[0]").State == observatory.StepRunning
		})
		// The held park stays unanswered, and the deactivation's park
		// after the release is answered.
		w.indi.release("Telescope Simulator", "TELESCOPE_PARK")
		at := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "end": at})
		run := w.runEnds(observatory.MountKind, "east", "triggers[0]", 5*time.Minute)
		if run.State != observatory.StepSkipped || run.Summary != "The activity of Mount east ended" {
			t.Errorf("the mount's trigger = %s %q", run.State, run.Summary)
		}
		r := w.phase("east-tonight", observatory.ReservationReleased, 15*time.Minute)
		if text := stepText(stepOf(r, observatory.StepDeactivation)); !strings.Contains(text, "Mount east state: Parked Done: Parked Mount east") {
			t.Errorf("Deactivation =\n%s", text)
		}
	})
}

// An operator that stops during a trigger's run leaves it Running, and
// the next copy resumes it with no second start.
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
			t.Errorf("the mount's run = %s %q", run.State, run.Summary)
		}
		if n := startedRuns(w.api, observatory.MountKind, "east", "triggers[0]"); n != 1 {
			t.Errorf("the mount's run started %d times, want once", n)
		}
		if n := w.indi.count("east-telescope", "Telescope Simulator.TELESCOPE_PARK"); n != 2 {
			t.Errorf("the mount received %d parks, want the held one and one from the new copy", n)
		}
	})
}

// A trigger whose condition names nothing records one failed run, and
// posts one Warning.
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
			t.Errorf("the mount's trigger = %s %q", run.State, run.Summary)
		}
		if n := procedureEvents(w.api, observatory.MountKind, "east", reasonProcedureFailed); n != 1 {
			t.Errorf("the mount posted %d ProcedureFailed Events, want 1", n)
		}
	})
}
