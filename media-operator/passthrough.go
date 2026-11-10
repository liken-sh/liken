package main

// Passthrough sends a film's Dolby or DTS track to the AV receiver as the
// encoded bitstream, so the receiver decodes it itself. That is the only
// way a receiver's height speakers play Dolby Atmos or DTS:X, because
// mpv's decoders keep the channel bed and drop the height objects.
//
// mpv passes a track through only when --audio-spdif names its codec, and
// it decodes every other track. mpv does not decode a track again when the
// PipeWire sink refuses its bitstream: the film plays with no sound. So
// mpv gets the codecs that the remembered Sink reports in
// status.passthrough, and no other codec.
//
// PipeWire cannot scale a bitstream, so the sink's level and mute do
// nothing while one plays. Only the receiver's own volume works. So a
// Player passes through only when a reachable Receiver sets its level, the
// same Receiver that volumeDevices sends its presses to. A Player with
// more than one sink decodes too, because one mpv stream goes to one sink.

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// The values of spec.passthrough. An absent field is Off, because
// passthrough moves control of the level from the cluster to the
// receiver.
const (
	passthroughOn  = "On"
	passthroughOff = "Off"
)

// spdifFormats pairs each encoded format that the audio operator reports,
// in PipeWire's IEC958 codec names, with mpv's name for its codec. The
// order is the order the option names them in.
var spdifFormats = []struct{ sink, mpv string }{
	{"AC3", "ac3"},
	{"EAC3", "eac3"},
	{"DTS", "dts"},
	{"DTS-HD", "dts-hd"},
	{"TrueHD", "truehd"},
}

// spdifCodecs maps a Sink's status.passthrough to mpv's codec names. A
// name that is not one of the five is dropped, so a format that a newer
// audio operator reports never reaches mpv as a codec it does not know.
func spdifCodecs(formats []string) []string {
	var codecs []string
	for _, format := range spdifFormats {
		if slices.Contains(formats, format.sink) {
			codecs = append(codecs, format.mpv)
		}
	}
	return codecs
}

// spdifOption is the mpv option that passes the codecs through.
func spdifOption(codecs []string) string {
	return "--audio-spdif=" + strings.Join(codecs, ",")
}

// passthroughDecision is what one play start passes through. codecs is
// empty when mpv decodes. reason says why a Player that states On
// decodes, and it is empty for a Player that does not state On.
type passthroughDecision struct {
	asked  bool
	codecs []string
	sink   string
	reason string
}

// passthroughFor decides what mpv passes through on one Player, from its
// spec, the Receiver that sets its level, and its remembered Sink. The
// remembered Sink is the one the last allocated playback claim resolved
// (playersinks.go). A Player's first play has no remembered Sink, because
// the operator creates the pod before the scheduler allocates its claim.
func (o *operator) passthroughFor(player *Player) passthroughDecision {
	if player.Spec.Passthrough != passthroughOn {
		return passthroughDecision{}
	}
	decodes := func(reason string) passthroughDecision {
		return passthroughDecision{asked: true, reason: reason}
	}
	if count := len(player.Spec.Sinks); count != 1 {
		return decodes(fmt.Sprintf("passthrough needs exactly one entry in spec.sinks, and the player states %d", count))
	}
	if _, sets := o.levelReceiver(player); !sets {
		return decodes("no Receiver with a Reachable condition of True sets the unit's level")
	}
	if len(player.Status.Sinks) == 0 || player.Status.Sinks[0].Name == "" {
		return decodes("status.sinks names no Sink yet, which is the case until the player's first playback claim is allocated")
	}
	name := player.Status.Sinks[0].Name
	sink, err := o.view.Sink(name)
	if errors.Is(err, apiclient.ErrNotFound) {
		return decodes("the operator holds no Sink " + name)
	}
	if err != nil {
		return decodes(fmt.Sprintf("reading Sink %s failed: %v", name, err))
	}
	codecs := spdifCodecs(sink.Status.Passthrough)
	if len(codecs) == 0 {
		return decodes("sink " + name + " accepts no encoded format in status.passthrough")
	}
	return passthroughDecision{asked: true, codecs: codecs, sink: name}
}

// logPassthrough writes the decision's line for one play start. A Player
// that does not state On writes none.
func (o *operator) logPassthrough(play *Play, player *Player, decision passthroughDecision) {
	if !decision.asked {
		return
	}
	key := runKey(play.Metadata.Namespace, play.Metadata.Name)
	if len(decision.codecs) == 0 {
		logLine(o.log, "play %s: mpv decodes every track, although player %s states passthrough On, because %s",
			key, player.Metadata.Name, decision.reason)
		return
	}
	logLine(o.log, "play %s: mpv passes %s through to sink %s, because player %s states passthrough On",
		key, strings.Join(decision.codecs, ","), decision.sink, player.Metadata.Name)
}
