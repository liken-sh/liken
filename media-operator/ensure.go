package main

// When a controller with focus on a Player sends a key press, the
// operator writes an input request on the Player's Receiver, in the
// session's inputAsk (receiverasks.go). The request is "ensure" or
// "show" and names no input, zone, or port, because equipment-operator
// reads those from the Receiver's session. Each request has its own time,
// because it is an event and not a state.
//
// A home press writes a show. A TV that shows its own apps is on
// another input, so selecting the Receiver's input does not bring the
// Player back on screen. A show asks equipment-operator to select the
// Player's input on the Receiver and on the TV. equipment-operator
// sends the TV commands only while the TV is on, so a home press does
// not turn on a room that is off. InputSelected says nothing about the
// TV, so every home press writes a show.
//
// Any other press writes an ensure, but only while the Receiver's
// InputSelected condition is not True, so the operator does not write
// to the API server on every press.

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"
)

// The playback pod binds the same key names to home in keybindings.go.
var homeKeys = []string{"KEY_HOMEPAGE", "KEY_WWW"}

// playerReceivers maps each Player key to the Receiver it is wired to. The
// reconcile pass writes it and the bus reader reads it on each key
// press, so it has its own mutex.
type playerReceivers struct {
	mutex     sync.Mutex
	receivers map[string]receiverWiring
}

// receiverWiring is the Receiver that a Player is wired to, and whether
// its InputSelected condition is True. An empty name records that the
// Player is wired to no Receiver.
type receiverWiring struct {
	name          string
	inputSelected bool
}

func newPlayerReceivers() *playerReceivers {
	return &playerReceivers{receivers: map[string]receiverWiring{}}
}

func (e *playerReceivers) set(player string, receiver receiverWiring) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	e.receivers[player] = receiver
}

// receiverFor returns false both when the Player is wired to no
// Receiver and when the pass has not recorded the Player yet.
func (e *playerReceivers) receiverFor(player string) (receiverWiring, bool) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	receiver, recorded := e.receivers[player]
	return receiver, recorded && receiver.name != ""
}

// retain deletes the entry of every Player that is not in live, so a
// Player created again with the same name starts with no stale entry.
func (e *playerReceivers) retain(live map[string]bool) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	for player := range e.receivers {
		if !live[player] {
			delete(e.receivers, player)
		}
	}
}

// pressOf returns the key of a Remote's event and whether the event
// starts a press. Value 1 is a press, 2 is a key repeat, and 0 is a
// release.
func pressOf(payload []byte) (string, bool) {
	var event keyEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return "", false
	}
	return event.Key, event.Value == 1
}

// focusedPlayer returns the name and the namespace/name key of the
// Player that the controller has focus on, or two empty strings when it
// has focus on none. Every request that a controller press writes goes
// through it, so a controller with focus on another Player changes
// nothing on this one.
func (o *operator) focusedPlayer(namespace, controller string) (string, string) {
	player := o.focus.markFor(controllerKey(namespace, controller))
	if player == "" {
		return "", ""
	}
	return player, playerKey(namespace, player)
}

// ensureInput writes nothing for a power key. Turning a Player on
// already selects its input (powerkeys.go), and an input request next
// to a power-off would turn the Receiver on again. The key is the name
// after the playback pod applied the Keymap, so a power key is the
// key that the Keymap maps a button to. A press that writes no request
// logs nothing, because the playback pod logs every press.
func (o *operator) ensureInput(namespace, controller string, payload []byte) {
	key, pressed := pressOf(payload)
	if !pressed || powerKey(key) {
		return
	}
	player, focused := o.focusedPlayer(namespace, controller)
	if focused == "" {
		return
	}
	receiver, wired := o.playerReceivers.receiverFor(focused)
	if !wired {
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

// showForHomeAsk writes a show for a home action on a Player's commands
// topic, the same as a home press on a Remote, so a program that asks
// for home brings the Player back on screen. It skips a home action
// that has Remote set, because the playback pod published it for that
// Remote's press, and ensureInput already wrote the show for that press.
func (o *operator) showForHomeAsk(namespace, name string, payload []byte) {
	var ask mediaCommand
	if err := json.Unmarshal(payload, &ask); err != nil {
		return
	}
	if ask.Action != actionHome || ask.Remote != "" {
		return
	}
	receiver, wired := o.playerReceivers.receiverFor(playerKey(namespace, name))
	if !wired {
		return
	}
	trigger := fmt.Sprintf("player %s: a home command on its commands topic", playerKey(namespace, name))
	o.askInput(trigger, receiver.name, inputShow)
}

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
