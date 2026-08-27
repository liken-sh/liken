package main

// The two capability strings in testdata are the panels of the lab
// drill, read over the wire, and the parser is proven against them
// rather than against a string written to suit it.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// One fragment of a capability reply, built the way a display
// builds it: the opcode, the offset the fragment starts at, and the
// bytes of the string.
func capabilitiesReply(offset uint16, data string) []byte {
	reply := []byte{
		ddcReplySource, ddcLengthFlag | byte(3+len(data)), vcpCapabilitiesReply,
		byte(offset >> 8), byte(offset),
	}
	reply = append(reply, data...)
	return append(reply, ddcChecksum(ddcVirtualHost, reply))
}

// The whole reply is the length a caller reads, so a short
// fragment pads. The padding is what a display sends rather than
// holding the bus.
func paddedCapabilitiesReply(offset uint16, data string) []byte {
	reply := capabilitiesReply(offset, data)
	return append(reply, make([]byte, capabilitiesReplyLength-len(reply))...)
}

func TestCapabilitiesReadsEveryFragment(t *testing.T) {
	first := strings.Repeat("a", capabilitiesFragmentBytes)
	fixture := newDDCFixture(t,
		busAnswer{reply: paddedCapabilitiesReply(0, first)},
		busAnswer{reply: paddedCapabilitiesReply(uint16(len(first)), "bb")},
		busAnswer{reply: paddedCapabilitiesReply(uint16(len(first)+2), "")},
	)

	text, err := fixture.client.Capabilities()
	if err != nil {
		t.Fatal(err)
	}
	if want := first + "bb"; text != want {
		t.Errorf("capability string = %q, want %q", text, want)
	}
	// The offsets the host asked for, which is the whole of
	// how the fragments join.
	var offsets []uint16
	for _, write := range fixture.bus.writes {
		offsets = append(offsets, uint16(write[3])<<8|uint16(write[4]))
	}
	if want := []uint16{0, 32, 34}; !slices.Equal(offsets, want) {
		t.Errorf("asked for offsets %v, want %v", offsets, want)
	}
}

// The capability string is the first thing the probe asks for,
// so a panel that answers late must get the same growing wait the
// controls get. The lab's LG is that panel.
func TestCapabilitiesWaitsLongerOnEachAttempt(t *testing.T) {
	panel := &slowPanel{
		answers: 2 * ddcReplyDelay,
		reply:   paddedCapabilitiesReply(0, "(vcp(10))"),
	}
	client := newDDC(panel)
	client.sleep = func(waited time.Duration) { panel.waited = waited }

	fragment, err := client.capabilitiesFragment(0)
	if err != nil {
		t.Fatal(err)
	}
	if string(fragment) != "(vcp(10))" {
		t.Errorf("fragment = %q, want the panel's own string", fragment)
	}
	if panel.reads != 2 {
		t.Errorf("the panel was read %d times, want 2", panel.reads)
	}
}

func TestCapabilitiesRefusesAReplyThatIsNotOne(t *testing.T) {
	cases := []struct {
		name  string
		reply []byte
	}{
		{
			name:  "a wire nothing drives",
			reply: bytesOf(0xff, capabilitiesReplyLength),
		},
		{
			name:  "a reply to another opcode",
			reply: flipped(paddedCapabilitiesReply(0, "ab"), 2, vcpGetReply),
		},
		{
			name:  "a reply whose checksum does not add up",
			reply: flipped(paddedCapabilitiesReply(0, "ab"), 6, 'c'),
		},
		{
			// A display that answers an older offset would
			// otherwise repeat its bytes into the string.
			name:  "a reply to another offset",
			reply: paddedCapabilitiesReply(8, "ab"),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fixture := newDDCFixture(t,
				busAnswer{reply: c.reply}, busAnswer{reply: c.reply}, busAnswer{reply: c.reply})

			if _, err := fixture.client.Capabilities(); err == nil {
				t.Fatal("the client read a capability string out of bytes that are not a reply")
			}
		})
	}
}

// One byte of a reply changed, with the checksum left as the
// display computed it.
func flipped(reply []byte, index int, value byte) []byte {
	broken := slices.Clone(reply)
	broken[index] = value
	return broken
}

func bytesOf(value byte, length int) []byte {
	filled := make([]byte, length)
	for i := range filled {
		filled[i] = value
	}
	return filled
}

