package main

import (
	"testing"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// INDI's weather base class sets each light from its limits, and the
// status reads the light as a verdict.
func TestAWeatherLightIsAVerdict(t *testing.T) {
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

// A device's phase follows from its pod and from its CONNECTION.
func TestADevicesPhase(t *testing.T) {
	connection := func(state indi.State, connect bool) indi.Property {
		return indi.Property{Name: "CONNECTION", State: state, Members: []indi.Member{{Name: "CONNECT", Switch: connect}, {Name: "DISCONNECT", Switch: !connect}}}
	}
	cases := []struct {
		name               string
		stopping, pod, def bool
		connection         indi.Property
		fault              string
		want               observatory.DevicePhase
	}{
		{"no pod", false, false, false, indi.Property{}, "", observatory.DeviceInventory},
		{"a pod that stops", true, true, true, connection(indi.Ok, true), "", observatory.DeviceDisconnecting},
		{"a driver not yet on the server", false, true, false, indi.Property{}, "", observatory.DeviceStarting},
		{"a connect in flight", false, true, true, connection(indi.Busy, true), "", observatory.DeviceConnecting},
		{"a disconnect in flight", false, true, true, connection(indi.Busy, false), "", observatory.DeviceDisconnecting},
		{"a refused connect", false, true, true, connection(indi.Alert, false), "", observatory.DeviceError},
		{"a failed step", false, true, true, connection(indi.Idle, false), "it broke", observatory.DeviceError},
		{"connected", false, true, true, connection(indi.Ok, true), "", observatory.DeviceConnected},
		{"disconnected", false, true, true, connection(indi.Idle, false), "", observatory.DeviceStarting},
	}
	for _, c := range cases {
		if got := devicePhase(c.stopping, c.pod, c.def, c.connection, c.fault); got != c.want {
			t.Errorf("%s: phase = %s, want %s", c.name, got, c.want)
		}
	}
}
