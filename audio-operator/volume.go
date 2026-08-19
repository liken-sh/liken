package main

// The level a sink plays at, and who decides it.
//
// This pod stores no volumes: WirePlumber's persistent storage is
// off, because nothing durable backs a pod's filesystem. So every
// sink starts at whatever the default says, and WirePlumber ships
// that default at 40 percent, a desktop's guard for a first login's
// ears. An appliance wants unity instead. Every stage below 1.0
// multiplies the samples down before a codec encodes them, and
// loudness belongs to the consumer's own stream volume and to the
// hardware behind the jack. This file holds both halves of that
// stance: the settings fragment that starts every sink at unity,
// and the write that sets a delivered sink there.

import (
	"context"
	"fmt"
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

// volumeProps is the pod that sets one node's per-channel levels.
//
// The write names channelVolumes, not the single volume value
// beside it. The two are separate multipliers, and the lab's sink
// carried its 40 percent default in channelVolumes, cubed to
// 0.064, while volume read 1.0, so channelVolumes is the field
// that moves. The numbers are linear gain per channel, and 1.0 is
// unity.
func volumeProps(volumes []float64) string {
	levels := make([]string, 0, len(volumes))
	for _, volume := range volumes {
		levels = append(levels, strconv.FormatFloat(volume, 'f', -1, 64))
	}
	return fmt.Sprintf("{ channelVolumes: [ %s ] }", strings.Join(levels, ", "))
}

// setNodeVolumes writes the levels on one node.
func setNodeVolumes(ctx context.Context, node int, volumes []float64) error {
	return setParam(ctx, node, volumeProps(volumes))
}

// stereoChannels is the channel count a speaker's sink is assumed to
// have when the graph read reported none.
//
// An A2DP transport carries two channels, so a sink whose graph
// read reported no levels is written as stereo rather than not
// written at all.
const stereoChannels = 2

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
	channels := len(sink.Volumes)
	if channels == 0 {
		channels = stereoChannels
	}
	levels := make([]float64, channels)
	for channel := range levels {
		levels[channel] = 1
	}
	return levels
}
