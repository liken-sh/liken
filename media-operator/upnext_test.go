package main

// The sidecar's up-next half: the block it sends the display, the art it
// decodes for the card, and the ask it publishes on the Player's commands
// topic.

import (
	"context"
	"encoding/json"
	"image/color"
	"net"
	"os"
	"syscall"
	"testing"
	"time"
)

// The block the operator writes into MEDIA_NEXT, with the art at its
// in-pod path.
func nextBlockValue(art string) string {
	block, _ := json.Marshal(PlayNext{
		Reason:  "Next in Harbor Lights",
		Title:   "E05",
		Art:     art,
		Request: json.RawMessage(`{"library":"living-room/shows"}`),
	})
	return string(block)
}

// A value that is not one JSON object gives the sidecar no block, so it
// offers nothing and forwards nothing.
func TestParseNextReadsOneObject(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{name: "a block", value: `{"title":"E05"}`, want: `{"title":"E05"}`},
		{name: "nothing at all", value: "", want: ""},
		{name: "a value that does not decode", value: "not json", want: ""},
		{name: "a value that is not an object", value: `["E05"]`, want: ""},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, string(parseNext(one.value)), one.want)
		})
	}
}

// The sidecar sends the display the block when the playlist reaches its
// first item, after the presentation, and again on every replay the
// display asks for.
func TestTheSidecarSendsTheNextBlockAtTheStartAndOnAReplay(t *testing.T) {
	c, lines := bridgeToMPV(t)
	c.next = json.RawMessage(`{"title":"E05"}`)
	c.presentations = []json.RawMessage{json.RawMessage(`{"title":"First"}`)}

	changes := make(chan propertyChange, 8)
	go feedChanges(changes, changeOf("playlist-pos", "0"), changeOf("playlist-pos", "1"))
	runReporter(t.Context(), changes, func(playReport) error { return nil }, c.present, func(json.RawMessage) {})

	mustMatch(t, waitForLine(t, lines), `{"command":["script-message-to","display","presentation","{\"title\":\"First\"}"]}`)
	mustMatch(t, waitForLine(t, lines), `{"command":["script-message-to","display","next","{\"title\":\"E05\"}"]}`)
	mustMatch(t, waitForLine(t, lines), `{"command":["script-message-to","display","presentation","{}"]}`)

	c.serveMessage([]string{presentationRequestMessage})
	mustMatch(t, waitForLine(t, lines), `{"command":["script-message-to","display","presentation","{}"]}`)
	mustMatch(t, waitForLine(t, lines), `{"command":["script-message-to","display","next","{\"title\":\"E05\"}"]}`)
}

// A Play with no next block sends the display nothing after the
// presentation, so the display offers nothing.
func TestASidecarWithNoNextBlockSendsNone(t *testing.T) {
	c, lines := bridgeToMPV(t)
	c.artItem = 1

	c.serveMessage([]string{presentationRequestMessage})

	mustMatch(t, waitForLine(t, lines), `{"command":["script-message-to","display","presentation","{}"]}`)
	expectNoArtReply(t, lines)
}

// The display's ask becomes one message on the Player's commands topic,
// not retained, with the block's own request.
func TestTheAskPublishesOnThePlayersCommandsTopic(t *testing.T) {
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	c := &commander{
		bus:                 bus,
		playerCommandsTopic: playerCommandsTopic(defaultTopicBase, "house", "theater"),
		next:                json.RawMessage(nextBlockValue("/media/1/next.jpg")),
	}

	c.serveMessage([]string{nextRequestMessage})

	published := waitForPublish(t, brokers[0].pubs)
	mustMatch(t, published.topic, c.playerCommandsTopic)
	mustMatch(t, published.retained, false)
	mustMatch(t, string(published.payload), `{"action":"play-next","request":{"library":"living-room/shows"}}`)
}

