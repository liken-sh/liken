package main

// The sidecar's half of the up-next offer the display draws. The
// operator writes the Play's next block into the pod, the sidecar sends
// it to the display, and a select on the offer becomes one message on
// the Player's commands topic, where the program that wrote the Play
// reads its own request back.

import (
	"encoding/json"
	"fmt"
	"os"
)

// playNextCommand is the message the sidecar publishes for a select on
// the offer. The action is the vocabulary word, and the request is the
// block's own request, byte for byte, because this operator never reads
// it.
type playNextCommand struct {
	Action  string          `json:"action"`
	Request json.RawMessage `json:"request,omitempty"`
}

// parseNext reads the block the operator wrote into the pod. A value
// that is not one JSON object gives no block, so a pod that reads a value
// it cannot use offers nothing and forwards nothing.
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

// sendNext sends the display the block as one string argument, the way
// a presentation block is sent. A Play with no next block sends nothing.
func (c *commander) sendNext() {
	if len(c.next) == 0 {
		return
	}
	c.command(nextCommand(c.next))
}

// publishNext answers the display's select with one message on the
// Player's commands topic. It is not retained, because the ask is an
// event. A sidecar with no block, or one that read no Player, publishes
// nothing.
func (c *commander) publishNext() {
	if len(c.next) == 0 || c.playerCommandsTopic == "" || c.bus == nil {
		return
	}
	payload, err := json.Marshal(playNextCommand{Action: actionPlayNext, Request: nextOf(c.next).Request})
	if err != nil {
		fmt.Fprintf(os.Stderr, "command: next: %v\n", err)
		return
	}
	c.bus.Publish(c.playerCommandsTopic, payload, false)
}

// nextOf reads the block's fields. An absent block, or one that does not
// decode, reads as a block with nothing in it.
func nextOf(block json.RawMessage) PlayNext {
	var next PlayNext
	if err := json.Unmarshal(block, &next); err != nil {
		return PlayNext{}
	}
	return next
}
