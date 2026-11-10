package main

import (
	"slices"
	"testing"
)

// withEncodedTypes answers a parsed block that also lists each coding
// type, as parseELD reads a descriptor of that type.
func withEncodedTypes(block eld, codingTypes ...byte) eld {
	for _, codingType := range codingTypes {
		block.EncodedTypes |= 1 << codingType
	}
	return block
}

// receiverBlock assembles the block of a receiver: 8-channel LPCM, a
// 7.1 speaker allocation, and one descriptor for each coding type. The
// second and third bytes of a descriptor for an encoded format hold
// its rates and its bitrate, which this operator does not read.
func receiverBlock(t *testing.T, codingTypes ...byte) []byte {
	t.Helper()
	raw := make([]byte, eldFixedBytes+3)
	raw[0] = eldVersionCEA861D << 3
	raw[5] = byte(1+len(codingTypes)) << 4
	raw[7] = fiveOne | rearCenterPair
	raw[eldFixedBytes] = audioCodingTypeLPCM<<3 | 7
	raw[eldFixedBytes+1] = 0x07
	raw[eldFixedBytes+2] = 0x07
	for _, codingType := range codingTypes {
		raw = append(raw, codingType<<3|7, 0x07, 0x50)
	}
	return raw
}

func TestParseNamesTheEncodedFormatsAMonitorAccepts(t *testing.T) {
	cases := []struct {
		name        string
		codingTypes []byte
		want        []string
	}{
		{"a monitor with LPCM alone", nil, nil},
		{"a television", []byte{2, 10}, []string{"AC3", "EAC3"}},
		{"a receiver, in the order of the coding types", []byte{12, 11, 10, 7, 2},
			[]string{"AC3", "DTS", "EAC3", "DTS-HD", "TrueHD"}},
		{"formats no sink passes through: MP3, AAC, and WMA Pro", []byte{4, 6, 14}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			block, err := parseELD(receiverBlock(t, c.codingTypes...))
			if err != nil {
				t.Fatal(err)
			}
			if got := block.encodedFormats(); !slices.Equal(got, c.want) {
				t.Errorf("encoded formats = %v, want %v", got, c.want)
			}
			if block.LPCMChannels != 8 {
				t.Errorf("LPCM channels = %d, want 8", block.LPCMChannels)
			}
		})
	}
}

func TestEncodedTypesReadsBackTheNamesItWrote(t *testing.T) {
	block, err := parseELD(receiverBlock(t, 2, 7, 10, 11, 12))
	if err != nil {
		t.Fatal(err)
	}
	if got := encodedTypes(block.encodedFormats()); got != block.EncodedTypes {
		t.Errorf("types = %#x, want %#x", got, block.EncodedTypes)
	}
	if got := encodedTypes([]string{"AC3", "Vorbis"}); got != 1<<2 {
		t.Errorf("types of a list with an unknown name = %#x, want AC3 alone", got)
	}
}

func TestSelectEncodedTakesTheFormatsOfAnHDACardsMonitor(t *testing.T) {
	receiver := withEncodedTypes(eldWith(t, 8, fiveOne|rearCenterPair), 2, 10, 12)
	formats := []string{"AC3", "EAC3", "TrueHD"}
	hda := cardIdentity{Bus: pciBus, Driver: hdaDriver}
	declared := channelLayout{Source: layoutFromELD, Positions: surround71Layout, Encoded: formats}

	cases := []struct {
		name     string
		output   alsaEndpoint
		declared channelLayout
		want     []string
	}{
		{"a receiver that answers",
			alsaEndpoint{PCM: 3, HDMI: true, Monitor: true, ELD: receiver, Identity: hda}, channelLayout{}, formats},
		{"a receiver that turned off keeps the declared formats",
			alsaEndpoint{PCM: 3, HDMI: true, Identity: hda}, declared, formats},
		{"an output with no monitor and no declared formats",
			alsaEndpoint{PCM: 3, HDMI: true, Identity: hda}, channelLayout{}, nil},
		{"a monitor with LPCM alone drops the declared formats",
			alsaEndpoint{PCM: 3, HDMI: true, Monitor: true, ELD: eldWith(t, 2, frontPair), Identity: hda}, declared, nil},
		{"a card of another driver",
			alsaEndpoint{PCM: 3, HDMI: true, Monitor: true, ELD: receiver,
				Identity: cardIdentity{Bus: pciBus, Driver: "sof-hda-dsp"}}, channelLayout{}, nil},
		{"a PCM device alsa-lib defines no hdmi device for",
			alsaEndpoint{PCM: 4, HDMI: true, Monitor: true, ELD: receiver, Identity: hda}, channelLayout{}, nil},
		{"the analog jack of the same card",
			alsaEndpoint{PCM: 3, Identity: hda}, declared, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := selectEncoded(c.output, c.declared); !slices.Equal(got, c.want) {
				t.Errorf("encoded formats = %v, want %v", got, c.want)
			}
		})
	}
}

// The index of the hdmi device is the output's place in alsa-lib's
// table for the driver, and not its place among the PCM devices the
// card has.
func TestHDMIDeviceIndexFollowsALSALibsTable(t *testing.T) {
	cases := []struct {
		pcm     int
		index   int
		defined bool
	}{
		{3, 0, true},
		{7, 1, true},
		{8, 2, true},
		{16, 10, true},
		{0, 0, false},
		{4, 0, false},
		{17, 0, false},
	}
	for _, c := range cases {
		index, defined := hdmiDeviceIndex(c.pcm)
		if defined != c.defined || (defined && index != c.index) {
			t.Errorf("PCM device %d: index %d, defined %v; want index %d, defined %v",
				c.pcm, index, defined, c.index, c.defined)
		}
	}
}

