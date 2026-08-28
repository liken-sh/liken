package main

import (
	"slices"
	"testing"
)

// SwitchControl and levelControl build the two
// control shapes the attach rule sorts, because the rule reads the
// interface, the name, and the index and nothing else.
func switchControl(name string, index uint32) control {
	return control{Name: name, Index: index, Interface: ctlElemIfaceMixer,
		Capability: controlCapability{Type: capabilityBoolean, Channels: 1}}
}

func levelControl(name string, index uint32) control {
	return control{Name: name, Index: index, Interface: ctlElemIfaceMixer,
		Capability: controlCapability{Type: capabilityInteger, Channels: 2}}
}

// The attach rule, on the three cards the lab holds: an Intel
// HDMI codec, a Realtek analog codec, and a USB DAC.
func TestAttachControls(t *testing.T) {
	cases := []struct {
		name      string
		controls  []control
		endpoints []endpointControls
		want      map[string][]string
	}{
		{
			name: "an Intel HDMI codec gives each slot the IEC958 control of its own ordinal",
			controls: []control{
				switchControl("IEC958 Playback Switch", 0),
				switchControl("IEC958 Playback Switch", 1),
				switchControl("IEC958 Playback Switch", 2),
				switchControl("IEC958 Playback Switch", 3),
				// The card declares one of these on the PCM interface
				// for each HDMI device, and its name carries the
				// Playback word. It reaches no endpoint.
				{Name: "Playback Channel Map", Interface: ctlElemIfacePCM, Device: 3,
					Capability: controlCapability{Type: capabilityInteger, Channels: 8}},
				{Name: "Playback Channel Map", Interface: ctlElemIfacePCM, Device: 7,
					Capability: controlCapability{Type: capabilityInteger, Channels: 8}},
			},
			endpoints: []endpointControls{
				{Name: "hdmi-0", Direction: sinkDirection, HDMIOrdinal: 0},
				{Name: "hdmi-1", Direction: sinkDirection, HDMIOrdinal: 1},
				{Name: "hdmi-2", Direction: sinkDirection, HDMIOrdinal: 2},
				{Name: "hdmi-3", Direction: sinkDirection, HDMIOrdinal: 3},
			},
			want: map[string][]string{
				"hdmi-0": {"IEC958 Playback Switch"},
				"hdmi-1": {"IEC958 Playback Switch"},
				"hdmi-2": {"IEC958 Playback Switch"},
				"hdmi-3": {"IEC958 Playback Switch"},
			},
		},
		{
			name: "a Realtek codec splits its controls by direction",
			controls: []control{
				levelControl("Master Playback Volume", 0),
				switchControl("Master Playback Switch", 0),
				levelControl("Headphone Playback Volume", 0),
				switchControl("Headphone Playback Switch", 0),
				levelControl("Speaker Playback Volume", 0),
				switchControl("Speaker Playback Switch", 0),
				{Name: "Auto-Mute Mode", Interface: ctlElemIfaceMixer,
					Capability: controlCapability{Type: capabilityEnumerated, Values: []string{"Disabled", "Enabled"}, Channels: 1}},
				levelControl("Capture Volume", 0),
				switchControl("Capture Switch", 0),
				{Name: "Input Source", Interface: ctlElemIfaceMixer,
					Capability: controlCapability{Type: capabilityEnumerated, Values: []string{"Mic", "Internal Mic"}, Channels: 1}},
				levelControl("Mic Boost Volume", 0),
				switchControl("Auto Gain Control", 0),
			},
			endpoints: []endpointControls{
				{Name: "analog", Direction: sinkDirection, HDMIOrdinal: -1},
				{Name: "analog-in", Direction: sourceDirection, HDMIOrdinal: -1},
			},
			want: map[string][]string{
				"analog": {
					"Master Playback Volume", "Master Playback Switch",
					"Headphone Playback Volume", "Headphone Playback Switch",
					"Speaker Playback Volume", "Speaker Playback Switch",
					"Auto-Mute Mode",
				},
				"analog-in": {"Capture Volume", "Capture Switch", "Input Source",
					"Mic Boost Volume", "Auto Gain Control"},
			},
		},
		{
			name: "a USB DAC gives its playback controls to the sink and its capture controls to the source",
			controls: []control{
				levelControl("PCM Playback Volume", 0),
				switchControl("PCM Playback Switch", 0),
				levelControl("Mic Capture Volume", 0),
				switchControl("Mic Capture Switch", 0),
			},
			endpoints: []endpointControls{
				{Name: "usb-audio", Direction: sinkDirection, HDMIOrdinal: -1},
				{Name: "usb-audio-in", Direction: sourceDirection, HDMIOrdinal: -1},
			},
			want: map[string][]string{
				"usb-audio":    {"PCM Playback Volume", "PCM Playback Switch"},
				"usb-audio-in": {"Mic Capture Volume", "Mic Capture Switch"},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			attached := attachControls(c.controls, c.endpoints)
			if len(attached) != len(c.want) {
				t.Fatalf("the rule attached to %d endpoints, want %d", len(attached), len(c.want))
			}
			for endpoint, want := range c.want {
				got := controlNames(attached[endpoint])
				if !slices.Equal(got, want) {
					t.Errorf("%s holds %v, want %v", endpoint, got, want)
				}
			}
		})
	}
}

