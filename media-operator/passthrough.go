package main

// Passthrough sends a film's Dolby or DTS track to the A/V receiver as an
// encoded bitstream, and the receiver decodes it. A receiver plays the
// height channels of Dolby Atmos or DTS:X only from the bitstream,
// because mpv's decoders output the channel bed and drop the height
// objects.
//
// mpv passes a track through when its codec is in --audio-spdif, and
// decodes every other track. When the PipeWire sink refuses a
// bitstream, mpv does not fall back to decoding, and the film plays with
// no sound. So the option lists only the codecs in status.passthrough of
// the Player's Sink.
//
// PipeWire cannot scale a bitstream, so the Sink's volume and mute do
// nothing while one plays. A Player therefore passes through only when
// a reachable Receiver handles its volume presses (levelReceiver), so
// the receiver's own volume works. A Player with more than one sink
// decodes, because mpv sends one stream to one sink.

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// The values of spec.passthrough. An absent field means Off, because
// passthrough moves volume control from the cluster to the receiver.
const (
	passthroughOn  = "On"
	passthroughOff = "Off"
)

// spdifFormats maps each encoded format that a Sink reports, in
// PipeWire's IEC958 names, to mpv's codec name. The option lists codecs
// in this order.
var spdifFormats = []struct{ sink, mpv string }{
	{"AC3", "ac3"},
	{"EAC3", "eac3"},
	{"DTS", "dts"},
	{"DTS-HD", "dts-hd"},
	{"TrueHD", "truehd"},
}

// spdifCodecs drops a format that is not in spdifFormats, so a format
// that a newer audio-operator reports cannot reach mpv as an unknown
// codec.
func spdifCodecs(formats []string) []string {
	var codecs []string
	for _, format := range spdifFormats {
		if slices.Contains(formats, format.sink) {
			codecs = append(codecs, format.mpv)
		}
	}
	return codecs
}

func spdifOption(codecs []string) string {
	return "--audio-spdif=" + strings.Join(codecs, ",")
}

// passthroughDecision is the result for one play start. requested is
// true when spec.passthrough is On. When requested is true and codecs is
// empty, mpv decodes and reason says why.
type passthroughDecision struct {
	requested bool
	codecs    []string
	sink      string
	reason    string
}

// passthroughFor decides which codecs mpv passes through for a Player.
// It reads the Sink from status.sinks, which records the Sink of the
// Player's last allocated playback claim (playersinks.go). So the first
// play of a Player decodes, because the operator creates its pod before
// the scheduler allocates the claim.
func (o *operator) passthroughFor(player *Player) passthroughDecision {
	if player.Spec.Passthrough != passthroughOn {
		return passthroughDecision{}
	}
	decodes := func(reason string) passthroughDecision {
		return passthroughDecision{requested: true, reason: reason}
	}
	if count := len(player.Spec.Sinks); count != 1 {
		return decodes(fmt.Sprintf("spec.sinks has %d entries, and passthrough needs exactly one", count))
	}
	if _, sets := o.levelReceiver(player); !sets {
		return decodes("no Receiver matches the Player's screen with a Reachable condition of True, and passthrough needs one to set the volume")
	}
	if len(player.Status.Sinks) == 0 || player.Status.Sinks[0].Name == "" {
		return decodes("status.sinks lists no Sink, and stays empty until the Player's first playback claim is allocated")
	}
	name := player.Status.Sinks[0].Name
	sink, err := o.view.Sink(name)
	if errors.Is(err, apiclient.ErrNotFound) {
		return decodes("Sink " + name + " does not exist")
	}
	if err != nil {
		return decodes(fmt.Sprintf("reading Sink %s: %v", name, err))
	}
	codecs := spdifCodecs(sink.Status.Passthrough)
	if len(codecs) == 0 {
		return decodes("Sink " + name + " lists no encoded format in status.passthrough")
	}
	return passthroughDecision{requested: true, codecs: codecs, sink: name}
}

// logPassthrough logs nothing when the Player did not request
// passthrough.
func (o *operator) logPassthrough(play *Play, player *Player, decision passthroughDecision) {
	if !decision.requested {
		return
	}
	key := runKey(play.Metadata.Namespace, play.Metadata.Name)
	if len(decision.codecs) == 0 {
		logLine(o.log, "play %s: mpv decodes every track, although Player %s sets spec.passthrough to On, because %s",
			key, player.Metadata.Name, decision.reason)
		return
	}
	logLine(o.log, "play %s: mpv passes %s through to sink %s, because Player %s sets spec.passthrough to On",
		key, strings.Join(decision.codecs, ","), decision.sink, player.Metadata.Name)
}
