package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

// The gate stands before the fold. The reader folds every EV_KEY event
// and the two hat axes, and everything else, the analog sticks and the
// EV_SYN and EV_MSC frames, reaches the fold not at all.
func TestPublishableKeepsOnlyBindableEvents(t *testing.T) {
	cases := []struct {
		name  string
		event inputEvent
		want  bool
	}{
		{
			name:  "a button press",
			event: inputEvent{Type: evKey, Code: 0x130, Value: 1},
			want:  true,
		},
		{
			name:  "a button release",
			event: inputEvent{Type: evKey, Code: 0x130, Value: 0},
			want:  true,
		},
		{
			name:  "a hat axis",
			event: inputEvent{Type: evAbs, Code: 0x11, Value: -1},
			want:  true,
		},
		{
			name:  "an analog stick",
			event: inputEvent{Type: evAbs, Code: 0x00, Value: 128},
			want:  false,
		},
		{
			name:  "a sync frame",
			event: inputEvent{Type: 0x00, Code: 0x00, Value: 0},
			want:  false,
		},
		{
			name:  "a misc event",
			event: inputEvent{Type: 0x04, Code: 0x04, Value: 589825},
			want:  false,
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatch(t, publishable(each.event), each.want)
		})
	}
}

// testReader wires a reader to a fake broker, so a test reads what the
// standing pod publishes.
func testReader(t *testing.T) (*reader, *fakeBroker) {
	t.Helper()
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	return &reader{
		bus:               bus,
		eventsTopic:       remoteEventsTopic(defaultTopicBase, "house", "sofa"),
		availabilityTopic: remoteAvailabilityTopic(defaultTopicBase, "house", "sofa"),
		codesTopic:        remoteCodesTopic(defaultTopicBase, "house", "sofa"),
		keysTopic:         remoteKeysTopic(defaultTopicBase, "house", "sofa"),
		repeatCtx:         context.Background(),
		keys:              keyState{table: baseKeys},
	}, brokers[0]
}

// The Bus remembers subscriptions across a reconnect but not publishes, so
// a fresh session republishes the availability and the declared codes the
// pod last held. The operator's gap report survives a broker restart.
func TestTheReaderRepublishesItsRetainedStateOnConnect(t *testing.T) {
	r, broker := testReader(t)
	r.publishCodes(remoteCodes{Keys: []uint16{0x130}})
	waitForPublish(t, broker.pubs)

	r.onConnect(r.bus)

	availability := waitForPublish(t, broker.pubs)
	mustMatch(t, availability.topic, remoteAvailabilityTopic(defaultTopicBase, "house", "sofa"))
	mustMatch(t, string(availability.payload), availabilityOnline)
	mustMatch(t, availability.retained, true)

	codes := waitForPublish(t, broker.pubs)
	mustMatch(t, codes.topic, remoteCodesTopic(defaultTopicBase, "house", "sofa"))
	mustMatch(t, string(codes.payload), `{"keys":[304]}`)
	mustMatch(t, codes.retained, true)
}

// The declared codes are the union over the kept nodes, in code order
// and with no repeat.
func TestTheDeclaredCodesAreTheUnionOverTheKeptNodes(t *testing.T) {
	nodes := []openNode{
		{
			keys: bitmapOf(keyBitmapBytes, 0x131, 0x130),
			axes: bitmapOf(absBitmapBytes, 0x11),
		},
		{
			keys: bitmapOf(keyBitmapBytes, 0x130, 0x14a),
			axes: bitmapOf(absBitmapBytes, 0x00, 0x10),
		},
	}

	codes := declaredCodes(nodes)

	mustMatchAll(t, codes.Keys, []uint16{0x130, 0x131, 0x14a})
	mustMatchAll(t, codes.Axes, []uint16{0x10, 0x11})
}

