package main

// The level a node plays or records at, and how a percent in a spec
// becomes a gain in the graph.
//
// This pod stores no volumes: WirePlumber's persistent storage is
// off, because nothing durable backs a pod's filesystem. So every
// sink starts at whatever the default says, and WirePlumber ships
// that default at 40 percent, a desktop's guard for a first login's
// ears. An appliance wants unity instead. Every stage below 1.0
// multiplies the samples down before a codec encodes them, and
// loudness belongs to the consumer's own stream volume and to the
// hardware behind the jack. This file holds the settings fragment
// that starts every sink at unity, the write that sets a delivered
// sink there, and the write behind spec.volume and spec.mute, which
// is the one channel a person has to a level below the consumer's
// own.

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// volumeDropInName is the settings fragment's file name. 61 sorts it
// after the image's own fragments and after the Bluetooth one.
//
// The fragment is its own file, not lines in the Bluetooth
// monitor's, because the two answer different conditions: the
// monitor's fragment follows the delivered bus, and this one is
// policy for every sink on every machine, radio or not.
const volumeDropInName = "61-liken-volume.conf"

// volumeConfig sets the level a sink with no stored volume is born
// at.
//
// WirePlumber ships the default at 40 percent, which guards a
// desktop's first login. On a machine that plays only what a claim
// delivers, it is a hidden multiplier that costs resolution before
// the codec runs.
const volumeConfig = `# The declare init container writes this file on every pod start,
# so an edit made here lasts until the next start and no longer.
# The operator's own source is the place to change it.

# device.routes.default-sink-volume is the level WirePlumber gives
# a sink it holds no stored volume for, and WirePlumber ships it at
# 0.4. This pod stores no volumes at all, so without this setting
# every sink would start at 40 percent, a desktop default. liken
# sets unity: loudness belongs to the consumer's stream volume and
# to the hardware behind the jack, not to a multiplier in the
# middle of the chain.
wireplumber.settings = {
  device.routes.default-sink-volume = 1.0
}
`

// writeVolumeConfig writes the settings fragment where WirePlumber
// reads it.
//
// The write is unconditional, where the Bluetooth monitor's
// fragment follows the delivered bus, because the unity default
// applies to the card's own sinks as much as to a radio's.
func writeVolumeConfig() error {
	if err := os.MkdirAll(wireplumberConfigDir, 0o755); err != nil {
		return fmt.Errorf("making %s: %w", wireplumberConfigDir, err)
	}
	path := filepath.Join(wireplumberConfigDir, volumeDropInName)
	if err := os.WriteFile(path, []byte(volumeConfig), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	fmt.Printf("set WirePlumber's default sink volume to unity in %s\n", path)
	return nil
}

// levelList spells the levels for a pod. pw-cli's pod holds bare
// numbers, and the shortest exact print of each level is what goes
// in it, so 1 and 0.5 print as themselves.
func levelList(volumes []float64) string {
	levels := make([]string, 0, len(volumes))
	for _, volume := range volumes {
		levels = append(levels, strconv.FormatFloat(volume, 'f', -1, 64))
	}
	return strings.Join(levels, ", ")
}

// volumeProps is the pod that sets one node's per-channel levels.
//
// The write names channelVolumes, not the single volume value
// beside it. The two are separate multipliers, and the lab's sink
// carried its 40 percent default in channelVolumes, cubed to
// 0.064, while volume read 1.0, so channelVolumes is the field
// that moves. The numbers are linear gain per channel, and 1.0 is
// unity.
func volumeProps(volumes []float64) string {
	return fmt.Sprintf("{ channelVolumes: [ %s ] }", levelList(volumes))
}

// setNodeVolumes writes the levels on one node.
func setNodeVolumes(ctx context.Context, node int, volumes []float64) error {
	return setParam(ctx, node, "Props", volumeProps(volumes))
}

// levelProps is the pod that sets one node's levels, its mute, or
// both. They go in one write because PipeWire applies one Props pod
// at once, and a reader that saw the level move without the mute
// would report a state the endpoint was never in. A part the write
// does not state stays out of the pod, so the other one keeps its
// value.
func levelProps(volumes []float64, mute *bool) string {
	var parts []string
	if volumes != nil {
		parts = append(parts, fmt.Sprintf("channelVolumes: [ %s ]", levelList(volumes)))
	}
	if mute != nil {
		parts = append(parts, fmt.Sprintf("mute: %t", *mute))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// setNodeLevel is the write behind spec.volume and spec.mute on an
// ALSA endpoint, and on a speaker with no absolute volume. The
// channel count is the node's own, so that a six-channel node does
// not keep four channels at the old level.
func setNodeLevel(ctx context.Context, node pwNode, level levelWrite) error {
	var levels []float64
	if level.Volume != nil {
		levels = volumeLevels(*level.Volume, channelCount(node.Volumes))
	}
	return setParam(ctx, node.ID, "Props", levelProps(levels, level.Mute))
}

// sinkNode is the speaker's node as a level write sees it. A
// speaker's node takes the same Props write an ALSA node does, and
// the operator writes it only when the speaker reports no absolute
// volume, because on a speaker that does, the Route is the level
// that moves.
func (s bluezSink) sinkNode() pwNode {
	return pwNode{ID: s.NodeID, Name: s.Node, Mute: s.Mute, Volumes: s.Volumes, Format: s.Format}
}

// volumeLevels maps the spec's percent onto PipeWire's gain:
// volume/100 on every channel, not cubed. channelVolumes is the
// applied gain, and the cube is WirePlumber's own curve for a desktop
// fader, which a number in a resource has no reason to follow.
func volumeLevels(volume, channels int) []float64 {
	levels := make([]float64, channels)
	for channel := range levels {
		levels[channel] = float64(volume) / 100
	}
	return levels
}

// volumePercent is the read side of volumeLevels. The first channel
// is the level the status reports, because the operator writes every
// channel alike, and a node with no levels reports none rather than
// zero.
func volumePercent(volumes []float64) (int, bool) {
	if len(volumes) == 0 {
		return 0, false
	}
	return int(math.Round(volumes[0] * 100)), true
}

// stereoChannels is the channel count a speaker's sink is assumed to
// have when the graph read reported none.
//
// An A2DP transport carries two channels, so a sink whose graph
// read reported no levels is written as stereo rather than not
// written at all.
const stereoChannels = 2

// channelCount is how many channels a level write covers: the count
// the graph reported, or stereo when it reported none, which is what
// a suspended node prints in channelVolumes.
func channelCount(volumes []float64) int {
	if len(volumes) == 0 {
		return stereoChannels
	}
	return len(volumes)
}

// unityPercent is the level a delivered sink arrives at.
const unityPercent = 100

// unityLevels is the level a delivered sink arrives at: 1.0 on every
// channel the node has.
//
// Every prepare of a speaker writes this, switch or no switch. A
// speaker allocates to one claim at a time, so any level a prepare
// finds is a leftover, from an earlier tenant or from a hand-run
// tool, and never the arriving consumer's choice. The prepare's
// contract is that the sink arrives at unity, and loudness belongs
// to the consumer's own stream volume.
//
// The count comes from the node's current channelVolumes because
// the write must cover every channel the node has: a six-channel
// sink written as stereo would keep four channels at the old
// level.
func unityLevels(sink bluezSink) []float64 {
	return volumeLevels(unityPercent, channelCount(sink.Volumes))
}
