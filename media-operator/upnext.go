package main

// The sidecar's half of the up-next offer the display draws. The
// operator writes the Play's next block into the pod, the sidecar sends
// it to the display and decodes its art, and a select on the offer
// becomes one message on the Player's commands topic, where the program
// that wrote the Play reads its own request back.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// serveNext decodes the card's art into the box the display asked for
// and replies the way the logo path does. A block with no art, or a
// decode that failed, gets no reply, and the display draws its lines
// alone.
func (c *commander) serveNext(request artRequest) {
	art := nextOf(c.next).Art
	if art == "" {
		return
	}
	blob, err := c.decodeNext(art, request.width, request.height)
	if err != nil {
		fmt.Fprintf(os.Stderr, "command: next art %q: %v\n", art, err)
		return
	}
	c.replyArt(request.kind, blob)
}

// decodeNext reads the art, fits it inside the box, and writes the bgra
// pixels to the shared volume. One block serves the whole run, so the
// file is named by the box alone, and an item swap does not remove it.
func (c *commander) decodeNext(art string, w, h int) (artBlob, error) {
	reader, err := openArt(art)
	if err != nil {
		return artBlob{}, err
	}
	defer reader.Close()

	pixels, outW, outH, stride, err := scaleToBGRA(reader, w, h)
	if err != nil {
		return artBlob{}, err
	}

	path := filepath.Join(c.artDir, fmt.Sprintf("next-%dx%d.bgra", w, h))
	if err := os.WriteFile(path, pixels, 0o644); err != nil {
		return artBlob{}, err
	}
	return artBlob{path: path, width: outW, height: outH, stride: stride}, nil
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
