package main

// The listening level as Player state. This file holds the retained
// payload every pod for one unit follows, the arithmetic of a press,
// the mpv words that apply a state, and the operator's record of what
// the broker holds per unit.

import (
	"encoding/json"
	"strconv"
	"sync"
)

// The level runs 0 to 100, and 100 is unity, mpv's 0 dB and its own
// default. The cap sits at unity because the sinks play at unity on
// the audio side, and a software gain above unity only distorts.
const (
	minVolume   = 0
	unityVolume = 100
)

// volumeState is the whole payload on a unit's volume topic. Both
// fields are always written, so a reader never needs a default for a
// field a message omits, and every pod applies the same state.
type volumeState struct {
	Level int  `json:"level"`
	Muted bool `json:"muted"`
}

// defaultVolumeState is the state a pod holds before any message
// reaches it, and the value the operator seeds a unit at.
func defaultVolumeState() volumeState {
	return volumeState{Level: unityVolume, Muted: false}
}

// clamped holds the level inside 0 to 100. Every path that computes
// or accepts a level runs through here, so no arithmetic elsewhere
// bounds itself.
func (v volumeState) clamped() volumeState {
	if v.Level < minVolume {
		v.Level = minVolume
	}
	if v.Level > unityVolume {
		v.Level = unityVolume
	}
	return v
}

// parseVolumeState reads one message off the topic. A payload that
// does not decode is no state at all. A level outside the range is
// clamped rather than refused, so another program's message cannot
// drive mpv past unity.
func parseVolumeState(payload []byte) (volumeState, bool) {
	var state volumeState
	if err := json.Unmarshal(payload, &state); err != nil {
		return volumeState{}, false
	}
	return state.clamped(), true
}

// marshalVolumeState encodes the state for the topic. The clamp runs
// here too, so nothing this operator publishes is out of range.
func marshalVolumeState(state volumeState) ([]byte, error) {
	return json.Marshal(state.clamped())
}

// isVolumeAction names the two actions that never reach mpv as
// commands. A press of either publishes the unit's next state, and
// the subscription applies it.
func isVolumeAction(action string) bool {
	return action == actionVolume || action == actionMute
}

// nextVolume is the whole arithmetic of a press: the keymap's step,
// signed by the direction, and mute as a plain toggle. The state it
// reads is the last message from the topic, so two pods that press
// against the same message compute the same absolute value and the
// step lands once.
func nextVolume(state volumeState, command mediaCommand) volumeState {
	switch command.Action {
	case actionVolume:
		state.Level += command.Amount
		return state.clamped()
	case actionMute:
		state.Muted = !state.Muted
		return state.clamped()
	}
	return state
}

// volumeCommands turns one state into mpv's words. Both carry
// no-osd, because the display draws the indicator and mpv's own text
// would be a second one over it. The level travels as a string,
// which is what mpv's set command takes.
func volumeCommands(state volumeState) [][]any {
	state = state.clamped()
	return [][]any{
		{"no-osd", "set", "volume", strconv.Itoa(state.Level)},
		{"no-osd", "set", "mute", mpvYesNo(state.Muted)},
	}
}

// volumeChangedMessage is the display's only show trigger for the
// indicator. The display records mpv's volume and mute properties as
// they change and draws nothing until this message arrives, so a
// sidecar that applies the retained level at pod start pops no
// indicator on the screen.
const volumeChangedMessage = "volume-changed"

// volumeChangedCommand is the script message that follows an applied
// press. It goes out on the same connection as the two sets above,
// after them, so the display draws the value it already holds.
func volumeChangedCommand() []any {
	return []any{"script-message", volumeChangedMessage}
}

// mpvYesNo writes a flag the way mpv reads one, on the command line
// and on the IPC socket alike.
func mpvYesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

// mergedWith lays a Play's declared starting state over the unit's
// current one. A field the Play omits changes nothing, so the level
// a person left on the unit stands, and only what the Play states
// moves.
func (v volumeState) mergedWith(override *PlayVolume) volumeState {
	if override == nil {
		return v.clamped()
	}
	if override.Level != nil {
		v.Level = *override.Level
	}
	if override.Muted != nil {
		v.Muted = *override.Muted
	}
	return v.clamped()
}

// asPlayVolume writes a resolved state back into a Play's spec
// block. The operator sets it on the copy of the Play it builds the
// pod from, the same way it sets the film's saved place there, so
// the pod builder reads one field and never reads the bus.
func (v volumeState) asPlayVolume() *PlayVolume {
	state := v.clamped()
	return &PlayVolume{Level: &state.Level, Muted: &state.Muted}
}

// volumeDesk is the boundary between the bus and the reconcile loop
// for each unit's level, the way panelDesk is for each unit's panel.
// It answers the one question the seed asks: whether the broker
// already holds a state for this unit.
type volumeDesk struct {
	mutex sync.Mutex
	state map[string]volumeState
}

func newVolumeDesk() *volumeDesk {
	return &volumeDesk{state: map[string]volumeState{}}
}

// setState records one unit's state, whether it arrived on the bus
// or the operator just published it. Recording the operator's own
// publish keeps the next pass from seeding the same unit again
// before the broker echoes the message back.
func (d *volumeDesk) setState(key string, state volumeState) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.state[key] = state.clamped()
}

// stateFor returns one unit's state, and whether the desk holds one
// at all. The bool is the seed's whole decision.
func (d *volumeDesk) stateFor(key string) (volumeState, bool) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	state, held := d.state[key]
	return state, held
}

// retain drops the units the cluster no longer holds, so a
// long-running operator does not keep a key per deleted Player.
func (d *volumeDesk) retain(live map[string]bool) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	for key := range d.state {
		if !live[key] {
			delete(d.state, key)
		}
	}
}
