package main

import (
	"os"
	"path/filepath"
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
