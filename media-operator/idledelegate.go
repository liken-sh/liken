package main

// The idle command pod's half of a delegated screen. The pod holds
// the focus gate and the shade under every controller, and a
// delegate's client holds neither. So the pod forwards each navigation
// press to the client on the Player's commands topic, and it reads
// back the one request the client makes, a sleep.

import "encoding/json"

// forwardPress publishes one press on the Player's commands topic,
// not retained, because a press is an event and not a state. The
// payload is the key event exactly as it arrived, so the client reads
// the kernel's name for the control and holds its own table. A release
// is never forwarded.
func (ic *idleCommander) forwardPress(event keyEvent) {
	// A name and a value always marshal, so the error is the
	// interface's and not a state this code reaches.
	payload, _ := json.Marshal(event)
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
