package main

// The ensure ask. A press on a controller whose mark names a unit asks
// that unit's receiver for the player's input, in the receiver's own
// generic vocabulary: the media layer names no input, no zone, and no
// port, and the receiver resolves the answer from the session it
// already holds.
//
// The ask is an event and not a state, so it is not retained. A press
// arrives often and the ask is cheap: the receiver compares it against
// the input it already reports and sends the equipment nothing when the
// room is where it should be. Nothing here wakes the room, either; the
// power key is the one that does that, and it asks for no input.
//
// A home press asks for more. A TV that shows its own apps is on
// another input, and the receiver's input alone does not bring the
// unit back to the screen. So the home keys ask the receiver to show
// the unit: its input, and the room's TV on the unit's input. The
// equipment operator owns the TV's bus, so the media layer asks in the
// same player terms and sends the TV nothing itself. The TV gets its
// commands only when it is on, so home does not wake a room that is off
// either.

import (
	"encoding/json"
	"slices"
	"sync"
)

// theEnsureCommand is the receiver's generic player action. The media
// layer asks in these terms alone, so no receiver vocabulary reaches
// this side.
const theEnsureCommand = "input.ensure"

// theShowCommand is the receiver's generic player action for a home
// press: the ensure, and the room's TV on the unit's input when the TV
// is on.
const theShowCommand = "input.show"

// homeKeys are the names a controller's home button reaches the bus
// under, the same two the playback pod binds to home in keybindings.go.
var homeKeys = []string{"KEY_HOMEPAGE", "KEY_WWW"}

// powerKeys are the three names a controller's power button reaches
// the bus under, the same three the idle screen answers as the room's
// toggle. The equipment operator reads that toggle on the unit's power
// topic, and turns the room off or on. An input ask sent with it would
// make the receiver select the unit's input and power on again, while
// the toggle puts the room to standby. The playback pod's table binds
// the same names to power. The Rust crate media-screen holds a copy of
// this list as POWER in media-screen/src/screen/keys.rs, because a Go
// list cannot reach it: a change to one changes both.
var powerKeys = []string{"KEY_POWER", "KEY_POWER2", "KEY_SLEEP"}

// ensureDesk maps each unit to the commands topic of the receiver its
// cable lands on. The pass fills it from the Receivers and the bus
// reader reads it for a press, so it carries a mutex of its own rather
// than the pass's.
type ensureDesk struct {
	mutex    sync.Mutex
	commands map[string]string
}

func newEnsureDesk() *ensureDesk {
	return &ensureDesk{commands: map[string]string{}}
}

// set records one unit's receiver commands topic. An empty topic, the
// shape a unit that stopped matching arrives in, drops the entry.
func (e *ensureDesk) set(player string, commands string) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	if commands == "" {
		delete(e.commands, player)
		return
	}
	e.commands[player] = commands
}

// commandsFor reads one unit's receiver commands topic.
func (e *ensureDesk) commandsFor(player string) (string, bool) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	commands, held := e.commands[player]
	return commands, held
}

// pressOf reads one events payload as the start of a press, and names
// the key. The press is value 1; a repeat is 2 and a release is 0, and
// neither is a new ask.
func pressOf(payload []byte) (string, bool) {
	var event keyEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return "", false
	}
	return event.Key, event.Value == 1
}

// ensureInput translates one controller press into the receiver's
// generic ask. The press asks only while its controller's mark names a
// unit wired to a receiver, so a controller pointed at another room, or
// at a unit with no equipment, asks nothing. A power press asks nothing
// either. The key is the name the standing pod publishes after the
// Keymap's fold, so a button a Keymap maps to a power key is exempt too,
// and a power button a Keymap maps to another key is not. A home press
// asks for theShowCommand in place of the ensure. An ask the operator
// sends earns a line, and a press that asks nothing writes none,
// because the pod the press reached writes its own.
func (o *operator) ensureInput(namespace, controller string, payload []byte) {
	key, pressed := pressOf(payload)
	if !pressed || slices.Contains(powerKeys, key) {
		return
	}
	player := o.focus.markFor(controllerKey(namespace, controller))
	if player == "" {
		return
	}
	commands, held := o.ensure.commandsFor(playerKey(namespace, player))
	if !held {
		return
	}
	command := theEnsureCommand
	if slices.Contains(homeKeys, key) {
		command = theShowCommand
	}
	ask, err := json.Marshal(ensureAsk{Command: command})
	if err != nil {
		return
	}
	o.bus.Publish(commands, ask, false)
	logLine(o.log, "remote %s: %s pressed with focus on player %s, published %s to %s",
		controllerKey(namespace, controller), key, player, command, commands)
}

// ensureAsk is the payload a receiver's commands topic carries.
type ensureAsk struct {
	Command string `json:"command"`
}
