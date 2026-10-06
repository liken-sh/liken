package main

// The conditions of a device's state, which the triggers of procedures
// read, and the Events that their transitions post.

import (
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// settle lets the status writer compose and write every status.
func (w *world) settle() {
	time.Sleep(2 * statusWindow)
	synctest.Wait()
}

// stateOf answers one condition of a device as "<status> <reason>:
// <message>", or "" when the device has no such condition.
func (w *world) stateOf(kind observatory.Kind, name, condition string) string {
	w.t.Helper()
	object, _ := decode[deviceObject](w.t, w.api, kindCollection(kind), name)
	c := conditionOf(object.Status.Conditions, condition)
	if c.Type == "" {
		return ""
	}
	return fmt.Sprintf("%s %s: %s", c.Status, c.Reason, c.Message)
}

// eventsWith answers the Events about a device whose reason is one of
// reasons, as "<type> <reason>: <message> x<count>".
func (w *world) eventsWith(kind observatory.Kind, name string, reasons ...string) []string {
	var out []string
	for _, e := range w.api.recorded.About(kind.Name, name) {
		if slices.Contains(reasons, e.Reason) {
			out = append(out, fmt.Sprintf("%s %s: %s x%d", e.Type, e.Reason, e.Message, e.Count))
		}
	}
	return out
}

func TestAConnectedDeviceReportsItsState(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		for _, c := range []struct {
			kind      observatory.Kind
			name      string
			condition string
			want      string
		}{
			{observatory.DomeKind, "lab", observatory.ConditionParked, "False Unparked: Dome lab is unparked"},
			{observatory.DomeKind, "lab", observatory.ConditionOpen, "False Closed: The shutter of Dome lab is closed"},
			{observatory.MountKind, "east", observatory.ConditionParked, "False Unparked: Mount east is unparked"},
			{observatory.MountKind, "east", observatory.ConditionTracking, "False NotTracking: Mount east is not tracking"},
			{observatory.DustCapKind, "east", observatory.ConditionOpen, "True Open: DustCap east is open"},
			{observatory.FlatPanelKind, "east", observatory.ConditionLit, "False LightOff: The light of FlatPanel east is off"},
			{observatory.CameraKind, "east-main", observatory.ConditionCooling, "False CoolerOff: The cooler of Camera east-main is off"},
			{observatory.WeatherStationKind, "lab", observatory.ConditionSafe, "True Safe: WeatherStation lab reports Safe"},
			// A device of a telescope that no reservation holds is not
			// connected, so it has none of them.
			{observatory.MountKind, "west", observatory.ConditionParked, ""},
		} {
			if got := w.stateOf(c.kind, c.name, c.condition); got != c.want {
				t.Errorf("%s %s %s = %q\nwant %q", c.kind.Name, c.name, c.condition, got, c.want)
			}
		}
	})
}

// Each state's other side, and a driver that reports neither side.
func TestTheOtherSideOfEachState(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.ask("lab-observatory", "Dome Simulator", "DOME_SHUTTER", "SHUTTER_OPEN=On", "SHUTTER_CLOSE=Off")
		w.indi.ask("east-telescope", "Telescope Simulator", "TELESCOPE_TRACK_STATE", "TRACK_ON=On", "TRACK_OFF=Off")
		w.indi.ask("east-telescope", "Dust Cover Simulator", "CAP_PARK", "PARK=On", "UNPARK=Off")
		w.indi.ask("east-telescope", "Light Panel Simulator", "FLAT_LIGHT_CONTROL", "FLAT_LIGHT_ON=On", "FLAT_LIGHT_OFF=Off")
		w.indi.ask("east-telescope", "CCD Simulator", "CCD_COOLER", "COOLER_ON=On", "COOLER_OFF=Off")
		w.indi.ask("lab-observatory", "Dome Simulator", "DOME_PARK", "PARK=Off", "UNPARK=Off")
		w.indi.setState("lab-observatory", "Weather Simulator", "SAFETY_STATUS", "Idle")
		w.settle()
		for _, c := range []struct {
			kind      observatory.Kind
			name      string
			condition string
			want      string
		}{
			{observatory.DomeKind, "lab", observatory.ConditionOpen, "True Open: The shutter of Dome lab is open"},
			{observatory.MountKind, "east", observatory.ConditionTracking, "True Tracking: Mount east is tracking"},
			{observatory.DustCapKind, "east", observatory.ConditionOpen, "False Closed: DustCap east is closed"},
			{observatory.FlatPanelKind, "east", observatory.ConditionLit, "True LightOn: The light of FlatPanel east is on"},
			{observatory.CameraKind, "east-main", observatory.ConditionCooling, "True CoolerOn: The cooler of Camera east-main is on"},
			{observatory.DomeKind, "lab", observatory.ConditionParked, "Unknown NotReported: Dome lab reports neither PARK nor UNPARK On in DOME_PARK"},
			{observatory.WeatherStationKind, "lab", observatory.ConditionSafe, "Unknown NotReported: WeatherStation lab reports no safety verdict"},
		} {
			if got := w.stateOf(c.kind, c.name, c.condition); got != c.want {
				t.Errorf("%s %s %s = %q\nwant %q", c.kind.Name, c.name, c.condition, got, c.want)
			}
		}
	})
}