// A node-open cycle logs one line per node, and a scan that finds the
// same picture logs nothing.
func TestTheReaderLogsAVerdictOnlyWhenThePictureChanges(t *testing.T) {
	var log bytes.Buffer
	r := &reader{log: &log}

	r.logVerdicts([]string{"event3 \"pad\" keep: 2 key codes, no hat axes"})
	first := log.String()
	r.logVerdicts([]string{"event3 \"pad\" keep: 2 key codes, no hat axes"})
	mustMatch(t, log.String(), first)

	r.logVerdicts([]string{"event4 \"pad\" reject: no key codes, no hat axes"})
	mustMatch(t, strings.Contains(log.String(), "event4"), true)
	mustMatch(t, strings.HasPrefix(first, "remote: event3"), true)
}

// The declared codes are a state, so the pod publishes them retained
// at every node open, clears them when the nodes vanish, and
// republishes whichever stands on a reconnect.
func TestTheReaderPublishesTheDeclaredCodesRetained(t *testing.T) {
	r, broker := testReader(t)

	r.publishCodes(remoteCodes{Keys: []uint16{0x130}, Axes: []uint16{0x10}})

	published := waitForPublish(t, broker.pubs)
	mustMatch(t, published.topic, remoteCodesTopic(defaultTopicBase, "house", "sofa"))
	mustMatch(t, published.retained, true)
	mustMatch(t, string(published.payload), `{"keys":[304],"axes":[16]}`)

	r.onConnect(r.bus)
	waitForPublish(t, broker.pubs)
	again := waitForPublish(t, broker.pubs)
	mustMatch(t, again.topic, remoteCodesTopic(defaultTopicBase, "house", "sofa"))
	mustMatch(t, string(again.payload), `{"keys":[304],"axes":[16]}`)

	r.clearCodes()
	cleared := waitForPublish(t, broker.pubs)
	mustMatch(t, cleared.topic, remoteCodesTopic(defaultTopicBase, "house", "sofa"))
	mustMatch(t, len(cleared.payload), 0)
	mustMatch(t, cleared.retained, true)
}

// A reader in discovery logs each raw event the way a Keymap names it
// and folds it all the same, so a controller a person maps still drives
// the unit it holds.
func TestADiscoveringReaderLogsEachEventAndPublishesItAnyway(t *testing.T) {
	r, broker := testReader(t)
	r.discovery = true
	var log bytes.Buffer
	r.log = &log

	read, write, err := os.Pipe()
	mustSucceed(t, err)
	_, err = write.Write(eventBytes(inputEvent{Type: evKey, Code: 0x130, Value: 1}))
	mustSucceed(t, err)
	mustSucceed(t, write.Close())

	r.readAndPublish(context.Background(), []openNode{
		{file: read, path: "/dev/input/event3", name: "Wireless Controller"},
	})

	published := waitForPublish(t, broker.pubs)
	mustMatch(t, published.topic, remoteEventsTopic(defaultTopicBase, "house", "sofa"))
	mustMatch(t, string(published.payload), `{"key":"KEY_ENTER","value":1}`)

	written := log.String()
	for _, want := range []string{
		`event3 "Wireless Controller"`, "EV_KEY (1)", "BTN_SOUTH (304)", "press (1)",
		"- press: BTN_SOUTH", "key: " + keymapKeyHint,
	} {
		mustMatch(t, strings.Contains(written, want), true)
	}
}

// Out of discovery the reader logs no event, because a healthy run's
// log is empty.
func TestAnOrdinaryReaderLogsNoEvent(t *testing.T) {
	r, broker := testReader(t)
	var log bytes.Buffer
	r.log = &log

	read, write, err := os.Pipe()
	mustSucceed(t, err)
	_, err = write.Write(eventBytes(inputEvent{Type: evKey, Code: 0x130, Value: 1}))
	mustSucceed(t, err)
	mustSucceed(t, write.Close())

	r.readAndPublish(context.Background(), []openNode{
		{file: read, path: "/dev/input/event3", name: "Wireless Controller"},
	})

	waitForPublish(t, broker.pubs)
	mustMatch(t, log.String(), "")
}