// A control that belongs to the card and to no one endpoint
// carries neither the Capture word nor an IEC958 name, and the card's
// analog and USB sinks list it.
func TestAttachControlsGivesTheCardsOwnControlsToItsSinks(t *testing.T) {
	controls := []control{
		{Name: "Auto-Mute Mode", Interface: ctlElemIfaceMixer,
			Capability: controlCapability{Type: capabilityEnumerated, Values: []string{"Disabled", "Enabled"}}},
		{Name: "Loopback Mixing", Interface: ctlElemIfaceMixer,
			Capability: controlCapability{Type: capabilityEnumerated, Values: []string{"Disabled", "Enabled"}}},
		{Name: "Independent HP", Interface: ctlElemIfaceMixer,
			Capability: controlCapability{Type: capabilityBoolean, Channels: 1}},
	}
	endpoints := []endpointControls{
		{Name: "analog", Direction: sinkDirection, HDMIOrdinal: -1},
		{Name: "hdmi-0", Direction: sinkDirection, HDMIOrdinal: 0},
		{Name: "analog-in", Direction: sourceDirection, HDMIOrdinal: -1},
	}
	attached := attachControls(controls, endpoints)

	want := []string{"Auto-Mute Mode", "Loopback Mixing", "Independent HP"}
	if got := controlNames(attached["analog"]); !slices.Equal(got, want) {
		t.Errorf("the analog sink holds %v, want %v", got, want)
	}
	for _, endpoint := range []string{"hdmi-0", "analog-in"} {
		if got := controlNames(attached[endpoint]); len(got) != 0 {
			t.Errorf("%s holds %v, want nothing", endpoint, got)
		}
	}
}

// The ordinal an HDMI slot takes, on the card the lab holds:
// the slots are the PCM devices the codec filled in order, and the
// analog jack and the microphone beside them are no slots at all.
func TestEndpointControlsOfNumbersTheHDMISlots(t *testing.T) {
	listed := endpointControlsOf([]alsaEndpoint{
		{Card: 0, PCM: 9, HDMI: true, DeviceName: "hdmi-3"},
		{Card: 0, PCM: 3, HDMI: true, DeviceName: "hdmi-0"},
		{Card: 0, PCM: 0, DeviceName: "analog"},
		{Card: 0, PCM: 0, Capture: true, DeviceName: "analog-capture"},
		{Card: 0, PCM: 7, HDMI: true, DeviceName: "hdmi-1"},
	})

	want := map[string]endpointControls{
		"hdmi-3":         {Name: "hdmi-3", Direction: sinkDirection, HDMIOrdinal: 2},
		"hdmi-0":         {Name: "hdmi-0", Direction: sinkDirection, HDMIOrdinal: 0},
		"hdmi-1":         {Name: "hdmi-1", Direction: sinkDirection, HDMIOrdinal: 1},
		"analog":         {Name: "analog", Direction: sinkDirection, HDMIOrdinal: -1},
		"analog-capture": {Name: "analog-capture", Direction: sourceDirection, HDMIOrdinal: -1},
	}
	if len(listed) != len(want) {
		t.Fatalf("listed %d endpoints, want %d", len(listed), len(want))
	}
	for _, endpoint := range listed {
		if endpoint != want[endpoint.Name] {
			t.Errorf("%s = %+v, want %+v", endpoint.Name, endpoint, want[endpoint.Name])
		}
	}
}

// What the card's jacks say about one endpoint. A jack is
// named for the connector behind it, so the direction picks which
// jacks answer, and a card that senses none for the endpoint says so.
func TestJackState(t *testing.T) {
	jacks := map[string]bool{
		"Headphone Jack":     false,
		"Speaker Jack":       true,
		"Mic Jack":           false,
		"HDMI/DP,pcm=3 Jack": true,
	}
	cases := []struct {
		name      string
		direction pwDirection
		jacks     map[string]bool
		plugged   bool
		sensed    bool
	}{
		{
			// The speaker jack answers for the analog sink, and one
			// plug in either jack is a plug this endpoint plays into.
			name: "a sink with a plug in one of its jacks", direction: directionSink,
			jacks: jacks, plugged: true, sensed: true,
		},
		{
			name: "a source whose jack is empty", direction: directionSource,
			jacks: jacks, sensed: true,
		},
		{
			name: "a card that senses no jack", direction: directionSink,
			jacks: map[string]bool{"HDMI/DP,pcm=3 Jack": true},
		},
		{name: "a card that senses nothing at all", direction: directionSink},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plugged, sensed := jackState(c.direction, c.jacks)
			if plugged != c.plugged || sensed != c.sensed {
				t.Errorf("jackState = %v, %v, want %v, %v", plugged, sensed, c.plugged, c.sensed)
			}
		})
	}
}

// CapabilitiesOf is what status.capabilities is built from, and
// the kernel's own control name is the key.
func TestCapabilitiesOfKeysByControlName(t *testing.T) {
	capabilities := capabilitiesOf([]control{
		levelControl("Master Playback Volume", 0),
		switchControl("Master Playback Switch", 0),
	})
	if got := capabilities["Master Playback Volume"].Type; got != capabilityInteger {
		t.Errorf("Master Playback Volume is %q, want %q", got, capabilityInteger)
	}
	if got := capabilities["Master Playback Switch"].Type; got != capabilityBoolean {
		t.Errorf("Master Playback Switch is %q, want %q", got, capabilityBoolean)
	}
}
