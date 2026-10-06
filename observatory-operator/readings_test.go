package main

import (
	"testing"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// INDI's weather base class sets each light from its limits, and the
// status reads the light as a verdict.
func TestAWeatherLightIsAVerdict(t *testing.T) {
	t.Parallel()
	cases := map[indi.State]observatory.Safety{
		indi.Ok:    observatory.SafetySafe,
		indi.Busy:  observatory.SafetyWarning,
		indi.Alert: observatory.SafetyDanger,
		indi.Idle:  observatory.SafetyUnknown,
	}
	for light, want := range cases {
		if got := safety(light); got != want {
			t.Errorf("safety(%s) = %s, want %s", light, got, want)
		}
	}
}

// A weather station's verdict is its SAFETY light. The weather
// simulator leaves the light Idle and sets the state of SAFETY_STATUS,
// as the transcript in indi/testdata/weather/connect.xml records, so
// an Idle light reads the property's state.
func TestAWeatherStationsVerdict(t *testing.T) {
	t.Parallel()
	status := func(state, light indi.State) indi.Property {
		return indi.Property{Name: "SAFETY_STATUS", State: state, Members: []indi.Member{{Name: "SAFETY", Light: light}}}
	}
	cases := []struct {
		name     string
		property indi.Property
		want     observatory.Safety
	}{
		{"the simulator after connect", status(indi.Ok, indi.Idle), observatory.SafetySafe},
		{"a station before its first reading", status(indi.Idle, indi.Idle), observatory.SafetyUnknown},
		{"a light that warns", status(indi.Ok, indi.Busy), observatory.SafetyWarning},
		{"a light in danger", status(indi.Alert, indi.Alert), observatory.SafetyDanger},
		{"no SAFETY member", indi.Property{Name: "SAFETY_STATUS", State: indi.Ok}, observatory.SafetySafe},
	}
	for _, c := range cases {
		if got := stationSafety(c.property); got != c.want {
			t.Errorf("%s: safety = %s, want %s", c.name, got, c.want)
		}
	}
}

// A device's phase follows from its pod and from its CONNECTION.
func TestADevicesPhase(t *testing.T) {
	t.Parallel()
	connection := func(state indi.State, connect bool) indi.Property {
		return indi.Property{Name: "CONNECTION", State: state, Members: []indi.Member{{Name: "CONNECT", Switch: connect}, {Name: "DISCONNECT", Switch: !connect}}}
	}
	cases := []struct {
		name               string
		stopping, pod, def bool
		standing           standing
		connection         indi.Property
		fault              string
		want               observatory.DevicePhase
	}{
		{"on the shelf", false, false, false, onShelf, indi.Property{}, "", observatory.DeviceInventory},
		{"installed, with no active reservation", false, false, false, idle, indi.Property{}, "", observatory.DeviceIdle},
		{"no pod yet while a reservation activates", false, false, false, activating, indi.Property{}, "", observatory.DeviceStarting},
		{"no pod while a Ready reservation keeps it", false, false, false, kept, indi.Property{}, "", observatory.DeviceStarting},
		{"a pod that stops", true, true, true, idle, connection(indi.Ok, true), "", observatory.DeviceDisconnecting},
		{"a driver not yet on the server", false, true, false, activating, indi.Property{}, "", observatory.DeviceStarting},
		{"a connect in flight", false, true, true, activating, connection(indi.Busy, true), "", observatory.DeviceConnecting},
		{"a disconnect in flight", false, true, true, idle, connection(indi.Busy, false), "", observatory.DeviceDisconnecting},
		{"a refused connect", false, true, true, activating, connection(indi.Alert, false), "", observatory.DeviceError},
		{"a failed step", false, true, true, activating, connection(indi.Idle, false), "it broke", observatory.DeviceError},
		{"connected", false, true, true, kept, connection(indi.Ok, true), "", observatory.DeviceConnected},
		{"disconnected", false, true, true, kept, connection(indi.Idle, false), "", observatory.DeviceStarting},
		{"connected while a release runs its deactivation", false, true, true, releasing, connection(indi.Ok, true), "", observatory.DeviceConnected},
		{"disconnected by a release", false, true, true, releasing, connection(indi.Idle, false), "", observatory.DeviceDisconnecting},
		{"a driver that a release stops", false, true, false, releasing, indi.Property{}, "", observatory.DeviceDisconnecting},
		{"a pod that a release deleted", false, false, false, releasing, indi.Property{}, "", observatory.DeviceIdle},
	}
	for _, c := range cases {
		if got := devicePhase(c.stopping, c.pod, c.def, c.standing, c.connection, c.fault); got != c.want {
			t.Errorf("%s: phase = %s, want %s", c.name, got, c.want)
		}
	}
}
