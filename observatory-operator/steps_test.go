package main

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// When a device's pod never becomes Ready, StartDevices fails at its
// deadline, and the Ready condition's message names the step and the
// pod.
func TestAStepThatPassesItsDeadlineFailsTheReservation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.holdPending("east-main-camera")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationActivating, time.Minute)
		began := time.Now()
		r := w.phase("east-tonight", observatory.ReservationFailed, 15*time.Minute)
		step := stepOf(r, observatory.StepStartDevices)
		if step.State != observatory.StepFailed || !strings.Contains(step.Summary, "Timed out after 10 min: ") || !strings.Contains(step.Summary, "east-main-camera") {
			t.Errorf("StartDevices = %+v, want Failed with a summary that starts \"Timed out after 10 min: \" and names east-main-camera", step)
		}
		if waited := time.Since(began); waited < 10*time.Minute-time.Second || waited > 11*time.Minute {
			t.Errorf("the step failed after %v, want its 10-minute deadline", waited)
		}
		ready := conditionOf(r.Status.Conditions, observatory.ConditionReady)
		if ready.Reason != reasonTimedOut || !strings.HasPrefix(ready.Message, "StartDevices timed out after 10 min: ") || !strings.Contains(ready.Message, "Camera east-main") {
			t.Errorf("Ready = %+v, want reason %s and a message that starts \"StartDevices timed out after 10 min: \" and names Camera east-main", ready, reasonTimedOut)
		}
		if got := stepStates(r); !slices.Equal(got[6:], []string{"Configure=Pending", "Activation=Pending", "StartGuider=Pending"}) {
			t.Errorf("the steps after StartDevices are %v, want Configure, Activation, and StartGuider Pending", got)
		}
		if !slices.Contains(w.api.eventReasons(), reasonTimedOut) {
			t.Errorf("events = %v, want one with reason %s", w.api.eventReasons(), reasonTimedOut)
		}
		time.Sleep(2 * statusWindow)
		synctest.Wait()
		if east, _ := decode[observatory.Telescope](t, w.api, kindCollection(observatory.TelescopeKind), "east"); east.Status.Phase != observatory.PhaseError {
			t.Errorf("the telescope is %s, want %s", east.Status.Phase, observatory.PhaseError)
		}
		if camera, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), "east-main"); camera.Status.Phase != observatory.DeviceStarting {
			t.Errorf("the camera whose pod is Pending is %s, want %s", camera.Status.Phase, observatory.DeviceStarting)
		}

		// A delete runs deactivation from StopWork, which stops what
		// activation started.
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(10*time.Minute, "the reservation is not deleted", func() bool {
			_, ok := w.reservation("east-tonight")
			return !ok
		})
		if pods := w.api.names(podsCollection); len(pods) != 0 {
			t.Errorf("pods after the release: %v, want none", pods)
		}
	})
}

// When the camera never reports its cooler setpoint, the cool action
// times out after 20 minutes and fails the Activation step, and the
// step's summary names the camera.
func TestACoolerThatNeverReachesItsSetpointFailsActivation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.indi.hold("CCD Simulator", "CCD_TEMPERATURE")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationActivating, time.Minute)
		began := time.Now()
		r := w.phase("east-tonight", observatory.ReservationFailed, 30*time.Minute)
		step := stepOf(r, observatory.StepActivation)
		want := "Failed: Camera east-main: cool: -10 °C within 0.5 °C: timed out after 20 min: cooling Camera east-main to -10 °C"
		if step.State != observatory.StepFailed || step.Summary != want {
			t.Errorf("Activation = %+v, want Failed with the summary %q", step, want)
		}
		if waited := time.Since(began); waited < 20*time.Minute || waited > 21*time.Minute {
			t.Errorf("the step failed after %v, want the action's 20-minute timeout", waited)
		}
		if !slices.Contains(w.api.eventReasons(), reasonProcedureFailed) {
			t.Errorf("events = %v, want one with reason %s", w.api.eventReasons(), reasonProcedureFailed)
		}
	})
}

