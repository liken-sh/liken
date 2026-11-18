package main

// The player sidecar's part of the up-next offer that the display
// draws. The operator writes the Play's next block into the pod, the
// sidecar sends the block to the display, and when a person selects the
// offer, the sidecar publishes one message on the Player's commands
// topic. The program that created the Play reads its own request back
// from that message.

import (
	"encoding/json"
	"fmt"
	"os"
)

// playNextCommand is the message that the sidecar publishes when the
// offer is selected. Request is the block's own request, copied byte for
// byte, because the operator does not read it.
type playNextCommand struct {
	Action  string          `json:"action"`
	Request json.RawMessage `json:"request,omitempty"`
}

// parseNext returns the next block that the operator wrote into the
// pod, or nil when the value is empty or is not one JSON object. With no
// block, the sidecar offers nothing and publishes nothing.
func parseNext(value string) json.RawMessage {
	if value == "" {
		return nil
	}
	var block PlayNext
	if err := json.Unmarshal([]byte(value), &block); err != nil {
		return nil
	}
	return json.RawMessage(value)
}

// sendNext sends the next block to the display as one string argument,
// the way the presentation block is sent. A Play with no next block
// sends nothing.
func (c *commander) sendNext() {
	if len(c.next) == 0 {
		return
	}
	c.command(nextCommand(c.next))
}

// publishNext publishes one message on the Player's commands topic when
// the display reports that the offer was selected. The message is not
// retained, because a selection is an event, and a subscriber that
// connects later must not act on it. It publishes nothing when the Play
// has no next block or the pod has no Player topic or bus. Each outcome
// is logged, because the selection is a person's press.
func (c *commander) publishNext() {
	const trigger = "command: the display accepted the up-next offer"
	if len(c.next) == 0 {
		logLine(c.log, "%s, ignored, because the Play has no next block", trigger)
		return
	}
	if c.playerCommandsTopic == "" || c.bus == nil {
		logLine(c.log, "%s, ignored, because the pod has no Player commands topic to publish on", trigger)
		return
	}
	payload, err := json.Marshal(playNextCommand{Action: actionPlayNext, Request: nextOf(c.next).Request})
	if err != nil {
		fmt.Fprintf(os.Stderr, "command: next: %v\n", err)
		return
	}
	c.bus.Publish(c.playerCommandsTopic, payload, false)
	logLine(c.log, "%s, published %s to %s", trigger, actionPlayNext, c.playerCommandsTopic)
}

// nextOf decodes a next block. A block that does not decode gives an
// empty PlayNext.
func nextOf(block json.RawMessage) PlayNext {
	var next PlayNext
	if err := json.Unmarshal(block, &next); err != nil {
		return PlayNext{}
	}
	return next
}
