package main

// The input asks. A press on a controller whose mark names a unit asks
// that unit's receiver for the player's input, in the receiver's own
// generic vocabulary: the media layer names no input, no zone, and no
// port, and the receiver resolves the answer from the session it
// already holds.
//
// The ask is an event and not a state, so it goes into the session's
// inputAsk with the time it was made (receiverasks.go). A press arrives
// often, and an ask on each press would be one API write for each
// press. So the operator writes an ensure only while the Receiver's
// InputSelected condition is not True: the room already shows the unit
// while it is True, and the equipment would answer the ask with nothing.
// Nothing here wakes the room, either. A press that turns the Player on
// wakes it (powerkeys.go), and that wake selects the input, so such a
// press asks for no input here.
//
// A home press asks for more. A TV that shows its own apps is on
// another input, and the receiver's input alone does not bring the
// unit back to the screen. So the home keys ask the receiver to show
// the unit: its input, and the room's TV on the unit's input. The
// equipment operator owns the TV's bus, so the media layer asks in the
// same player terms and sends the TV nothing itself. The TV gets its
// commands only when it is on, so home does not wake a room that is off
// either. A home press is rare, and the condition says nothing about
// the TV, so every show is written.

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"
)

// homeKeys are the names a controller's home button reaches the bus
// under, the same two the playback pod binds to home in keybindings.go.
var homeKeys = []string{"KEY_HOMEPAGE", "KEY_WWW"}

// ensureDesk maps each unit to the Receiver its cable lands on. The
// pass fills it from the Receivers and the bus reader reads it for a
// press, so it carries a mutex of its own rather than the pass's.
//
// A unit the pass placed with no Receiver keeps an entry with no name,
// and receiverFor answers none for it, the same as for a unit the pass
// has not reached yet.
type ensureDesk struct {
	mutex     sync.Mutex
	receivers map[string]unitReceiver
}

// unitReceiver is the Receiver a unit's cable lands on, and whether its
// InputSelected condition is True. An empty name is a unit wired to no
// Receiver.
type unitReceiver struct {
	name          string
	inputSelected bool
}

func newEnsureDesk() *ensureDesk {
	return &ensureDesk{receivers: map[string]unitReceiver{}}
}

// set records one unit's Receiver. A Receiver with no name, the shape a
// unit that matches no input arrives in, records the unit as unwired.
func (e *ensureDesk) set(player string, receiver unitReceiver) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	e.receivers[player] = receiver
}

// receiverFor reads one unit's Receiver. An unwired unit and a unit the
// pass has not placed both hold none.
func (e *ensureDesk) receiverFor(player string) (unitReceiver, bool) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	receiver, placed := e.receivers[player]
	return receiver, placed && receiver.name != ""
}

// retain drops every unit outside the live set, so a deleted Player
// leaves no entry for a Player created again under its name.
func (e *ensureDesk) retain(live map[string]bool) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	for player := range e.receivers {
		if !live[player] {
			delete(e.receivers, player)
		}
	}
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

// focusedUnit names the unit a controller's mark names, as
// namespace/name, or empty while the mark names none. Every ask a
// controller's press makes through this operator is gated here, so a
// controller pointed at another room asks nothing of this one.
func (o *operator) focusedUnit(namespace, controller string) (string, string) {
	player := o.focus.markFor(controllerKey(namespace, controller))
	if player == "" {
		return "", ""
	}
	return player, playerKey(namespace, player)
}

// ensureInput translates one controller press into the receiver's
// generic ask. The press asks only while its controller's mark names a
// unit wired to a receiver, so a controller pointed at another room, or
// at a unit with no equipment, asks nothing. A power press asks nothing
// either: an input ask beside a power-off would make the receiver
// select the input and power on again. The key is the name the standing
// pod publishes after the Keymap's fold, so a button a Keymap maps to a
// power key is exempt too, and a power button a Keymap maps to another
// key is not. A home press
// asks for a show in place of the ensure. An ask the operator writes
// earns a line, and a press that asks nothing writes none, because the
// pod the press reached writes its own.
func (o *operator) ensureInput(namespace, controller string, payload []byte) {
	key, pressed := pressOf(payload)
	if !pressed || powerKey(key) {
		return
	}
	player, unit := o.focusedUnit(namespace, controller)
	if unit == "" {
		return
	}
	receiver, held := o.ensure.receiverFor(unit)
	if !held {
		return
	}
	action := inputEnsure
	if slices.Contains(homeKeys, key) {
		action = inputShow
	}
	if action == inputEnsure && receiver.inputSelected {
		return
	}
	trigger := fmt.Sprintf("remote %s: %s pressed with focus on player %s", controllerKey(namespace, controller), key, player)
	o.askInput(trigger, receiver.name, action)
}

// askInput writes one input ask on a Receiver.
func (o *operator) askInput(trigger, receiver, action string) {
	err := o.sessions.ask(o.client, receiver, func(asks *receiverAsks) {
		asks.input = &ReceiverAction{Action: action, At: nextAskTime(previousAt(asks.input), time.Now())}
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s, writing inputAsk %s on receiver %s: %v\n", trigger, action, receiver, err)
		return
	}
	logLine(o.log, "%s, wrote inputAsk %s on receiver %s", trigger, action, receiver)
}
