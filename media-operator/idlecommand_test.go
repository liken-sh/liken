package main

// These tests cover the idle command pod: the bus identity it takes
// from its commands topic, and the one command it acts on.

import (
	"testing"
	"time"
)

// The sidecar's bus identity comes from its commands topic, so one
// identity per Player falls out and two idle command pods never collide.
func TestIdleCommandClientIDDerivesFromTheTopic(t *testing.T) {
	topic := playerCommandsTopic(defaultTopicBase, "house", "theater")
	mustMatch(t, idleCommandClientID(topic), "idle-command-liken-media-players-house-theater-commands")
}

// Only re-present and sleep act. Every other action, a press the pod
// forwarded and reads back included, and a payload that does not
// decode, leave the screen as it is rather than crashing the pod.
func TestIdleCommandHandleActsOnTwoActionsAlone(t *testing.T) {
	ic, watch := fadingCommander(t, 0, nil)
	sendActivity(t, ic, playerIdle)

	ic.handle(ic.commandsTopic, mustEncode(t, mediaCommand{Action: actionPause}))
	ic.handle(ic.commandsTopic, mustEncode(t, mediaCommand{Action: actionUp}))
	ic.handle(ic.commandsTopic, []byte("not json"))

	noMoment(t, watch, 100*time.Millisecond)
}

// A re-present states the present moment and nothing else. The moment
// names no controller, because only a focus does, and the client reads
// it as the request to map a fresh surface.
func TestIdleCommandARePresentStatesThePresentMoment(t *testing.T) {
	ic, watch := fadingCommander(t, 0, nil)
	sendActivity(t, ic, playerIdle)

	ic.handle(ic.commandsTopic, mustEncode(t, mediaCommand{Action: actionRePresent}))

	moment := nextMoment(t, watch)
	mustMatch(t, moment.Event, screenPresentEvent)
	if moment.Remote != nil {
		t.Errorf("the present named controller %d, and only a focus names one", *moment.Remote)
	}
	noMoment(t, watch, 100*time.Millisecond)
}

// A re-present that reaches the sidecar while a Play runs states
// nothing, so a stray one never maps the idle clock over a running film.
// It is the same gate back, volume, and cycle read.
func TestIdleCommandARePresentStatesNothingWhileAPlayRuns(t *testing.T) {
	ic, watch := fadingCommander(t, 0, nil)
	sendActivity(t, ic, playerPlaying)

	ic.handle(ic.commandsTopic, mustEncode(t, mediaCommand{Action: actionRePresent}))

	noMoment(t, watch, 100*time.Millisecond)
}

// An unset variable is no lines at all, not one empty line, and a
// blank line is kept, because the events list and the focus list stay
// aligned by position.
func TestTheIdlePodReadsItsTopicListsALinePerEntry(t *testing.T) {
	cases := []struct {
		name  string
		value string
		lines []string
	}{
		{name: "two lines", value: "first\nsecond", lines: []string{"first", "second"}},
		{name: "one line", value: "first", lines: []string{"first"}},
		{name: "a controller with no focus topic", value: "first\n", lines: []string{"first", ""}},
		{name: "the variable is unset", lines: nil},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatchAll(t, splitTopicLines(each.value), each.lines)
		})
	}
}

// The idle command pod states the desire it holds on every bus session, because
// the retained topic can hold the off desire of the process before it. A Player
// with no panel topic states no desire.
func TestPublishDesireStatesTheDesireTheSidecarHolds(t *testing.T) {
	cases := []struct {
		name    string
		topic   string
		desire  string
		payload string
	}{
		{
			name:    "the panel stands on a topic",
			topic:   playerPanelTopic(defaultTopicBase, "house", "theater"),
			desire:  panelDesireOff,
			payload: `{"desire":"off"}`,
		},
		{name: "the Player names no panel topic", desire: panelDesireOff},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			published := make(chan string, 2)
			ic := &idleCommander{
				panelTopic: each.topic,
				desire:     each.desire,
				publish: func(topic string, payload []byte, retained bool) {
					published <- topic + " " + string(payload)
				},
			}

			ic.publishDesire()

			if each.payload == "" {
				mustMatch(t, len(published), 0)
				return
			}
			mustMatch(t, <-published, each.topic+" "+each.payload)
		})
	}
}
