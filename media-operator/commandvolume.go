package main

// The command sidecar's volume half: the level as Player state on the
// bus, the owner mark that hands the level to equipment, and the one
// path from either to mpv.

import (
	"fmt"
	"os"
)

// applyVolume folds one message off the volume topic and writes it
// to mpv. It is the only place in the pod that sets the level, so a
// press made here and a press made on another screen of the same
// unit reach mpv the same way.
func (c *commander) applyVolume(payload []byte) {
	state, ok := parseVolumeState(payload)
	if !ok {
		return
	}
	c.volumeMutex.Lock()
	c.volume = state
	c.haveVolume = true
	signal := c.volumeCaughtUp
	c.volumeCaughtUp = true
	owned := c.volumeOwned
	c.volumeMutex.Unlock()
	// While the mark stands the level is the equipment's. The state is
	// recorded for the next press, and mpv is left at unity with no
	// indicator drawn.
	//
	// Every message re-asserts unity instead of setting it once, so no
	// earlier write leaves mpv below unity while the mark stands.
	if owned {
		for _, command := range volumeCommands(defaultVolumeState()) {
			c.command(command)
		}
		return
	}
	for _, command := range volumeCommands(state) {
		c.command(command)
	}
	if signal {
		c.command(volumeChangedCommand())
	}
}

// applyVolumeOwner folds one message off the owner topic. A non-empty
// payload hands the level to equipment, and mpv goes to unity. An
// empty payload is the mark cleared, and mpv takes the level back at
// the state the topic last delivered.
//
// Every non-empty mark writes unity, a redelivered one included, so a
// mark that still stands is enough on its own to put mpv back at unity.
func (c *commander) applyVolumeOwner(payload []byte) {
	owned := len(payload) > 0
	c.volumeMutex.Lock()
	c.volumeOwned = owned
	apply, state := true, defaultVolumeState()
	if !owned {
		apply, state = c.haveVolume, c.volume
	}
	c.volumeMutex.Unlock()
	if !apply {
		return
	}
	for _, command := range volumeCommands(state) {
		c.command(command)
	}
}

// pressVolume publishes what a press means, retained, and writes
// nothing to mpv. It computes from the last message the topic
// delivered, or from unity before any message arrives, so a pod
// that just started still steps from a definite level.
func (c *commander) pressVolume(command mediaCommand) {
	if c.volumeTopic == "" {
		return
	}
	payload, err := marshalVolumeState(nextVolume(c.heldVolume(), command))
	if err != nil {
		fmt.Fprintf(os.Stderr, "command: volume: %v\n", err)
		return
	}
	c.bus.Publish(c.volumeTopic, payload, true)
}

// heldVolume is the state the last message left, and unity before
// any message arrives.
func (c *commander) heldVolume() volumeState {
	c.volumeMutex.Lock()
	defer c.volumeMutex.Unlock()
	if !c.haveVolume {
		return defaultVolumeState()
	}
	return c.volume
}

// applyHeldVolume writes the state the bus already delivered, once
// mpv's socket is live. The broker delivers the retained level and
// mark within milliseconds of the subscribe, and mpv opens its socket
// seconds later, so those messages find no socket and write nothing.
// Without this write the film runs at the level mpv's command line
// set until the next press. It draws no indicator, because nothing
// was pressed.
func (c *commander) applyHeldVolume() {
	c.volumeMutex.Lock()
	owned, have, state := c.volumeOwned, c.haveVolume, c.volume
	c.volumeMutex.Unlock()
	if owned {
		state = defaultVolumeState()
	} else if !have {
		return
	}
	for _, command := range volumeCommands(state) {
		c.command(command)
	}
}
