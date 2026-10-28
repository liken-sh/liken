package main

// Telescopes of other shapes than the example's: one with a mount
// alone in an observatory with no devices of its own, one with only a
// Switch, and drivers that lack the properties a step writes.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func TestASmallTelescopeSkipsWhatItLacks(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		devices func(w *world)
		steps   []string
	}{
		{
			name: "a mount alone, in an observatory with no devices",
			devices: func(w *world) {
				w.put(observatory.MountKind, "solo-mount", map[string]any{"telescope": "solo", "driver": map[string]any{"name": "indi_simulator_telescope"}})
			},
			steps: []string{
				"Wait=Done", "StartSite=Skipped", "Requires=Skipped", "PowerOn=Done", "StartDevices=Done", "Connect=Done", "Configure=Done", "Activation=Skipped", "StartGuider=Skipped",
				"StopWork=Skipped", "Deactivation=Skipped", "StopGuider=Skipped", "Disconnect=Done", "StopDevices=Done", "PowerOff=Done", "StopSite=Skipped",
			},
		},
		{
			name: "a Switch alone",
			devices: func(w *world) {
				w.put(observatory.SwitchKind, "solo-power", map[string]any{"telescope": "solo", "driver": map[string]any{"name": "indi_simulator_io"}})
			},
			steps: []string{
				"Wait=Done", "StartSite=Skipped", "Requires=Skipped", "PowerOn=Done", "StartDevices=Skipped", "Connect=Skipped", "Configure=Skipped", "Activation=Skipped", "StartGuider=Skipped",
				"StopWork=Skipped", "Deactivation=Skipped", "StopGuider=Skipped", "Disconnect=Skipped", "StopDevices=Skipped", "PowerOff=Done", "StopSite=Skipped",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.put(observatory.ObservatoryKind, "bare", map[string]any{"location": map[string]any{"latitude": 1, "longitude": 2, "elevation": 3}})
				w.put(observatory.TelescopeKind, "solo", map[string]any{"observatory": "bare"})
				c.devices(w)
				end := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
				w.reserve("solo-tonight", map[string]any{"telescope": "solo", "holder": "desktop", "end": end})
				w.phase("solo-tonight", observatory.ReservationReady, 10*time.Minute)
				time.Sleep(time.Hour)
				r := w.phase("solo-tonight", observatory.ReservationReleased, 10*time.Minute)
				if got := stepStates(r); !slices.Equal(got, c.steps) {
					t.Errorf("steps = %v\nwant %v", got, c.steps)
				}
			})
		})
	}
}

// A resource whose driver does not define its interface's properties
// reports Error, and the rest of the telescope starts. The polar
// aligner's driver defines no CCD_EXPOSURE, so it serves no Camera, and
// the steps send it nothing for the camera.
func TestADeviceWhoseDriverLacksItsInterfaceReportsError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.put(observatory.CameraKind, "east-odd", map[string]any{"opticalTrain": "east-imaging", "driver": map[string]any{"name": "indi_simulator_pac"},
			"activation": []any{map[string]any{"cool": map[string]any{"celsius": -5}}}, "gain": 1})
		w.api.deleteNamed(kindCollection(observatory.PolarAlignerKind), "east")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		r := w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		time.Sleep(2 * statusWindow)
		synctest.Wait()

		lacks := "INDI device Alignment Correction Simulator defines no CCD_EXPOSURE after it connects, so it serves no Camera"
		if message := stepText(stepOf(r, observatory.StepConnect)); !strings.Contains(message, "Camera east-odd is not served: "+lacks) {
			t.Errorf("Connect: %q does not note %q", message, lacks)
		}
		if message := stepText(stepOf(r, observatory.StepActivation)); strings.Contains(message, "east-odd") {
			t.Errorf("Activation: %q runs the camera's activation", message)
		}
		camera, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), "east-odd")
		if ready := conditionOf(camera.Status.Conditions, observatory.ConditionReady); camera.Status.Phase != observatory.DeviceError || ready.Message != "Failed: "+lacks {
			t.Errorf("camera = %s, Ready = %+v", camera.Status.Phase, ready)
		}
		for _, change := range w.indi.changes() {
			if strings.Contains(change, "Alignment Correction Simulator.") && !strings.Contains(change, ".CONNECTION ") {
				t.Errorf("the steps sent %q to a device that serves no Camera", change)
			}
		}
	})
}

