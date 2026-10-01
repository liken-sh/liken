package main

import (
	"slices"
	"testing"
)

// eldWith assembles the smallest block parseELD reads: the fixed
// part, no monitor name, and one LPCM descriptor with the given
// channel count. The speaker allocation is byte 7, in CEA-861 bit
// order.
func eldWith(t *testing.T, channels int, allocation byte) eld {
	t.Helper()
	raw := make([]byte, eldFixedBytes+3)
	raw[0] = eldVersionCEA861D << 3
	raw[5] = 1 << 4
	raw[7] = allocation
	raw[eldFixedBytes] = audioCodingTypeLPCM<<3 | byte(channels-1)
	raw[eldFixedBytes+1] = 0x07
	raw[eldFixedBytes+2] = 0x07
	block, err := parseELD(raw)
	if err != nil {
		t.Fatal(err)
	}
	return block
}

// The speaker allocation bits, named for the table below.
const (
	frontPair      byte = 1 << 0
	lfe            byte = 1 << 1
	frontCenter    byte = 1 << 2
	rearPair       byte = 1 << 3
	rearCenter     byte = 1 << 4
	frontCenterLR  byte = 1 << 5
	rearCenterPair byte = 1 << 6
	fiveOne             = frontPair | lfe | frontCenter | rearPair
)

func TestTheELDSelectsOneOfPipeWiresHDMILayouts(t *testing.T) {
	cases := []struct {
		name       string
		channels   int
		allocation byte
		want       []string
	}{
		{"a stereo television that accepts 8-channel LPCM", 8, frontPair, stereoLayout},
		{"a stereo monitor", 2, frontPair, stereoLayout},
		{"a 5.1 receiver", 6, fiveOne, surround51Layout},
		{"a 5.1 receiver that accepts 8 channels", 8, fiveOne, surround51Layout},
		{"a 7.1 receiver", 8, fiveOne | rearCenterPair, surround71Layout},
		{"a 7.1 receiver that also names a rear center", 8, fiveOne | rearCenter | rearCenterPair, surround71Layout},
		{"a 7.1 receiver held to 6 channels of LPCM", 6, fiveOne | rearCenterPair, surround51Layout},
		{"5.1 speakers held to 2 channels of LPCM", 2, fiveOne, stereoLayout},
		{"a rear center and front centers in place of the back pair", 8, fiveOne | rearCenter | frontCenterLR, surround51Layout},
		{"surround speakers with no subwoofer", 8, frontPair | frontCenter | rearPair | rearCenterPair, stereoLayout},
		{"an allocation that names no speaker", 8, 0, stereoLayout},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := eldPositions(eldWith(t, c.channels, c.allocation)); !slices.Equal(got, c.want) {
				t.Errorf("positions = %v, want %v", got, c.want)
			}
		})
	}
}

// The parser keeps the raw allocation byte beside the names, because
// the layout reads the bits and the names are for a person.
func TestParseKeepsTheSpeakerAllocation(t *testing.T) {
	block := eldWith(t, 8, fiveOne|rearCenterPair)
	if block.SpeakerAllocation != fiveOne|rearCenterPair {
		t.Errorf("allocation = %#x, want %#x", block.SpeakerAllocation, fiveOne|rearCenterPair)
	}
}

