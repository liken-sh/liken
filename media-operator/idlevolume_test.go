package main

// These tests cover the idle sidecar's half of the level: the level it
// holds so a press has something to step from, the press it publishes,
// the two states it does not press in, and the speaker gate that turns
// the whole path off.

import (
	"testing"
	"time"
)

// volumeCommander builds one idle sidecar that holds a volume topic, with
// no fade window so nothing but the test drives it.
func volumeCommander(t *testing.T, remotes map[string]string) (*idleCommander, *idleWatch) {
	t.Helper()
	ic, watch := fadingCommander(t, 0, remotes)
	ic.volumeTopic = playerVolumeTopic(defaultTopicBase, "house", "theater")
	return ic, watch
}

// bindVolume hands the sidecar the compiled table that binds the north
// button to a volume step and the south button to mute, the way the
// operator publishes it on the retained keymap topic.
func bindVolume(t *testing.T, ic *idleCommander, keymap string) {
	t.Helper()
	ic.handle(keymap, mustEncode(t, []compiledBinding{
		{EventType: evKey, Code: buttonCodes["BTN_NORTH"], Value: 1, Action: actionVolume, Amount: 5},
		{EventType: evKey, Code: buttonCodes["BTN_SOUTH"], Value: 1, Action: actionMute},
	}))
}

// pressButton presses one named button down on the given events topic.
func pressButton(t *testing.T, ic *idleCommander, events, button string) {
	t.Helper()
	sendEvent(t, ic, events, remoteEvent{Type: evKey, Code: buttonCodes[button], Value: 1})
}

// The sidecar keeps the last level the topic delivered, because a
// volume press steps from it. The client subscribes to the same topic
// and draws the indicator, so the sidecar draws nothing.
func TestTheIdleSidecarHoldsTheLevelItReads(t *testing.T) {
	ic, watch := volumeCommander(t, nil)

	ic.handle(ic.volumeTopic, []byte(`{"level":45,"muted":true}`))

	mustMatch(t, ic.heldVolume(), volumeState{Level: 45, Muted: true})
	noPublish(t, watch, 100*time.Millisecond)
	noMoment(t, watch, 100*time.Millisecond)
}

// A payload that does not decode leaves the held level where it is, so
// one bad message does not step the next press from unity.
func TestTheIdleSidecarKeepsTheLevelThroughABadMessage(t *testing.T) {
	ic, _ := volumeCommander(t, nil)
	ic.handle(ic.volumeTopic, []byte(`{"level":45,"muted":true}`))

	ic.handle(ic.volumeTopic, []byte("not json"))

	mustMatch(t, ic.heldVolume(), volumeState{Level: 45, Muted: true})
}

// Before any message arrives the sidecar steps from unity, so a press on
// a unit whose level the broker never held still publishes a level.
func TestTheIdleSidecarStepsFromUnityBeforeAnyMessage(t *testing.T) {
	ic, _ := volumeCommander(t, nil)

	mustMatch(t, ic.heldVolume(), defaultVolumeState())
}

// A volume press on the idle screen publishes the unit's next level,
// retained, and states no screen moment, because the client reads the
// level off that topic. It steps from the last message the topic
// delivered, so a person sets the room before they choose any media.
func TestAnIdleVolumePressPublishesTheNextLevel(t *testing.T) {
	events, keymap := fadeTopics()
	ic, watch := volumeCommander(t, map[string]string{events: keymap})
	bindVolume(t, ic, keymap)
	sendActivity(t, ic, playerIdle)

	ic.handle(ic.volumeTopic, []byte(`{"level":40,"muted":false}`))

	pressButton(t, ic, events, "BTN_NORTH")

	publish := nextPublish(t, watch)
	mustMatch(t, publish.topic, ic.volumeTopic)
	mustMatch(t, publish.retained, true)
	mustMatch(t, string(publish.payload), `{"level":45,"muted":false}`)
	noMoment(t, watch, 100*time.Millisecond)
}

// A mute press toggles the flag the same way, so the state survives
// into the next Play and the indicator's glyph is what says so.
func TestAnIdleMutePressPublishesTheToggledFlag(t *testing.T) {
	events, keymap := fadeTopics()
	ic, watch := volumeCommander(t, map[string]string{events: keymap})
	bindVolume(t, ic, keymap)
	sendActivity(t, ic, playerIdle)

	pressButton(t, ic, events, "BTN_SOUTH")

	mustMatch(t, string(nextPublish(t, watch).payload), `{"level":100,"muted":true}`)
}

