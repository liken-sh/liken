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
// power key is the one that does that.

import (
	"encoding/json"
	"sync"
)

// theEnsureCommand is the receiver's generic player action. The media
// layer asks in these terms alone, so no receiver vocabulary reaches
// this side.
const theEnsureCommand = "input.ensure"

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

// isPress reports whether one events payload begins a press. The press
// is value 1; a repeat is 2 and a release is 0, and neither is a new
// ask.
func isPress(payload []byte) bool {
	var event keyEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return false
	}
	return event.Value == 1
}

// ensureInput translates one controller press into the receiver's
// generic ask. The press asks only while its controller's mark names a
// unit wired to a receiver, so a controller pointed at another room, or
// at a unit with no equipment, asks nothing.
func (o *operator) ensureInput(namespace, controller string, payload []byte) {
	if !isPress(payload) {
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
	ask, err := json.Marshal(ensureAsk{Command: theEnsureCommand})
	if err != nil {
		return
	}
	o.bus.Publish(commands, ask, false)
}

// ensureAsk is the payload a receiver's commands topic carries.
type ensureAsk struct {
	Command string `json:"command"`
}
