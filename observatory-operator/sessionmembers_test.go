package main

// Once a session records what it starts, the record alone decides
// which devices, trains, tubes, and Guiders belong to the session. A
// pod, a drivers annotation, or a name that a new object reuses does
// not add one, and a spec edit does not change where the record says
// that a device runs.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// waitsFor asserts that a device has no pod and no finalizer, and
// reads Idle with the message of a device that waits for a session to
// end.
func waitsFor(t *testing.T, w *world, kind observatory.Kind, name, pod, reservation string) {
	t.Helper()
	if slices.Contains(w.api.names(podsCollection), pod) {
		t.Errorf("pods = %q, want no %s", w.api.names(podsCollection), pod)
	}
	d, _ := decode[deviceObject](t, w.api, kindCollection(kind), name)
	ready := conditionOf(d.Status.Conditions, observatory.ConditionReady)
	want := "Starts at the next reservation, after Reservation " + reservation + " ends"
	if d.Status.Phase != observatory.DeviceIdle || ready.Message != want || holdsFinalizer(d.Metadata) {
		t.Errorf("%s %s = %s %q, finalizers %v; want Idle %q and no finalizer", kind.Name, name, d.Status.Phase, ready.Message, d.Metadata.Finalizers, want)
	}
}

// east-tonight starts while the observatory has no devices, so its
// session records an observatory with none. A Dome created during the
// session waits for the next one, and the observatory's server does
// not start.
func TestADomeCreatedDuringASessionOfAnEmptyObservatoryWaits(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.deleteNamed(kindCollection(observatory.DomeKind), "lab")
		w.api.deleteNamed(kindCollection(observatory.WeatherStationKind), "lab")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)

		w.put(observatory.DomeKind, "lab", map[string]any{"observatory": "lab", "driver": map[string]any{"name": "indi_simulator_dome"}})
		time.Sleep(time.Minute)
		synctest.Wait()

		if slices.Contains(w.api.names(podsCollection), "lab-observatory") {
			t.Errorf("pods = %q, want no lab-observatory", w.api.names(podsCollection))
		}
		waitsFor(t, w, observatory.DomeKind, "lab", "lab-dome", "east-tonight")
	})
}

// The main camera's pod is gone while the operator is down, and a
// person creates a focuser that the camera's driver serves. The
// session did not record the new focuser, so it waits for the next
// session, though no pod of its owner exists to show that the session
// started without it.
func TestAServedDeviceCreatedWhileItsOwnersPodIsGoneWaits(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := heldWorld(t, false)
		w.halt()
		w.api.deleteNamed(podsCollection, "east-main-camera")
		w.put(observatory.FocuserKind, "east-onboard", map[string]any{"opticalTrain": "east-imaging",
			"device": map[string]any{"kind": "Camera", "name": "east-main"}})
		w.start()
		w.until(5*time.Minute, "the camera's pod is not back", func() bool {
			return slices.Contains(w.api.names(podsCollection), "east-main-camera")
		})
		w.settle()

		waitsFor(t, w, observatory.FocuserKind, "east-onboard", "east-onboard-focuser", "east-tonight")
	})
}

// A person deletes the focuser while PowerOn waits for the pod of a
// Switch, after the server's pod lists the focuser's driver, and
// creates it again.
// The new focuser is not the one that the session recorded, so it
// waits for the next session.
func TestADeviceCreatedAgainDuringActivationWaits(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.holdPending("east-switch")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.until(5*time.Minute, "the server's pod does not list the focuser", func() bool {
			p, ok := w.api.object(podsCollection, "east-telescope")
			return ok && slices.Contains(links(p), "east-focuser")
		})
		focuser, _ := w.api.object(kindCollection(observatory.FocuserKind), "east")
		w.api.deleteNamed(kindCollection(observatory.FocuserKind), "east")
		w.gone(observatory.FocuserKind, "east", time.Minute)
		w.api.put(kindCollection(observatory.FocuserKind), map[string]any{"apiVersion": observatory.APIVersion, "kind": "Focuser",
			"metadata": map[string]any{"name": "east"}, "spec": focuser["spec"]})
		w.api.setPodReady("east-switch")
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		w.settle()

		waitsFor(t, w, observatory.FocuserKind, "east", "east-focuser", "east-tonight")
	})
}

// A person deletes the Guider during the session, its PHD2's pod goes,
// and the person creates a Guider of the same name. The new Guider is
// not the one that the session recorded, so its PHD2 waits for the
// next session.
func TestAGuiderCreatedAgainDuringASessionWaits(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyGuider(t)
		spec, _ := w.api.object(kindCollection(observatory.GuiderKind), "east")
		w.api.deleteNamed(kindCollection(observatory.GuiderKind), "east")
		// The garbage collector deletes the pod after its owner is
		// gone. Pods and Guiders come through separate watches, so an
		// operator that saw the pod go first would still hold the
		// Guider and create its pod again, and the fake API server
		// has no garbage collector to delete that pod.
		synctest.Wait()
		w.api.deleteNamed(podsCollection, "east-guider")
		w.settle()
		w.put(observatory.GuiderKind, "east", spec["spec"].(map[string]any))
		time.Sleep(time.Minute)
		synctest.Wait()

		if slices.Contains(w.api.names(podsCollection), "east-guider") {
			t.Errorf("pods = %q, want no east-guider", w.api.names(podsCollection))
		}
	})
}

// A person moves the train east-imaging to west during the session.
// The session keeps the train's devices on east, and their ParentFound
// says that they move. A focuser placed on the train goes where the
// train's spec places it now, so it does not wait for east's session.
func TestATrainMovedDuringASessionMovesItsDevicesAtTheEnd(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.amend(observatory.OpticalTrainKind, "east-imaging", func(spec map[string]any) { spec["telescope"] = "west" })
		w.put(observatory.FocuserKind, "extra", map[string]any{"opticalTrain": "east-imaging", "driver": map[string]any{"name": "indi_simulator_focus"}})
		w.settle()

		want := "True MovePending: Moves to OpticalTrain east-imaging when Reservation east-tonight ends"
		if got := w.stateOf(observatory.FocuserKind, "east", observatory.ConditionParentFound); got != want {
			t.Errorf("ParentFound of Focuser east = %q, want %q", got, want)
		}
		if got := w.stateOf(observatory.FocuserKind, "extra", observatory.ConditionReady); got != "False Idle: Not reserved" {
			t.Errorf("Ready of Focuser extra = %q, want %q", got, "False Idle: Not reserved")
		}
	})
}
