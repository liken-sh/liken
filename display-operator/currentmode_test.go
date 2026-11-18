package main

import "testing"

func TestConnectorNameMatchesTheKernelsName(t *testing.T) {
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

// crtcWithMode builds a GETCRTC result with the mode name and the
// ModeValid value that a test gives.
func crtcWithMode(name string, valid uint32) drmCrtc {
	crtc := drmCrtc{ModeValid: valid}
	copy(crtc.Mode.Name[:], name)
	return crtc
}

func TestCrtcModeReadsTheKernelsOwnName(t *testing.T) {
	if got := crtcMode(crtcWithMode("1280x720", 1)); got != "1280x720" {
		t.Errorf("mode = %q, want %q", got, "1280x720")
	}
}

func TestCrtcModeAppendsTheRefreshTheCardReports(t *testing.T) {
	crtc := crtcWithMode("3840x1600", 1)
	crtc.Mode.Vrefresh = 24
	if got := crtcMode(crtc); got != "3840x1600@24" {
		t.Errorf("mode = %q, want %q", got, "3840x1600@24")
	}
}

func TestCrtcModeOmitsAZeroRefresh(t *testing.T) {
	if got := crtcMode(crtcWithMode("1280x720", 1)); got != "1280x720" {
		t.Errorf("mode = %q, want %q", got, "1280x720")
	}
}

func TestModeNameStopsAtTheNulByte(t *testing.T) {
	info := drmModeInfo{Vrefresh: 60}
	copy(info.Name[:], "1920x1080")
	if got := modeName(info); got != "1920x1080" {
		t.Errorf("name = %q, want %q", got, "1920x1080")
	}
}

func TestCrtcModeReportsNothingForACrtcThatDrivesNothing(t *testing.T) {
	// The kernel clears mode_valid while the crtc is disabled, and Mode
	// then holds whatever the caller sent.
	if got := crtcMode(crtcWithMode("1280x720", 0)); got != "" {
		t.Errorf("mode = %q, want nothing", got)
	}
}
