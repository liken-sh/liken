package main

import "testing"

func TestDeviceNameRoundTrip(t *testing.T) {
	cases := []struct {
		card, pcm int
		name      string
	}{
		{card: 0, pcm: 0, name: "card0-pcm0"},
		{card: 0, pcm: 3, name: "card0-pcm3"},
		{card: 1, pcm: 12, name: "card1-pcm12"},
	}
	for _, c := range cases {
		if got := deviceName(c.card, c.pcm); got != c.name {
			t.Errorf("deviceName(%d, %d) = %q", c.card, c.pcm, got)
		}
		card, pcm, ok := outputFromDeviceName(c.name)
		if !ok || card != c.card || pcm != c.pcm {
			t.Errorf("outputFromDeviceName(%q) = %d, %d, %v", c.name, card, pcm, ok)
		}
	}
}

func TestOutputFromDeviceNameRejectsWhatItDidNotWrite(t *testing.T) {
	// A prepare call carries whatever the claim's allocation says, so
	// a name that this driver did not build must not resolve to an
	// output.
	names := []string{
		"",
		"card0",
		"pcm3",
		"card0-pcm",
		"cardx-pcm3",
		"card0-pcm3p",
		"card-1-pcm3",
		"card0-pcm+3",
		"card00-pcm3",
		"a0-ab-51-33-b7-12",
	}
	for _, name := range names {
		if _, _, ok := outputFromDeviceName(name); ok {
			t.Errorf("outputFromDeviceName(%q) accepted it", name)
		}
	}
}

func TestPNPID(t *testing.T) {
	cases := []struct {
		id   uint16
		want string
	}{
		// The two bytes in EDID order. The kernel prints the ELD's own
		// manufacture_id in the other byte order, 0x6d1e for this one.
		{id: 0x1e6d, want: "GSM"},
		{id: 0x10ac, want: "DEL"},
		{id: 0x0000, want: ""},
		{id: 0xffff, want: ""},
	}
	for _, c := range cases {
		if got := pnpID(c.id); got != c.want {
			t.Errorf("pnpID(%#04x) = %q, want %q", c.id, got, c.want)
		}
	}
}

// The pairing vectors. The display operator's suite carries the same
// table, because the two operators must publish the same value for
// the same monitor. A change to either derivation that broke the
// parity fails a test in both repositories.
func TestMonitorID(t *testing.T) {
	cases := []struct {
		name         string
		manufacturer string
		product      uint16
		monitor      string
		want         string
	}{
		{
			name:         "the plain form",
			manufacturer: "GSM", product: 0x5b09, monitor: "LG ULTRAWIDE",
			want: "gsm-5b09-lg-ultrawide",
		},
		{
			// A panel with no name descriptor. The value is the
			// manufacturer and the product alone, and not nothing,
			// because the display operator reads the same monitor and
			// must build the same value.
			name:         "no monitor name",
			manufacturer: "BOE", product: 0x095f, monitor: "",
			want: "boe-095f",
		},
		{
			// EDID pads a descriptor with spaces, and the padding must
			// not reach the value.
			name:         "padding does not reach the value",
			manufacturer: "DEL", product: 0xa0c5, monitor: "  DELL U2415  ",
			want: "del-a0c5-dell-u2415",
		},
		{
			// Two bytes that decode to something other than three
			// letters name no monitor, so the caller publishes no
			// pairing attribute and a constraint on it allocates
			// nothing.
			name:         "the manufacturer did not decode",
			manufacturer: pnpID(0x0000), product: 0x5b09, monitor: "LG ULTRAWIDE",
			want: "",
		},
		{
			name:         "a product code with leading zeros",
			manufacturer: "GSM", product: 0x0001, monitor: "LG HDR WQHD",
			want: "gsm-0001-lg-hdr-wqhd",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := monitorID(c.manufacturer, c.product, c.monitor); got != c.want {
				t.Errorf("monitorID = %q, want %q", got, c.want)
			}
		})
	}
}
