package main

// The procedures of the two lifecycle triggers: the order the tree
// gives them, the record that a new copy of the operator resumes from,
// and the waits of requires and after.

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// amend changes the spec of one of the example's resources.
func (w *world) amend(kind observatory.Kind, name string, change func(spec map[string]any)) {
	w.t.Helper()
	object, ok := w.api.object(kindCollection(kind), name)
	if !ok {
		w.t.Fatalf("no %s %s", kind.Name, name)
	}
	change(object["spec"].(map[string]any))
	w.api.put(kindCollection(kind), object)
}

// procedureEvents counts the Events of one reason on one resource,
// with the repeats that the recorder folds into one Event's count.
func procedureEvents(a *fakeAPI, kind observatory.Kind, name, reason string) int {
	n := 0
	for _, e := range a.recorded.About(kind.Name, name) {
		if e.Reason == reason {
			n += int(e.Count)
		}
	}
	return n
}

// Every simulator starts unparked and open, so the presets park the
// dome and the mount and close the cap, and each activation then sends
// its move. The dome unparks before the mount, and the cap opens and
// the camera cools after the mount.
func TestActivationRunsTheTreeFromTheTop(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.indi.preset("Dome Simulator", "DOME_PARK", "PARK")
		w.indi.preset("Telescope Simulator", "TELESCOPE_PARK", "PARK")
		w.indi.preset("Dust Cover Simulator", "CAP_PARK", "PARK")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		r := w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		order := [][2]string{
			{"lab-observatory Dome Simulator.DOME_PARK PARK=Off UNPARK=On", "east-telescope Telescope Simulator.TELESCOPE_PARK PARK=Off UNPARK=On"},
			{"east-telescope Telescope Simulator.TELESCOPE_PARK PARK=Off UNPARK=On", "east-telescope Dust Cover Simulator.CAP_PARK PARK=Off UNPARK=On"},
			{"east-telescope Telescope Simulator.TELESCOPE_PARK PARK=Off UNPARK=On", "east-telescope CCD Simulator.CCD_TEMPERATURE CCD_TEMPERATURE_VALUE=-10"},
		}
		for _, pair := range order {
			if !w.indi.sentBefore(pair[0], pair[1]) {
				t.Errorf("%q was not sent before %q: %v", pair[0], pair[1], w.indi.changes())
			}
		}
		want := "Ran the activation of Observatory lab, Dome lab, Mount east, Camera east-main, DustCap east\n" +
			exampleJobDone + "\n" +
			"Dome lab state: Unparked Done: Unparked Dome lab\n" +
			"Mount east state: Unparked Done: Unparked Mount east\n" +
			"Camera east-main cool: -10 °C within 0.5 °C Done: Cooled Camera east-main to -10 °C\n" +
			"DustCap east state: Open Done: Opened DustCap east"
		if got := stepText(stepOf(r, observatory.StepActivation)); got != want {
			t.Errorf("Activation =\n%s\nwant\n%s", got, want)
		}
		w.until(time.Minute, "the dome's status does not record its run", func() bool {
			dome, _ := decode[observatory.Dome](t, w.api, kindCollection(observatory.DomeKind), "lab")
			site, _ := decode[observatory.Observatory](t, w.api, kindCollection(observatory.ObservatoryKind), "lab")
			active := conditionOf(site.Status.Conditions, observatory.ConditionActive)
			runs := dome.Status.Procedures
			return active.Status == observatory.ConditionTrue && len(runs) == 1 && runs[0].Trigger == observatory.TriggerActivation &&
				runs[0].State == observatory.StepDone && runs[0].Since != nil && runs[0].Since.Equal(active.LastTransitionTime)
		})
	})
}

