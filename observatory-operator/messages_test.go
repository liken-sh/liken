package main

// What a person reads in `kubectl get`, `kubectl describe`, and the
// Events: each message leads with the state, in a few words.

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
	cases := []struct {
		name   string
		status deviceStatus
		fault  string
		// kept is true while a Ready reservation's runner keeps the
		// device's pod.
		kept bool
		want string
	}{
		{"a device with no pod", deviceStatus{DeviceStatus: observatory.DeviceStatus{Phase: observatory.DeviceInventory}}, "", false, "Not reserved"},
		{"a device whose pod a Ready reservation creates again", deviceStatus{DeviceStatus: observatory.DeviceStatus{Phase: observatory.DeviceInventory}}, "", true,
			"Creating pod east-main-camera"},
		{"a pod whose driver has not defined its device", deviceStatus{DeviceStatus: observatory.DeviceStatus{Phase: observatory.DeviceStarting, Pod: "camera-east-main"}}, "",
			false, "Waiting for its driver on east-telescope"},
		{"a connected device", deviceStatus{DeviceStatus: observatory.DeviceStatus{Phase: observatory.DeviceConnected, IndiDevice: "CCD Simulator"}}, "", true, "Connected on east-telescope"},
		{"a device that failed", deviceStatus{DeviceStatus: observatory.DeviceStatus{Phase: observatory.DeviceError, IndiDevice: "CCD Simulator"}}, "indi: CCD Simulator.CCD_GAIN is Alert",
			true, "Failed: indi: CCD Simulator.CCD_GAIN is Alert"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := deviceMessage(c.status, ref, "east-main-camera", c.fault, c.kept); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// eventMessages answers the reason and the message of each Event, in
// the order the operator wrote them.
func eventMessages(a *fakeAPI) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, e := range a.events {
		if e.collection == eventsCollection && e.kind == "ADDED" {
			out = append(out, e.object["reason"].(string)+": "+e.object["message"].(string))
		}
	}
	return out
}

// A finished reservation shows no step, so `kubectl get rsv` never
// shows a step that ended as if it ran. The Ready condition, which the
// Message column shows, gives the endpoint.
func TestAFinishedReservationShowsNoStep(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		end := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop", "end": end})
		r := w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		ready := conditionOf(r.Status.Conditions, observatory.ConditionReady)
		if r.Status.Step != "" || ready.Reason != "Ready" || ready.Message != "Ready at east-telescope.observatory.svc:7624" {
			t.Errorf("a Ready reservation shows step %q and Ready %+v", r.Status.Step, ready)
		}
		time.Sleep(time.Hour)
		r = w.phase("east-tonight", observatory.ReservationReleased, 10*time.Minute)
		safe := conditionOf(r.Status.Conditions, observatory.ConditionSafeToPowerOff)
		if r.Status.Step != "" || safe.Message != "Safe to power off" {
			t.Errorf("a Released reservation shows step %q and SafeToPowerOff %+v", r.Status.Step, safe)
		}
		// The simulators answer at once, so each step takes no time on
		// the bubble's clock.
		events := eventMessages(w.api)
		for _, want := range []string{
			"Wait: Done in 0 s: took Telescope east",
			"Ready: Ready in 0 s at east-telescope.observatory.svc:7624",
			"Deactivating: Deactivating Telescope east",
			"Released: Released in 0 s: Telescope east is safe to power off",
		} {
			if !slices.Contains(events, want) {
				t.Errorf("no Event %q in %q", want, events)
			}
		}
	})
}

// SafeToPowerOff says why the devices are not safe to power off yet,
// and leads with the state.
func TestSafeToPowerOffSaysWhyNot(t *testing.T) {
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
				t.Errorf("SafeToPowerOff = %s: %s, want %s: %s", safe.Reason, safe.Message, c.reason, c.message)
			}
		})
	}
}

// printedLines answers the columns of `kubectl get rsv -o wide` for each
// change of a reservation's status, in order. A change of the metadata
// alone, such as a delete that a finalizer holds, is left out: the API
// server writes it, and the operator cannot give it a column of its own.
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
			t.Fatal(err)
		}
		line := []string{string(r.Status.Phase), string(r.Status.Step), conditionOf(r.Status.Conditions, observatory.ConditionReady).Message}
		if r.Status.Endpoint != nil {
			line = append(line, r.Status.Endpoint.Host, strconv.Itoa(int(r.Status.Endpoint.Port)))
		}
		out = append(out, strings.Join(line, " | "))
	}
	return out
}

// repeatedLines answers each line that equals the line before it.
func repeatedLines(lines []string) []string {
	var out []string
	for i := 1; i < len(lines); i++ {
		if lines[i] == lines[i-1] {
			out = append(out, lines[i])
		}
	}
	return out
}

// Each status write of a reservation changes a line of
// `kubectl get rsv -w`, so the watch prints no line twice for the
// operator's writes. A write that changes only a time or a step's
// record below the columns waits for a change that a person can see.
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

// While a step runs, the Ready condition gives the step as its reason
// and the step's summary as its message, so the Message column of
// `kubectl get rsv -w` shows what changes.
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
