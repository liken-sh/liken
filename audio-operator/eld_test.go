package main

// The blocks in testdata are laid out the way sound/pci/hda/hda_eld.c
// lays them out, and they hold the field values of two monitors: an
// LG UltraWide on HDMI, and a Dell U2415 on DisplayPort. A machine
// with no monitor connected reports no block at all, so these are
// assembled rather than captured.

import (
	"os"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParseHDMIBlock(t *testing.T) {
	block, err := parseELD(fixture(t, "eld-hdmi-lg-ultrawide.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if block.ConnectionType != "hdmi" {
		t.Errorf("connection type = %q", block.ConnectionType)
	}
	if block.Manufacturer != "GSM" {
		t.Errorf("manufacturer = %q, want GSM", block.Manufacturer)
	}
	if block.Product != 0x5b09 {
		t.Errorf("product = %#04x", block.Product)
	}
	if block.MonitorName != "LG ULTRAWIDE" {
		t.Errorf("monitor name = %q", block.MonitorName)
	}
	if block.LPCMChannels != 2 {
		t.Errorf("LPCM channels = %d, want 2", block.LPCMChannels)
	}
	if block.LPCMMaxRateHz != 48000 {
		t.Errorf("LPCM maximum rate = %d, want 48000", block.LPCMMaxRateHz)
	}
	if got := block.bitDepths(); got != "16 20 24" {
		t.Errorf("LPCM bit depths = %q, want \"16 20 24\"", got)
	}
	if block.Speakers != "FL/FR" {
		t.Errorf("speakers = %q", block.Speakers)
	}
	if block.PortID != 0x800 {
		t.Errorf("port id = %#x", block.PortID)
	}
	if got := block.monitorID(); got != "gsm-5b09-lg-ultrawide" {
		t.Errorf("monitor id = %q", got)
	}
}

func TestParseDisplayPortBlock(t *testing.T) {
	block, err := parseELD(fixture(t, "eld-displayport-dell-u2415.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if block.ConnectionType != "displayport" {
		t.Errorf("connection type = %q", block.ConnectionType)
	}
	if block.Manufacturer != "DEL" {
		t.Errorf("manufacturer = %q, want DEL", block.Manufacturer)
	}
	if block.Product != 0x4071 {
		t.Errorf("product = %#04x", block.Product)
	}
	if block.MonitorName != "DELL U2415" {
		t.Errorf("monitor name = %q", block.MonitorName)
	}
	if block.LPCMChannels != 8 {
		t.Errorf("LPCM channels = %d, want 8", block.LPCMChannels)
	}
	if block.LPCMMaxRateHz != 48000 {
		t.Errorf("LPCM maximum rate = %d, want 48000", block.LPCMMaxRateHz)
	}
	if got := block.bitDepths(); got != "16 20 24" {
		t.Errorf("LPCM bit depths = %q, want \"16 20 24\"", got)
	}
	if block.Speakers != "FL/FR FC" {
		t.Errorf("speakers = %q", block.Speakers)
	}
	if got := block.monitorID(); got != "del-4071-dell-u2415" {
		t.Errorf("monitor id = %q", got)
	}
}

func TestParseRejectsBlocksItCannotRead(t *testing.T) {
	full := fixture(t, "eld-hdmi-lg-ultrawide.bin")

	cases := []struct {
		name string
		raw  []byte
	}{
		{name: "shorter than the fixed part", raw: full[:19]},
		{name: "empty", raw: nil},
		{name: "an unknown version", raw: withByte(full, 0, 5<<3)},
		{name: "a reserved monitor name length", raw: withByte(full, 4, 0x60|17)},
		{name: "a monitor name past the end", raw: withByte(full, 4, 0x60|16)[:30]},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := parseELD(c.raw); err == nil {
				t.Fatal("the parser accepted a block it cannot read")
			}
		})
	}
}

// withByte copies a block with one byte replaced, so that a test can
// break one field and leave the rest of a real block alone.
func withByte(raw []byte, index int, value byte) []byte {
	broken := make([]byte, len(raw))
	copy(broken, raw)
	broken[index] = value
	return broken
}

func TestParseKeepsWhatItReadBeforeATruncatedDescriptor(t *testing.T) {
	// A descriptor that runs past the end is an error. The fields
	// ahead of it are still the monitor's, and the caller handles the
	// pair.
	truncated := fixture(t, "eld-hdmi-lg-ultrawide.bin")[:34]
	block, err := parseELD(truncated)
	if err == nil {
		t.Fatal("the parser accepted a descriptor that runs past the end")
	}
	if block.MonitorName != "LG ULTRAWIDE" {
		t.Errorf("monitor name = %q", block.MonitorName)
	}
}

func TestParseTakesTheHighestRateAndTheUnionOfTheDepths(t *testing.T) {
	full := fixture(t, "eld-hdmi-lg-ultrawide.bin")
	twoDescriptors := append(withByte(full, 5, full[5]&0x0f|0x20)[:35], 0x09, 0x18, 0x02)

	block, err := parseELD(twoDescriptors)
	if err != nil {
		t.Fatal(err)
	}
	if block.LPCMMaxRateHz != 96000 {
		t.Errorf("LPCM maximum rate = %d, want 96000", block.LPCMMaxRateHz)
	}
	if got := block.bitDepths(); got != "16 20 24" {
		t.Errorf("LPCM bit depths = %q, want \"16 20 24\"", got)
	}
}

func TestParseReportsNoRateOrDepthWithoutAnLPCMDescriptor(t *testing.T) {
	full := fixture(t, "eld-hdmi-lg-ultrawide.bin")

	block, err := parseELD(withByte(full, 32, 0x11))
	if err != nil {
		t.Fatal(err)
	}
	if block.LPCMChannels != 0 {
		t.Errorf("LPCM channels = %d, want 0", block.LPCMChannels)
	}
	if block.LPCMMaxRateHz != 0 {
		t.Errorf("LPCM maximum rate = %d, want 0", block.LPCMMaxRateHz)
	}
	if got := block.bitDepths(); got != "" {
		t.Errorf("LPCM bit depths = %q, want the empty string", got)
	}
}

func TestMaxSampleRate(t *testing.T) {
	cases := []struct {
		rates byte
		want  int
	}{
		{rates: 0x00, want: 0},
		{rates: 0x01, want: 32000},
		{rates: 0x07, want: 48000},
		{rates: 0x1f, want: 96000},
		{rates: 0x40, want: 192000},
		{rates: 0x7f, want: 192000},
	}
	for _, c := range cases {
		if got := maxSampleRate(c.rates); got != c.want {
			t.Errorf("maxSampleRate(%#02x) = %d, want %d", c.rates, got, c.want)
		}
	}
}

func TestBitDepths(t *testing.T) {
	cases := []struct {
		depths byte
		want   string
	}{
		{depths: 0x00, want: ""},
		{depths: 0x01, want: "16"},
		{depths: 0x05, want: "16 24"},
		{depths: 0x07, want: "16 20 24"},
	}
	for _, c := range cases {
		if got := (eld{LPCMDepths: c.depths}).bitDepths(); got != c.want {
			t.Errorf("bitDepths(%#02x) = %q, want %q", c.depths, got, c.want)
		}
	}
}

func TestSpeakerNames(t *testing.T) {
	cases := []struct {
		allocation byte
		want       string
	}{
		{allocation: 0x00, want: ""},
		{allocation: 0x01, want: "FL/FR"},
		{allocation: 0x03, want: "FL/FR LFE"},
		{allocation: 0x5f, want: "FL/FR LFE FC RL/RR RC RLC/RRC"},
	}
	for _, c := range cases {
		if got := speakerNames(c.allocation); got != c.want {
			t.Errorf("speakerNames(%#02x) = %q, want %q", c.allocation, got, c.want)
		}
	}
}
