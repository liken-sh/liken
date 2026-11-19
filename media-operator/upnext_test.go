package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

// nextBlockValue returns the JSON that the operator sets in MEDIA_NEXT.
func nextBlockValue(art string) string {
	block, _ := json.Marshal(PlayNext{
		Reason:  "Next in Harbor Lights",
		Title:   "E05",
		Art:     art,
		Request: json.RawMessage(`{"library":"living-room/shows"}`),
	})
	return string(block)
}

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
		{name: "a JSON null", value: "null", want: ""},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, string(parseNext(one.value)), one.want)
		})
	}
}

func TestTheSidecarSendsTheNextBlockAtTheStartAndOnAReplay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, lines := bridgeToMPV(t)
		c.next = json.RawMessage(`{"title":"E05"}`)
		c.presentations = []json.RawMessage{json.RawMessage(`{"title":"First"}`)}

		changes := make(chan propertyChange, 8)
		go feedChanges(changes, changeOf("playlist-pos", "0"), changeOf("playlist-pos", "1"))
		runReporter(t.Context(), changes, func(playReport) error { return nil }, c.present, nil, io.Discard)

		mustMatch(t, waitForLine(t, lines), `{"command":["script-message","presentation","{\"title\":\"First\"}"]}`)
		mustMatch(t, waitForLine(t, lines), `{"command":["script-message","next","{\"title\":\"E05\"}"]}`)
		mustMatch(t, waitForLine(t, lines), `{"command":["script-message","presentation","{}"]}`)

		c.serveMessage([]string{presentationRequestMessage})
		mustMatch(t, waitForLine(t, lines), `{"command":["script-message","presentation","{}"]}`)
		mustMatch(t, waitForLine(t, lines), `{"command":["script-message","next","{\"title\":\"E05\"}"]}`)
	})
}

func TestASidecarWithNoNextBlockSendsNone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, lines := bridgeToMPV(t)
		c.item = 1

		c.serveMessage([]string{presentationRequestMessage})

		mustMatch(t, waitForLine(t, lines), `{"command":["script-message","presentation","{}"]}`)
		mustNoLine(t, lines)
	})
}

func TestSelectingTheOfferPublishesOnThePlayersCommandsTopic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
	})
}

func TestSelectingTheOfferPublishesNothingWithoutABlockOrAPlayer(t *testing.T) {
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
			synctest.Test(t, func(t *testing.T) {
				bus, brokers, connected := startBus(t, 1, nil, nil)
				waitForConnect(t, connected)
				c := &commander{bus: bus, playerCommandsTopic: one.topic, next: one.block}

				c.serveMessage([]string{nextRequestMessage})

				mustPublishNothing(t, brokers[0])
			})
		})
	}
}

func TestTheTerminationSignalPublishesTheEndingBeforeTheQuitReachesMpv(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
			t.Fatal("the run did not end within one second of the signal")
		}
	})
}

// After the run ends, the signal handler publishes nothing and ends
// nothing.
func TestTheSignalHandlerDoesNothingAfterTheRunEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
	})
}
