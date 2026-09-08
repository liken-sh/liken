package main

// These tests cover the bridge's drain: the message loop serves the newest
// tile request in the queue, drops the ones it overtook, and still serves
// every other message it passed.

import (
	"encoding/json"
	"image/color"
	"strconv"
	"testing"
	"time"
)

// A hold fills the queue with tile requests, and the bridge crops the newest
// one alone: the three requests name three cells, and the one reply carries
// the third cell's color. The exit press queued between them is served once,
// because a press is not a tile.
func TestTheDrainServesTheLastTrickplayRequestAndTheExitPress(t *testing.T) {
	root := writeSheet(t, map[int]color.RGBA{
		0: {R: 200, A: 255},
		1: {G: 200, A: 255},
		2: {B: 200, A: 255},
		3: {R: 200, G: 200, A: 255},
	})
	t.Setenv(trickplayIntervalVariable, "10s")

	c, lines := bridgeToMPV(t)
	c.artItem = 1
	c.presentations = []json.RawMessage{trickBlock(root)}

	messages := make(chan clientMessage, 4)
	messages <- clientMessage{Args: trickplayArgs(5_000)}
	messages <- clientMessage{Args: []string{exitMessage}}
	messages <- clientMessage{Args: trickplayArgs(15_000)}
	messages <- clientMessage{Args: trickplayArgs(25_000)}
	close(messages)

	go c.serveMessages(messages)

	mustMatch(t, waitForLine(t, lines), `{"command":["quit","0"]}`)

	path, w, h, stride := parseArtReply(t, waitForLine(t, lines))
	b, g, r := centerPixel(t, path, w, h, stride)
	if !(b > 150 && r < 60 && g < 60) {
		t.Fatalf("center pixel bgra = (%d,%d,%d), want the blue cell 2", b, g, r)
	}

	mustNoLine(t, lines, 100*time.Millisecond)
}

// The drain drops nothing but tile requests. A logo request queued between
// two of them is answered first, in the order it arrived, and the newest tile
// follows it.
func TestTheDrainServesALogoRequestBetweenTwoTiles(t *testing.T) {
	root := writeSheet(t, map[int]color.RGBA{
		0: {R: 200, A: 255},
		1: {G: 200, A: 255},
		2: {B: 200, A: 255},
		3: {R: 200, G: 200, A: 255},
	})
	logo := writeLogo(t, t.TempDir(), "logo.png", color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	t.Setenv(trickplayIntervalVariable, "10s")

	c, lines := bridgeToMPV(t)
	c.artItem = 1
	c.presentations = []json.RawMessage{blockWithLogo(t, root, logo)}

	messages := make(chan clientMessage, 3)
	messages <- clientMessage{Args: trickplayArgs(5_000)}
	messages <- clientMessage{Args: []string{artRequestMessage, artKindLogo, "16", "16"}}
	messages <- clientMessage{Args: trickplayArgs(25_000)}
	close(messages)

	go c.serveMessages(messages)

	mustMatch(t, artReplyKind(t, waitForLine(t, lines)), artKindLogo)

	path, w, h, stride := parseArtReply(t, waitForLine(t, lines))
	b, g, r := centerPixel(t, path, w, h, stride)
	if !(b > 150 && r < 60 && g < 60) {
		t.Fatalf("center pixel bgra = (%d,%d,%d), want the blue cell 2", b, g, r)
	}

	mustNoLine(t, lines, 100*time.Millisecond)
}

// The drain reads only what is queued. The channel stays open here, so a
// drain that waited for one more message would never return.
func TestTheDrainReadsOnlyWhatIsQueued(t *testing.T) {
	messages := make(chan clientMessage, 4)
	messages <- clientMessage{Args: []string{presentationRequestMessage}}
	messages <- clientMessage{Args: trickplayArgs(15_000)}

	last, passed := drainTrickplay(trickplayArgs(5_000), messages)

	mustMatchAll(t, last, trickplayArgs(15_000))
	mustMatch(t, len(passed), 1)
	mustMatchAll(t, passed[0], []string{presentationRequestMessage})
}

// Only a well-formed tile request counts as one. A request the bridge cannot
// read is served in order like any other message, and parseArtRequest drops it
// there.
func TestIsTrickplayRequest(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{name: "a tile request", args: trickplayArgs(5_000), want: true},
		{name: "a logo request", args: []string{artRequestMessage, artKindLogo, "16", "16"}},
		{name: "the exit press", args: []string{exitMessage}},
		{name: "a tile request with no box", args: []string{artRequestMessage, artKindTrickplay, "5000"}},
		{name: "another script's broadcast", args: []string{"other", artKindTrickplay, "5000", "16", "16"}},
		{name: "nothing", args: nil},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatch(t, isTrickplayRequest(each.args), each.want)
		})
	}
}

// trickplayArgs is one tile request for a time, the shape the display
// broadcasts it in.
func trickplayArgs(timeMs int) []string {
	return []string{artRequestMessage, artKindTrickplay, strconv.Itoa(timeMs), "24", "24"}
}

// blockWithLogo is one presentation block that carries both a trickplay
// directory and a logo, so one item answers both requests.
func blockWithLogo(t *testing.T, dir, logo string) json.RawMessage {
	t.Helper()
	block, err := json.Marshal(Presentation{Trickplay: dir, Logo: logo})
	mustSucceed(t, err)
	return block
}

// artReplyKind reads the kind out of one liken-art reply.
func artReplyKind(t *testing.T, line string) string {
	t.Helper()
	var command mpvCommand
	mustSucceed(t, json.Unmarshal([]byte(line), &command))
	if len(command.Command) != 8 || command.Command[2] != artReplyMessage {
		t.Fatalf("reply = %q, want a liken-art message", line)
	}
	return command.Command[3].(string)
}
