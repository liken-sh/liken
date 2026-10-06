package main

// A device with no parent is on the shelf: described in full, and
// installed nowhere. The operator creates nothing for it, and a
// reservation of any telescope leaves it alone.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// shelveCamera puts a camera on the shelf with a claim, as a camera on
// real hardware states one. A ResourceClaim for it would reserve the
// hardware.
func (w *world) shelveCamera() {
	w.put(observatory.CameraKind, "spare", map[string]any{
		"driver": map[string]any{"name": "indi_simulator_ccd"},
		"claim":  map[string]any{"devices": map[string]any{"requests": []any{map[string]any{"name": "camera", "exactly": map[string]any{"deviceClassName": "usb.liken.sh"}}}}},
	})
}

func TestADeviceOnTheShelfGetsNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.shelveCamera()
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		time.Sleep(2 * statusWindow)
		synctest.Wait()

		for _, name := range []string{"spare-focuser", "spare-camera"} {
			if slices.Contains(w.api.names(podsCollection), name) || slices.Contains(w.api.names(servicesCollection), name) {
				t.Errorf("the operator created a pod or a Service %s for a device on the shelf", name)
			}
		}
		if names := w.api.names(claimsCollection); len(names) != 0 {
			t.Errorf("claims = %v, want none", names)
		}

		focuser, _ := decode[observatory.Focuser](t, w.api, kindCollection(observatory.FocuserKind), "spare")
		s := focuser.Status
		if s.Phase != observatory.DeviceInventory || s.Pod != "" || s.Driver != "indi_simulator_focus" {
			t.Errorf("focuser status = %+v", s)
		}
		if c := conditionOf(s.Conditions, observatory.ConditionReady); c.Reason != "Inventory" || c.Message != "Not installed in an optical train" {
			t.Errorf("Ready = %+v", c)
		}
		if c := conditionOf(s.Conditions, observatory.ConditionParentFound); c.Status != observatory.ConditionTrue || c.Reason != "NoParent" {
			t.Errorf("ParentFound = %+v", c)
		}
		row := printerRow(t, "deploy/focusers-crd.yaml", focuser)
		t.Logf("kubectl get focuser: %v", row)
		if row["Phase"] != "Inventory" || row["Train"] != "" {
			t.Errorf("row = %v", row)
		}

		east, _ := decode[observatory.Telescope](t, w.api, kindCollection(observatory.TelescopeKind), "east")
		for _, train := range east.Status.Trains {
			for _, d := range train.Devices {
				if d.Name == "spare" {
					t.Errorf("train %s lists %s %s", train.Name, d.Kind, d.Name)
				}
			}
		}
		lab, _ := decode[observatory.Observatory](t, w.api, kindCollection(observatory.ObservatoryKind), "lab")
		if len(lab.Status.Devices) != 3 || lab.Status.Phase != observatory.PhaseReady {
			t.Errorf("lab = %+v", lab.Status)
		}
	})
}

// A device whose telescope's reservation has begun activation is
// Starting before its pod exists, so `kubectl get` shows the whole
// telescope starting while the site's pods start.
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
			t.Errorf("before the reservation, east-main = %+v", s)
		}

		w.api.holdPending("lab-observatory")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.until(time.Minute, "east-main is not Starting", func() bool { return camera("east-main").Status.Phase == observatory.DeviceStarting })

		s := camera("east-main").Status
		if c := conditionOf(s.Conditions, observatory.ConditionReady); s.Pod != "" || c.Message != "Waiting for activation to create pod east-main-camera" {
			t.Errorf("east-main = %+v, Ready %+v", s, c)
		}
		if s := camera("west-main").Status; s.Phase != observatory.DeviceIdle {
			t.Errorf("west-main = %+v", s)
		}

		w.api.setPodReady("lab-observatory")
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
	})
}
