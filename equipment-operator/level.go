package main

// The level path: the bus payload the room's level travels in, the
// owner mark that says who applies it, and the arithmetic between the
// bus scale and a driver's own steps.

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

// formatSteps writes a driver's step count the way a person reads it,
// which is what the status carries. The resolution is the driver's
// number of steps in one display unit, so two half steps read as one.
func formatSteps(steps, resolution int) string {
	if steps < 0 || resolution <= 0 {
		return ""
	}
	return strconv.FormatFloat(float64(steps)/float64(resolution), 'f', -1, 64)
}

// stepsFromScale reads a figure a person wrote in the driver's own
// scale as a count of the driver's smallest steps.
func stepsFromScale(value float64, resolution int) int {
	if resolution <= 0 {
		return 0
	}
	return int(math.Round(value * float64(resolution)))
}

// ceilingSteps is what 100 on the bus means, in the driver's steps. It
// is the ceiling a person declared and nothing else.
func ceilingSteps(rule ReceiverVolume, resolution int) int {
	stated := stepsFromScale(rule.Max, resolution)
	if stated <= 0 {
		return 0
	}
	return stated
}

// pressSteps is how far one press moves the driver, in its steps. Where
// no step is declared, one press moves one whole display unit.
func pressSteps(rule ReceiverVolume, resolution int) int {
	if stated := stepsFromScale(rule.Step, resolution); stated > 0 {
		return stated
	}
	if resolution <= 0 {
		return 1
	}
	return resolution
}

// stepsForLevel maps the bus scale onto the driver's, so 100 is the
// ceiling the room is allowed. A ceiling that is not known yet cannot
// be mapped onto.
func stepsForLevel(level, maxSteps int) (int, bool) {
	if maxSteps <= 0 {
		return 0, false
	}
	if level < minLevel {
		level = minLevel
	}
	if level > maxLevel {
		level = maxLevel
	}
	return int(math.Round(float64(level) * float64(maxSteps) / maxLevel)), true
}

// levelForSteps maps the driver's scale back onto the bus against the
// same ceiling, which is what the session publishes after every move.
func levelForSteps(steps, maxSteps int) (int, bool) {
	if maxSteps <= 0 || steps < 0 {
		return 0, false
	}
	level := int(math.Round(float64(steps) * maxLevel / float64(maxSteps)))
	if level > maxLevel {
		level = maxLevel
	}
	return level, true
}
