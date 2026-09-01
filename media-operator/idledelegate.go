package main

// The idle command pod's half of a delegated screen. The pod holds
// the keymaps, the focus gate, and the shade under every controller,
// and a delegate's client holds none of them. So the pod forwards each
// navigation press to the client on the Player's commands topic, and
// it reads back the one request the client makes, a sleep.

import "encoding/json"

// applyPress publishes what one press means. A navigation press goes
// to the delegate's client on the commands topic, and a volume or mute
// press publishes the unit's next level. An empty action is the
// ordinary case of a press that named neither, and it publishes
// nothing.
func (ic *idleCommander) applyPress(command mediaCommand) {
	if isNavigationAction(command.Action) {
		ic.forwardPress(command)
		return
	}
	ic.pressVolume(command)
}

// forwardPress publishes one press on the Player's commands topic,
// not retained, because a press is an event and not a state. The
// payload is the shape a translator publishes on a Play's commands
// topic, so a client reads one vocabulary on both trees.
func (ic *idleCommander) forwardPress(command mediaCommand) {
	// An action and an amount always marshal, so the error is the
	// interface's and not a state this code reaches.
	payload, _ := json.Marshal(command)
	ic.publish(ic.commandsTopic, payload, false)
}

// sleepOnRequest brings the shade down on a client's request, with the
// same three moves this operator's own back press makes, so the shade
// and the panel desire behave the same whichever process asked. It
// acts only while the unit plays nothing and the screen is awake.
func (ic *idleCommander) sleepOnRequest() {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	if !ic.idle || ic.asleep {
		return
	}
	ic.asleep = true
	ic.rearmLocked()
	ic.applyShadeLocked(screenSleepEvent)
}