func TestSelectLayoutTakesTheFirstSourceThatGivesOne(t *testing.T) {
	receiver := eldWith(t, 8, fiveOne|rearCenterPair)
	television := eldWith(t, 8, frontPair)
	hdmi := func(block eld) alsaEndpoint {
		return alsaEndpoint{Card: 0, PCM: 3, HDMI: true, Monitor: true, ELD: block,
			Identity: cardIdentity{Bus: pciBus}}
	}
	unplugged := alsaEndpoint{Card: 0, PCM: 3, HDMI: true, Identity: cardIdentity{Bus: pciBus}}
	analog := alsaEndpoint{Card: 0, PCM: 0, Identity: cardIdentity{Bus: pciBus}}
	usb := alsaEndpoint{Card: 1, PCM: 0, Identity: cardIdentity{Bus: usbBus}}
	quad := []string{"FL", "FR", "RL", "RR"}

	cases := []struct {
		name     string
		output   alsaEndpoint
		spec     []string
		declared channelLayout
		want     channelLayout
	}{
		{"a 7.1 receiver", hdmi(receiver), nil, channelLayout{},
			channelLayout{Source: layoutFromELD, Positions: surround71Layout}},
		{"a stereo television", hdmi(television), nil, channelLayout{},
			channelLayout{Source: layoutFromELD, Positions: stereoLayout}},
		{"an HDMI output with no monitor", unplugged, nil, channelLayout{},
			channelLayout{Source: layoutNone}},
		{"a monitor that turned off after its layout was declared", unplugged, nil,
			channelLayout{Source: layoutFromELD, Positions: surround71Layout},
			channelLayout{Source: layoutFromELD, Positions: surround71Layout}},
		{"a spec removed while the monitor is off", unplugged, nil,
			channelLayout{Source: layoutFromSpec, Positions: quad},
			channelLayout{Source: layoutNone}},
		{"a USB card", usb, nil, channelLayout{},
			channelLayout{Source: layoutFromChannelMap}},
		{"the analog jack", analog, nil, channelLayout{},
			channelLayout{Source: layoutNone}},
		{"a spec on the analog jack", analog, quad, channelLayout{},
			channelLayout{Source: layoutFromSpec, Positions: quad}},
		{"a spec over the ELD", hdmi(receiver), quad, channelLayout{},
			channelLayout{Source: layoutFromSpec, Positions: quad}},
		{"a spec over the channel map", usb, quad, channelLayout{},
			channelLayout{Source: layoutFromSpec, Positions: quad}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := selectLayout(c.output, c.spec, c.declared)
			if !got.equal(c.want) {
				t.Errorf("layout = %+v, want %+v", got, c.want)
			}
		})
	}
}

// Only a playback endpoint has a layout. A capture endpoint of a USB
// card shares its PCM device number with the playback one, and the
// declaration must not give the source the sink's channel map.
func TestSelectLayoutsSkipsCaptureEndpoints(t *testing.T) {
	outputs := []alsaEndpoint{
		{Card: 1, PCM: 0, Identity: cardIdentity{Bus: usbBus}},
		{Card: 1, PCM: 0, Capture: true, Identity: cardIdentity{Bus: usbBus}},
	}
	layouts := selectLayouts(outputs, nil, nil)
	if len(layouts) != 1 {
		t.Fatalf("layouts = %+v, want the sink's alone", layouts)
	}
	if got := layouts[outputs[0].graphAddress()]; got.Source != layoutFromChannelMap {
		t.Errorf("the sink's layout = %+v", got)
	}
}

func TestLayoutsCompareBySourceAndPositions(t *testing.T) {
	cases := []struct {
		name string
		a, b channelLayout
		want bool
	}{
		{"the same layout", channelLayout{layoutFromELD, stereoLayout}, channelLayout{layoutFromELD, stereoLayout}, true},
		{"another source", channelLayout{layoutFromELD, stereoLayout}, channelLayout{layoutFromSpec, stereoLayout}, false},
		{"other positions", channelLayout{layoutFromELD, stereoLayout}, channelLayout{layoutFromELD, surround51Layout}, false},
		{"none and the zero value", channelLayout{Source: layoutNone}, channelLayout{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.a.equal(c.b); got != c.want {
				t.Errorf("equal = %v, want %v", got, c.want)
			}
		})
	}
}

func TestLayoutString(t *testing.T) {
	cases := []struct {
		layout channelLayout
		want   string
	}{
		{channelLayout{Source: layoutFromELD, Positions: stereoLayout}, "FL,FR from ELD"},
		{channelLayout{Source: layoutFromChannelMap}, "the device's channel map"},
		{channelLayout{}, "no positions"},
	}
	for _, c := range cases {
		if got := c.layout.String(); got != c.want {
			t.Errorf("%+v reads %q, want %q", c.layout, got, c.want)
		}
	}
}