// Active keeps its transition time across an operator restart, so the
// new copy runs no procedure again. A second telescope in the
// observatory finds it Active, and runs only its own tiers.
func TestActiveSurvivesARestart(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		time.Sleep(2 * statusWindow)
		synctest.Wait()
		active := func() (observatory.Condition, observatory.Condition) {
			east, _ := decode[observatory.Telescope](t, w.api, kindCollection(observatory.TelescopeKind), "east")
			lab, _ := decode[observatory.Observatory](t, w.api, kindCollection(observatory.ObservatoryKind), "lab")
			return conditionOf(east.Status.Conditions, observatory.ConditionActive), conditionOf(lab.Status.Conditions, observatory.ConditionActive)
		}
		eastBefore, labBefore := active()
		if eastBefore.Status != observatory.ConditionTrue || labBefore.Status != observatory.ConditionTrue {
			t.Fatalf("Active = %+v and %+v, want both True", eastBefore, labBefore)
		}

		w.restart()
		w.reserve("west-tonight", map[string]any{"telescope": "west", "holder": "desktop"})
		w.phase("west-tonight", observatory.ReservationReady, 10*time.Minute)
		time.Sleep(2 * statusWindow)
		synctest.Wait()
		eastAfter, labAfter := active()
		if !eastAfter.LastTransitionTime.Equal(eastBefore.LastTransitionTime) || !labAfter.LastTransitionTime.Equal(labBefore.LastTransitionTime) {
			t.Errorf("Active moved from %v and %v to %v and %v", eastBefore.LastTransitionTime, labBefore.LastTransitionTime,
				eastAfter.LastTransitionTime, labAfter.LastTransitionTime)
		}
		for _, d := range []struct {
			kind observatory.Kind
			name string
		}{{observatory.DomeKind, "lab"}, {observatory.MountKind, "east"}, {observatory.MountKind, "west"}} {
			if n := procedureEvents(w.api, d.kind, d.name, reasonProcedureStarted); n != 1 {
				t.Errorf("%s %s started %d runs, want 1", d.kind.Name, d.name, n)
			}
		}
	})
}

// An operator that stops in the middle of a run leaves it Running, and
// the next copy runs only the actions that are not Done.
func TestANewOperatorResumesARunFromItsRecord(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.amend(observatory.CameraKind, "east-main", func(spec map[string]any) {
			spec["activation"] = []any{
				map[string]any{"cool": map[string]any{"celsius": -10}},
				map[string]any{"cool": map[string]any{"celsius": -5},
					"requires": []any{map[string]any{"kind": "Mount", "name": "east", "type": "Tracking"}}},
			}
		})
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.until(time.Minute, "the second cool does not wait", func() bool {
			r, _ := w.reservation("east-tonight")
			return strings.Contains(stepText(stepOf(r, observatory.StepActivation)),
				"Camera east-main cool: -5 °C within 0.5 °C Running: Waiting for Mount east Tracking=True")
		})
		time.Sleep(2 * statusWindow)

		w.restart()
		w.indi.ask("east-telescope", "Telescope Simulator", "TELESCOPE_TRACK_STATE", "TRACK_ON=On", "TRACK_OFF=Off")
		r := w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		if n := w.indi.count("east-telescope", "CCD Simulator.CCD_TEMPERATURE"); n != 2 {
			t.Errorf("the camera received %d setpoints, want -10 once and -5 once", n)
		}
		if n := procedureEvents(w.api, observatory.CameraKind, "east-main", reasonProcedureStarted); n != 1 {
			t.Errorf("the camera's run started %d times, want once", n)
		}
		want := "Camera east-main cool: -5 °C within 0.5 °C Done: Cooled Camera east-main to -5 °C"
		if text := stepText(stepOf(r, observatory.StepActivation)); !strings.Contains(text, want) {
			t.Errorf("Activation =\n%s\nwant %q", text, want)
		}
	})
}