// A step that was Running when the operator stopped continues after
// the restart with its original start time, and the restarted operator
// does not send again the changes of the steps before it.
func TestANewOperatorContinuesTheStepThatRan(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.indi.hold("CCD Simulator", "CCD_TEMPERATURE")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.until(time.Minute, "the cool action does not run", func() bool {
			r, _ := w.reservation("east-tonight")
			return strings.Contains(stepText(stepOf(r, observatory.StepActivation)), "Camera east-main cool: -10 °C within 0.5 °C Running")
		})
		before, _ := w.reservation("east-tonight")
		connects := w.indi.count("east-telescope", "Telescope Simulator.CONNECTION")

		w.restart()
		w.indi.release("CCD Simulator", "CCD_TEMPERATURE")
		r := w.phase("east-tonight", observatory.ReservationReady, 25*time.Minute)
		for _, name := range []observatory.StepName{observatory.StepWait, observatory.StepConnect, observatory.StepActivation} {
			if a, b := stepOf(before, name).StartTime, stepOf(r, name).StartTime; a == nil || b == nil || !a.Equal(*b) {
				t.Errorf("%s started at %v before the restart and %v after it", name, a, b)
			}
		}
		if after := w.indi.count("east-telescope", "Telescope Simulator.CONNECTION"); after != connects {
			t.Errorf("the mount received CONNECTION %d times before the restart and %d after it", connects, after)
		}
		if n := w.indi.count("east-telescope", "CCD Simulator.CCD_TEMPERATURE"); n != 2 {
			t.Errorf("the camera received its setpoint %d times, want once from each copy of the operator", n)
		}
	})
}

// A restart during deactivation continues it, and the finalizer keeps
// the reservation until deactivation ends.
func TestANewOperatorContinuesDeactivation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		w.indi.hold("Telescope Simulator", "TELESCOPE_PARK")
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(time.Minute, "the park does not run", func() bool {
			r, _ := w.reservation("east-tonight")
			return strings.Contains(stepText(stepOf(r, observatory.StepDeactivation)), "Mount east state: Parked Running")
		})
		w.restart()
		w.indi.release("Telescope Simulator", "TELESCOPE_PARK")
		// The driver never answered the first park, so the restarted
		// operator sends it again. The simulator starts unparked, so
		// activation sent no TELESCOPE_PARK of its own.
		w.until(25*time.Minute, "the reservation is not deleted", func() bool {
			_, ok := w.reservation("east-tonight")
			return !ok
		})
		if n := w.indi.count("east-telescope", "Telescope Simulator.TELESCOPE_PARK"); n != 2 {
			t.Errorf("the mount received TELESCOPE_PARK %d times, want the park and the park again", n)
		}
		if pods := w.api.names(podsCollection); len(pods) != 0 {
			t.Errorf("pods after the release: %v, want none", pods)
		}
	})
}

// A restarted operator that finds a park in progress waits for it to
// end and sends no second park. libindi's telescope aborts a park when
// a client sends TELESCOPE_PARK while the mount is parking, so a second
// park would fail the Deactivation step.
func TestANewOperatorWaitsForAParkThatRuns(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		w.indi.move("Telescope Simulator", "TELESCOPE_PARK")
		parks := w.indi.count("east-telescope", "Telescope Simulator.TELESCOPE_PARK")
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(time.Minute, "the park is not sent", func() bool {
			return w.indi.count("east-telescope", "Telescope Simulator.TELESCOPE_PARK") == parks+1
		})
		synctest.Wait()
		r, _ := w.reservation("east-tonight")
		if text := stepText(stepOf(r, observatory.StepDeactivation)); !strings.Contains(text, "Mount east state: Parked Running: Parking Mount east") {
			t.Errorf("the Deactivation step during the park =\n%s", text)
		}

		w.restart()
		synctest.Wait()
		w.indi.setState("east-telescope", "Telescope Simulator", "TELESCOPE_PARK", "Ok")
		w.until(25*time.Minute, "the reservation is not deleted", func() bool {
			_, ok := w.reservation("east-tonight")
			return !ok
		})
		if n := w.indi.count("east-telescope", "Telescope Simulator.TELESCOPE_PARK"); n != parks+1 {
			t.Errorf("the mount received TELESCOPE_PARK %d times after Ready, want once", n-parks)
		}
	})
}

// A second reservation of a telescope stays in Wait until the first is
// released, and its message names the first.
func TestASecondReservationWaitsForTheFirst(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		w.reserve("east-later", map[string]any{"telescope": "east", "holder": "session"})
		r := w.phase("east-later", observatory.ReservationScheduled, time.Minute)
		time.Sleep(time.Minute)
		synctest.Wait()
		r, _ = w.reservation("east-later")
		if wait := stepOf(r, observatory.StepWait); wait.State != observatory.StepRunning || wait.Summary != "Waiting for Reservation east-tonight" {
			t.Errorf("Wait = %+v, want Running with the summary \"Waiting for Reservation east-tonight\"", wait)
		}
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.phase("east-later", observatory.ReservationReady, 20*time.Minute)
	})
}

