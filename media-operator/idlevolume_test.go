package main

// These tests cover the idle command pod's half of the level: the level it
// holds so a press has something to step from, the press it publishes,
// the two states it does not press in, and the speaker gate that turns
// the whole path off.

import (
	"testing"
	"time"
)

// volumeCommander builds one idle command pod that holds a volume topic, with
// no fade window so nothing but the test drives it.
func volumeCommander(t *testing.T, remotes []string) (*idleCommander, *idleWatch) {
	t.Helper()
	ic, watch := fadingCommander(t, 0, remotes)
	ic.volumeTopic = playerVolumeTopic(defaultTopicBase, "house", "theater")
	return ic, watch
}

// sendVolumeUp presses the level up, under the kernel's name for the
// control, the one every remote and keyboard reports.
func sendVolumeUp(t *testing.T, ic *idleCommander, events string) {
	t.Helper()
	sendEvent(t, ic, events, keyEvent{Key: "KEY_VOLUMEUP", Value: 1})
}

// repeatVolumeUp is the same key held down. The standing remote pod
// sends this value while a person holds the control, and this pod
// synthesises none of its own.
func repeatVolumeUp(t *testing.T, ic *idleCommander, events string) {
	t.Helper()
	sendEvent(t, ic, events, keyEvent{Key: "KEY_VOLUMEUP", Value: 2})
}

// releaseVolumeUp lets the level key back up.
func releaseVolumeUp(t *testing.T, ic *idleCommander, events string) {
	t.Helper()
	sendEvent(t, ic, events, keyEvent{Key: "KEY_VOLUMEUP", Value: 0})
}

// sendMute presses mute.
func sendMute(t *testing.T, ic *idleCommander, events string) {
	t.Helper()
	sendEvent(t, ic, events, keyEvent{Key: "KEY_MUTE", Value: 1})
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
	events := fadeTopics()
	ic, watch := volumeCommander(t, []string{events})
	sendActivity(t, ic, playerIdle)

	ic.handle(ic.volumeTopic, []byte(`{"level":40,"muted":false}`))

	sendVolumeUp(t, ic, events)

	publish := nextPublish(t, watch)
	mustMatch(t, publish.topic, ic.volumeTopic)
	mustMatch(t, publish.retained, true)
	mustMatch(t, string(publish.payload), `{"level":45,"muted":false}`)
	noMoment(t, watch, 100*time.Millisecond)
}

// A mute press toggles the flag the same way, so the state survives
// into the next Play and the indicator's glyph is what says so.
func TestAnIdleMutePressPublishesTheToggledFlag(t *testing.T) {
	events := fadeTopics()
	ic, watch := volumeCommander(t, []string{events})
	sendActivity(t, ic, playerIdle)

	sendMute(t, ic, events)

	mustMatch(t, string(nextPublish(t, watch).payload), `{"level":100,"muted":true}`)
}

// Mute acts on the press alone, because a held mute that toggled on
// every repeat would flip the flag back and forth under the hand.
func TestAnIdleMuteRepeatTogglesNothing(t *testing.T) {
	events := fadeTopics()
	ic, watch := volumeCommander(t, []string{events})
	sendActivity(t, ic, playerIdle)

	sendMute(t, ic, events)
	mustMatch(t, string(nextPublish(t, watch).payload), `{"level":100,"muted":true}`)
	sendEvent(t, ic, events, keyEvent{Key: "KEY_MUTE", Value: 2})

	noPublish(t, watch, 100*time.Millisecond)
}

// A unit that is playing has the film's own pod answering its
// presses, so the idle command pod publishes no level while a Play runs. Two
// publishers on one press would race to the same value for no gain.
func TestTheIdleSidecarPressesNoVolumeWhileAPlayRuns(t *testing.T) {
	events := fadeTopics()
	ic, watch := volumeCommander(t, []string{events})
	sendActivity(t, ic, playerPlaying)

	sendVolumeUp(t, ic, events)

	noPublish(t, watch, 100*time.Millisecond)
}

// A press on a sleeping screen is a wake and nothing more, so the
// press that brings the picture back does not also move the level.
func TestAPressOnASleepingScreenOnlyWakesIt(t *testing.T) {
	events := fadeTopics()
	ic, watch := volumeCommander(t, []string{events})
	sendActivity(t, ic, playerIdle)
	ic.mu.Lock()
	ic.asleep = true
	ic.mu.Unlock()

	sendVolumeUp(t, ic, events)

	mustMatch(t, nextMoment(t, watch).Event, screenWakeEvent)
	noPublish(t, watch, 100*time.Millisecond)
}

// A Player with no sinks hands its idle command pod no volume topic.
// That sidecar answers no press and applies no level, because a unit with
// nothing to hear has no level to mean anything.
func TestAnIdleSidecarWithNoSpeakersIgnoresTheVolume(t *testing.T) {
	events := fadeTopics()
	ic, watch := volumeCommander(t, []string{events})
	ic.volumeTopic = ""
	sendActivity(t, ic, playerIdle)

	sendVolumeUp(t, ic, events)

	noPublish(t, watch, 100*time.Millisecond)
	noMoment(t, watch, 100*time.Millisecond)
}

// The standing remote pod sends the repeat, and the level steps again
// on each one, so a person ramps the level by holding the key.
func TestAVolumeRepeatFromTheBusStepsTheLevelAgain(t *testing.T) {
	events := fadeTopics()
	ic, watch := volumeCommander(t, []string{events})
	sendActivity(t, ic, playerIdle)
	ic.handle(ic.volumeTopic, []byte(`{"level":40,"muted":false}`))

	sendVolumeUp(t, ic, events)
	mustMatch(t, string(nextPublish(t, watch).payload), `{"level":45,"muted":false}`)
	ic.handle(ic.volumeTopic, []byte(`{"level":45,"muted":false}`))
	repeatVolumeUp(t, ic, events)

	mustMatch(t, string(nextPublish(t, watch).payload), `{"level":50,"muted":false}`)
}

// The pod acts on the press and the repeat, and a release changes
// nothing at all, so the level stops the moment the control comes up.
func TestAVolumeReleaseStepsNothing(t *testing.T) {
	events := fadeTopics()
	ic, watch := volumeCommander(t, []string{events})
	sendActivity(t, ic, playerIdle)

	sendVolumeUp(t, ic, events)
	nextPublish(t, watch)
	releaseVolumeUp(t, ic, events)

	noPublish(t, watch, 100*time.Millisecond)
	noMoment(t, watch, 100*time.Millisecond)
}

// The gate reads every event the standing pod sends, so the repeats
// that arrive after a Play starts step nothing.
func TestAVolumeRepeatStepsNothingWhileAPlayRuns(t *testing.T) {
	events := fadeTopics()
	ic, watch := volumeCommander(t, []string{events})
	sendActivity(t, ic, playerIdle)

	sendVolumeUp(t, ic, events)
	nextPublish(t, watch)
	sendActivity(t, ic, playerPlaying)
	repeatVolumeUp(t, ic, events)

	noPublish(t, watch, 100*time.Millisecond)
}

// The pod holds no clock of its own, so one press publishes one level
// and the next level waits for the next event off the bus.
func TestAVolumePressPublishesOneLevelAndRepeatsNothing(t *testing.T) {
	events := fadeTopics()
	ic, watch := volumeCommander(t, []string{events})
	sendActivity(t, ic, playerIdle)

	sendVolumeUp(t, ic, events)

	nextPublish(t, watch)
	noPublish(t, watch, 200*time.Millisecond)
}