// An action that fails fails its run and the step, and the retry
// annotation runs the step again: the failed run runs again, and a run
// that is Done does not.
func TestAFailedActionFailsTheStepUntilARetry(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.indi.refuse("CCD Simulator", "CCD_TEMPERATURE")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		r := w.phase("east-tonight", observatory.ReservationFailed, 10*time.Minute)
		want := "Failed: Camera east-main: cool: -10 °C within 0.5 °C: Camera east-main: indi: CCD Simulator.CCD_TEMPERATURE is Alert"
		if step := stepOf(r, observatory.StepActivation); step.State != observatory.StepFailed || step.Summary != want {
			t.Errorf("Activation = %s %q, want Failed %q", step.State, step.Summary, want)
		}
		if n := procedureEvents(w.api, observatory.CameraKind, "east-main", reasonProcedureFailed); n != 1 {
			t.Errorf("the camera posted %d ProcedureFailed Events, want 1", n)
		}

		w.indi.accept("CCD Simulator", "CCD_TEMPERATURE")
		w.retry("east-tonight")
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		if n := procedureEvents(w.api, observatory.CameraKind, "east-main", reasonProcedureStarted); n != 2 {
			t.Errorf("the camera's run started %d times, want twice", n)
		}
		for _, d := range []struct {
			kind observatory.Kind
			name string
		}{{observatory.DomeKind, "lab"}, {observatory.MountKind, "east"}} {
			if n := procedureEvents(w.api, d.kind, d.name, reasonProcedureStarted); n != 1 {
				t.Errorf("the run of %s %s started %d times, want once", d.kind.Name, d.name, n)
			}
		}
	})
}

// retry annotates a failed reservation, as a person does to run its
// failed step again.
func (w *world) retry(name string) {
	object, _ := w.api.object(kindCollection(observatory.ReservationKind), name)
	object["metadata"].(map[string]any)["annotations"] = map[string]any{annotationRetry: "1"}
	w.api.mu.Lock()
	w.api.store(kindCollection(observatory.ReservationKind), name, object, "MODIFIED")
	w.api.mu.Unlock()
}

// requires holds an action until its condition holds, and fails it at
// its timeout.
func TestRequiresWaitsForItsCondition(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		// unsafe turns the weather unsafe while the mount waits.
		unsafe bool
		phase  observatory.ReservationPhase
		want   string
	}{
		{"a condition that comes", true, observatory.ReservationReady,
			"Ran the activation of Observatory lab, Dome lab, Mount east, Camera east-main, DustCap east"},
		{"a condition that never comes", false, observatory.ReservationFailed,
			"Failed: Mount east: state: Unparked: timed out after 10 min: waiting for WeatherStation lab Safe=False"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.amend(observatory.MountKind, "east", func(spec map[string]any) {
					spec["activation"] = []any{map[string]any{"state": "Unparked",
						"requires": []any{map[string]any{"kind": "WeatherStation", "name": "lab", "type": "Safe", "status": "False"}}}}
				})
				w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
				w.until(time.Minute, "the mount does not wait", func() bool {
					r, _ := w.reservation("east-tonight")
					return stepOf(r, observatory.StepActivation).Summary == "Mount east: waiting for WeatherStation lab Safe=False"
				})
				if c.unsafe {
					w.indi.setState("lab-observatory", "Weather Simulator", "SAFETY_STATUS", "Alert")
				}
				r := w.phase("east-tonight", c.phase, 15*time.Minute)
				if got := stepOf(r, observatory.StepActivation).Summary; got != c.want {
					t.Errorf("Activation = %q, want %q", got, c.want)
				}
			})
		})
	}
}

// after waits for the runs of the same step. A resource with no run in
// the step is not waited for, and a resource in a later tier is waited
// for until the action's timeout.
func TestAfterWaitsForTheRunsOfTheSameStep(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		after map[string]any
		phase observatory.ReservationPhase
		want  string
	}{
		{"a resource with no procedure", map[string]any{"kind": "FilterWheel", "name": "east"}, observatory.ReservationReady,
			"Ran the activation of Observatory lab, Dome lab, Mount east, Camera east-main, DustCap east"},
		{"a resource in a later tier", map[string]any{"kind": "Mount", "name": "east"}, observatory.ReservationFailed,
			"Failed: Dome lab: state: Unparked: timed out after 2 min: waiting for Mount east"},
		{"every mount, which run in a later tier", map[string]any{"kind": "Mount"}, observatory.ReservationFailed,
			"Failed: Dome lab: state: Unparked: timed out after 2 min: waiting for Mount east"},
		{"a resource that does not exist", map[string]any{"kind": "Mount", "name": "north"}, observatory.ReservationFailed,
			"Failed: Dome lab: state: Unparked: after[0]: no Mount north"},
		{"a telescope of a device that has none", map[string]any{"kind": "Telescope"}, observatory.ReservationFailed,
			"Failed: Dome lab: state: Unparked: after[0]: Dome lab belongs to no Telescope"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.amend(observatory.DomeKind, "lab", func(spec map[string]any) {
					spec["activation"] = []any{map[string]any{"state": "Unparked", "timeout": "2m", "after": []any{c.after}}}
				})
				w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
				r := w.phase("east-tonight", c.phase, 10*time.Minute)
				if got := stepOf(r, observatory.StepActivation).Summary; got != c.want {
					t.Errorf("Activation = %q, want %q", got, c.want)
				}
			})
		})
	}
}

