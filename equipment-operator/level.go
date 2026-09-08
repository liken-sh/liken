package main

// The level path: the bus payload the room's level travels in, the
// owner mark that says who applies it, and the arithmetic between the
// bus scale and the receiver's own.

import (
	"encoding/json"
	"math"
	"strconv"
)

// The bus scale runs 0 to 100, and 100 is as loud as the room ever
// goes.
const (
	minLevel = 0
	maxLevel = 100
)

// volumeState is the whole payload on a Player's volume topic, the
// shape every pod for the unit applies. It matches the media operator's
// own type.
type volumeState struct {
	Level int  `json:"level"`
	Muted bool `json:"muted"`
}

// clamped holds the level inside the bus scale, so no arithmetic
// elsewhere bounds itself.
func (v volumeState) clamped() volumeState {
	if v.Level < minLevel {
		v.Level = minLevel
	}
	if v.Level > maxLevel {
		v.Level = maxLevel
	}
	return v
}

// parseVolumeState reads one message off the topic. A payload that does
// not decode is no state at all.
func parseVolumeState(payload []byte) (volumeState, bool) {
	var state volumeState
	if err := json.Unmarshal(payload, &state); err != nil {
		return volumeState{}, false
	}
	return state.clamped(), true
}

// marshalVolumeState encodes a state for the topic. The clamp runs here
// too, so nothing this operator publishes is out of range.
func marshalVolumeState(state volumeState) ([]byte, error) {
	return json.Marshal(state.clamped())
}

// volumeOwner is the retained payload on <volumeTopic>/owner. While it
// stands, the named owner applies the level and every pod leaves mpv at
// unity.
type volumeOwner struct {
	Owner string `json:"owner"`
}

// ownerTopic is where the mark for one volume topic sits.
func ownerTopic(volumeTopic string) string {
	return volumeTopic + "/owner"
}

// ownerMark is the payload this operator publishes while it holds a
// receiver's session.
func ownerMark(receiver string) ([]byte, error) {
	return json.Marshal(volumeOwner{Owner: "receiver/" + receiver})
}

// The receiver's scale is counted in half steps, because a Denon moves
// in halves and an integer count of them never rounds. unknownHalves is
// a value the receiver has not reported yet.
const unknownHalves = -1

// parseHalfSteps reads the digits a Denon sends. Two digits are whole
// steps and three digits are tenths, and the receiver only ever sends a
// tenth of five.
func parseHalfSteps(digits string) (int, bool) {
	value, err := strconv.Atoi(digits)
	if err != nil || value < 0 {
		return unknownHalves, false
	}
	switch len(digits) {
	case 2:
		return value * 2, true
	case 3:
		if value%5 != 0 {
			return unknownHalves, false
		}
		return value / 5, true
	}
	return unknownHalves, false
}

// formatHalfSteps writes a count the way a person reads it, which is
// what the status carries.
func formatHalfSteps(halves int) string {
	if halves < 0 {
		return ""
	}
	if halves%2 == 0 {
		return strconv.Itoa(halves / 2)
	}
	return strconv.Itoa(halves/2) + ".5"
}

// halfStepDigits writes the same count in the digits a set command
// carries: two for a whole step, three for a half.
func halfStepDigits(halves int) string {
	whole := halves / 2
	digits := strconv.Itoa(whole)
	if whole < 10 {
		digits = "0" + digits
	}
	if halves%2 == 1 {
		digits += "5"
	}
	return digits
}

// halvesForLevel maps the bus scale onto the receiver's, so 100 is the
// ceiling the room is allowed. A ceiling that is not known yet cannot
// be mapped onto.
func halvesForLevel(level, maxHalves int) (int, bool) {
	if maxHalves <= 0 {
		return unknownHalves, false
	}
	if level < minLevel {
		level = minLevel
	}
	if level > maxLevel {
		level = maxLevel
	}
	return int(math.Round(float64(level) * float64(maxHalves) / maxLevel)), true
}

// levelForHalves maps the receiver's scale back onto the bus against
// the same ceiling, which is what the session publishes after every
// move.
func levelForHalves(halves, maxHalves int) (int, bool) {
	if maxHalves <= 0 || halves < 0 {
		return 0, false
	}
	level := int(math.Round(float64(halves) * maxLevel / float64(maxHalves)))
	if level > maxLevel {
		level = maxLevel
	}
	return level, true
}

// Where no step is declared, one press moves the receiver by one whole
// unit of its own scale, which is two half steps.
const defaultStepHalves = 2

// halvesFromScale reads a figure a person wrote in the receiver's own
// scale as a count of half steps. A figure between two half steps is
// not one the receiver can take, so it goes to the nearest.
func halvesFromScale(value float64) int {
	return int(math.Round(value * 2))
}

// The top of a Denon's own scale, 98, which no declared ceiling may
// exceed.
const denonScaleTop = 196

// ceilingHalves is what 100 on the bus means. It is the ceiling a
// person declared and nothing else. The receiver's own MVMAX line is
// not a limit: one AVR reported 69.5, then 70.5, then 64.5 in one
// evening, so a ceiling read from it would move under the room.
func ceilingHalves(rule ReceiverVolume) int {
	stated := halvesFromScale(rule.Max)
	if stated <= 0 {
		return 0
	}
	if stated > denonScaleTop {
		return denonScaleTop
	}
	return stated
}

// pressHalves is how far one press moves the receiver.
func pressHalves(rule ReceiverVolume) int {
	if stated := halvesFromScale(rule.Step); stated > 0 {
		return stated
	}
	return defaultStepHalves
}
