package main

// A sink declared without channel positions reports a channel count
// and no positions. WirePlumber 0.5 reads a node's formats once, when
// it configures the node, and treats a node with no positions as
// unpositioned (si_audio_adapter_find_format in
// module-si-audio-adapter.c). It then configures every stream linked
// to that node as FL,FR, so a 7.1 stream is folded into the front
// pair. A node created with audio.channels and audio.position reports
// both in its EnumFormat from the start, and WirePlumber links each
// channel of a multichannel stream to the port of the same name. So
// the operator declares the positions of each sink.
//
// The positions come from the first source that has them: the Sink's
// spec.layout, the monitor's ELD for an HDMI or DisplayPort output, or
// the USB card's own channel map. A sink with none of these, such as
// the analog jack, is declared with no positions, and its streams stay
// stereo.

import (
	"slices"
	"strings"
)

// layoutSource is where a sink's layout came from. status.layoutSource
// reports it.
type layoutSource string

const (
	layoutFromSpec       layoutSource = "Spec"
	layoutFromELD        layoutSource = "ELD"
	layoutFromChannelMap layoutSource = "ChannelMap"
	layoutNone           layoutSource = "None"
)

// Positions lists the PCM slots in order, in PipeWire's channel names.
// A layout from the channel map has no positions here, because
// PipeWire reads them from the device when it opens the PCM.
//
// Encoded lists the encoded formats that the sink passes to its
// monitor without decoding them (bitstream.go). They are part of the
// layout because they reach PipeWire through the same drop-in as
// the positions, so equal detects a change in either and one restart
// applies both.
type channelLayout struct {
	Source    layoutSource
	Positions []string
	Encoded   []string
}

// source treats an empty Source as None, so a layout that was never set
// equals one that selectLayout set to None.
func (l channelLayout) source() layoutSource {
	if l.Source == "" {
		return layoutNone
	}
	return l.Source
}

// equal compares the source too, because the drop-in records the
// source, so adding a spec.layout changes the drop-in even when its
// positions equal the ELD's.
func (l channelLayout) equal(other channelLayout) bool {
	return l.source() == other.source() &&
		slices.Equal(l.Positions, other.Positions) &&
		slices.Equal(l.Encoded, other.Encoded)
}

// String is the text of the layout in log lines and Event messages.
func (l channelLayout) String() string {
	switch l.source() {
	case layoutNone:
		return "no positions"
	case layoutFromChannelMap:
		return "the device's channel map"
	}
	positions := strings.Join(l.Positions, ",") + " from " + string(l.Source)
	if len(l.Encoded) == 0 {
		return positions
	}
	return positions + ", with " + strings.Join(l.Encoded, ",") + " passed through"
}

// The channel orders of PipeWire's hdmi-surround71, hdmi-surround, and
// hdmi-stereo mappings, from the channel-map lines of ACP's default
// profile set (spa/plugins/alsa/mixer/profile-sets/default.conf), in
// PipeWire's short channel names.
//
// Each order matches the way the kernel routes PCM slots. PipeWire never
// writes the PCM's channel map, so the HDA HDMI driver picks the CEA
// channel allocation from the channel count and the ELD, and routes
// the slots in hdmi_std_setup_channel_mapping
// (sound/hda/core/hdmi_chmap.c). For 8 channels that is CEA allocation
// 0x13: slots 3 and 4 go to CEA RLC and RRC, the back pair, and slots
// 7 and 8 go to CEA RL and RR, which a 7.1 receiver plays on its
// surround speakers.
var (
	surround71Layout = []string{"FL", "FR", "RL", "RR", "FC", "LFE", "SL", "SR"}
	surround51Layout = []string{"FL", "FR", "RL", "RR", "FC", "LFE"}
	stereoLayout     = []string{"FL", "FR"}
)

