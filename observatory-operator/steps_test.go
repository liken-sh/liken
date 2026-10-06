package main

// The rules of the steps: the deadline of each step, the record that a
// new copy of the operator continues from, the order in which two
// reservations take one telescope, and the times in a reservation's
// spec.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// A device whose pod never becomes Ready fails StartDevices at its
// deadline, and the Ready condition names the step and the pod.
func TestAStepThatPassesItsDeadlineFailsTheReservation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.holdPending("camera-east-main")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationActivating, time.Minute)
		began := time.Now()
		r := w.phase("east-tonight", observatory.ReservationFailed, 15*time.Minute)
		step := stepOf(r, observatory.StepStartDevices)
		if step.State != observatory.StepFailed || !strings.Contains(step.Message, "timed out after 10m0s") || !strings.Contains(step.Message, "camera-east-main") {
			t.Errorf("StartDevices = %+v", step)
		}
		if waited := time.Since(began); waited < 10*time.Minute-time.Second || waited > 11*time.Minute {
			t.Errorf("the step failed after %v, want its 10-minute deadline", waited)
		}
		ready := conditionOf(r.Status.Conditions, observatory.ConditionReady)
		if ready.Reason != reasonTimedOut || !strings.HasPrefix(ready.Message, "StartDevices: ") || !strings.Contains(ready.Message, "Camera east-main") {
			t.Errorf("Ready = %+v", ready)
		}
		if got := stepStates(r); !slices.Equal(got[5:], []string{"Configure=Pending", "Prepare=Pending"}) {
			t.Errorf("the steps after the failed one ran: %v", got)
		}
		if !slices.Contains(w.api.eventReasons(), reasonTimedOut) {
			t.Errorf("events = %v", w.api.eventReasons())
		}
		time.Sleep(2 * statusWindow)
		synctest.Wait()
		if east, _ := decode[observatory.Telescope](t, w.api, kindCollection(observatory.TelescopeKind), "east"); east.Status.Phase != observatory.PhaseError {
			t.Errorf("the telescope is %s", east.Status.Phase)
		}
		if camera, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), "east-main"); camera.Status.Phase != observatory.DeviceStarting {
			t.Errorf("the camera whose pod is Pending is %s", camera.Status.Phase)
		}

		// A delete runs deactivation from Abort, and stops what
		// activation started.
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(10*time.Minute, "the reservation stays", func() bool {
			_, ok := w.reservation("east-tonight")
			return !ok
		})
		if pods := w.api.names(podsCollection); len(pods) != 0 {
			t.Errorf("pods after the release: %v", pods)
		}
	})
}

// A device that never answers a change fails its step at the deadline,
// and the message names the device.
func TestACoolerThatNeverReachesItsSetpointFailsPrepare(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.indi.hold("CCD Simulator", "CCD_TEMPERATURE")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		r := w.phase("east-tonight", observatory.ReservationFailed, 30*time.Minute)
		step := stepOf(r, observatory.StepPrepare)
		if step.State != observatory.StepFailed || !strings.Contains(step.Message, "timed out after 20m0s") || !strings.Contains(step.Message, "Camera east-main") {
			t.Errorf("Prepare = %+v", step)
		}
	})
}

// An operator that stops in the middle of a step leaves the step
// Running, and the next copy continues it with its first start time,
// and sends no change that the steps before it sent.
func TestANewOperatorContinuesTheStepThatRan(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.indi.hold("CCD Simulator", "CCD_TEMPERATURE")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.until(time.Minute, "Prepare does not run", func() bool {
			r, _ := w.reservation("east-tonight")
			return stepOf(r, observatory.StepPrepare).State == observatory.StepRunning
		})
		before, _ := w.reservation("east-tonight")
		connects := w.indi.count("telescope-east", "Telescope Simulator.CONNECTION")

		w.restart()
		w.indi.release("CCD Simulator", "CCD_TEMPERATURE")
		r := w.phase("east-tonight", observatory.ReservationReady, 25*time.Minute)
		for _, name := range []observatory.StepName{observatory.StepWait, observatory.StepConnect, observatory.StepPrepare} {
			if a, b := stepOf(before, name).StartTime, stepOf(r, name).StartTime; a == nil || b == nil || !a.Equal(*b) {
				t.Errorf("%s started at %v before the restart and %v after it", name, a, b)
			}
		}
		if after := w.indi.count("telescope-east", "Telescope Simulator.CONNECTION"); after != connects {
			t.Errorf("the mount received CONNECTION %d times before the restart and %d after it", connects, after)
		}
		if n := w.indi.count("telescope-east", "CCD Simulator.CCD_TEMPERATURE"); n != 2 {
			t.Errorf("the camera received its setpoint %d times, want once from each copy of the operator", n)
		}
	})
}

// A restart during deactivation continues it, and the finalizer holds
// the reservation until the end.
func TestANewOperatorContinuesDeactivation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		w.indi.hold("Telescope Simulator", "TELESCOPE_PARK")
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(time.Minute, "Secure does not run", func() bool {
			r, _ := w.reservation("east-tonight")
			return stepOf(r, observatory.StepSecure).State == observatory.StepRunning
		})
		w.restart()
		w.indi.release("Telescope Simulator", "TELESCOPE_PARK")
		// The held park was sent and never answered, so the new copy
		// sends it again, and the driver answers this time. The
		// simulator starts unparked, so activation sent no unpark.
		w.until(25*time.Minute, "the reservation stays", func() bool {
			_, ok := w.reservation("east-tonight")
			return !ok
		})
		if n := w.indi.count("telescope-east", "Telescope Simulator.TELESCOPE_PARK"); n != 2 {
			t.Errorf("the mount received TELESCOPE_PARK %d times; want the park and the park again", n)
		}
		if pods := w.api.names(podsCollection); len(pods) != 0 {
			t.Errorf("pods after the release: %v", pods)
		}
	})
}

