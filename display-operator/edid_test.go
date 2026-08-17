package main

// The fixtures in testdata are whole EDIDs, read from real monitors
// with `od -An -tx1 /sys/class/drm/<card>-<connector>/edid`:
//
//   - lg-hdr-wqhd, the ultrawide on the lab machine's HDMI-A-1. It
//     carries a CTA extension block, and its name and its label serial
//     are in that block rather than in the base block.
//   - portable-display, the second monitor on the lab machine's
//     HDMI-A-2. Its bytes 21 and 22 claim 29 by 27 centimeters, which
//     no 15.6-inch panel measures, so it is what proves the detailed
//     timing's millimeters win.
//   - framework-edp, a laptop's built-in panel. It states no monitor
//     name and no serial at all, which is ordinary for a panel with no
//     bezel and no label.

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// loadEDID reads one hex-encoded fixture.
func loadEDID(t *testing.T, name string) []byte {
	t.Helper()
	text, err := os.ReadFile("testdata/" + name + ".edid.hex")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(text)))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParseEDIDReadsRealMonitors(t *testing.T) {
	cases := []struct {
		fixture string
		want    EDID
	}{
		{
			fixture: "lg-hdr-wqhd",
			want: EDID{
				Manufacturer:      "GSM",
				ProductCode:       0x7716,
				Serial:            "202NTRLCC070",
				ModelName:         "LG HDR WQHD",
				WidthPixels:       3840,
				HeightPixels:      1600,
				WidthMillimeters:  879,
				HeightMillimeters: 366,
			},
		},
		{
			fixture: "portable-display",
			want: EDID{
				Manufacturer:      "BOE",
				ProductCode:       0x1080,
				Serial:            "000000001",
				ModelName:         "Display",
				WidthPixels:       1920,
				HeightPixels:      1080,
				WidthMillimeters:  344,
				HeightMillimeters: 196,
			},
		},
		{
			fixture: "framework-edp",
			want: EDID{
				Manufacturer:      "BOE",
				ProductCode:       0x095f,
				WidthPixels:       2256,
				HeightPixels:      1504,
				WidthMillimeters:  285,
				HeightMillimeters: 190,
			},
		},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			edid, err := ParseEDID(loadEDID(t, c.fixture))
			if err != nil {
				t.Fatal(err)
			}
			if edid != c.want {
				t.Errorf("got  %+v\nwant %+v", edid, c.want)
			}
		})
	}
}

func TestParseEDIDRejectsWhatItCannotTrust(t *testing.T) {
	good := loadEDID(t, "portable-display")

	badHeader := append([]byte(nil), good...)
	badHeader[1] = 0x00

	badChecksum := append([]byte(nil), good...)
	badChecksum[40]++

	cases := []struct {
		name string
		raw  []byte
	}{
		// An empty file is what a connector answers while no monitor
		// is on it, and the read of it succeeds.
		{name: "no monitor", raw: nil},
		{name: "a partial block", raw: good[:100]},
		{name: "a wrong header", raw: badHeader},
		{name: "a checksum that does not add up", raw: badChecksum},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ParseEDID(c.raw); err == nil {
				t.Fatal("the parser accepted it")
			}
		})
	}
}

func TestParseEDIDIgnoresACorruptExtension(t *testing.T) {
	// The base block stands on its own checksum. An extension that
	// fails its own must not take the manufacturer and the mode down
	// with it.
	raw := append([]byte(nil), loadEDID(t, "lg-hdr-wqhd")...)
	raw[200]++

	edid, err := ParseEDID(raw)
	if err != nil {
		t.Fatal(err)
	}
	if edid.Manufacturer != "GSM" || edid.WidthPixels != 3840 || edid.ModelName != "LG HDR WQHD" {
		t.Fatalf("the base block did not survive: %+v", edid)
	}
	// This monitor's label serial lives in the extension alone, so it
	// leaves with the extension and the base block's own numeric
	// serial is what publishes.
	if edid.Serial != "420070" {
		t.Fatalf("serial = %q, want the base block's 420070", edid.Serial)
	}
}

func TestParseEDIDKeepsANameThatAnExtensionLeavesBlank(t *testing.T) {
	// A monitor may write the name descriptor twice, once in the base
	// block and once in a CTA extension, and the second one is
	// sometimes 13 spaces. An empty name that overwrote the real one
	// would drop the model attribute and shorten
	// monitor.liken.sh/id, which the audio operator matches byte for
	// byte, so the pairing would fail with nothing to read that said
	// why.
	raw := append([]byte(nil), loadEDID(t, "lg-hdr-wqhd")...)
	extension := raw[blockSize:]
	offset := indexOfDescriptor(t, extension, tagSerialNumber)
	extension[offset+3] = tagMonitorName
	for i := offset + 5; i < offset+18; i++ {
		extension[i] = ' '
	}
	fixChecksum(extension)

	edid, err := ParseEDID(raw)
	if err != nil {
		t.Fatal(err)
	}
	if edid.ModelName != "LG HDR WQHD" {
		t.Errorf("model = %q, want the base block's name", edid.ModelName)
	}
	if got := monitorID(edid); got != "gsm-7716-lg-hdr-wqhd" {
		t.Errorf("monitorID = %q", got)
	}
}

// indexOfDescriptor finds one display descriptor in a block by its
// tag. A display descriptor starts with three zero bytes, then the
// tag.
func indexOfDescriptor(t *testing.T, block []byte, tag byte) int {
	t.Helper()
	for offset := 0; offset+18 <= blockSize-1; offset++ {
		if block[offset] == 0 && block[offset+1] == 0 && block[offset+2] == 0 && block[offset+3] == tag {
			return offset
		}
	}
	t.Fatalf("the block carries no descriptor with tag %#x", tag)
	return 0
}

// fixChecksum makes an edited block sum to zero again, so the parser
// reads it instead of rejecting it.
func fixChecksum(block []byte) {
	var sum byte
	for _, b := range block[:blockSize-1] {
		sum += b
	}
	block[blockSize-1] = -sum
}

func TestManufacturerID(t *testing.T) {
	cases := []struct {
		name        string
		high, low   byte
		wantPNPCode string
	}{
		{name: "LG Electronics", high: 0x1e, low: 0x6d, wantPNPCode: "GSM"},
		{name: "BOE Technology", high: 0x09, low: 0xe5, wantPNPCode: "BOE"},
		// Bit 15 is reserved. A monitor that sets it still names a
		// real manufacturer in the other fifteen bits.
		{name: "the reserved bit set", high: 0x89, low: 0xe5, wantPNPCode: "BOE"},
		{name: "no letters at all", high: 0x00, low: 0x00, wantPNPCode: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := manufacturerID(c.high, c.low); got != c.wantPNPCode {
				t.Fatalf("manufacturerID = %q, want %q", got, c.wantPNPCode)
			}
		})
	}
}

func TestDescriptorText(t *testing.T) {
	cases := []struct {
		name       string
		descriptor []byte
		want       string
	}{
		{
			name:       "a line feed ends it and spaces pad it",
			descriptor: append([]byte{0, 0, 0, 0xfc, 0}, []byte("LG HDR WQHD\n ")...),
			want:       "LG HDR WQHD",
		},
		{
			name:       "a full thirteen characters has no line feed",
			descriptor: append([]byte{0, 0, 0, 0xfc, 0}, []byte("ABCDEFGHIJKLM")...),
			want:       "ABCDEFGHIJKLM",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := descriptorText(c.descriptor); got != c.want {
				t.Fatalf("descriptorText = %q, want %q", got, c.want)
			}
		})
	}
}
