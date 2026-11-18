package main

// The messages that a person reads in `kubectl get`, `kubectl describe`,
// and Events start with the state in a few words.

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func TestADevicesMessageLeadsWithItsState(t *testing.T) {
	ref := serverRef{observatory.TelescopeKind, "east"}
	phase := func(p observatory.DevicePhase) deviceStatus {
		return deviceStatus{DeviceStatus: observatory.DeviceStatus{Phase: p}}
	}
	cases := []struct {
		name   string
		kind   observatory.Kind
		status deviceStatus
		fault  string
		use    serverUse
		want   string
	}{
		{"a mount that is not installed", observatory.MountKind, phase(observatory.DeviceInventory), "", notInstalled, "Not installed in a telescope"},
		{"a camera that is not installed", observatory.CameraKind, phase(observatory.DeviceInventory), "", notInstalled, "Not installed in an optical train"},
		{"a dome that is not installed", observatory.DomeKind, phase(observatory.DeviceInventory), "", notInstalled, "Not installed in an observatory"},
		{"a switch that is not installed", observatory.SwitchKind, phase(observatory.DeviceInventory), "", notInstalled, "Not installed in a telescope or an observatory"},
		{"an installed device with no reservation", observatory.CameraKind, phase(observatory.DeviceIdle), "", idle, "Not reserved"},
		{"a device whose reservation activates", observatory.CameraKind, phase(observatory.DeviceStarting), "", activating,
			"Waiting for activation to create pod east-main-camera"},
		{"a device whose pod a Ready reservation creates again", observatory.CameraKind, phase(observatory.DeviceStarting), "", maintained,
			"Creating pod east-main-camera"},
		{"a pod whose driver has not defined its device", observatory.CameraKind, deviceStatus{DeviceStatus: observatory.DeviceStatus{Phase: observatory.DeviceStarting, Pod: "camera-east-main"}}, "",
			activating, "Waiting for its driver on east-telescope"},
		{"a pod that a device's server no longer runs", observatory.CameraKind, deviceStatus{DeviceStatus: observatory.DeviceStatus{Phase: observatory.DeviceDisconnecting, Pod: "east-main-camera"}}, "",
			notInstalled, "Stopping pod east-main-camera"},
		{"a connected device", observatory.CameraKind, deviceStatus{DeviceStatus: observatory.DeviceStatus{Phase: observatory.DeviceConnected, IndiDevice: "CCD Simulator"}}, "", maintained, "Connected on east-telescope"},
		{"a device that failed", observatory.CameraKind, deviceStatus{DeviceStatus: observatory.DeviceStatus{Phase: observatory.DeviceError, IndiDevice: "CCD Simulator"}}, "indi: CCD Simulator.CCD_GAIN is Alert",
			maintained, "Failed: indi: CCD Simulator.CCD_GAIN is Alert"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := deviceMessage(c.status, c.kind, ref.String(), "east-main-camera", c.fault, c.use); got != c.want {
				t.Errorf("deviceMessage = %q, want %q", got, c.want)
			}
		})
	}
}

// eventMessages returns "<reason>: <message>" of each Event, in the
// order the operator wrote them.
func eventMessages(a *fakeAPI) []string {
	var out []string
	for _, e := range a.recorded.List() {
		out = append(out, e.Reason+": "+e.Message)
	}
	return out
}

// A finished reservation shows no step, so `kubectl get rsv` does not
// show a step that has ended as if it were running.
func TestAFinishedReservationShowsNoStep(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		end := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "end": end})
		r := w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		ready := conditionOf(r.Status.Conditions, observatory.ConditionReady)
		if r.Status.Step != "" || ready.Reason != "Ready" || ready.Message != "Ready at east-telescope.observatory.svc:7624" {
			t.Errorf("a Ready reservation has step %q and Ready %+v, want no step and Ready \"Ready at east-telescope.observatory.svc:7624\"", r.Status.Step, ready)
		}
		time.Sleep(time.Hour)
		r = w.phase("east-tonight", observatory.ReservationReleased, 10*time.Minute)
		safe := conditionOf(r.Status.Conditions, observatory.ConditionSafeToPowerOff)
		if r.Status.Step != "" || safe.Message != "Safe to power off" {
			t.Errorf("a Released reservation has step %q and SafeToPowerOff %+v, want no step and \"Safe to power off\"", r.Status.Step, safe)
		}
		// The simulators respond at once, so a step takes 0 s on the
		// bubble's clock. Activation takes 1 s because the dome waits
		// for the weather station's Safe condition, which the status
		// writer writes within a second.
		events := eventMessages(w.api)
		for _, want := range []string{
			"Wait: Done in 0 s: took Telescope east",
			"Ready: Ready in 1 s at east-telescope.observatory.svc:7624",
			"Deactivating: Deactivating Telescope east",
			"Released: Released in 0 s: Telescope east is safe to power off",
		} {
			if !slices.Contains(events, want) {
				t.Errorf("the Events are %q, want one to be %q", events, want)
			}
		}
	})
}