// The string of each panel in the drill, read from testdata so
// a replacement string proves the parser again.
func labCapabilities(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name+".capabilities"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestCoreCapabilitiesOfTheDrillsPanels(t *testing.T) {
	cases := []struct {
		name     string
		panel    string
		carries  []string
		refuses  []string
		values   map[string][]string
		maximums []string
	}{
		{
			// The ultrawide answers audio and no sharpness.
			name:    "the LG ultrawide",
			panel:   "lg-hdr-wqhd",
			carries: []string{"audioMute", "audioVolume", "brightness", "colorPreset", "contrast", "input", "power"},
			refuses: []string{"sharpness"},
			values: map[string][]string{
				"input":       {"DP-1", "DP-2", "HDMI-1", "HDMI-2"},
				"power":       {"on", "off"},
				"audioMute":   {"mute", "unmute"},
				"colorPreset": {"6500K", "9300K", "user-1"},
			},
			maximums: []string{"brightness", "contrast", "audioVolume"},
		},
		{
			// The portable panel answers sharpness and seven
			// inputs, and no audio at all.
			name:    "the portable panel",
			panel:   "portable-display",
			carries: []string{"brightness", "colorPreset", "contrast", "input", "power", "sharpness"},
			refuses: []string{"audioMute", "audioVolume"},
			values: map[string][]string{
				"input": {"VGA-1", "DVI-1", "DVI-2", "DP-1", "DP-2", "HDMI-1", "HDMI-2"},
				"power": {"on", "off", "hardOff"},
			},
			maximums: []string{"brightness", "contrast", "sharpness"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			capabilities := coreCapabilities(capabilityCodes(labCapabilities(t, c.panel)))

			var carried []string
			for name := range capabilities {
				carried = append(carried, name)
			}
			slices.Sort(carried)
			if !slices.Equal(carried, c.carries) {
				t.Errorf("the panel carries %q, want %q", carried, c.carries)
			}
			for _, name := range c.refuses {
				if _, published := capabilities[name]; published {
					t.Errorf("the panel publishes %s, and its capability string states none", name)
				}
			}
			for name, want := range c.values {
				if got := capabilities[name].Values; !slices.Equal(got, want) {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
			// A continuous control states no value list, and
			// the probe reads its maximum from the panel instead.
			for _, name := range c.maximums {
				if values := capabilities[name].Values; values != nil {
					t.Errorf("%s states the values %q, and a continuous control states none", name, values)
				}
			}
		})
	}
}

// Only the common core publishes. A manufacturer's own code is
// read from no panel and named in no resource.
func TestCoreCapabilitiesPublishNoOtherCode(t *testing.T) {
	codes := capabilityCodes(labCapabilities(t, "lg-hdr-wqhd"))
	if _, carried := codes[0xe0]; !carried {
		t.Fatal("the fixture's string states no manufacturer code, so this test proves nothing")
	}
	if capabilityName(0xe0) != "" {
		t.Errorf("the code %#02x publishes under the name %q", 0xe0, capabilityName(0xe0))
	}
}

func TestCapabilityCodesReadsTheVCPSection(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		want   map[byte][]uint16
		absent []byte
	}{
		{
			name: "codes with and without value lists",
			text: "(vcp(10 14(05 08) D6(01 04)))",
			want: map[byte][]uint16{0x10: nil, 0x14: {0x05, 0x08}, 0xd6: {0x01, 0x04}},
		},
		{
			// A group inside a value list belongs to a code
			// this operator does not read, and it must not become a
			// value of the code that carries it.
			name: "a value list with a group inside it",
			text: "(vcp(60(01 11) DF(01(02 03) 04)))",
			want: map[byte][]uint16{0x60: {0x01, 0x11}, 0xdf: {0x01, 0x04}},
		},
		{
			name:   "a string with no vcp section",
			text:   "(prot(monitor)type(LCD)mccs_ver(2.1))",
			absent: []byte{0x10},
		},
		{
			// The section is found by its own name, so a
			// section whose name ends in vcp is not this one.
			name: "another section whose name ends in vcp",
			text: "(mvcp(10 12)vcp(60(11)))",
			want: map[byte][]uint16{0x60: {0x11}},
		},
		{
			name: "lowercase codes and uneven spacing",
			text: "( vcp( 10   12  8d(01 02) ) )",
			want: map[byte][]uint16{0x10: nil, 0x12: nil, 0x8d: {0x01, 0x02}},
		},
		{
			// A string cut short by a display that stopped
			// answering still yields the codes that arrived.
			name: "a string that ends inside the section",
			text: "(vcp(10 12 14(05",
			want: map[byte][]uint16{0x10: nil, 0x12: nil, 0x14: {0x05}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			codes := capabilityCodes(c.text)
			for code, want := range c.want {
				if got, carried := codes[code]; !carried || !slices.Equal(got, want) {
					t.Errorf("code %#02x = %v (carried=%v), want %v", code, got, carried, want)
				}
			}
			for _, code := range c.absent {
				if _, carried := codes[code]; carried {
					t.Errorf("code %#02x is carried, and the string states none", code)
				}
			}
		})
	}
}

// A value the table does not name still publishes, because the
// panel accepts it and a list that dropped it would be wrong.
func TestValueNamesCoverWhatTheTableDoesNot(t *testing.T) {
	cases := []struct {
		code byte
		raw  uint16
		name string
	}{
		{code: vcpInput, raw: 0x11, name: "HDMI-1"},
		{code: vcpInput, raw: 0x1b, name: "0x1b"},
		{code: vcpPowerMode, raw: powerModeOff, name: "off"},
		{code: vcpAudioMute, raw: 0x01, name: audioMuted},
		{code: vcpBrightness, raw: 40, name: "0x28"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := valueName(c.code, c.raw); got != c.name {
				t.Errorf("valueName(%#02x, %d) = %q, want %q", c.code, c.raw, got, c.name)
			}
			raw, known := valueRaw(c.code, c.name)
			if !known || raw != c.raw {
				t.Errorf("valueRaw(%#02x, %q) = %d, %v, want %d", c.code, c.name, raw, known, c.raw)
			}
		})
	}
}

func TestValueRawRefusesAValueNoPanelStates(t *testing.T) {
	if raw, known := valueRaw(vcpInput, "SCART"); known {
		t.Errorf("valueRaw read %d out of a name no panel states", raw)
	}
}
