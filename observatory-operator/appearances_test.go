package main

import "testing"

// take reports each device once after a server's subscription opens,
// on the first check of that device, even when the check comes several
// passes later because the driver had not yet defined DRIVER_INFO. On
// a server with no subscription, take reports only a CONNECTION definition.
func TestEachDeviceCountsOnceAfterASubscription(t *testing.T) {
	t.Parallel()
	a := newAppearances()
	a.opened("east-telescope")
	cases := []struct {
		look, device string
		want         bool
	}{
		{"the first look", "CCD Simulator", true},
		{"a later look", "Simulator IO", true},
		{"a second look at the same device", "Simulator IO", false},
		{"a device on another server", "Dome Simulator", false},
	}
	for _, c := range cases {
		server := "east-telescope"
		if c.device == "Dome Simulator" {
			server = "lab-observatory"
		}
		if got := a.take(server, c.device); got != c.want {
			t.Errorf("on %s, take(%s, %s) = %v, want %v", c.look, server, c.device, got, c.want)
		}
	}
}
