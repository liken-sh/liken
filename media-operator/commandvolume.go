package main

// The command sidecar's volume half: the level as Player state on the
// bus, the owner mark that hands the level to equipment, and the one
// path from either to mpv.

import (
	"encoding/json"
	"fmt"
	"os"
)

// applyVolume folds one message off the volume topic and writes it
// to mpv. It is the only place in the pod that sets the level, so a
// press made here and a press made on another screen of the same
// unit reach mpv the same way.
//
// A live message that moves the level is the answer to a person's press
// somewhere on the unit, so it earns a line that says where the level
// went. The catch-up, and a message that repeats the level held, write
// none.
func (c *commander) applyVolume(payload []byte) {
	state, ok := parseVolumeState(payload)
	if !ok {
		return
	}
	c.volumeMutex.Lock()
	moved := !c.haveVolume || state != c.volume
	c.volume = state
	c.haveVolume = true
	signal := c.volumeCaughtUp
	c.volumeCaughtUp = true
	owned, owner := c.volumeOwned, c.volumeOwner
	c.volumeMutex.Unlock()
	line := ""
	if signal && moved {
		line = fmt.Sprintf("command: %s delivered %s", c.volumeTopic, describeVolume(state))
	}
	// While the mark stands the level is the equipment's. The state is
	// recorded for the next press, and mpv is left at unity with no
	// indicator drawn.
	//
	// Every message re-asserts unity instead of setting it once, so no
	// earlier write leaves mpv below unity while the mark stands.
	if owned {
		c.applyVolumeState(defaultVolumeState(), "")
		if line != "" {
			logLine(c.log, "%s, applied by %s, mpv stays at unity", line, owner)
		}
		return
	}
	c.applyVolumeState(state, line)
	// Only a level that moved draws the indicator. A message that repeats
	// the held level is a restore, such as this pod's own republish after
	// a reconnect, and a person changed nothing the screen should show.
	if signal && moved {
		c.command(volumeChangedCommand())
	}
}

// applyVolumeState writes one state to mpv. With a line, the two
// commands are one request, and the line logs when mpv has answered
// both; with none, they go out as the sidecar's own writes.
func (c *commander) applyVolumeState(state volumeState, line string) {
	commands := volumeCommands(state)
	if line != "" {
		c.request(line, commands...)
		return
	}
	for _, command := range commands {
		c.command(command)
	}
}

// describeVolume names one state the way every volume line does.
func describeVolume(state volumeState) string {
	if state.Muted {
		return fmt.Sprintf("level %d, muted", state.Level)
	}
	return fmt.Sprintf("level %d, not muted", state.Level)
}

// volumeOwnerName reads the owner a mark names. Nothing else reads the
// value, so a mark that does not decode names itself.
func volumeOwnerName(payload []byte) string {
	var mark struct {
		Owner string `json:"owner"`
	}
	if err := json.Unmarshal(payload, &mark); err != nil || mark.Owner == "" {
		return string(payload)
	}
	return mark.Owner
}

// applyVolumeOwner folds one message off the owner topic. A non-empty
// payload hands the level to equipment, and mpv goes to unity. An
// empty payload is the mark cleared, and mpv takes the level back at
// the state the topic last delivered.
//
// Every non-empty mark writes unity, a redelivered one included, so a
// mark that still stands is enough on its own to put mpv back at unity.
// A mark that changes hands the level to someone new, so it earns a
// line; a redelivered mark writes none.
func (c *commander) applyVolumeOwner(payload []byte) {
	owned := len(payload) > 0
	owner := ""
	if owned {
		owner = volumeOwnerName(payload)
	}
	c.volumeMutex.Lock()
	changed := owned != c.volumeOwned || owner != c.volumeOwner
	c.volumeOwned, c.volumeOwner = owned, owner
	apply, state := true, defaultVolumeState()
	if !owned {
		apply, state = c.haveVolume, c.volume
	}
	c.volumeMutex.Unlock()
	switch {
	case changed && owned:
		logLine(c.log, "command: %s names %s, so %s applies the level and mpv stays at unity",
			c.volumeOwnerTopic, owner, owner)
	case changed && apply:
		logLine(c.log, "command: %s cleared, so mpv applies %s", c.volumeOwnerTopic, describeVolume(state))
	case changed:
		logLine(c.log, "command: %s cleared, and no level has arrived yet", c.volumeOwnerTopic)
	}
	if !apply {
		return
	}
	c.applyVolumeState(state, "")
}

// pressVolume publishes what a press means, retained, and writes
// no level to mpv. It computes from the last message the topic
// delivered, or from unity before any message arrives, so a pod
// that just started still steps from a definite level.
//
// The press's line says what it published and where. The level reaches
// mpv, or the owner, when the topic delivers it back, and that message
// has a line of its own.
func (c *commander) pressVolume(trigger string, command mediaCommand, quiet bool) {
	if c.volumeTopic == "" {
		if !quiet {
			logLine(c.log, "command: %s ignored, because the player has no sinks", trigger)
		}
		return
	}
	held := c.heldVolume()
	next := nextVolume(held, command)
	// A press that moves nothing, a step up at the cap, sends nothing.
	// The topic already holds the level, so the press draws the
	// indicator here as its feedback, unless equipment owns the level.
	if next == held {
		c.volumeMutex.Lock()
		owned := c.volumeOwned
		c.volumeMutex.Unlock()
		if !owned {
			c.command(volumeChangedCommand())
		}
		if !quiet {
			logLine(c.log, "command: %s: %s, published nothing, because the level is already %s",
				trigger, describeCommand(command), describeVolume(held))
		}
		return
	}
	payload, err := marshalVolumeState(next)
	if err != nil {
		fmt.Fprintf(os.Stderr, "command: volume: %v\n", err)
		return
	}
	c.bus.Publish(c.volumeTopic, payload, true)
	if !quiet {
		logLine(c.log, "command: %s: %s, published %s to %s",
			trigger, describeCommand(command), describeVolume(next), c.volumeTopic)
	}
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
	c.applyVolumeState(state, "")
}
