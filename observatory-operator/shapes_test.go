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
				"Wait=Done", "StartSite=Skipped", "PowerOn=Done", "StartDevices=Done", "Connect=Done", "Configure=Done", "Prepare=Skipped",
				"Abort=Skipped", "Secure=Done", "Disconnect=Done", "StopDevices=Done", "PowerOff=Done", "StopSite=Skipped",
			},
		},
		{
			name: "a Switch alone",
			devices: func(w *world) {
				w.put(observatory.SwitchKind, "solo-power", map[string]any{"telescope": "solo", "driver": map[string]any{"name": "indi_simulator_io"}})
			},
			steps: []string{
				"Wait=Done", "StartSite=Skipped", "PowerOn=Done", "StartDevices=Skipped", "Connect=Skipped", "Configure=Skipped", "Prepare=Skipped",
				"Abort=Skipped", "Secure=Skipped", "Disconnect=Skipped", "StopDevices=Skipped", "PowerOff=Done", "StopSite=Skipped",
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

// A driver that lacks a property that a step writes is noted, and the
// step goes on.
func TestDriversThatLackTheirKindsPropertiesAreNoted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		// A camera and a filter wheel that run drivers of other kinds:
		// the sky quality meter defines no CCD_TEMPERATURE, no
		// ACTIVE_DEVICES, no CCD_GAIN, and no SCOPE_INFO, and the polar
		// aligner no FILTER_NAME.
		w.put(observatory.CameraKind, "east-odd", map[string]any{"opticalTrain": "east-imaging", "driver": map[string]any{"name": "indi_simulator_sqm"}, "temperature": -5, "gain": 1})
		w.put(observatory.FilterWheelKind, "east-odd", map[string]any{"opticalTrain": "east-imaging", "driver": map[string]any{"name": "indi_simulator_pac"}, "filters": []any{"L"}})
		w.api.deleteNamed(kindCollection(observatory.PolarAlignerKind), "east")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		r := w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		notes := map[observatory.StepName][]string{
			observatory.StepConfigure: {"no CCD_GAIN on Camera east-odd", "no SCOPE_INFO on Camera east-odd", "no FILTER_NAME on FilterWheel east-odd"},
			observatory.StepPrepare:   {"no cooler on Camera east-odd: no CCD_TEMPERATURE"},
		}
		for name, want := range notes {
			for _, note := range want {
				if message := stepOf(r, name).Summary; !strings.Contains(message, note) {
					t.Errorf("%s: %q does not note %q", name, message, note)
				}
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
		if step := stepOf(r, observatory.StepPrepare); step.State != observatory.StepSkipped || step.Summary != "Reservation ended" {
			t.Errorf("Prepare = %+v", step)
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
		if !slices.Contains(r.Metadata.Finalizers, observatory.ReservationFinalizer) {
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

// A device added while a reservation is Ready gets its pod, and the
// server restarts with a link to it, because indiserver reads its
// drivers from its arguments. The devices come back on the new server,
// and the operator connects each one again.
func TestADeviceAddedWhileReadyRestartsTheServer(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		before, _ := w.api.object(podsCollection, "east-telescope")
		w.put(observatory.SkyQualityMeterKind, "east", map[string]any{"telescope": "east", "driver": map[string]any{"name": "indi_simulator_sqm"}})
		w.until(time.Minute, "the new device is not connected", func() bool {
			return slices.Contains(w.indi.connected("east-telescope"), "SQM Simulator") && len(w.indi.connected("east-telescope")) == 13
		})
		after, _ := w.api.object(podsCollection, "east-telescope")
		if podUID(before) == podUID(after) {
			t.Error("the server pod was not replaced")
		}
		if !slices.Contains(links(after), "east-skyqualitymeter") {
			t.Errorf("the new server links %v", links(after))
		}
		if r, _ := w.reservation("east-tonight"); r.Status.Phase != observatory.ReservationReady {
			t.Errorf("the reservation is %s", r.Status.Phase)
		}
	})
}

// claimsCollection is where the operator creates each device's
// ResourceClaim.
const claimsCollection = "/apis/resource.k8s.io/v1/namespaces/" + testNamespace + "/resourceclaims"

// claimFocuser gives the east focuser a claim, as a focuser on real
// hardware states one.
func (w *world) claimFocuser() {
	w.put(observatory.FocuserKind, "east", map[string]any{
		"opticalTrain": "east-imaging", "driver": map[string]any{"name": "indi_simulator_focus"},
		"claim": map[string]any{"devices": map[string]any{"requests": []any{map[string]any{"name": "focuser", "exactly": map[string]any{"deviceClassName": "usb.liken.sh"}}}}},
	})
}

// A device on real hardware states a claim, and its ResourceClaim lives
// as long as its pod.
func TestAClaimLivesAsLongAsItsDevicesPod(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.claimFocuser()
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		if names := w.api.names(claimsCollection); !slices.Equal(names, []string{"east-focuser"}) {
			t.Errorf("claims = %v", names)
		}
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(10*time.Minute, "the reservation stays", func() bool {
			_, ok := w.reservation("east-tonight")
			return !ok
		})
		if names := w.api.names(claimsCollection); len(names) != 0 {
			t.Errorf("claims after the release: %v", names)
		}
	})
}
