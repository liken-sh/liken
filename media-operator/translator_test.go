package main

// These tests cover the translator sidecar: a controller event plus a
// retained keymap becomes a named command on the commands topic, a
// binding that repeats re-publishes until the release, an event before
// any keymap matches nothing, and a focus message is inert for now.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// testTranslator wires a translator with a bus to a fake broker, so a
// test reads what it publishes to the commands topic.
func testTranslator(t *testing.T) (*translator, *fakeBroker) {
	t.Helper()
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	return &translator{
		commandsTopic: playCommandsTopic(defaultTopicBase, "house", "movie"),
		eventsTopic:   remoteEventsTopic(defaultTopicBase, "house", "sofa"),
		keymapTopicID: keymapTopic(defaultTopicBase, "gamepad"),
		focusTopicID:  remoteFocusTopic(defaultTopicBase, "house", "sofa"),
		bus:           bus,
		repeatCtx:     context.Background(),
		repeats:       map[uint16]context.CancelFunc{},
	}, brokers[0]
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

// A controller event, matched against the retained keymap, becomes a
// named command on the commands topic, not retained.
func TestATranslatorTurnsAnEventIntoACommand(t *testing.T) {
	tr, broker := testTranslator(t)
	tr.handle(tr.keymapTopicID, mustJSON(t,
		[]compiledBinding{{EventType: evKey, Code: 0x130, Value: 1, Action: actionPause}}))

	tr.handle(tr.eventsTopic, mustJSON(t, remoteEvent{Type: evKey, Code: 0x130, Value: 1}))

	published := waitForPublish(t, broker.pubs)
	if published.topic != tr.commandsTopic {
		t.Errorf("topic = %q, want %q", published.topic, tr.commandsTopic)
	}
	if published.retained {
		t.Error("a command was retained; a command is an event and not a state")
	}
	var command mediaCommand
	if err := json.Unmarshal(published.payload, &command); err != nil {
		t.Fatal(err)
	}
	if command.Action != actionPause {
		t.Errorf("action = %q, want pause", command.Action)
	}
}

// A held control re-publishes its command until the release, and the
// release stops it.
func TestARepeatRepublishesUntilTheReleaseStopsIt(t *testing.T) {
	tr, broker := testTranslator(t)
	tr.handle(tr.keymapTopicID, mustJSON(t, []compiledBinding{
		{EventType: evKey, Code: 0x137, Value: 1, Action: actionSeek, Amount: 10, RepeatDelay: 1, RepeatInterval: 5},
	}))

	var mu sync.Mutex
	count := 0
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-broker.pubs:
				mu.Lock()
				count++
				mu.Unlock()
			case <-done:
				return
			}
		}
	}()
	total := func() int {
		mu.Lock()
		defer mu.Unlock()
		return count
	}

	tr.handle(tr.eventsTopic, mustJSON(t, remoteEvent{Type: evKey, Code: 0x137, Value: 1}))
	time.Sleep(50 * time.Millisecond)
	tr.handle(tr.eventsTopic, mustJSON(t, remoteEvent{Type: evKey, Code: 0x137, Value: 0}))
	time.Sleep(20 * time.Millisecond)

	held := total()
	if held < 3 {
		t.Fatalf("the repeat published %d times while held, want several", held)
	}
	if _, present := tr.repeats[0x137]; present {
		t.Error("the release left the code in the repeats map")
	}

	time.Sleep(30 * time.Millisecond)
	after := total()
	close(done)
	if after != held {
		t.Errorf("the repeat kept publishing after the release: %d then %d", held, after)
	}
}

// Before any keymap arrives, the table is empty and an event matches
// nothing.
func TestATranslatorMatchesNothingBeforeAKeymapArrives(t *testing.T) {
	tr, broker := testTranslator(t)

	tr.handle(tr.eventsTopic, mustJSON(t, remoteEvent{Type: evKey, Code: 0x130, Value: 1}))

	select {
	case got := <-broker.pubs:
		t.Fatalf("an event before any keymap published %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

// A focus message publishes nothing, and for now the translator acts on
// every press whatever the focus mark says.
func TestAFocusMessageIsInert(t *testing.T) {
	tr, broker := testTranslator(t)
	tr.handle(tr.keymapTopicID, mustJSON(t,
		[]compiledBinding{{EventType: evKey, Code: 0x130, Value: 1, Action: actionPause}}))

	tr.handle(tr.focusTopicID, []byte(`{"binding":"other"}`))
	select {
	case got := <-broker.pubs:
		t.Fatalf("a focus message published %+v", got)
	case <-time.After(50 * time.Millisecond):
	}

	tr.handle(tr.eventsTopic, mustJSON(t, remoteEvent{Type: evKey, Code: 0x130, Value: 1}))
	published := waitForPublish(t, broker.pubs)
	var command mediaCommand
	if err := json.Unmarshal(published.payload, &command); err != nil {
		t.Fatal(err)
	}
	if command.Action != actionPause {
		t.Errorf("action = %q, want pause despite the focus mark", command.Action)
	}
}