// The CEA-861 speaker allocation bits that identify a 5.1 or 7.1 set,
// in the bit order of eldSpeakerNames.
const (
	allocationFrontPair      byte = 1 << 0
	allocationLFE            byte = 1 << 1
	allocationFrontCenter    byte = 1 << 2
	allocationRearPair       byte = 1 << 3
	allocationRearCenterPair byte = 1 << 6

	allocation51 = allocationFrontPair | allocationLFE | allocationFrontCenter | allocationRearPair
	allocation71 = allocation51 | allocationRearCenterPair
)

// eldPositions returns the HDMI layout for a monitor's ELD: 7.1 when
// the monitor accepts at least 8 LPCM channels and its speaker
// allocation has every 7.1 bit, 5.1 when it accepts at least 6 and has
// every 5.1 bit, and stereo otherwise.
//
// The channel count alone is not enough. The LPCM descriptor lists
// what the monitor's audio decoder accepts, and most televisions
// accept 8-channel LPCM but play only two speakers. A 7.1 layout on
// such a set would send the center channel, which carries dialog, to a
// slot the set does not play. With FL,FR, PipeWire mixes the center
// into the front pair instead. Any allocation that is not a full 5.1
// or 7.1 set gets stereo for the same reason.
func eldPositions(block eld) []string {
	speakers := block.SpeakerAllocation
	switch {
	case block.LPCMChannels >= 8 && speakers&allocation71 == allocation71:
		return surround71Layout
	case block.LPCMChannels >= 6 && speakers&allocation51 == allocation51:
		return surround51Layout
	}
	return stereoLayout
}

// selectLayout returns the layout of one playback endpoint from the first
// source that has one. spec is the Sink's spec.layout. declared is the
// endpoint's layout in the drop-in that PipeWire runs, and is the zero
// value when the pod starts.
//
// An HDMI output whose monitor is off or unplugged has no ELD. When
// the declared layout came from an ELD, selectLayout keeps it, so a
// television that turns off and on again does not change the
// drop-in. Any other declared layout becomes None, so removing
// spec.layout while the monitor is off gives None.
//
// selectEncoded chooses the encoded formats the same way whatever the
// source of the positions, so a spec.layout on a receiver keeps the
// receiver's formats.
//
// api.alsa.use-chmap stays off for HDMI. PipeWire would take the
// positions in the order the PCM channel map lists them, FL FR LFE FC
// RL RR RLC RRC for 8 channels, but the kernel routes slots in the
// order of the layouts above, so the center and the subwoofer would
// reach the wrong speakers. A USB Audio Class device describes its
// channels in its descriptors, and the kernel builds the PCM channel
// map from them (sound/usb/stream.c), so for USB the map is correct
// and PipeWire reads it.
func selectLayout(output alsaEndpoint, spec []string, declared channelLayout) channelLayout {
	switch {
	case len(spec) > 0:
		return channelLayout{Source: layoutFromSpec, Positions: spec, Encoded: selectEncoded(output, declared)}
	case output.HDMI && output.Monitor:
		return channelLayout{Source: layoutFromELD, Positions: eldPositions(output.ELD),
			Encoded: selectEncoded(output, declared)}
	case output.HDMI && declared.source() == layoutFromELD:
		return declared
	case output.HDMI:
		return channelLayout{Source: layoutNone}
	case output.Identity.Bus == usbBus:
		return channelLayout{Source: layoutFromChannelMap}
	}
	return channelLayout{Source: layoutNone}
}

// selectLayouts returns the layout of each playback endpoint, keyed by
// its graph address. specs and declared use the same keys, and either
// can be nil.
func selectLayouts(outputs []alsaEndpoint, specs map[nodeAddress][]string,
	declared map[nodeAddress]channelLayout) map[nodeAddress]channelLayout {
	layouts := map[nodeAddress]channelLayout{}
	for _, output := range outputs {
		if output.Capture {
			continue
		}
		address := output.graphAddress()
		layouts[address] = selectLayout(output, specs[address], declared[address])
	}
	return layouts
}