// A new operator that finds a park still running waits for it to end,
// and sends no park of its own. libindi's telescope aborts a park when
// a client sends TELESCOPE_PARK while the mount moves to its park
// position, so a second park would fail Secure.
func TestANewOperatorWaitsForAParkThatRuns(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		w.indi.move("Telescope Simulator", "TELESCOPE_PARK")
		parks := w.indi.count("telescope-east", "Telescope Simulator.TELESCOPE_PARK")
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(time.Minute, "the park is not sent", func() bool {
			return w.indi.count("telescope-east", "Telescope Simulator.TELESCOPE_PARK") == parks+1
		})
		synctest.Wait()
		r, _ := w.reservation("east-tonight")
		if message := stepOf(r, observatory.StepSecure).Message; message != "parking Mount east-mount" {
			t.Errorf("Secure's message during the park = %q", message)
		}

		w.restart()
		synctest.Wait()
		w.indi.setState("telescope-east", "Telescope Simulator", "TELESCOPE_PARK", "Ok")
		w.until(25*time.Minute, "the reservation stays", func() bool {
			_, ok := w.reservation("east-tonight")
			return !ok
		})
		if n := w.indi.count("telescope-east", "Telescope Simulator.TELESCOPE_PARK"); n != parks+1 {
			t.Errorf("the mount received TELESCOPE_PARK %d times after Ready, want once", n-parks)
		}
	})
}

// A second reservation of a telescope waits in Wait until the first is
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
		if wait := stepOf(r, observatory.StepWait); wait.State != observatory.StepRunning || wait.Message != "waiting for the Reservation east-tonight to release the Telescope east" {
			t.Errorf("Wait = %+v", wait)
		}
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.phase("east-later", observatory.ReservationReady, 20*time.Minute)
	})
}

// Two reservations that wait take the telescope in the order of their
// starts, whatever order they were created in.
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

// A reservation with a start waits for it.
func TestAReservationWaitsForItsStart(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		start := time.Now().Add(2 * time.Hour).UTC()
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "start": start.Format(time.RFC3339)})
		r := w.phase("east-tonight", observatory.ReservationScheduled, time.Minute)
		if wait := stepOf(r, observatory.StepWait); wait.Message != "waiting for spec.start at "+start.Format(time.RFC3339) {
			t.Errorf("Wait = %+v", wait)
		}
		if pods := w.api.names(podsCollection); len(pods) != 0 {
			t.Errorf("pods before the start: %v", pods)
		}
		time.Sleep(2*time.Hour - time.Minute)
		if r, _ := w.reservation("east-tonight"); r.Status.Phase != observatory.ReservationScheduled {
			t.Errorf("a minute before its start the reservation is %s", r.Status.Phase)
		}
		r = w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		if took := stepOf(r, observatory.StepWait).FinishTime; took == nil || took.Before(start) {
			t.Errorf("Wait finished at %v, before the start at %v", took, start)
		}
	})
}

// A reservation deleted before it took its telescope started nothing,
// and goes away with no deactivation.
func TestAReservationDeletedWhileItWaitsGoesAtOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		start := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "start": start})
		w.phase("east-tonight", observatory.ReservationScheduled, time.Minute)
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(time.Minute, "the reservation stays", func() bool {
			_, ok := w.reservation("east-tonight")
			return !ok
		})
		if slices.ContainsFunc(w.api.eventReasons(), func(r string) bool { return r == string(observatory.StepAbort) }) {
			t.Errorf("deactivation ran: %v", w.api.eventReasons())
		}
	})
}

// The retry annotation runs a failed step again.
func TestTheRetryAnnotationRunsTheFailedStepAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.holdPending("camera-east-main")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationFailed, 15*time.Minute)
		// The person powers the camera on, and asks for a retry.
		w.api.setPodReady("camera-east-main")
		object, _ := w.api.object(kindCollection(observatory.ReservationKind), "east-tonight")
		object["metadata"].(map[string]any)["annotations"] = map[string]any{annotationRetry: "1"}
		w.api.mu.Lock()
		// The first removal of the annotation meets an API server that
		// refuses it, and the operator sends it again.
		w.api.patchRefusals = 1
		w.api.store(kindCollection(observatory.ReservationKind), "east-tonight", object, "MODIFIED")
		w.api.mu.Unlock()
		r := w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		if _, still := r.Metadata.Annotations[annotationRetry]; still {
			t.Errorf("the annotation stays: %v", r.Metadata.Annotations)
		}
		if !slices.Contains(w.api.eventReasons(), "Retry") {
			t.Errorf("events = %v", w.api.eventReasons())
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
		if wait := stepOf(r, observatory.StepWait); wait.Message != "waiting for the Telescope north, which does not exist" {
			t.Errorf("Wait = %+v", wait)
		}
	})
}
