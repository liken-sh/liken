package main

// These tests cover what the command sidecar sends for the level when
// nothing a person did changed it: a reconnect restores the level the pod
// holds and draws nothing, a first session publishes no level it did not
// read, and a press that moves nothing publishes nothing.

import (
	"net"
	"testing"
	"time"
)

// volumeCommander is a sidecar on a live bus with mpv on a pipe the test
// reads, holding no level yet.
func volumeCommander(t *testing.T) (*commander, *fakeBroker, *Bus, <-chan string) {
	t.Helper()
	bus, brokers, connected := startBus(t, 1, nil, nil)
	waitForConnect(t, connected)
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close() })
	c := &commander{
		availabilityTopic: playAvailabilityTopic(defaultTopicBase, "house", "movie"),
		commandsTopic:     playCommandsTopic(defaultTopicBase, "house", "movie"),
		volumeTopic:       playerVolumeTopic(defaultTopicBase, "house", "theater"),
		bus:               bus,
		mpv:               client,
	}
	return c, brokers[0], bus, readAsync(server)
}

// A broker that restarts holds no level. The sidecar holds the level the
// room hears, so its reconnect publishes that level again, retained, and
// the operator finds a level and seeds nothing over the film.
func TestAReconnectRepublishesTheHeldLevel(t *testing.T) {
	c, broker, bus, lines := volumeCommander(t)
	c.handle(c.volumeTopic, []byte(`{"level":30,"muted":false}`))
	waitForLine(t, lines)
	waitForLine(t, lines)

	c.onConnect(bus)

	mustMatch(t, string(waitForPublish(t, broker.pubs).payload), availabilityOnline)
	restored := waitForPublish(t, broker.pubs)
	mustMatch(t, restored.topic, c.volumeTopic)
	mustMatch(t, restored.retained, true)
	mustMatch(t, string(restored.payload), `{"level":30,"muted":false}`)
}

// A first session holds no level, so the sidecar publishes none: a pod
// that starts writes nothing it did not read.
func TestAFirstSessionPublishesNoLevel(t *testing.T) {
	c, broker, bus, _ := volumeCommander(t)

	c.onConnect(bus)

	mustMatch(t, string(waitForPublish(t, broker.pubs).payload), availabilityOnline)
	mustPublishNothing(t, broker)
}

// The broker delivers the restored level back, once as the catch-up and
// once as the echo of the republish. Neither moved the level, so mpv
// draws no indicator. A level that moves still draws one.
func TestTheRestoredLevelDrawsNoIndicator(t *testing.T) {
	c, _, bus, lines := volumeCommander(t)
	c.handle(c.volumeTopic, []byte(`{"level":30,"muted":false}`))
	waitForLine(t, lines)
	waitForLine(t, lines)
	c.onConnect(bus)

	for range 2 {
		c.handle(c.volumeTopic, []byte(`{"level":30,"muted":false}`))
		mustMatch(t, waitForLine(t, lines), `{"command":["no-osd","set","volume","30"]}`)
		mustMatch(t, waitForLine(t, lines), `{"command":["no-osd","set","mute","no"]}`)
	}
	mustNoLine(t, lines, 100*time.Millisecond)

	c.handle(c.volumeTopic, []byte(`{"level":35,"muted":false}`))
	waitForLine(t, lines)
	waitForLine(t, lines)
	mustMatch(t, waitForLine(t, lines), `{"command":["script-message","volume-changed"]}`)
}

// A step up at the cap moves nothing, so it publishes nothing. The press
// still draws the indicator, which is its feedback.
func TestAPressThatMovesNothingPublishesNothing(t *testing.T) {
	c, broker, _, lines := volumeCommander(t)
	c.volume, c.haveVolume = volumeState{Level: 100}, true

	c.handle(c.commandsTopic, mustEncode(t, mediaCommand{Action: actionVolume, Amount: 5}))

	mustMatch(t, waitForLine(t, lines), `{"command":["script-message","volume-changed"]}`)
	mustPublishNothing(t, broker)
}

// While equipment owns the level, the press at the cap draws nothing on
// the film either, because mpv sits at unity and the level is the
// receiver's.
func TestAPressThatMovesNothingOnAnOwnedUnitDrawsNothing(t *testing.T) {
	c, broker, _, lines := volumeCommander(t)
	c.volume, c.haveVolume, c.volumeOwned = volumeState{Level: 100}, true, true

	c.handle(c.commandsTopic, mustEncode(t, mediaCommand{Action: actionVolume, Amount: 5}))

	mustNoLine(t, lines, 100*time.Millisecond)
	mustPublishNothing(t, broker)
}
