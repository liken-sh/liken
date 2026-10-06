package main

// What a person reads in `kubectl get`, `kubectl describe`, and the
// Events: each message leads with the state, in a few words.

import (
	"slices"
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
		want   string
	}{
		{"a device with no pod", deviceStatus{DeviceStatus: observatory.DeviceStatus{Phase: observatory.DeviceInventory}}, "", "Not reserved"},
		{"a pod whose driver has not defined its device", deviceStatus{DeviceStatus: observatory.DeviceStatus{Phase: observatory.DeviceStarting, Pod: "camera-east-main"}}, "",
			"Waiting for its driver on east-telescope"},
		{"a connected device", deviceStatus{DeviceStatus: observatory.DeviceStatus{Phase: observatory.DeviceConnected, IndiDevice: "CCD Simulator"}}, "", "Connected on east-telescope"},
		{"a device that failed", deviceStatus{DeviceStatus: observatory.DeviceStatus{Phase: observatory.DeviceError, IndiDevice: "CCD Simulator"}}, "indi: CCD Simulator.CCD_GAIN is Alert",
			"Failed: indi: CCD Simulator.CCD_GAIN is Alert"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := deviceMessage(c.status, ref, c.fault); got != c.want {
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
