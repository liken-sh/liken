package main

// Which endpoint of a card each of the card's controls belongs to.
//
// ALSA mixer controls belong to a card, and a card publishes several
// endpoints: a Realtek codec has an analog output, an analog input,
// and three or four HDMI slots, all on one control device. A person
// declares a control on the endpoint they hear, so the controls have
// to be sorted onto the endpoints. The kernel's control names carry
// the direction and, for an HDMI slot, an index, and that is what
// the rule reads.

import (
	"slices"
	"strings"
)

// endpointControls is what the attach rule needs to know about one
// endpoint of a card. HDMIOrdinal is the endpoint's place among the
// card's HDMI PCMs, and it is negative on every endpoint that is not
// an HDMI slot. The rule reads these three fields and nothing else.
type endpointControls struct {
	Name        string
	Direction   string
	HDMIOrdinal int
}

// The two directions an endpoint carries sound in, which are the
// words the Sink and the Source take.
const (
	sinkDirection   = "sink"
	sourceDirection = "source"
)

// The names the rule reads: the prefix of an HDMI PCM's own controls,
// and the four words a card gives the controls that capture, pick,
// boost, and level an input. The last three carry no Capture word of
// their own, which is why they are listed.
const (
	iec958Prefix    = "IEC958"
	captureWord     = "Capture"
	inputSourceWord = "Input Source"
	micBoostWord    = "Mic Boost"
	autoGainWord    = "Auto Gain Control"
)

// attachControls sorts one card's controls onto the card's endpoints.
//
// The rule is plan 07's. An IEC958 control goes to the HDMI slot of
// its own index. A Capture control, and the ones that pick, boost,
// and level an input, go to the card's sources. Everything else the
// card declares goes to its analog and USB sinks, which covers a
// Playback control and covers the controls that belong to the card
// rather than to one endpoint, such as Auto-Mute Mode. A jack
// reaches no endpoint, and it is not in the list to begin with.
//
// A card with two analog sinks lists the same controls on both. The
// operator writes a control only when a spec states it, so the
// duplication costs nothing until two specs disagree, and then the
// write that came last wins, because the hardware has one register.
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

// endpointTakes reports whether one control belongs to one endpoint.
//
// Only the mixer interface is sorted. An HDA card also declares a
// Playback Channel Map on the PCM interface of every HDMI device, and
// its name carries the Playback word. It is the PCM's own control and
// no spec declares it, so the rule reads the mixer interface alone.
//
// The IEC958 test comes first because an IEC958 control carries the
// Playback word too, and it belongs to one HDMI slot and to no
// analog sink. The last line needs no test for the connection type:
// a Bluetooth speaker is on no card, so the only sink that is not an
// HDMI slot here is an analog or a USB one.
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

// endpointControlsOf lists one card's endpoints the way the attach
// rule reads them.
//
// The HDMI ordinal comes from the PCM device order. The HDMI codec
// fills its PCM slots in converter order, so the slot with the
// lowest device number is the first, and that is the ordinal the
// IEC958 elements are indexed by. Every other endpoint carries a
// negative ordinal, which is what says it is not an HDMI slot.
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

// The words a jack's name carries for each direction. The kernel
// names a jack for the connector behind it, so the words are the
// connectors a person plugs into, and a jack whose name carries none
// of them reaches no endpoint. An HDMI jack is named for its own PCM
// device, HDMI/DP,pcm=3 Jack, and carries none of these words on
// purpose: an HDMI slot reads its ELD instead.
var (
	sinkJackWords   = []string{"Headphone", "Speaker", "Line Out"}
	sourceJackWords = []string{"Mic", "Line In"}
)

// jackState answers what the card's jacks say about one endpoint:
// whether a plug is in, and whether the card senses a jack for it at
// all.
//
// Several jacks answer for one endpoint, and any one of them is
// enough. A card plays one analog PCM out of its headphone jack and
// its speaker jack alike, so a plug in either one is a plug this
// endpoint can play into. A card that senses no jack for the
// endpoint reports so, and the Connected condition then says the
// endpoint can play, because nothing says otherwise.
func jackState(direction pwDirection, jacks map[string]bool) (plugged, sensed bool) {
	words := sinkJackWords
	if direction == directionSource {
		words = sourceJackWords
	}
	for name, on := range jacks {
		if !carriesWord(name, words) {
			continue
		}
		sensed = true
		plugged = plugged || on
	}
	return plugged, sensed
}

func carriesWord(name string, words []string) bool {
	for _, word := range words {
		if strings.Contains(name, word) {
			return true
		}
	}
	return false
}

// capabilitiesOf builds what status.capabilities carries for one
// endpoint, keyed by the kernel's own control name.
func capabilitiesOf(controls []control) map[string]controlCapability {
	capabilities := make(map[string]controlCapability, len(controls))
	for _, element := range controls {
		capabilities[element.Name] = element.Capability
	}
	return capabilities
}

// controlNames lists the names of a set of controls, in the order the
// card declares them.
func controlNames(controls []control) []string {
	names := make([]string, 0, len(controls))
	for _, element := range controls {
		names = append(names, element.Name)
	}
	return names
}
