package main

// ALSA mixer controls belong to a card, but a card publishes several
// endpoints. An HDA card can have an analog output, an analog input,
// and several HDMI slots, all on one control device. A spec declares a
// control on one endpoint, so attachControls sorts the card's controls
// onto its endpoints by the kernel's control name and, for an IEC958
// control, by the control's index.

import (
	"slices"
	"strings"
)

// endpointControls is what attachControls reads about one endpoint.
// HDMIOrdinal is the endpoint's position among the card's HDMI PCMs,
// and is negative for an endpoint that is not an HDMI slot.
type endpointControls struct {
	Name        string
	Direction   string
	HDMIOrdinal int
}

// The direction of an endpoint, spelled as in pwDirection.
const (
	sinkDirection   = "sink"
	sourceDirection = "source"
)

// The parts of a control name that endpointTakes matches. IEC958
// starts the name of each control of an HDMI PCM. The others mark the
// controls of an input: Input Source, Mic Boost, and Auto Gain Control
// do not contain the word Capture, so they are listed separately.
const (
	iec958Prefix    = "IEC958"
	captureWord     = "Capture"
	inputSourceWord = "Input Source"
	micBoostWord    = "Mic Boost"
	autoGainWord    = "Auto Gain Control"
)

// attachControls returns the controls of each endpoint, keyed by
// endpoint name. The rule is in the section "How controls attach to an
// endpoint" of audio-operator/plans/completed/07-sinks-and-sources.md.
//
// An IEC958 control goes to the HDMI slot with the same index. A
// Capture control, and the controls that select, boost, or gain-control
// an input, go to the card's sources. Every other control goes to the
// card's analog and USB sinks. That includes Playback controls and the
// controls of the whole card, such as Auto-Mute Mode. Jacks are not
// in controls, because mixer.go reads them separately.
//
// A card with two analog sinks lists the same controls on both. The
// operator writes a control only when a spec sets it, so the
// duplication is harmless until two specs disagree. Then the later
// write wins, because the hardware has one register.
func attachControls(controls []control, endpoints []endpointControls) map[string][]control {
	attached := make(map[string][]control, len(endpoints))
	for _, endpoint := range endpoints {
		attached[endpoint.Name] = nil
	}
	for _, element := range controls {
		for _, endpoint := range endpoints {
			if endpointTakes(endpoint, element) {
				attached[endpoint.Name] = append(attached[endpoint.Name], element)
			}
		}
	}
	return attached
}

// endpointTakes reports whether a control belongs to an endpoint.
//
// Only controls on the mixer interface count. An HDA card also
// declares a Playback Channel Map on the PCM interface of each HDMI
// device. Its name contains "Playback", but it belongs to the PCM and
// no spec declares it.
//
// The IEC958 case comes first because an IEC958 control also contains
// "Playback", and it belongs to one HDMI slot, not to the analog
// sinks. The final return needs no check of the connection type,
// because a Bluetooth speaker is on no card, so any sink here that is
// not an HDMI slot is an analog or USB one.
func endpointTakes(endpoint endpointControls, element control) bool {
	if element.Interface != ctlElemIfaceMixer {
		return false
	}
	switch {
	case strings.HasPrefix(element.Name, iec958Prefix):
		return endpoint.Direction == sinkDirection &&
			endpoint.HDMIOrdinal >= 0 && uint32(endpoint.HDMIOrdinal) == element.Index
	case strings.Contains(element.Name, captureWord),
		strings.Contains(element.Name, inputSourceWord),
		strings.Contains(element.Name, micBoostWord),
		strings.Contains(element.Name, autoGainWord):
		return endpoint.Direction == sourceDirection
	}
	return endpoint.Direction == sinkDirection && endpoint.HDMIOrdinal < 0
}

// endpointControlsOf converts a card's endpoints to the form that
// attachControls reads.
//
// The HDMI codec assigns its PCM devices to slots in converter order,
// so the HDMI PCM with the lowest device number is slot 0, and the
// IEC958 controls are indexed by that slot number. Every endpoint that
// is not an HDMI playback PCM gets -1.
func endpointControlsOf(endpoints []alsaEndpoint) []endpointControls {
	hdmi := make([]alsaEndpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if endpoint.HDMI && !endpoint.Capture {
			hdmi = append(hdmi, endpoint)
		}
	}
	slices.SortFunc(hdmi, func(a, b alsaEndpoint) int { return a.PCM - b.PCM })
	ordinals := make(map[string]int, len(hdmi))
	for ordinal, endpoint := range hdmi {
		ordinals[endpoint.Name()] = ordinal
	}

	listed := make([]endpointControls, 0, len(endpoints))
	for _, endpoint := range endpoints {
		ordinal, slot := ordinals[endpoint.Name()]
		if !slot {
			ordinal = -1
		}
		listed = append(listed, endpointControls{
			Name:        endpoint.Name(),
			Direction:   string(endpoint.direction()),
			HDMIOrdinal: ordinal,
		})
	}
	return listed
}

// The kernel names a jack after its connector, so these words in a
// jack's name give its direction. A jack whose name contains none of
// them is matched to no endpoint. An HDMI jack is named for its PCM
// device, as in "HDMI/DP,pcm=3 Jack", and matches none of them on
// purpose, because an HDMI slot reads its ELD instead.
var (
	sinkJackWords   = []string{"Headphone", "Speaker", "Line Out"}
	sourceJackWords = []string{"Mic", "Line In"}
)

// jackState returns whether a plug is in any jack that matches the
// direction, and whether the card has any such jack.
//
// A card plays one analog PCM out of its headphone jack and its
// speaker jack alike, so a plug in either counts as a plug in the
// endpoint. When the card has no matching jack, sensed is false, and
// the Connected condition then reports an analog endpoint connected,
// because no fact says otherwise.
func jackState(direction pwDirection, jacks map[string]bool) (plugged, sensed bool) {
	words := sinkJackWords
	if direction == directionSource {
		words = sourceJackWords
	}
	for name, on := range jacks {
		if !containsAny(name, words) {
			continue
		}
		sensed = true
		plugged = plugged || on
	}
	return plugged, sensed
}

func containsAny(name string, words []string) bool {
	for _, word := range words {
		if strings.Contains(name, word) {
			return true
		}
	}
	return false
}

// capabilitiesOf returns the status.capabilities of one endpoint,
// keyed by the kernel's control name.
func capabilitiesOf(controls []control) map[string]controlCapability {
	capabilities := make(map[string]controlCapability, len(controls))
	for _, element := range controls {
		capabilities[element.Name] = element.Capability
	}
	return capabilities
}

func controlNames(controls []control) []string {
	names := make([]string, 0, len(controls))
	for _, element := range controls {
		names = append(names, element.Name)
	}
	return names
}
