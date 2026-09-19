// Package denon is the protocol driver for a Denon or Marantz A/V
// receiver. It parses the Denon ASCII protocol on port 23, records the
// state each line reports, and reconnects when the read deadline or a
// socket error ends the connection. It implements equipment.Driver and
// reports volume in half steps, two per display unit.
//
// The protocol reference is https://assets.denon.com/documentmaster/uk/
// avr1713_avr1613_protocol_v860.pdf. The lines this file parses were
// also read from a live AVR-X1700H.
package denon

import (
	"strings"

	"github.com/liken-sh/equipment-operator/equipment"
)

// Port is the control port a Denon answers on, used when the declared
// address names none.
const Port = "23"

// Terminator ends every command and every reply: an ASCII carriage
// return.
const Terminator = '\r'

// Queries are the five queries the operator sends on every connect.
// Their answers are the whole of the status.
var Queries = []string{"PW?", "MV?", "MU?", "SI?", "MS?"}

// The set commands this operator sends.
const (
	PowerOnCommand      = "PWON"
	MuteOnCommand       = "MUON"
	MuteOffCommand      = "MUOFF"
	powerStandbyCommand = "PWSTANDBY"
	VolumePrefix        = "MV"
	InputPrefix         = "SI"
	SoundModePrefix     = "MS"
)

// The two power words the status carries.
const (
	powerOn      = "on"
	powerStandby = "standby"
)

// The fields a line can name, which is what a listener switches on.
const (
	powerField     = "power"
	inputField     = "input"
	volumeField    = "volume"
	volumeMaxField = "volumeMax"
	muteField      = "mute"
	soundModeField = "soundMode"
)

// denonState stores the latest values the receiver reported in its own
// units. Reachable becomes true on a recognized reply and changes when
// the connection fails.
type denonState struct {
	Power     string
	Input     string
	SoundMode string
	Mute      bool
	Volume    int
	VolumeMax int
	Reachable equipment.ConditionStatus
}

// newDenonState is the state before the receiver has said anything.
func newDenonState() denonState {
	return denonState{
		Volume:    unknownHalves,
		VolumeMax: unknownHalves,
		Reachable: equipment.ConditionUnknown,
	}
}

// applyDenonLine folds one line into the state and answers which field
// it named. A line this operator does not know is ignored, because a
// Denon volunteers dozens of settings nothing here reads.
func applyDenonLine(state denonState, line string) (denonState, string, bool) {
	switch {
	case line == "PWON":
		state.Power = powerOn
		return state, powerField, true
	case line == "PWSTANDBY":
		state.Power = powerStandby
		return state, powerField, true
	case line == MuteOnCommand:
		state.Mute = true
		return state, muteField, true
	case line == MuteOffCommand:
		state.Mute = false
		return state, muteField, true
	case strings.HasPrefix(line, "MVMAX "):
		halves, ok := ParseHalfSteps(strings.TrimSpace(line[len("MVMAX "):]))
		if !ok {
			return state, "", false
		}
		state.VolumeMax = halves
		return state, volumeMaxField, true
	case strings.HasPrefix(line, VolumePrefix):
		halves, ok := ParseHalfSteps(line[len(VolumePrefix):])
		if !ok {
			return state, "", false
		}
		state.Volume = halves
		return state, volumeField, true
	case strings.HasPrefix(line, InputPrefix) && len(line) > 2:
		state.Input = line[2:]
		return state, inputField, true
	case strings.HasPrefix(line, SoundModePrefix) && len(line) > 2:
		state.SoundMode = line[2:]
		return state, soundModeField, true
	}
	return state, "", false
}

// VolumeCommand is the set command for one half-step count.
func VolumeCommand(halves int) string {
	return VolumePrefix + HalfStepDigits(halves)
}

// MuteCommand is the set command for one mute state.
func MuteCommand(muted bool) string {
	if muted {
		return MuteOnCommand
	}
	return MuteOffCommand
}

// InputCommand selects one input by the name the receiver carries for
// it.
func InputCommand(input string) string {
	return InputPrefix + input
}

// SoundModeCommand selects one sound mode by the name the receiver
// carries for it.
func SoundModeCommand(mode string) string {
	return SoundModePrefix + mode
}
