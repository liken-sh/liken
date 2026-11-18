package main

// A device with no parent is not installed: it is declared in full but
// is in no optical train. The operator creates no pod,
// Service, or claim for it, and a reservation of any telescope does not
// touch it.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// addParentlessCamera adds a camera with no parent but with a device
// claim, as a camera on real hardware has. A ResourceClaimTemplate for it
// would reserve the hardware, so the test checks that the operator
// creates none.
func (w *world) addParentlessCamera() {
	w.put(observatory.CameraKind, "spare", map[string]any{
		"driver": map[string]any{"name": "indi_simulator_ccd"},
		"claim":  map[string]any{"devices": map[string]any{"requests": []any{map[string]any{"name": "camera", "exactly": map[string]any{"deviceClassName": "usb.liken.sh"}}}}},
	})
}

func TestADeviceWithNoParentGetsNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.addParentlessCamera()
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		time.Sleep(2 * statusWindow)
		synctest.Wait()

		for _, name := range []string{"spare-focuser", "spare-camera"} {
			if slices.Contains(w.api.names(podsCollection), name) || slices.Contains(w.api.names(servicesCollection), name) {
				t.Errorf("the operator created a pod or a Service %s for a device that is not installed", name)
			}
		}
		if names := w.api.names(templatesCollection); len(names) != 0 {
			t.Errorf("claims = %v, want none", names)
		}

		focuser, _ := decode[observatory.Focuser](t, w.api, kindCollection(observatory.FocuserKind), "spare")
		s := focuser.Status
		if s.Phase != observatory.DeviceInventory || s.Pod != "" || s.Driver != "indi_simulator_focus" {
			t.Errorf("the focuser status is %+v, want phase Inventory, no pod, and driver indi_simulator_focus", s)
		}
		if c := conditionOf(s.Conditions, observatory.ConditionReady); c.Reason != "Inventory" || c.Message != "Not installed in an optical train" {
			t.Errorf("the Ready condition is %+v, want reason Inventory and message \"Not installed in an optical train\"", c)
		}
		if c := conditionOf(s.Conditions, observatory.ConditionParentFound); c.Status != observatory.ConditionTrue || c.Reason != "NoParent" {
			t.Errorf("the ParentFound condition is %+v, want True with reason NoParent", c)
		}
		row := printerRow(t, "deploy/focusers-crd.yaml", focuser)
		t.Logf("kubectl get focuser: %v", row)
		if row["Phase"] != "Inventory" || row["Train"] != "" {
			t.Errorf("the printer row is %v, want Phase Inventory and an empty Train", row)
		}

		east, _ := decode[observatory.Telescope](t, w.api, kindCollection(observatory.TelescopeKind), "east")
		for _, train := range east.Status.Trains {
			for _, d := range train.Devices {
				if d.Name == "spare" {
					t.Errorf("train %s lists the %s %s, want no device that has no parent", train.Name, d.Kind, d.Name)
				}
			}
		}
		lab, _ := decode[observatory.Observatory](t, w.api, kindCollection(observatory.ObservatoryKind), "lab")
		if len(lab.Status.Devices) != 2 || lab.Status.Phase != observatory.PhaseReady {
			t.Errorf("the lab status is %+v, want 2 devices and phase Ready", lab.Status)
		}
	})
}

// When a reservation of a device's telescope starts activation, the
// device's phase is Starting before its pod exists. So `kubectl get`
// shows every device of the telescope as starting while the
// observatory's pods start.
func TestADeviceIsStartingFromTheStartOfActivation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		time.Sleep(2 * statusWindow)
		synctest.Wait()
		camera := func(name string) observatory.Camera {
			c, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), name)
			return c
		}
		if s := camera("east-main").Status; s.Phase != observatory.DeviceIdle || conditionOf(s.Conditions, observatory.ConditionReady).Message != "Not reserved" {
			t.Errorf("before the reservation, east-main is %+v, want phase Idle and message \"Not reserved\"", s)
		}

		w.api.holdPending("lab-observatory")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.until(time.Minute, "east-main is not Starting", func() bool { return camera("east-main").Status.Phase == observatory.DeviceStarting })

		s := camera("east-main").Status
		if c := conditionOf(s.Conditions, observatory.ConditionReady); s.Pod != "" || c.Message != "Waiting for activation to create pod east-main-camera" {
			t.Errorf("east-main is %+v with Ready %+v, want no pod and message \"Waiting for activation to create pod east-main-camera\"", s, c)
		}
		if s := camera("west-main").Status; s.Phase != observatory.DeviceIdle {
			t.Errorf("west-main is %+v, want phase Idle", s)
		}

		w.api.setPodReady("lab-observatory")
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
	})
}
