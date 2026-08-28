package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Every pod writes the fragment, radio or not, because every sink
// PipeWire builds is born at the setting's value.
func TestWriteVolumeConfigDoesNotFollowTheDeliveredBus(t *testing.T) {
	cases := []struct {
		name    string
		address string
	}{
		{
			name:    "the claim delivered a media bus",
			address: "unix:path=/var/run/bluetooth.liken.sh/dbus/system_bus_socket",
		},
		{name: "the claim delivered none"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := monitorDirectory(t)
			t.Setenv(busAddressVariable, c.address)

			if err := writeVolumeConfig(); err != nil {
				t.Fatal(err)
			}
			written, err := os.ReadFile(filepath.Join(dir, volumeDropInName))
			if err != nil {
				t.Fatal(err)
			}
			if string(written) != volumeConfig {
				t.Fatalf("the file holds something other than the fragment:\n%s", written)
			}
		})
	}
}

// The setting's name is WirePlumber 0.5's own. A name that drifts
// here leaves every sink at the stock 40 percent with no error
// anywhere, so the test pins it.
func TestVolumeConfigStatesTheDefaultSinkVolume(t *testing.T) {
	if !strings.Contains(volumeConfig, "device.routes.default-sink-volume = 1.0") {
		t.Errorf("the fragment does not state the setting:\n%s", volumeConfig)
	}
	if !strings.Contains(volumeConfig, "wireplumber.settings = {") {
		t.Errorf("the fragment does not open a settings block:\n%s", volumeConfig)
	}
}

// The two fragments are separate files, because one is written on
// every machine and the other only when the claim delivered a bus.
func TestVolumeConfigIsItsOwnFragment(t *testing.T) {
	if volumeDropInName == monitorDropInName {
		t.Fatalf("both fragments are named %s", volumeDropInName)
	}
}

// The map both ways: a percent of unity onto the
// linear gain PipeWire applies, and back. It is linear and not cubed,
// because channelVolumes is the applied gain.
func TestVolumeMapsAPercentOntoLinearGain(t *testing.T) {
	cases := []struct {
		name     string
		percent  int
		channels int
		levels   []float64
	}{
		{name: "unity", percent: 100, channels: 2, levels: []float64{1, 1}},
		{name: "half", percent: 50, channels: 2, levels: []float64{0.5, 0.5}},
		{name: "silent", percent: 0, channels: 1, levels: []float64{0}},
		{name: "six channels", percent: 25, channels: 6,
			levels: []float64{0.25, 0.25, 0.25, 0.25, 0.25, 0.25}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			levels := volumeLevels(c.percent, c.channels)
			if !reflect.DeepEqual(levels, c.levels) {
				t.Fatalf("levels = %v, want %v", levels, c.levels)
			}
			percent, read := volumePercent(levels)
			if !read || percent != c.percent {
				t.Errorf("percent = %d, %t, want %d", percent, read, c.percent)
			}
		})
	}
}

// The status reports a level only when the graph
// held one, because a node that reports no levels is not a node at
// zero, and that a level between two percents rounds to the nearer.
func TestVolumePercentReadsWhatTheGraphHeld(t *testing.T) {
	if _, read := volumePercent(nil); read {
		t.Error("a node with no levels reported a percent")
	}
	if got, _ := volumePercent([]float64{0.064, 0.064}); got != 6 {
		t.Errorf("percent = %d, want 6", got)
	}
	if got, _ := volumePercent([]float64{0.335}); got != 34 {
		t.Errorf("percent = %d, want 34", got)
	}
}

// A level write covers every channel the node
// has, and that a node whose graph read reported none is written as
// stereo rather than not written at all.
func TestChannelCountFallsBackToStereo(t *testing.T) {
	if got := channelCount(nil); got != stereoChannels {
		t.Errorf("count = %d, want %d", got, stereoChannels)
	}
	if got := channelCount([]float64{}); got != stereoChannels {
		t.Errorf("count = %d, want %d", got, stereoChannels)
	}
	if got := channelCount([]float64{1, 1, 1, 1, 1, 1}); got != 6 {
		t.Errorf("count = %d, want 6", got)
	}
}

// The level and the mute go in one pod, and that
// this is the write behind spec.volume and spec.mute on an ALSA
// endpoint.
func TestLevelProps(t *testing.T) {
	cases := []struct {
		name    string
		volumes []float64
		mute    bool
		want    string
	}{
		{name: "half, playing", volumes: []float64{0.5, 0.5},
			want: "{ channelVolumes: [ 0.5, 0.5 ], mute: false }"},
		{name: "unity, muted", volumes: []float64{1, 1}, mute: true,
			want: "{ channelVolumes: [ 1, 1 ], mute: true }"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := levelProps(c.volumes, c.mute); got != c.want {
				t.Errorf("pod = %q, want %q", got, c.want)
			}
		})
	}
}

// The prepare's unity write is the same map at a
// hundred percent, and that the channel count is the node's own.
func TestUnityLevels(t *testing.T) {
	cases := []struct {
		name string
		sink bluezSink
		want []float64
	}{
		{name: "the graph reported none", sink: bluezSink{}, want: []float64{1, 1}},
		{name: "stereo", sink: bluezSink{Volumes: []float64{0.3, 0.3}}, want: []float64{1, 1}},
		{
			name: "six channels",
			sink: bluezSink{Volumes: []float64{0.3, 0.3, 0.3, 0.3, 0.3, 0.3}},
			want: []float64{1, 1, 1, 1, 1, 1},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := unityLevels(c.sink); !reflect.DeepEqual(got, c.want) {
				t.Errorf("levels = %v, want %v", got, c.want)
			}
		})
	}
}

// A speaker's node answers the same level write
// an ALSA node does, and that the operator takes it from the graph
// value rather than building one.
func TestSpeakerSinkNodeCarriesTheNodeFacts(t *testing.T) {
	sink := bluezSink{
		Node:    testSpeakerNode,
		NodeID:  63,
		Mute:    true,
		Volumes: []float64{0.25, 0.25},
		Format:  pwFormat{Rate: 44100, Channels: 2, Positions: []string{"FL", "FR"}},
	}
	want := pwNode{
		ID:      63,
		Name:    testSpeakerNode,
		Mute:    true,
		Volumes: []float64{0.25, 0.25},
		Format:  pwFormat{Rate: 44100, Channels: 2, Positions: []string{"FL", "FR"}},
	}
	if got := sink.sinkNode(); !reflect.DeepEqual(got, want) {
		t.Errorf("node = %+v, want %+v", got, want)
	}
}

// The pod is what pw-cli parses, and the numbers are the levels the
// old node held.
func TestVolumeProps(t *testing.T) {
	cases := []struct {
		name    string
		volumes []float64
		want    string
	}{
		{name: "two channels", volumes: []float64{0.064, 0.064}, want: "{ channelVolumes: [ 0.064, 0.064 ] }"},
		{name: "unity", volumes: []float64{1, 1}, want: "{ channelVolumes: [ 1, 1 ] }"},
		{name: "one channel", volumes: []float64{0.5}, want: "{ channelVolumes: [ 0.5 ] }"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := volumeProps(c.volumes); got != c.want {
				t.Errorf("props = %q, want %q", got, c.want)
			}
		})
	}
}
