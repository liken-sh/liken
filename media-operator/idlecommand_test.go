package main

// These tests cover the idle command sidecar: the bus identity it takes
// from its commands topic, and the one command it acts on.

import (
	"testing"
	"time"
)

// The sidecar's bus identity comes from its commands topic, so one
// identity per Player falls out and two idle sidecars never collide.
func TestIdleCommandClientIDDerivesFromTheTopic(t *testing.T) {
	topic := playerCommandsTopic(defaultTopicBase, "house", "theater")
	mustMatch(t, idleCommandClientID(topic), "idle-command-liken-media-players-house-theater-commands")
}

// Any action other than re-present, and a payload that does not decode,
// state nothing, so a newer command on the topic leaves the screen as it
// is rather than crashing the sidecar.
func TestIdleCommandHandleActsOnlyOnRePresent(t *testing.T) {
	ic, watch := fadingCommander(t, 0, nil)

	ic.handle(ic.commandsTopic, mustEncode(t, mediaCommand{Action: actionPause}))
	ic.handle(ic.commandsTopic, []byte("not json"))

	noMoment(t, watch, 100*time.Millisecond)
}

// A re-present states the present moment and nothing else. The moment
// names no controller, because only a focus does, and the client reads
// it as the request to map a fresh surface.
func TestIdleCommandARePresentStatesThePresentMoment(t *testing.T) {
	ic, watch := fadingCommander(t, 0, nil)

	ic.handle(ic.commandsTopic, mustEncode(t, mediaCommand{Action: actionRePresent}))

	moment := nextMoment(t, watch)
	mustMatch(t, moment.Event, screenPresentEvent)
	if moment.Remote != nil {
		t.Errorf("the present named controller %d, and only a focus names one", *moment.Remote)
	}
	noMoment(t, watch, 100*time.Millisecond)
}
