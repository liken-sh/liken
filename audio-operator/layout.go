package main

// The channel layout each declared sink carries.
//
// A sink declared with no channel positions reports its channel count
// and no position for any channel. WirePlumber 0.5 reads a node's
// formats once, when it configures the node, and marks a node with no
// positions unpositioned (si_audio_adapter_find_format in
// module-si-audio-adapter.c). It then configures every stream it
// links to that node as two channels, FL and FR, so a 7.1 stream
// reaches the sink folded into the front pair. A node that PipeWire
// creates with audio.channels and audio.position reports both in its
// EnumFormat from the start, and WirePlumber links each channel of a
// multichannel stream to the port of the same name.
//
// The positions come from the first source that gives them: the
// Sink's spec.layout, the monitor's ELD for an HDMI or DisplayPort
// output, and the device's own channel map for a USB card. A sink
// that none of them covers, such as the analog jack, is declared
// with no positions, and its streams stay stereo.

import "strings"

// layoutSource is where a sink's layout came from. The four values
// are the ones status.layoutSource reports.
type layoutSource string

const (
	layoutFromSpec       layoutSource = "Spec"
	layoutFromELD        layoutSource = "ELD"
	layoutFromChannelMap layoutSource = "ChannelMap"
	layoutNone           layoutSource = "None"
)

// channelLayout is the layout one sink is declared with. Positions
// is the PCM slot order in PipeWire's channel names. A layout from
// the channel map has no positions here, because PipeWire reads them
// from the device when it opens the PCM.
type channelLayout struct {
	Source    layoutSource
	Positions []string
}

// source answers the layout's source, with the zero value read as
// None, so a sink the declaration gave no layout compares equal to a
// sink this file chose none for.
func (l channelLayout) source() layoutSource {
	if l.Source == "" {
		return layoutNone
	}
	return l.Source
}

// equal reports whether two layouts declare the same node. The source
// is part of the comparison, because a change of spec.layout is a
// change of the declaration even when the new positions match the
// ELD's.
func (l channelLayout) equal(other channelLayout) bool {
	if l.source() != other.source() || len(l.Positions) != len(other.Positions) {
		return false
	}
	for i := range l.Positions {
		if l.Positions[i] != other.Positions[i] {
			return false
		}
	}
	return true
}

// String is the form a log line and an Event message name a layout in.
func (l channelLayout) String() string {
	switch l.source() {
	case layoutNone:
		return "no positions"
	case layoutFromChannelMap:
		return "the device's channel map"
	}
	return strings.Join(l.Positions, ",") + " from " + string(l.Source)
}

// PipeWire's own HDMI layouts: the channel-map lines of the
// hdmi-surround71, hdmi-surround, and hdmi-stereo mappings in ACP's
// default profile set (spa/plugins/alsa/mixer/profile-sets/
// default.conf), in PipeWire's short channel names.
//
// They match the order the kernel routes the PCM slots in. PipeWire
// never writes the PCM's channel map, so the HDA HDMI driver chooses
// the CEA channel allocation from the channel count and the ELD, and
// routes the slots by hdmi_std_setup_channel_mapping in
// sound/hda/core/hdmi_chmap.c. For 8 channels that is CEA allocation
// 0x13: slots 3 and 4 reach CEA RLC and RRC, the surround back pair,
// and slots 7 and 8 reach CEA RL and RR, which a 7.1 receiver plays
// on its surround speakers.
var (
	surround71Layout = []string{"FL", "FR", "RL", "RR", "FC", "LFE", "SL", "SR"}
	surround51Layout = []string{"FL", "FR", "RL", "RR", "FC", "LFE"}
	stereoLayout     = []string{"FL", "FR"}
)

// The speaker allocation bits the two surround layouts need, in
// CEA-861 order, the same order eldSpeakerNames lists.
const (
	allocationFrontPair      byte = 1 << 0
	allocationLFE            byte = 1 << 1
	allocationFrontCenter    byte = 1 << 2
	allocationRearPair       byte = 1 << 3
	allocationRearCenterPair byte = 1 << 6

	allocation51 = allocationFrontPair | allocationLFE | allocationFrontCenter | allocationRearPair
	allocation71 = allocation51 | allocationRearCenterPair
)

// eldPositions selects one of the three HDMI layouts from the
// monitor's speaker allocation, capped by the largest LPCM channel
// count it accepts.
//
// The channel count alone is not enough. A monitor's LPCM descriptor
// states what its audio receiver decodes, and most televisions accept
// 8-channel LPCM and play two speakers. A 7.1 layout on such a set
// would send the center channel, which carries the dialog, to a slot
// the set does not play. With FL,FR, PipeWire mixes the center into
// the front pair instead. Every allocation that is not a full 5.1 or
// 7.1 set selects stereo for the same reason.
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

// selectLayout chooses one playback endpoint's layout from the first
// source that gives one. spec is the Sink's spec.layout, and declared
// is the layout the running declaration holds for the endpoint, which
// is the zero value at pod start.
//
// An HDMI output whose monitor does not answer has no ELD. Its layout
// stays the one the declaration holds when that layout came from an
// ELD, so a television that turns off changes nothing, and a
// television that turns on with the same layout changes nothing. Any
// other layout falls to None, which is what removing spec.layout from
// such an output asks for.
//
// api.alsa.use-chmap stays off for HDMI. PipeWire takes the channel
// map's positions in the order the map lists them, FL FR LFE FC RL RR
// RLC RRC for 8 channels, while the kernel routes the slots in the
// order above, so the center and the subwoofer would reach the wrong
// speakers. A USB Audio Class device describes its own channels in
// its descriptors, and the kernel builds the PCM's channel map from
// them (sound/usb/stream.c), so for USB the map is the device's own
// statement.
func selectLayout(output alsaEndpoint, spec []string, declared channelLayout) channelLayout {
	switch {
	case len(spec) > 0:
		return channelLayout{Source: layoutFromSpec, Positions: spec}
	case output.HDMI && output.Monitor:
		return channelLayout{Source: layoutFromELD, Positions: eldPositions(output.ELD)}
	case output.HDMI && declared.source() == layoutFromELD:
		return declared
	case output.HDMI:
		return channelLayout{Source: layoutNone}
	case output.Identity.Bus == usbBus:
		return channelLayout{Source: layoutFromChannelMap}
	}
	return channelLayout{Source: layoutNone}
}

// selectLayouts chooses the layout of every playback endpoint, keyed
// by its address in the graph. specs holds each Sink's spec.layout
// and declared each layout the running declaration holds, both keyed
// the same way, and either may be nil.
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
