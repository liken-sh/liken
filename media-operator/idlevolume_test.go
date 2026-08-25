package main

// These tests cover the idle sidecar's half of the level: the
// message it applies to the idle mpv, the press it publishes, the two
// states it does not press in, and the speaker gate that turns the whole
// path off.

import (
	"testing"
	"time"
)

// volumeCommander builds one idle sidecar that holds a volume topic, with
// no fade window so nothing but the test drives it. The channel it returns
// carries every message the sidecar published.
func volumeCommander(t *testing.T, remotes map[string]string) (*idleCommander, chan brokerPublish) {
	t.Helper()
	ic := fadingCommander(t, 0, remotes)
	ic.volumeTopic = playerVolumeTopic(defaultTopicBase, "house", "theater")
	published := make(chan brokerPublish, 16)
	ic.publish = func(topic string, payload []byte, retained bool) {
		published <- brokerPublish{topic: topic, payload: append([]byte(nil), payload...), retained: retained}
	}
	return ic, published
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

// nextPublish returns the next message the sidecar published, on a bounded
// wait.
func nextPublish(t *testing.T, published chan brokerPublish) brokerPublish {
	t.Helper()
	select {
	case publish := <-published:
		return publish
	case <-time.After(2 * time.Second):
		t.Fatal("the sidecar published nothing inside 2s")
		return brokerPublish{}
	}
}

// noPublish fails when the sidecar publishes anything inside this window.
func noPublish(t *testing.T, published chan brokerPublish, window time.Duration) {
	t.Helper()
	select {
	case publish := <-published:
		t.Fatalf("the sidecar published %+v, and should have published nothing", publish)
	case <-time.After(window):
	}
}

// The message on the volume topic reaches the idle mpv. Its mpv
// plays no audio and holds the level as a property all the same, which is
// what the display draws the indicator from.
func TestTheIdleSidecarAppliesTheLevelToItsMpv(t *testing.T) {
	fake := startFakeMPV(t)
	ic, _ := volumeCommander(t, nil)

	ic.handle(ic.volumeTopic, []byte(`{"level":45,"muted":true}`))

	mustMatch(t, fake.next(t), `{"command":["no-osd","set","volume","45"]}`)
	mustMatch(t, fake.next(t), `{"command":["no-osd","set","mute","yes"]}`)
	mustMatch(t, ic.heldVolume(), volumeState{Level: 45, Muted: true})
}

// The first level of a bus session is the broker's retained
// catch-up, so it applies with no signal and the idle screen pops no
// indicator at pod start. Every level after it applies and then signals the
// display to draw. A fresh session redelivers the retained level, so the
// first message after a reconnect is silent again.
func TestTheIdleSidecarAppliesTheFirstLevelSilently(t *testing.T) {
	fake := startFakeMPV(t)
	ic, _ := volumeCommander(t, nil)

	ic.handle(ic.volumeTopic, []byte(`{"level":40,"muted":false}`))
	mustMatch(t, fake.next(t), `{"command":["no-osd","set","volume","40"]}`)
	mustMatch(t, fake.next(t), `{"command":["no-osd","set","mute","no"]}`)
	fake.quiet(t, 100*time.Millisecond)

	ic.handle(ic.volumeTopic, []byte(`{"level":45,"muted":false}`))
	mustMatch(t, fake.next(t), `{"command":["no-osd","set","volume","45"]}`)
	mustMatch(t, fake.next(t), `{"command":["no-osd","set","mute","no"]}`)
	mustMatch(t, fake.next(t), `{"command":["script-message","volume-changed"]}`)

	ic.onBusConnect(nil)

	ic.handle(ic.volumeTopic, []byte(`{"level":50,"muted":false}`))
	mustMatch(t, fake.next(t), `{"command":["no-osd","set","volume","50"]}`)
	mustMatch(t, fake.next(t), `{"command":["no-osd","set","mute","no"]}`)
	fake.quiet(t, 100*time.Millisecond)
}

// A volume press on the idle screen publishes the unit's next
// level, retained, and writes nothing to mpv itself. It steps from the last
// message the topic delivered, so a person sets the room before they choose
// any media.
func TestAnIdleVolumePressPublishesTheNextLevel(t *testing.T) {
	events, keymap := fadeTopics()
	fake := startFakeMPV(t)
	ic, published := volumeCommander(t, map[string]string{events: keymap})
	bindVolume(t, ic, keymap)
	sendActivity(t, ic, fake, playerIdle)

	ic.handle(ic.volumeTopic, []byte(`{"level":40,"muted":false}`))
	mustMatch(t, fake.next(t), `{"command":["no-osd","set","volume","40"]}`)
	mustMatch(t, fake.next(t), `{"command":["no-osd","set","mute","no"]}`)

	pressButton(t, ic, events, "BTN_NORTH")

	publish := nextPublish(t, published)
	mustMatch(t, publish.topic, ic.volumeTopic)
	mustMatch(t, publish.retained, true)
	mustMatch(t, string(publish.payload), `{"level":45,"muted":false}`)
	fake.quiet(t, 100*time.Millisecond)
}

// A mute press toggles the flag the same way, so the state survives
// into the next Play and the indicator's glyph is what says so.
func TestAnIdleMutePressPublishesTheToggledFlag(t *testing.T) {
	events, keymap := fadeTopics()
	fake := startFakeMPV(t)
	ic, published := volumeCommander(t, map[string]string{events: keymap})
	bindVolume(t, ic, keymap)
	sendActivity(t, ic, fake, playerIdle)

	pressButton(t, ic, events, "BTN_SOUTH")

	mustMatch(t, string(nextPublish(t, published).payload), `{"level":100,"muted":true}`)
}

// A unit that is playing has the film's own pod answering its
// presses, so the idle sidecar publishes no level while a Play runs. Two
// publishers on one press would race to the same value for no gain.
func TestTheIdleSidecarPressesNoVolumeWhileAPlayRuns(t *testing.T) {
	events, keymap := fadeTopics()
	fake := startFakeMPV(t)
	ic, published := volumeCommander(t, map[string]string{events: keymap})
	bindVolume(t, ic, keymap)
	sendActivity(t, ic, fake, playerPlaying)

	pressButton(t, ic, events, "BTN_NORTH")

	noPublish(t, published, 100*time.Millisecond)
}

// A press on a sleeping screen is a wake and nothing more, so the
// press that brings the picture back does not also move the level.
func TestAPressOnASleepingScreenOnlyWakesIt(t *testing.T) {
	events, keymap := fadeTopics()
	fake := startFakeMPV(t)
	ic, published := volumeCommander(t, map[string]string{events: keymap})
	bindVolume(t, ic, keymap)
	sendActivity(t, ic, fake, playerIdle)
	ic.mu.Lock()
	ic.asleep = true
	ic.mu.Unlock()

	pressButton(t, ic, events, "BTN_NORTH")

	mustMatch(t, fake.next(t), wakeCommand)
	noPublish(t, published, 100*time.Millisecond)
}

// A Player with no sinks hands its idle sidecar no volume topic.
// That sidecar answers no press and applies no level, because a unit with
// nothing to hear has no level to mean anything.
func TestAnIdleSidecarWithNoSpeakersIgnoresTheVolume(t *testing.T) {
	events, keymap := fadeTopics()
	fake := startFakeMPV(t)
	ic, published := volumeCommander(t, map[string]string{events: keymap})
	ic.volumeTopic = ""
	bindVolume(t, ic, keymap)
	sendActivity(t, ic, fake, playerIdle)

	pressButton(t, ic, events, "BTN_NORTH")

	noPublish(t, published, 100*time.Millisecond)
	fake.quiet(t, 100*time.Millisecond)
}
