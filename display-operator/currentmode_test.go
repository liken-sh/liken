package main

import "testing"

func TestConnectorNameMatchesTheOneSysfsSpells(t *testing.T) {
	// The published attributes, weston.ini, and the mode record all key
	// on the connector name sysfs prints, and this is the only place
	// the kernel's numeric type becomes that name again.
	cases := []struct {
		connectorType uint32
		typeID        uint32
		want          string
	}{
		{connectorType: 11, typeID: 1, want: "HDMI-A-1"},
		{connectorType: 11, typeID: 2, want: "HDMI-A-2"},
		{connectorType: 10, typeID: 1, want: "DP-1"},
		{connectorType: 14, typeID: 1, want: "eDP-1"},
		{connectorType: 15, typeID: 3, want: "Virtual-3"},
		{connectorType: 200, typeID: 1, want: ""},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			if got := connectorName(c.connectorType, c.typeID); got != c.want {
				t.Errorf("connectorName(%d, %d) = %q, want %q", c.connectorType, c.typeID, got, c.want)
			}
		})
	}
}

// crtcWithMode is what GETCRTC fills in for a lit output: the name is
// a NUL-terminated array of 32 bytes, the same vocabulary the sysfs
// mode list and a claim's mode parameter use.
func crtcWithMode(name string, valid uint32) drmCrtc {
	crtc := drmCrtc{ModeValid: valid}
	copy(crtc.Mode.Name[:], name)
	return crtc
}

func TestCrtcModeReadsTheKernelsOwnName(t *testing.T) {
	if got := crtcMode(crtcWithMode("1280x720", 1)); got != "1280x720" {
		t.Errorf("mode = %q", got)
	}
}

func TestCrtcModeReportsNothingForACrtcThatDrivesNothing(t *testing.T) {
	// The kernel sets mode_valid only while the crtc is enabled, and
	// the mode it leaves beside it means nothing then. An output that
	// runs no mode publishes no currentMode.
	if got := crtcMode(crtcWithMode("1280x720", 0)); got != "" {
		t.Errorf("mode = %q, want nothing", got)
	}
}
