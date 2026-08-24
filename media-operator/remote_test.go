package main

import (
	"encoding/json"
	"testing"
)

// The reader keeps the events a Keymap can bind off a busy topic. Every
// EV_KEY event goes out, the two hat axes go out, and everything else,
// the analog sticks and the EV_SYN and EV_MSC frames, stays off the
// wire.
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
		presenceTopic:     remotePresenceTopic(defaultTopicBase, "house", "sofa"),
		availabilityTopic: remoteAvailabilityTopic(defaultTopicBase, "house", "sofa"),
	}, brokers[0]
}

// The controller's nodes opening and vanishing are the two edges of
// presence, and each publishes the retained flag the operator folds.
func TestTheReaderPublishesEachPresenceEdgeRetained(t *testing.T) {
	cases := []struct {
		name      string
		connected bool
	}{
		{name: "the controller's nodes open", connected: true},
		{name: "the node batch ends", connected: false},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			r, broker := testReader(t)

			r.publishPresence(each.connected)

			published := waitForPublish(t, broker.pubs)
			mustMatch(t, published.topic, remotePresenceTopic(defaultTopicBase, "house", "sofa"))
			mustMatch(t, published.retained, true)
			var presence remotePresence
			mustSucceed(t, json.Unmarshal(published.payload, &presence))
			mustMatch(t, presence.Connected, each.connected)
		})
	}
}

// The Bus remembers subscriptions across a reconnect but not publishes, so
// a fresh session republishes the availability and the presence the pod
// last held. A pod whose controller is connected says so again, and the
// operator's fold survives a broker restart.
func TestTheReaderRepublishesItsRetainedStateOnConnect(t *testing.T) {
	r, broker := testReader(t)
	r.publishPresence(true)
	waitForPublish(t, broker.pubs)

	r.onConnect(r.bus)

	availability := waitForPublish(t, broker.pubs)
	mustMatch(t, availability.topic, remoteAvailabilityTopic(defaultTopicBase, "house", "sofa"))
	mustMatch(t, string(availability.payload), availabilityOnline)
	mustMatch(t, availability.retained, true)

	presence := waitForPublish(t, broker.pubs)
	mustMatch(t, presence.topic, remotePresenceTopic(defaultTopicBase, "house", "sofa"))
	mustMatch(t, string(presence.payload), `{"connected":true}`)
}