// Deactivation skips the action of a device that is not connected,
// such as a camera that a person disconnected in KStars.
func TestDeactivationSkipsADeviceThatIsNotConnected(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.ask("east-telescope", "CCD Simulator", "CONNECTION", "CONNECT=Off", "DISCONNECT=On")
		at := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "end": at})
		r := w.phase("east-tonight", observatory.ReservationReleased, 10*time.Minute)
		want := "Camera east-main warm: 5 °C within 0.5 °C Skipped: Camera east-main is not connected"
		if text := stepText(stepOf(r, observatory.StepDeactivation)); !strings.Contains(text, want) {
			t.Errorf("Deactivation =\n%s\nwant %q", text, want)
		}
	})
}

// The observatory stays Active while another telescope in it is
// Active, and the last deactivation parks the dome.
func TestTheLastTelescopeDeactivatesTheObservatory(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := bothReady(t)
		end := func(name, telescope string) observatory.Reservation {
			at := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
			w.reserve(name, map[string]any{"telescope": telescope, "holder": "desktop", "end": at})
			return w.phase(name, observatory.ReservationReleased, 10*time.Minute)
		}
		east := end("east-tonight", "east")
		if text := stepText(stepOf(east, observatory.StepDeactivation)); !strings.Contains(text, "Observatory lab stays active for Telescope west") {
			t.Errorf("east's Deactivation =\n%s", text)
		}
		if n := w.indi.count("lab-observatory", "Dome Simulator.DOME_PARK"); n != 0 {
			t.Errorf("the dome received %d parks while west is Active", n)
		}
		west := end("west-tonight", "west")
		if text := stepText(stepOf(west, observatory.StepDeactivation)); !strings.Contains(text, "Dome lab state: Parked Done: Parked Dome lab") {
			t.Errorf("west's Deactivation =\n%s", text)
		}
		if !w.indi.sentBefore("west-telescope Telescope Simulator.TELESCOPE_PARK PARK=On", "lab-observatory Dome Simulator.DOME_PARK PARK=On") {
			t.Errorf("the dome did not park after the west mount: %v", w.indi.changes())
		}
	})
}

// A cooler cannot warm a sensor above the air around it, so a warm-up
// that does not reach its setpoint ends at its timeout, notes where the
// sensor is, and switches the cooler off.
func TestAWarmUpEndsAtItsTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.hold("CCD Simulator", "CCD_TEMPERATURE")
		at := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "end": at})
		r := w.phase("east-tonight", observatory.ReservationReleased, 20*time.Minute)
		want := "Camera east-main warm: 5 °C within 0.5 °C Done: Warmed Camera east-main to -10 °C by the timeout; found the cooler of Camera east-main off"
		if text := stepText(stepOf(r, observatory.StepDeactivation)); !strings.Contains(text, want) {
			t.Errorf("Deactivation =\n%s\nwant %q", text, want)
		}
		if step := stepOf(r, observatory.StepDeactivation); step.StopTime.Sub(*step.StartTime) < observatory.WarmTimeout {
			t.Errorf("the Deactivation step took %v, want the warm-up's timeout", step.StopTime.Sub(*step.StartTime))
		}
	})
}