// A dome that parks reports Moving while DOME_PARK is Busy, then
// Parked, and each transition posts one Event.
func TestADomeReportsItsParkAndUnpark(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		// The dome does not park while the mount is unparked.
		w.indi.ask("east-telescope", "Telescope Simulator", "TELESCOPE_PARK", "PARK=On", "UNPARK=Off")
		w.relayedLast("lab-observatory", "TELESCOPE_PARK", mountsParkedToLab)

		w.indi.setState("lab-observatory", "Dome Simulator", "DOME_PARK", "Busy")
		w.settle()
		if got, want := w.stateOf(observatory.DomeKind, "lab", observatory.ConditionParked), "Unknown Moving: Dome lab is moving"; got != want {
			t.Errorf("Parked = %q, want %q", got, want)
		}
		w.indi.ask("lab-observatory", "Dome Simulator", "DOME_PARK", "PARK=On", "UNPARK=Off")
		w.settle()
		if got, want := w.stateOf(observatory.DomeKind, "lab", observatory.ConditionParked), "True Parked: Dome lab is parked"; got != want {
			t.Errorf("Parked = %q, want %q", got, want)
		}
		w.indi.ask("lab-observatory", "Dome Simulator", "DOME_PARK", "PARK=Off", "UNPARK=On")
		w.settle()

		want := []string{
			"Normal Unparked: Dome lab is unparked x2",
			"Normal Moving: Dome lab is moving x1",
			"Normal Parked: Dome lab is parked x1",
		}
		if got := w.eventsWith(observatory.DomeKind, "lab", "Unparked", "Moving", "Parked"); !slices.Equal(got, want) {
			t.Errorf("events = %q\nwant %q", got, want)
		}
	})
}

// A weather station that turns unsafe posts a Warning, and one that
// turns safe again posts a Normal Event.
func TestAWeatherStationThatTurnsUnsafeWarns(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		for _, state := range []string{"Busy", "Alert", "Ok"} {
			w.indi.setState("lab-observatory", "Weather Simulator", "SAFETY_STATUS", state)
			w.settle()
		}
		want := []string{
			"Normal Safe: WeatherStation lab reports Safe x2",
			"Warning Warning: WeatherStation lab reports Warning x1",
			"Warning Danger: WeatherStation lab reports Danger x1",
		}
		if got := w.eventsWith(observatory.WeatherStationKind, "lab", "Safe", "Warning", "Danger"); !slices.Equal(got, want) {
			t.Errorf("events = %q\nwant %q", got, want)
		}
	})
}

// A device that disconnects drops its state conditions, so a trigger
// never reads a stale state.
func TestADisconnectedDeviceHasNoState(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.ask("east-telescope", "Dust Cover Simulator", "CONNECTION", "CONNECT=Off", "DISCONNECT=On")
		w.settle()
		dustCap, _ := decode[observatory.DustCap](t, w.api, kindCollection(observatory.DustCapKind), "east")
		var types []string
		for _, c := range dustCap.Status.Conditions {
			types = append(types, c.Type)
		}
		if want := []string{observatory.ConditionParentFound, observatory.ConditionReady}; !slices.Equal(types, want) {
			t.Errorf("conditions = %v, want %v", types, want)
		}
	})
}
