package main

import "testing"

func TestDeviceNameIsTheConnectorAsADNSLabel(t *testing.T) {
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
			// Version 0 routes by the device name, so the two agree by
			// construction and a claim's DISPLAY_APP_ID is readable
			// from the allocation alone.
			if got := appID(c.connector); got != c.want {
				t.Fatalf("appID = %q, want %q", got, c.want)
			}
		})
	}
}

// The pairing vectors. The audio operator carries this same table
// against its own derivation from the ELD, and the two suites together
// are what prove the drivers agree. A matchAttribute constraint
// compares the values byte for byte, so a disagreement of one
// character parks every pairing claim forever. Change a row here only
// with the same row changed there.
func TestMonitorIDPairsAScreenWithItsSpeakers(t *testing.T) {
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
			// A nameless panel takes the two-part form, never a
			// trailing dash and never an empty value, because those
			// two parts are what the ELD also carries.
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
			// An empty value publishes no attribute at all, which
			// TestSliceDevicesPublishesNoPairingIDWithoutAManufacturer
			// holds to.
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
	// Two spaces between words make one dash, not two. A monitor that
	// pads its name descriptor mid-string is where this shows up.
	edid := EDID{Manufacturer: "GSM", ProductCode: 0x7716, ModelName: "LG   HDR  WQHD"}
	if got := monitorID(edid); got != "gsm-7716-lg-hdr-wqhd" {
		t.Fatalf("monitorID = %q", got)
	}
}

func TestMonitorIDOfARealMonitor(t *testing.T) {
	edid, err := ParseEDID(loadEDID(t, "lg-hdr-wqhd"))
	if err != nil {
		t.Fatal(err)
	}
	if got := monitorID(edid); got != "gsm-7716-lg-hdr-wqhd" {
		t.Fatalf("monitorID = %q", got)
	}
}

func TestPairingAttributeCarriesItsOwnDomain(t *testing.T) {
	// An attribute name with no domain belongs to the driver that
	// published it, so a bare name here and a bare name in the audio
	// operator would be two different fully qualified names and would
	// never match.
	if pairingAttribute != "monitor.liken.sh/id" {
		t.Fatalf("pairingAttribute = %q", pairingAttribute)
	}
}