// A reservation that ends during activation stops the step that runs,
// and deactivation stops what the steps before it started.
func TestAReservationThatEndsDuringActivationDeactivates(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.indi.hold("CCD Simulator", "CCD_TEMPERATURE")
		end := time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "end": end})
		r := w.phase("east-tonight", observatory.ReservationReleased, 20*time.Minute)
		if step := stepOf(r, observatory.StepActivation); step.State != observatory.StepSkipped || step.Summary != "Reservation ended" {
			t.Errorf("Activation = %+v", step)
		}
		if pods := w.api.names(podsCollection); len(pods) != 0 {
			t.Errorf("pods after the release: %v", pods)
		}
	})
}

// A finalizer patch that the API server refuses is sent again.
func TestARefusedFinalizerIsSentAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.mu.Lock()
		w.api.patchRefusals = 2
		w.api.mu.Unlock()
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		r := w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		if !slices.Contains(r.Metadata.Finalizers, observatory.Finalizer) {
			t.Errorf("finalizers = %v", r.Metadata.Finalizers)
		}
		w.api.mu.Lock()
		w.api.patchRefusals = 2
		w.api.mu.Unlock()
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(10*time.Minute, "the reservation stays", func() bool {
			_, ok := w.reservation("east-tonight")
			return !ok
		})
	})
}

// A finalizer that is gone with its reservation is removed: the
// operator does not send the patch again, and logs no retry.
func TestAFinalizerOfAGoneReservationIsRemoved(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.reserve("west-tonight", map[string]any{"telescope": "west", "holder": "desktop"})
		w.phase("west-tonight", observatory.ReservationReady, 10*time.Minute)
		w.api.mu.Lock()
		w.api.lateDeletes = 1
		w.api.mu.Unlock()
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "west-tonight")
		w.until(10*time.Minute, "the reservation stays", func() bool {
			_, ok := w.reservation("west-tonight")
			return !ok
		})
		time.Sleep(time.Minute)
		synctest.Wait()
		if logs := w.logs.String(); strings.Contains(logs, "retrying") {
			t.Errorf("the operator retried a finished removal:\n%s", logs)
		}
	})
}

// templatesCollection is where the operator creates each device's
// ResourceClaimTemplate.
const templatesCollection = "/apis/resource.k8s.io/v1/namespaces/" + testNamespace + "/resourceclaimtemplates"

// claimFocuser gives the east focuser a claim, as a focuser on real
// hardware states one.
func (w *world) claimFocuser() {
	w.put(observatory.FocuserKind, "east", map[string]any{
		"opticalTrain": "east-imaging", "driver": map[string]any{"name": "indi_simulator_focus"},
		"claim": map[string]any{"devices": map[string]any{"requests": []any{map[string]any{"name": "focuser", "exactly": map[string]any{"deviceClassName": "usb.liken.sh"}}}}},
	})
}

// A device on real hardware states a claim, and its
// ResourceClaimTemplate lives as long as the reservation.
func TestAClaimTemplateLivesAsLongAsTheReservation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.claimFocuser()
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		if names := w.api.names(templatesCollection); !slices.Equal(names, []string{"east-focuser"}) {
			t.Errorf("claims = %v", names)
		}
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(10*time.Minute, "the reservation stays", func() bool {
			_, ok := w.reservation("east-tonight")
			return !ok
		})
		if names := w.api.names(templatesCollection); len(names) != 0 {
			t.Errorf("claims after the release: %v", names)
		}
	})
}

// The observatory's devices start in StartSite, so its summary names a
// resource there whose device does not serve its interface. The GPS
// simulator defines no RECEIVER_SETTINGS.
func TestStartSiteNamesADeviceThatLacksItsInterface(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.put(observatory.ReceiverKind, "lab", map[string]any{"observatory": "lab", "driver": map[string]any{"name": "indi_simulator_gps"}})
		r := readyOf(w)
		want := "Receiver lab is not served: INDI device GPS Simulator defines no RECEIVER_SETTINGS after it connects, so it serves no Receiver"
		if summary := stepOf(r, observatory.StepStartSite).Summary; !strings.Contains(summary, want) {
			t.Errorf("StartSite = %q, want %q", summary, want)
		}
	})
}