// Two waiting reservations take the telescope in the order of their
// spec.start, whatever order they were created in.
func TestWaitingReservationsTakeTheTelescopeInOrderOfStart(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		now := time.Now().UTC()
		w.reserve("second", map[string]any{"telescope": "east", "holder": "b", "start": now.Add(2 * time.Minute).Format(time.RFC3339)})
		w.reserve("first", map[string]any{"telescope": "east", "holder": "a", "start": now.Add(time.Minute).Format(time.RFC3339)})
		time.Sleep(5 * time.Minute)
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.phase("first", observatory.ReservationReady, 20*time.Minute)
		if r, _ := w.reservation("second"); r.Status.Phase != observatory.ReservationScheduled {
			t.Errorf("second is %s while first holds the telescope", r.Status.Phase)
		}
	})
}

// A reservation with a spec.start waits until that time.
func TestAReservationWaitsForItsStart(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		start := time.Now().Add(2 * time.Hour).UTC()
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "start": start.Format(time.RFC3339)})
		r := w.phase("east-tonight", observatory.ReservationScheduled, time.Minute)
		if wait := stepOf(r, observatory.StepWait); wait.Summary != "Waiting until "+start.Format(time.RFC3339) {
			t.Errorf("Wait = %+v, want the summary \"Waiting until %s\"", wait, start.Format(time.RFC3339))
		}
		if pods := w.api.names(podsCollection); len(pods) != 0 {
			t.Errorf("pods before the start: %v, want none", pods)
		}
		time.Sleep(2*time.Hour - time.Minute)
		if r, _ := w.reservation("east-tonight"); r.Status.Phase != observatory.ReservationScheduled {
			t.Errorf("a minute before its start the reservation is %s, want %s", r.Status.Phase, observatory.ReservationScheduled)
		}
		r = w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		if took := stepOf(r, observatory.StepWait).StopTime; took == nil || took.Before(start) {
			t.Errorf("Wait stopped at %v, want a time at or after the start at %v", took, start)
		}
	})
}

// A reservation deleted before it took its telescope has started
// nothing, so the operator removes it without a deactivation.
func TestAReservationDeletedWhileItWaitsGoesAtOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		start := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "start": start})
		w.phase("east-tonight", observatory.ReservationScheduled, time.Minute)
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(time.Minute, "the reservation is not deleted", func() bool {
			_, ok := w.reservation("east-tonight")
			return !ok
		})
		if slices.ContainsFunc(w.api.eventReasons(), func(r string) bool { return r == string(observatory.StepStopWork) }) {
			t.Errorf("deactivation ran: %v", w.api.eventReasons())
		}
	})
}

func TestTheRetryAnnotationRunsTheFailedStepAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.holdPending("east-main-camera")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationFailed, 15*time.Minute)
		// The person powers the camera on and asks for a retry.
		w.api.setPodReady("east-main-camera")
		object, _ := w.api.object(kindCollection(observatory.ReservationKind), "east-tonight")
		object["metadata"].(map[string]any)["annotations"] = map[string]any{annotationRetry: "1"}
		w.api.mu.Lock()
		// The API server refuses the first removal of the annotation,
		// and the operator sends it again.
		w.api.patchRefusals = 1
		w.api.store(kindCollection(observatory.ReservationKind), "east-tonight", object, "MODIFIED")
		w.api.mu.Unlock()
		r := w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		if _, still := r.Metadata.Annotations[annotationRetry]; still {
			t.Errorf("annotations are %v, want the retry annotation removed", r.Metadata.Annotations)
		}
		if !slices.Contains(w.api.eventReasons(), "Retry") {
			t.Errorf("events = %v, want one with reason Retry", w.api.eventReasons())
		}
	})
}

// A reservation of a telescope that does not exist waits for it.
func TestAReservationWaitsForItsTelescope(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.reserve("north-tonight", map[string]any{"telescope": "north", "holder": "desktop"})
		r := w.phase("north-tonight", observatory.ReservationScheduled, time.Minute)
		if wait := stepOf(r, observatory.StepWait); wait.Summary != "Missing Telescope north" {
			t.Errorf("Wait = %+v, want the summary \"Missing Telescope north\"", wait)
		}
	})
}

// A deactivation step that fails after spec.end runs again only on the
// retry annotation. The runner sets no timer for a spec.end in the past
// (waitForChange in steady.go), because the timer would fire at once and
// the runner would loop without blocking.
func TestAFailedDeactivationAfterItsEndWaitsForARetry(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		end := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "end": end})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		w.indi.refuse("Telescope Simulator", "TELESCOPE_PARK")
		time.Sleep(time.Hour)
		w.phase("east-tonight", observatory.ReservationFailed, 10*time.Minute)
		time.Sleep(time.Hour)
		synctest.Wait()
		w.indi.accept("Telescope Simulator", "TELESCOPE_PARK")
		w.retry("east-tonight")
		w.phase("east-tonight", observatory.ReservationReleased, 10*time.Minute)
	})
}