// A sidecar with no next block, or one that read no Player, publishes
// nothing for the ask.
func TestTheAskPublishesNothingWithoutABlockOrAPlayer(t *testing.T) {
	cases := []struct {
		name  string
		block json.RawMessage
		topic string
	}{
		{name: "no block", topic: playerCommandsTopic(defaultTopicBase, "house", "theater")},
		{name: "no player", block: json.RawMessage(nextBlockValue(""))},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			bus, brokers, connected := startBus(t, 1, nil, nil)
			waitForConnect(t, connected)
			c := &commander{bus: bus, playerCommandsTopic: one.topic, next: one.block}

			c.serveMessage([]string{nextRequestMessage})

			mustPublishNothing(t, brokers[0])
		})
	}
}

// The card's art decodes the way a logo does: the bridge fits the image
// inside the box the display asked for, keeps its ratio, and answers with
// the file it wrote and that file's size.
func TestServeArtDecodesTheNextBlocksArt(t *testing.T) {
	c, lines := bridgeToMPV(t)
	c.next = json.RawMessage(nextBlockValue(writeLogo(t, t.TempDir(), "next.png", color.NRGBA{R: 10, G: 200, B: 30, A: 255})))

	go c.serveArt([]string{artRequestMessage, artKindNext, "20", "20"})

	kind, path, w, h, stride := parseLogoReply(t, waitForLine(t, lines))
	mustMatch(t, kind, artKindNext)
	mustMatch(t, w, 20)
	mustMatch(t, h, 10)
	mustMatch(t, stride, 80)
	mustExist(t, path)
}

// A request the bridge has no art for gets no answer, the way a missing
// logo does, and the display draws its lines alone.
func TestServeArtAnswersNothingWithoutNextArt(t *testing.T) {
	cases := []struct {
		name  string
		block json.RawMessage
	}{
		{name: "no block at all"},
		{name: "a block with no art", block: json.RawMessage(`{"title":"E05"}`)},
		{name: "art the bridge cannot read", block: json.RawMessage(`{"art":"/nowhere/next.jpg"}`)},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			c, lines := bridgeToMPV(t)
			c.next = one.block

			c.serveArt([]string{artRequestMessage, artKindNext, "20", "20"})

			expectNoArtReply(t, lines)
		})
	}
}

// The pod's termination signal runs the same ending the exit press runs:
// the report with the ended mark reaches the bus first, and only then
// does quit reach mpv.
func TestTheTerminationSignalPublishesTheEndingBeforeTheQuitReachesMpv(t *testing.T) {
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close() })

	c := &commander{
		statusTopic: playStatusTopic(defaultTopicBase, "house", "movie"),
		bus:         bus,
		mpv:         client,
		lastReport:  playReport{Item: 1, Position: "0:20:00"},
		haveReport:  true,
	}

	signals := make(chan os.Signal, 1)
	stopped := make(chan struct{})
	go c.exitOnSignal(t.Context(), signals, func() { close(stopped) })
	signals <- syscall.SIGTERM

	ending := waitForPublish(t, brokers[0].pubs)
	mustMatch(t, ending.topic, c.statusTopic)
	mustMatch(t, endedReport(t, ending.payload), playReport{Item: 1, Position: "0:20:00", Ended: true})
	mustMatchAll(t, readLines(t, server, 1), []string{`{"command":["quit","0"]}`})

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("the run never ended")
	}
}

// A run that ends on its own leaves the signal handler nothing to do, so
// the ending publishes once and no second quit reaches mpv.
func TestARunThatEndsFirstLeavesTheSignalHandlerNothing(t *testing.T) {
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)

	c := &commander{
		statusTopic: playStatusTopic(defaultTopicBase, "house", "movie"),
		bus:         bus,
		lastReport:  playReport{Item: 1},
		haveReport:  true,
	}

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.exitOnSignal(ctx, make(chan os.Signal), func() { t.Error("the handler ended a run that had ended") })
	}()
	stop()
	<-done

	mustPublishNothing(t, brokers[0])
}