func TestNodeConfigDeclaresASinkThatPassesBitstreamsThrough(t *testing.T) {
	absent := "<absent>"
	formats := []string{"AC3", "EAC3", "TrueHD"}
	cases := []struct {
		name   string
		pcm    int
		layout channelLayout
		path   string
		codecs string
	}{
		{"the first HDMI output of a receiver", 3,
			channelLayout{Source: layoutFromELD, Positions: surround71Layout, Encoded: formats},
			"hdmi:1,0", "[ AC3 EAC3 TrueHD ]"},
		{"the second HDMI output", 7,
			channelLayout{Source: layoutFromSpec, Positions: stereoLayout, Encoded: formats},
			"hdmi:1,1", "[ AC3 EAC3 TrueHD ]"},
		{"a monitor with LPCM alone", 3,
			channelLayout{Source: layoutFromELD, Positions: stereoLayout}, "hw:1,3", absent},
		{"a PCM device with no hdmi device", 4,
			channelLayout{Source: layoutFromELD, Positions: stereoLayout, Encoded: formats}, "hw:1,4", absent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			output := alsaEndpoint{Card: 1, PCM: c.pcm}
			args := declaredObjects(t, nodeConfig([]alsaEndpoint{output},
				map[nodeAddress]channelLayout{output.graphAddress(): c.layout}))[0].Args
			if got := args["api.alsa.path"]; got != c.path {
				t.Errorf("api.alsa.path = %q, want %q", got, c.path)
			}
			got, ok := args[encodedFormatsProperty]
			if !ok {
				got = absent
			}
			if got != c.codecs {
				t.Errorf("%s = %q, want %q", encodedFormatsProperty, got, c.codecs)
			}
		})
	}
}

// A spec.layout states the positions and nothing about the formats, so
// the formats of the receiver's ELD travel with it.
func TestSelectLayoutKeepsTheEncodedFormatsUnderEverySource(t *testing.T) {
	receiver := withEncodedTypes(eldWith(t, 8, fiveOne|rearCenterPair), 2, 10)
	formats := []string{"AC3", "EAC3"}
	output := alsaEndpoint{PCM: 3, HDMI: true, Monitor: true, ELD: receiver,
		Identity: cardIdentity{Bus: pciBus, Driver: hdaDriver}}
	asleep := output
	asleep.Monitor, asleep.ELD = false, eld{}
	quad := []string{"FL", "FR", "RL", "RR"}

	cases := []struct {
		name     string
		output   alsaEndpoint
		spec     []string
		declared channelLayout
		want     channelLayout
	}{
		{"the ELD", output, nil, channelLayout{},
			channelLayout{Source: layoutFromELD, Positions: surround71Layout, Encoded: formats}},
		{"a spec over the ELD", output, quad, channelLayout{},
			channelLayout{Source: layoutFromSpec, Positions: quad, Encoded: formats}},
		{"a spec while the receiver is off", asleep, quad,
			channelLayout{Source: layoutFromSpec, Positions: quad, Encoded: formats},
			channelLayout{Source: layoutFromSpec, Positions: quad, Encoded: formats}},
		{"the ELD's layout while the receiver is off", asleep, nil,
			channelLayout{Source: layoutFromELD, Positions: surround71Layout, Encoded: formats},
			channelLayout{Source: layoutFromELD, Positions: surround71Layout, Encoded: formats}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := selectLayout(c.output, c.spec, c.declared); !got.equal(c.want) {
				t.Errorf("layout = %+v, want %+v", got, c.want)
			}
		})
	}
}

// A receiver in standby can answer with the same speakers and no
// encoded formats, so the formats alone must make a layout difference.
func TestPlanLayoutsFindsAChangeOfTheEncodedFormatsAlone(t *testing.T) {
	plain := eldWith(t, 8, fiveOne|rearCenterPair)
	decoding := withEncodedTypes(plain, 10, 12)
	formats := []string{"EAC3", "TrueHD"}
	layout := func(encoded []string) channelLayout {
		return channelLayout{Source: layoutFromELD, Positions: surround71Layout, Encoded: encoded}
	}

	cases := []struct {
		name     string
		monitor  eld
		declared channelLayout
		want     []channelLayout
	}{
		{"the same formats", decoding, layout(formats), nil},
		{"formats that arrived", decoding, layout(nil), []channelLayout{layout(formats)}},
		{"formats that left", plain, layout(formats), []channelLayout{layout(nil)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hdmi := alsaEndpoint{Card: 0, PCM: 3, HDMI: true, Monitor: true, ELD: c.monitor,
				Identity: cardIdentity{Bus: pciBus, Driver: hdaDriver}}
			changes := planLayouts(map[nodeAddress]channelLayout{hdmi.graphAddress(): c.declared},
				[]alsaEndpoint{hdmi}, nil)
			if len(changes) != len(c.want) {
				t.Fatalf("changes = %+v, want %+v", changes, c.want)
			}
			for i, want := range c.want {
				if !changes[i].To.equal(want) {
					t.Errorf("change %d goes to %+v, want %+v", i, changes[i].To, want)
				}
			}
		})
	}
}
