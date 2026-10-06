package main

import "testing"

// A runner looks at a server's devices after each change, and a device
// counts as come back once for each subscription. A device that the
// runner cannot find on its first look, because its driver has not
// defined DRIVER_INFO yet, still counts at a later look.
func TestEachDeviceCountsOnceAfterASubscription(t *testing.T) {
	t.Parallel()
	a := newAppearances()
	a.opened("telescope-east")
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
		server := "telescope-east"
		if c.device == "Dome Simulator" {
			server = "observatory-lab"
		}
		if got := a.take(server, c.device); got != c.want {
			t.Errorf("%s: take(%s, %s) = %v, want %v", c.look, server, c.device, got, c.want)
		}
	}
}
