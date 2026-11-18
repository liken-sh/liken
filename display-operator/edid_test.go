package main

// The fixtures in testdata are complete EDIDs read from real monitors
// with `od -An -tx1 /sys/class/drm/<card>-<connector>/edid`:
//
//   - lg-hdr-wqhd is an ultrawide monitor with a CTA extension block.
//     Its name is in the base block and its text serial is in the
//     extension.
//   - portable-display is a 15.6-inch monitor whose centimeter size in
//     bytes 21 and 22 is wrong, so the parser must take the
//     millimeters of the detailed timing.
//   - framework-edp is a laptop's built-in panel. It has no monitor
//     name and no serial.

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func loadEDID(t *testing.T, name string) []byte {
	t.Helper()
	text, err := os.ReadFile("testdata/" + name + ".edid.hex")
	if err != nil {
		t.Fatalf("reading the EDID fixture %s: %v", name, err)
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(text)))
	if err != nil {
		t.Fatalf("decoding the EDID fixture %s: %v", name, err)
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
				RefreshMillihertz: 59999,
				WidthMillimeters:  879,
				HeightMillimeters: 366,
				PhysicalAddress:   "2.0.0.0",
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
				RefreshMillihertz: 60000,
				WidthMillimeters:  344,
				HeightMillimeters: 196,
				PhysicalAddress:   "1.0.0.0",
			},
		},
		{
			fixture: "framework-edp",
			want: EDID{
				Manufacturer:      "BOE",
				ProductCode:       0x095f,
				WidthPixels:       2256,
				HeightPixels:      1504,
				RefreshMillihertz: 59998,
				WidthMillimeters:  285,
				HeightMillimeters: 190,
			},
		},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			edid, err := ParseEDID(loadEDID(t, c.fixture))
			if err != nil {
				t.Fatalf("ParseEDID(%s) returned an error: %v", c.fixture, err)
			}
			if edid != c.want {
				t.Errorf("ParseEDID(%s) = %+v, want %+v", c.fixture, edid, c.want)
			}
		})
	}
}

func TestParseEDIDPublishesNoRefreshWithoutARasterTotal(t *testing.T) {
	// A zero raster total must give a refresh of zero instead of a
	// division by zero, and the rest of the block must still parse.
	cases := []struct {
		name  string
		zeros []int
	}{
		{name: "no horizontal total", zeros: []int{2, 3, 4}},
		{name: "no vertical total", zeros: []int{5, 6, 7}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw := append([]byte(nil), loadEDID(t, "portable-display")...)
			timing := raw[54:72]
			for _, index := range c.zeros {
				timing[index] = 0
			}
			fixChecksum(raw[:blockSize])

			edid, err := ParseEDID(raw)
			if err != nil {
				t.Fatalf("ParseEDID returned an error: %v", err)
			}
			if edid.RefreshMillihertz != 0 {
				t.Fatalf("RefreshMillihertz = %d, want 0", edid.RefreshMillihertz)
			}
			if edid.Manufacturer != "BOE" {
				t.Errorf("the manufacturer is %q, want BOE: the rest of the block must survive", edid.Manufacturer)
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
		{name: "no monitor", raw: nil},
		{name: "a partial block", raw: good[:100]},
		{name: "a wrong header", raw: badHeader},
		{name: "a checksum that does not add up", raw: badChecksum},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ParseEDID(c.raw); err == nil {
				t.Fatal("ParseEDID returned no error, want one")
			}
		})
	}
}

func TestParseEDIDIgnoresACorruptExtension(t *testing.T) {
	// An extension with a bad checksum must not make the parse fail
	// or discard the base block's manufacturer and mode.
	raw := append([]byte(nil), loadEDID(t, "lg-hdr-wqhd")...)
	raw[200]++

	edid, err := ParseEDID(raw)
	if err != nil {
		t.Fatalf("ParseEDID returned an error: %v", err)
	}
	if edid.Manufacturer != "GSM" || edid.WidthPixels != 3840 || edid.ModelName != "LG HDR WQHD" {
		t.Fatalf("the base block reads as %+v, want manufacturer GSM, width 3840, and model LG HDR WQHD", edid)
	}
	// This monitor's text serial is only in the extension, so the
	// base block's numeric serial is the one that remains.
	if edid.Serial != "420070" {
		t.Fatalf("serial = %q, want the base block's 420070", edid.Serial)
	}
}

func TestParseEDIDKeepsANameThatAnExtensionLeavesBlank(t *testing.T) {
	// A blank name in an extension must not replace the base block's
	// name, because monitorID builds monitor.liken.sh/id from it.
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
		t.Fatalf("ParseEDID returned an error: %v", err)
	}
	if edid.ModelName != "LG HDR WQHD" {
		t.Errorf("model = %q, want the base block's name", edid.ModelName)
	}
	if got := monitorID(edid); got != "gsm-7716-lg-hdr-wqhd" {
		t.Errorf("monitorID = %q, want gsm-7716-lg-hdr-wqhd", got)
	}
}

// indexOfDescriptor returns the offset of the display descriptor with
// the given tag. A display descriptor starts with three zero bytes and
// then the tag.
func indexOfDescriptor(t *testing.T, block []byte, tag byte) int {
	t.Helper()
	for offset := 0; offset+18 <= blockSize-1; offset++ {
		if block[offset] == 0 && block[offset+1] == 0 && block[offset+2] == 0 && block[offset+3] == tag {
			return offset
		}
	}
	t.Fatalf("the block has no descriptor with tag %#x", tag)
	return 0
}

// fixChecksum recomputes the last byte of an edited block so that it
// passes the checksum.
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