// SafeToPowerOff gives the reason that the devices are not yet safe to
// power off, and starts with the state.
func TestSafeToPowerOffGivesTheReason(t *testing.T) {
	cases := []struct {
		phase   observatory.ReservationPhase
		reason  string
		message string
	}{
		{observatory.ReservationScheduled, "Scheduled", "Waiting to activate Telescope east"},
		{observatory.ReservationActivating, "Activating", "Activating Telescope east"},
		{observatory.ReservationReady, "InUse", "In use by desktop"},
		{observatory.ReservationDeactivating, "Deactivating", "Deactivating Telescope east"},
		{observatory.ReservationReleased, "Released", "Safe to power off"},
	}
	for _, c := range cases {
		t.Run(string(c.phase), func(t *testing.T) {
			r := &runner{o: &operator{namespace: testNamespace}, res: &observatory.Reservation{
				Spec: observatory.ReservationSpec{Telescope: "east", Holder: "desktop"},
			}}
			r.status.Phase = c.phase
			r.compose()
			safe := conditionOf(r.status.Conditions, observatory.ConditionSafeToPowerOff)
			if safe.Reason != c.reason || safe.Message != c.message {
				t.Errorf("the SafeToPowerOff condition is %s: %s, want %s: %s", safe.Reason, safe.Message, c.reason, c.message)
			}
		})
	}
}

// printedLines returns the columns of `kubectl get rsv -o wide` for
// each change of a reservation's status, in order. It skips an update
// that changes only metadata, such as the deletionTimestamp that the API
// server sets when a finalizer blocks a delete, because the operator
// did not write it.
func printedLines(t *testing.T, a *fakeAPI, name string) []string {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	var last any
	for _, e := range a.events {
		if e.collection != kindCollection(observatory.ReservationKind) || e.kind != "MODIFIED" ||
			e.object["metadata"].(map[string]any)["name"] != name || equalJSON(e.object["status"], last) {
			continue
		}
		last = e.object["status"]
		var r observatory.Reservation
		body, _ := json.Marshal(e.object)
		if err := json.Unmarshal(body, &r); err != nil {
			t.Fatalf("decoding the Reservation: %v", err)
		}
		line := []string{string(r.Status.Phase), string(r.Status.Step), conditionOf(r.Status.Conditions, observatory.ConditionReady).Message}
		if r.Status.Endpoint != nil {
			line = append(line, r.Status.Endpoint.Host, strconv.Itoa(int(r.Status.Endpoint.Port)))
		}
		out = append(out, strings.Join(line, " | "))
	}
	return out
}

// repeatedLines returns each line that equals the line before it.
func repeatedLines(lines []string) []string {
	var out []string
	for i := 1; i < len(lines); i++ {
		if lines[i] == lines[i-1] {
			out = append(out, lines[i])
		}
	}
	return out
}

// Each status write of a reservation changes what `kubectl get rsv -w`
// prints, so the watch does not print the same line twice. The operator
// holds back a write that changes only a time, or a step record that no
// column shows, until a visible column changes.
func TestEachStatusWriteChangesWhatKubectlGetShows(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		time.Sleep(3 * time.Minute)
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		w.until(10*time.Minute, "the reservation stays", func() bool {
			_, ok := w.reservation("east-tonight")
			return !ok
		})
		if repeated := repeatedLines(printedLines(t, w.api, "east-tonight")); len(repeated) != 0 {
			t.Errorf("status writes printed the line before them again: %q", repeated)
		}
	})
}

// While a step runs, the Ready condition has the step as its reason
// and the step's summary as its message, so the Message column of
// `kubectl get rsv -w` shows each change.
func TestARunningStepIsTheReadyConditionsReason(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.reserve("first", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("first", observatory.ReservationReady, 10*time.Minute)
		w.reserve("second", map[string]any{"telescope": "east", "holder": "laptop"})
		w.until(time.Minute, "the second reservation does not wait", func() bool {
			r, _ := w.reservation("second")
			ready := conditionOf(r.Status.Conditions, observatory.ConditionReady)
			return r.Status.Step == observatory.StepWait && ready.Reason == "Wait" && ready.Message == "Waiting for Reservation first"
		})
	})
}
