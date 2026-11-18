package main

// writeVolumeConfig writes a WirePlumber fragment that starts every
// sink at unity, and the text of volumeConfig gives the reason.
//
// This file also builds the writes that set a node's per-channel
// levels: the unity write when a speaker's claim is prepared, and the
// write for a declared level and mute or for a volume request
// (status.session.volumeAsk, asks.go). Each write is a Props object in
// PipeWire's SPA POD format, which pw-cli parses from text.

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// volumeFragmentName is the file name of the volume fragment. The
// prefix 61 sorts it after the image's own fragments and after the
// Bluetooth monitor's fragment. It is a separate file because the
// Bluetooth monitor's fragment is written only when the claim includes
// a Bluetooth media bus, and this one applies to every sink.
const volumeFragmentName = "61-liken-volume.conf"

const volumeConfig = `# The declare init container writes this file on every pod start,
# so an edit made here lasts only until the next start. To change
# it, change the operator.

# device.routes.default-sink-volume is the level WirePlumber gives
# a sink that has no stored volume, and WirePlumber ships it at
# 0.4. This pod stores no volumes, so without this setting every
# sink would start at 40 percent. A level below 1.0 scales the
# samples down before a codec encodes them, which costs resolution.
# The consumer sets the loudness with its own stream volume, so
# liken starts every sink at unity.
wireplumber.settings = {
  device.routes.default-sink-volume = 1.0
}
`

func writeVolumeConfig() error {
	if err := os.MkdirAll(wireplumberConfigDir, 0o755); err != nil {
		return fmt.Errorf("making WirePlumber's configuration directory: %w", err)
	}
	path := filepath.Join(wireplumberConfigDir, volumeFragmentName)
	if err := os.WriteFile(path, []byte(volumeConfig), 0o644); err != nil {
		return fmt.Errorf("writing WirePlumber's volume fragment: %w", err)
	}
	fmt.Printf("set WirePlumber's default sink volume to unity in %s\n", path)
	return nil
}

// levelList formats levels as the bare numbers that a POD accepts, each
// in its shortest exact form.
func levelList(volumes []float64) string {
	levels := make([]string, 0, len(volumes))
	for _, volume := range volumes {
		levels = append(levels, strconv.FormatFloat(volume, 'f', -1, 64))
	}
	return strings.Join(levels, ", ")
}

// volumeProps returns the Props POD that sets a node's per-channel
// levels, as linear gain where 1.0 is unity. It sets channelVolumes and
// not the volume property, because the two are separate multipliers and
// WirePlumber writes its default into channelVolumes.
func volumeProps(volumes []float64) string {
	return fmt.Sprintf("{ channelVolumes: [ %s ] }", levelList(volumes))
}

func setNodeVolumes(ctx context.Context, node int, volumes []float64) error {
	return setParam(ctx, node, "Props", volumeProps(volumes))
}

// levelProps returns the Props POD that sets a node's level, its mute,
// or both. PipeWire applies a Props POD as a whole, so writing both in
// one POD keeps a reader from seeing the new level with the old mute. A
// nil argument leaves its property out, and the node keeps its value.
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

// setNodeLevel writes a declared level and mute, or a volume request,
// to an ALSA node or to the node of a speaker that reports no absolute
// volume.
func setNodeLevel(ctx context.Context, node pwNode, level levelWrite) error {
	var levels []float64
	if level.Volume != nil {
		levels = volumeLevels(*level.Volume, channelCount(node.Volumes))
	}
	return setParam(ctx, node.ID, "Props", levelProps(levels, level.Mute))
}

// sinkNode returns the speaker's PipeWire node as the pwNode that
// setNodeLevel accepts. The operator writes the node only when the
// speaker reports no absolute volume. When the speaker reports one,
// applyLevel writes the level to the device's Route.
func (s bluezSink) sinkNode() pwNode {
	return pwNode{ID: s.NodeID, Name: s.Node, Mute: s.Mute, Volumes: s.Volumes, Format: s.Format, Suspended: s.Suspended}
}

// volumeLevels converts a percent into linear gain, volume/100 on every
// channel. It does not cube the value, because channelVolumes is the
// gain that PipeWire applies, and the cube is WirePlumber's curve for a
// desktop volume slider, which a percent in a resource does not follow.
func volumeLevels(volume, channels int) []float64 {
	levels := make([]float64, channels)
	for channel := range levels {
		levels[channel] = float64(volume) / 100
	}
	return levels
}

// volumePercent converts levels back into a percent. It reads the first
// channel, because the operator writes every channel alike. It returns
// false for a node with no levels, so the status reports no level and
// not zero.
func volumePercent(volumes []float64) (int, bool) {
	if len(volumes) == 0 {
		return 0, false
	}
	return int(math.Round(volumes[0] * 100)), true
}

// stereoChannels is the channel count that channelCount uses for a
// node whose graph read reported no levels, so that the node is still
// written. An A2DP transport carries two channels. An ALSA node with
// more channels keeps the others at their old level.
const stereoChannels = 2

// channelCount returns the number of channels that a level write
// covers: every channel the node has, so a six-channel node does not
// keep four channels at the old level. A suspended node can print an
// empty channelVolumes.
func channelCount(volumes []float64) int {
	if len(volumes) == 0 {
		return stereoChannels
	}
	return len(volumes)
}

const unityPercent = 100

// unityLevels returns 1.0 for every channel of a speaker's sink, the
// levels that setSinkToUnity writes when a claim is prepared.
func unityLevels(sink bluezSink) []float64 {
	return volumeLevels(unityPercent, channelCount(sink.Volumes))
}
