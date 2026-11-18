package main

import "testing"

func TestDeviceNameIsTheLowercaseConnector(t *testing.T) {
	cases := []struct {
		connector string
		want      string
	}{
		{connector: "HDMI-A-1", want: "hdmi-a-1"},
		{connector: "DP-2", want: "dp-2"},
		{connector: "eDP-1", want: "edp-1"},
	}
	for _, c := range cases {
		t.Run(c.connector, func(t *testing.T) {
			if got := deviceName(c.connector); got != c.want {
				t.Fatalf("deviceName = %q, want %q", got, c.want)
			}
			if got := appID(c.connector); got != c.want {
				t.Fatalf("appID = %q, want %q", got, c.want)
			}
		})
	}
}

func TestControlNameIsTheOutputsNameWithASuffix(t *testing.T) {
	cases := []struct {
		connector string
		want      string
	}{
		{connector: "HDMI-A-1", want: "hdmi-a-1-control"},
		{connector: "DP-2", want: "dp-2-control"},
		{connector: "eDP-1", want: "edp-1-control"},
	}
	for _, c := range cases {
		t.Run(c.connector, func(t *testing.T) {
			if got := controlName(c.connector); got != c.want {
				t.Fatalf("controlName = %q, want %q", got, c.want)
			}
			output, control := outputOfControl(c.want)
			if !control || output != deviceName(c.connector) {
				t.Fatalf("outputOfControl(%q) = %q, %v, want %q, true", c.want, output, control, deviceName(c.connector))
			}
		})
	}
}

func TestOutputOfControlLeavesAnOutputsNameAlone(t *testing.T) {
	output, control := outputOfControl("hdmi-a-1")
	if control || output != "hdmi-a-1" {
		t.Fatalf("outputOfControl(%q) = %q, %v, want %q, false", "hdmi-a-1", output, control, "hdmi-a-1")
	}
}

// audio-operator tests the same table against the value it builds from
// the ELD. Change a row here only together with the same row there,
// because a matchAttribute constraint compares the values byte for
// byte.
func TestMonitorIDMatchesTheAudioOperatorsValueForTheSameMonitor(t *testing.T) {
	cases := []struct {
		name string
		edid EDID
		want string
	}{
		{
			name: "an LG ultrawide",
			edid: EDID{Manufacturer: "GSM", ProductCode: 0x5b09, ModelName: "LG ULTRAWIDE"},
			want: "gsm-5b09-lg-ultrawide",
		},
		{
			// With no name, the value has two parts and no trailing
			// dash, because the ELD has only those two parts.
			name: "a monitor with no name",
			edid: EDID{Manufacturer: "BOE", ProductCode: 0x095f},
			want: "boe-095f",
		},
		{
			name: "a name with spaces around it",
			edid: EDID{Manufacturer: "DEL", ProductCode: 0xa0c5, ModelName: "  DELL U2415  "},
			want: "del-a0c5-dell-u2415",
		},
		{
			// An empty value publishes no attribute, as
			// TestSliceDevicesPublishesNoPairingIDWithoutAManufacturer
			// checks.
			name: "a manufacturer that does not decode",
			edid: EDID{ProductCode: 0x095f, ModelName: "Panel"},
			want: "",
		},
		{
			name: "a product code below 0x1000 keeps its leading zeros",
			edid: EDID{Manufacturer: "GSM", ProductCode: 0x0001, ModelName: "LG HDR WQHD"},
			want: "gsm-0001-lg-hdr-wqhd",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := monitorID(c.edid); got != c.want {
				t.Fatalf("monitorID = %q, want %q", got, c.want)
			}
		})
	}
}

func TestMonitorIDCollapsesRunsOfSpaces(t *testing.T) {
	// A run of spaces becomes one dash. Some monitors pad the middle
	// of their name descriptor with extra spaces.
	edid := EDID{Manufacturer: "GSM", ProductCode: 0x7716, ModelName: "LG   HDR  WQHD"}
	if got := monitorID(edid); got != "gsm-7716-lg-hdr-wqhd" {
		t.Fatalf("monitorID = %q, want %q", got, "gsm-7716-lg-hdr-wqhd")
	}
}

func TestMonitorIDFromARealMonitorsEDID(t *testing.T) {
	edid, err := ParseEDID(loadEDID(t, "lg-hdr-wqhd"))
	if err != nil {
		t.Fatalf("parsing the lg-hdr-wqhd EDID: %v", err)
	}
	if got := monitorID(edid); got != "gsm-7716-lg-hdr-wqhd" {
		t.Fatalf("monitorID = %q, want %q", got, "gsm-7716-lg-hdr-wqhd")
	}
}

func TestPairingAttributeHasItsOwnDomain(t *testing.T) {
	// A name with no domain belongs to the publishing driver, and would
	// never match audio-operator's attribute.
	if pairingAttribute != "monitor.liken.sh/id" {
		t.Fatalf("pairingAttribute = %q, want %q", pairingAttribute, "monitor.liken.sh/id")
	}
}