// A unit that is playing has the film's own pod answering its
// presses, so the idle sidecar publishes no level while a Play runs. Two
// publishers on one press would race to the same value for no gain.
func TestTheIdleSidecarPressesNoVolumeWhileAPlayRuns(t *testing.T) {
	events, keymap := fadeTopics()
	ic, watch := volumeCommander(t, map[string]string{events: keymap})
	bindVolume(t, ic, keymap)
	sendActivity(t, ic, playerPlaying)

	pressButton(t, ic, events, "BTN_NORTH")

	noPublish(t, watch, 100*time.Millisecond)
}

// A press on a sleeping screen is a wake and nothing more, so the
// press that brings the picture back does not also move the level.
func TestAPressOnASleepingScreenOnlyWakesIt(t *testing.T) {
	events, keymap := fadeTopics()
	ic, watch := volumeCommander(t, map[string]string{events: keymap})
	bindVolume(t, ic, keymap)
	sendActivity(t, ic, playerIdle)
	ic.mu.Lock()
	ic.asleep = true
	ic.mu.Unlock()

	pressButton(t, ic, events, "BTN_NORTH")

	mustMatch(t, nextMoment(t, watch).Event, screenWakeEvent)
	noPublish(t, watch, 100*time.Millisecond)
}

// A Player with no sinks hands its idle sidecar no volume topic.
// That sidecar answers no press and applies no level, because a unit with
// nothing to hear has no level to mean anything.
func TestAnIdleSidecarWithNoSpeakersIgnoresTheVolume(t *testing.T) {
	events, keymap := fadeTopics()
	ic, watch := volumeCommander(t, map[string]string{events: keymap})
	ic.volumeTopic = ""
	bindVolume(t, ic, keymap)
	sendActivity(t, ic, playerIdle)

	pressButton(t, ic, events, "BTN_NORTH")

	noPublish(t, watch, 100*time.Millisecond)
	noMoment(t, watch, 100*time.Millisecond)
}

// bindRepeatingVolume compiles a table whose volume step repeats, the
// way the dualsense keymap's volume bindings do, with a repeat quick
// enough to land inside a test.
func bindRepeatingVolume(t *testing.T, ic *idleCommander, keymap string) {
	t.Helper()
	ic.handle(keymap, mustEncode(t, []compiledBinding{
		{EventType: evKey, Code: buttonCodes["BTN_NORTH"], Value: 1,
			Action: actionVolume, Amount: 5, RepeatDelay: 10, RepeatInterval: 10},
	}))
}

// releaseButton releases one named button on the given events topic.
func releaseButton(t *testing.T, ic *idleCommander, events, button string) {
	t.Helper()
	sendEvent(t, ic, events, remoteEvent{Type: evKey, Code: buttonCodes[button], Value: 0})
}

// A held volume control steps again while it is held, on the same clock
// the translator ticks during a film, so a hold feels the same on both
// screens. The press publishes once and the repeat publishes more.
func TestAHeldIdleVolumeControlRepeats(t *testing.T) {
	events, keymap := fadeTopics()
	ic, watch := volumeCommander(t, map[string]string{events: keymap})
	bindRepeatingVolume(t, ic, keymap)
	sendActivity(t, ic, playerIdle)
	ic.handle(ic.volumeTopic, []byte(`{"level":40,"muted":false}`))

	pressButton(t, ic, events, "BTN_NORTH")

	for range 3 {
		publish := nextPublish(t, watch)
		mustMatch(t, string(publish.payload), `{"level":45,"muted":false}`)
	}
	releaseButton(t, ic, events, "BTN_NORTH")
}

// The release ends the repeat its press started, so the level stops
// moving the moment the control comes up.
func TestAReleaseStopsTheIdleVolumeRepeat(t *testing.T) {
	events, keymap := fadeTopics()
	ic, watch := volumeCommander(t, map[string]string{events: keymap})
	bindRepeatingVolume(t, ic, keymap)
	sendActivity(t, ic, playerIdle)

	pressButton(t, ic, events, "BTN_NORTH")
	nextPublish(t, watch)
	releaseButton(t, ic, events, "BTN_NORTH")

	drainPublishes(watch, 50*time.Millisecond)
	noPublish(t, watch, 100*time.Millisecond)
}

// A Play that starts mid-hold silences the repeat's ticks, because a
// playing unit has the film's own pod answering its presses. The gate
// reads again on every tick, not once at the press.
func TestAPlayStartedMidHoldSilencesTheRepeat(t *testing.T) {
	events, keymap := fadeTopics()
	ic, watch := volumeCommander(t, map[string]string{events: keymap})
	bindRepeatingVolume(t, ic, keymap)
	sendActivity(t, ic, playerIdle)

	pressButton(t, ic, events, "BTN_NORTH")
	nextPublish(t, watch)
	sendActivity(t, ic, playerPlaying)

	drainPublishes(watch, 50*time.Millisecond)
	noPublish(t, watch, 100*time.Millisecond)
	releaseButton(t, ic, events, "BTN_NORTH")
}
