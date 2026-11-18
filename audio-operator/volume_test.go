package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The volume fragment is written whether or not the claim includes a
// Bluetooth media bus, because the setting applies to every sink that
// PipeWire creates.
func TestWriteVolumeConfigIsWrittenWithOrWithoutAMediaBus(t *testing.T) {
	cases := []struct {
		name    string
		address string
	}{
		{
			name:    "the claim includes a media bus",
			address: "unix:path=/var/run/bluetooth.liken.sh/dbus/system_bus_socket",
		},
		{name: "the claim includes none"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := monitorDirectory(t)
			t.Setenv(busAddressVariable, c.address)

			if err := writeVolumeConfig(); err != nil {
				t.Fatalf("writing the volume fragment: %v", err)
			}
			written, err := os.ReadFile(filepath.Join(dir, volumeFragmentName))
			if err != nil {
				t.Fatalf("reading the volume fragment back: %v", err)
			}
			if string(written) != volumeConfig {
				t.Fatalf("the file contains\n%s\nwant the fragment volumeConfig", written)
			}
		})
	}
}

// device.routes.default-sink-volume is the setting name in WirePlumber
// 0.5. A wrong name raises no error and leaves every sink at the stock
// 40 percent, so the test checks the text of the fragment.
func TestVolumeConfigStatesTheDefaultSinkVolume(t *testing.T) {
	if !strings.Contains(volumeConfig, "device.routes.default-sink-volume = 1.0") {
		t.Errorf("the fragment is\n%s\nwant it to set device.routes.default-sink-volume = 1.0", volumeConfig)
	}
	if !strings.Contains(volumeConfig, "wireplumber.settings = {") {
		t.Errorf("the fragment is\n%s\nwant it to open a wireplumber.settings block", volumeConfig)
	}
}

func TestVolumeConfigIsItsOwnFragment(t *testing.T) {
	if volumeFragmentName == monitorFragmentName {
		t.Fatalf("both fragments are named %s, want two different names", volumeFragmentName)
	}
}

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

// The status reports a level only when the graph reported one, because
// a node with no levels is not a node at zero. A level between two
// percents rounds to the nearer one.
func TestVolumePercentReadsWhatTheGraphReported(t *testing.T) {
	if _, read := volumePercent(nil); read {
		t.Error("a node with no levels reported a percent, want none")
	}
	if got, _ := volumePercent([]float64{0.064, 0.064}); got != 6 {
		t.Errorf("percent = %d, want 6", got)
	}
	if got, _ := volumePercent([]float64{0.335}); got != 34 {
		t.Errorf("percent = %d, want 34", got)
	}
}

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

// levelProps writes spec.volume and spec.mute of an ALSA endpoint in
// one POD.
func TestLevelProps(t *testing.T) {
	cases := []struct {
		name    string
		volumes []float64
		mute    *bool
		want    string
	}{
		{name: "half, playing", volumes: []float64{0.5, 0.5}, mute: pointerTo(false),
			want: "{ channelVolumes: [ 0.5, 0.5 ], mute: false }"},
		{name: "unity, muted", volumes: []float64{1, 1}, mute: pointerTo(true),
			want: "{ channelVolumes: [ 1, 1 ], mute: true }"},
		{name: "the level alone", volumes: []float64{0.5, 0.5},
			want: "{ channelVolumes: [ 0.5, 0.5 ] }"},
		{name: "the mute alone", mute: pointerTo(true),
			want: "{ mute: true }"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := levelProps(c.volumes, c.mute); got != c.want {
				t.Errorf("pod = %q, want %q", got, c.want)
			}
		})
	}
}

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

// sinkNode copies a speaker's sink into a pwNode, so setNodeLevel
// writes a speaker's node the same way as an ALSA node.
func TestSpeakerSinkNodeCopiesTheNodeFacts(t *testing.T) {
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
