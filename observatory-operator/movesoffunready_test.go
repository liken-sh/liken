package main

// A device that moves off a telescope whose reservation is not Ready:
// one whose activation failed, or one that still activates. The server
// in the device's pod label releases the device in either state, as a
// Ready server does, and the server the device joins then starts it.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// westReadyWorld runs the example with the west telescope reserved and
// Ready, and the east telescope free.
func westReadyWorld(t *testing.T) *world {
	w := startWorld(t)
	w.reserve("west-tonight", map[string]any{"telescope": "west", "holder": "desktop"})
	w.phase("west-tonight", observatory.ReservationReady, 10*time.Minute)
	return w
}

// focuserToWest moves the east focuser to the west telescope's train.
func focuserToWest(w *world) {
	w.put(observatory.FocuserKind, "east", map[string]any{"opticalTrain": "west-imaging", "driver": map[string]any{"name": "indi_simulator_focus"}})
}

func TestADeviceMovedOffAnUnreadyTelescopeJoinsTheOther(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		phase observatory.ReservationPhase
		// move reserves east, moves the focuser while east-tonight is
		// in phase, and lets east go on.
		move func(w *world)
	}{
		{name: "east Failed", phase: observatory.ReservationFailed, move: func(w *world) {
			w.indi.refuse("CCD Simulator", "CCD_TEMPERATURE")
			w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
			w.phase("east-tonight", observatory.ReservationFailed, 10*time.Minute)
			focuserToWest(w)
		}},
		{name: "east activating", phase: observatory.ReservationActivating, move: func(w *world) {
			w.api.holdPending("east-main-camera")
			w.indi.hold("CCD Simulator", "CCD_TEMPERATURE")
			w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
			w.until(10*time.Minute, "the camera's pod does not start", func() bool {
				return slices.Contains(w.api.names(podsCollection), "east-main-camera")
			})
			focuserToWest(w)
			w.until(time.Minute, "the focuser's status does not say that it waits for east", func() bool {
				focuser, _ := decode[observatory.Focuser](w.t, w.api, kindCollection(observatory.FocuserKind), "east")
				return conditionOf(focuser.Status.Conditions, observatory.ConditionReady).Message == "Waiting for east-telescope to release it to west-telescope"
			})
			w.api.setPodReady("east-main-camera")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := westReadyWorld(t)
				c.move(w)
				w.until(5*time.Minute, "the focuser does not connect on west", func() bool {
					return slices.Contains(w.indi.connected("west-telescope"), "Focuser Simulator")
				})
				time.Sleep(2 * statusWindow)
				synctest.Wait()

				if r, _ := w.reservation("east-tonight"); r.Status.Phase != c.phase {
					t.Errorf("east-tonight is %s, want %s", r.Status.Phase, c.phase)
				}
				deleted := "Normal PodDeleted: Deleted pod east-focuser, because the Focuser left Telescope east while Reservation east-tonight is " + string(c.phase) + "."
				if got := typedEvents(w.api, observatory.FocuserKind, "east"); !slices.Contains(got, deleted) {
					t.Errorf("events = %q, want %q", got, deleted)
				}
				focuser, _ := decode[observatory.Focuser](t, w.api, kindCollection(observatory.FocuserKind), "east")
				if s := focuser.Status; s.Phase != observatory.DeviceConnected || s.Endpoint == nil || s.Endpoint.Service != "west-telescope" {
					t.Errorf("focuser status = phase %s, endpoint %+v", s.Phase, s.Endpoint)
				}
			})
		})
	}
}

// A device that waits for the server it left says so in its Ready
// condition, and the condition's Event records the move. The hold on
// the cap's park keeps the cap on east while its deactivation runs
// there.
func TestADeviceThatWaitsForItsOldServerSaysSo(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := twoReadyWorld(t)
		w.indi.hold("Dust Cover Simulator", "CAP_PARK")
		w.put(observatory.DustCapKind, "east", map[string]any{"opticalTrain": "west-imaging", "driver": map[string]any{"name": "indi_simulator_dustcover"}, "deactivation": []any{map[string]any{"state": "Closed"}}})
		want := "Waiting for east-telescope to release it to west-telescope"
		w.until(time.Minute, "the cap's status does not say that it waits for east", func() bool {
			dustCap, _ := decode[observatory.DustCap](t, w.api, kindCollection(observatory.DustCapKind), "east")
			return conditionOf(dustCap.Status.Conditions, observatory.ConditionReady).Message == want
		})
		time.Sleep(2 * statusWindow)
		synctest.Wait()

		dustCap, _ := decode[observatory.DustCap](t, w.api, kindCollection(observatory.DustCapKind), "east")
		if ready := conditionOf(dustCap.Status.Conditions, observatory.ConditionReady); ready.Status != observatory.ConditionFalse {
			t.Errorf("Ready = %+v, want False", ready)
		}
		if got := typedEvents(w.api, observatory.DustCapKind, "east"); !slices.ContainsFunc(got, func(e string) bool { return strings.HasSuffix(e, ": "+want) }) {
			t.Errorf("events = %q, want one that says %q", got, want)
		}
	})
}
