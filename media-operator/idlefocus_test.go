package main

// These tests cover the idle sidecar's focus gate: the presses it answers
// only for a controller whose mark names this Player, the focus and the wake
// a live mark brings, the retained catch-up that brings neither, and the
// cycle request a focused press publishes.

import (
	"testing"
	"time"
)

// sofaFocus is the focus topic of the one controller these tests point at
// the unit, beside the events and keymap topics fadeTopics builds.
func sofaFocus() string {
	return remoteFocusTopic(defaultTopicBase, "house", "sofa")
}

// armchairTopics builds the topics of a second controller, so a test reads
// the index the pulse carries for a remote that is not the first.
func armchairTopics() (events, focus string) {
	return remoteEventsTopic(defaultTopicBase, "house", "armchair"),
		remoteFocusTopic(defaultTopicBase, "house", "armchair")
}

// sendMark publishes one retained mark on a controller's focus topic, the
// way the operator writes it.
func sendMark(ic *idleCommander, focus, player string) {
	ic.handle(focus, []byte(player))
}

// awaitCatchUp puts every mark back to the state a fresh bus session
// starts in, where the first message on each focus topic is the broker's
// retained catch-up.
func awaitCatchUp(ic *idleCommander) {
	ic.onBusConnect(nil)
}

// bindCycle hands the sidecar the compiled table that binds the east
// button to cycle-focus, the way the operator publishes it on the retained
// keymap topic.
func bindCycle(t *testing.T, ic *idleCommander, keymap string) {
	t.Helper()
	ic.handle(keymap, mustEncode(t, []compiledBinding{{
		EventType: evKey,
		Code:      buttonCodes["BTN_EAST"],
		Value:     1,
		Action:    actionCycleFocus,
	}}))
}

// nextPulse reads the next moment and returns the controller it named.
// Any other moment fails the test, because a focus is the only one that
// names a controller.
func nextPulse(t *testing.T, watch *idleWatch) int {
	t.Helper()
	moment := nextMoment(t, watch)
	mustMatch(t, moment.Event, screenFocusEvent)
	if moment.Remote == nil {
		t.Fatalf("the focus moment named no controller: %+v", moment)
		return 0
	}
	return *moment.Remote
}

// A controller whose mark names another unit touches nothing here, so a
// pad pointed at a film in another room does not wake this screen.
func TestIdleFocusIgnoresAPressFromAnUnfocusedRemote(t *testing.T) {
	events, _ := fadeTopics()
	ic, watch := fadingCommander(t, 20*time.Millisecond, map[string]string{events: ""})

	sendActivity(t, ic, playerIdle)
	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)
	sendMark(ic, sofaFocus(), "cinema")
	sendPress(t, ic, events)

	noMoment(t, watch, 100*time.Millisecond)
}

// The same gate holds the level: an unfocused controller's volume press
// publishes nothing, so the room it points at keeps the only say over its
// own unit.
func TestIdleFocusIgnoresAVolumePressFromAnUnfocusedRemote(t *testing.T) {
	events, keymap := fadeTopics()
	ic, watch := fadingCommander(t, 0, map[string]string{events: keymap})
	ic.volumeTopic = playerVolumeTopic(defaultTopicBase, "house", idleTestPlayer)
	bindVolume(t, ic, keymap)
	sendMark(ic, sofaFocus(), "cinema")

	sendActivity(t, ic, playerIdle)
	pressButton(t, ic, events, "BTN_NORTH")

	noPublish(t, watch, 100*time.Millisecond)
}

// A volume button held as focus cycles to another room stops stepping this
// unit at once. The repeat outliving the mark would move a level nobody is
// pointing at until the release, or until the repeat's own cap.
func TestIdleFocusMovingTheMarkAwayStopsAHeldRepeat(t *testing.T) {
	events, keymap := fadeTopics()
	ic, watch := volumeCommander(t, map[string]string{events: keymap})
	bindRepeatingVolume(t, ic, keymap)
	sendActivity(t, ic, playerIdle)

	pressButton(t, ic, events, "BTN_NORTH")
	nextPublish(t, watch)
	sendMark(ic, sofaFocus(), "cinema")

	drainPublishes(watch, 50*time.Millisecond)
	noPublish(t, watch, 100*time.Millisecond)
	releaseButton(t, ic, events, "BTN_NORTH")
}

// A mark that arrives during a session and names this Player is a person
// pointing the controller here, so the sidecar states wake and then the
// focus that names which controller it was.
func TestIdleFocusALiveMarkWakesTheScreenAndPulses(t *testing.T) {
	events, _ := fadeTopics()
	ic, watch := fadingCommander(t, 20*time.Millisecond, map[string]string{events: ""})

	sendActivity(t, ic, playerIdle)
	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)
	sendMark(ic, sofaFocus(), idleTestPlayer)

	mustMatch(t, nextMoment(t, watch).Event, screenWakeEvent)
	mustMatch(t, nextPulse(t, watch), 0)
}

// The first mark of a bus session is the broker's retained catch-up, a
// restore and not a press, so a pod that starts on a sleeping screen leaves
// it asleep and draws nothing.
func TestIdleFocusTheRetainedCatchUpNeitherWakesNorPulses(t *testing.T) {
	events, _ := fadeTopics()
	ic, watch := fadingCommander(t, 20*time.Millisecond, map[string]string{events: ""})
	awaitCatchUp(ic)

	sendActivity(t, ic, playerIdle)
	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)
	sendMark(ic, sofaFocus(), idleTestPlayer)

	noMoment(t, watch, 100*time.Millisecond)
}

// The catch-up still sets the gate, so the press that follows it acts.
func TestIdleFocusTheRetainedCatchUpOpensTheGate(t *testing.T) {
	events, keymap := fadeTopics()
	ic, watch := fadingCommander(t, time.Hour, map[string]string{events: keymap})
	awaitCatchUp(ic)
	bindBack(t, ic, keymap)
	sendMark(ic, sofaFocus(), idleTestPlayer)

	sendActivity(t, ic, playerIdle)
	sendPress(t, ic, events)

	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)
}

// A cycle that wraps back onto the unit it started on republishes the same
// mark. The focus goes out again, because that repeat is the answer to the
// press: this screen is the one the controller already holds.
func TestIdleFocusPulsesAgainOnTheSameMark(t *testing.T) {
	events, _ := fadeTopics()
	ic, watch := fadingCommander(t, 0, map[string]string{events: ""})

	sendMark(ic, sofaFocus(), idleTestPlayer)
	mustMatch(t, nextPulse(t, watch), 0)
	sendMark(ic, sofaFocus(), idleTestPlayer)

	mustMatch(t, nextPulse(t, watch), 0)
}

// The focus names the controller by its position in spec.remotes, so the
// client draws the one the mark landed on.
func TestIdleFocusThePulseCarriesTheRemoteIndex(t *testing.T) {
	sofa, _ := fadeTopics()
	armchair, armchairFocus := armchairTopics()
	ic, watch := fadingCommander(t, 0, map[string]string{sofa: "", armchair: ""})

	sendMark(ic, armchairFocus, idleTestPlayer)
	mustMatch(t, nextPulse(t, watch), 0)
	sendMark(ic, sofaFocus(), idleTestPlayer)

	mustMatch(t, nextPulse(t, watch), 1)
}

// A mark that names another unit draws nothing here, and neither does the
// Play name a mark carries while an older operator still writes one.
func TestIdleFocusPulsesNothingForAMarkThatNamesAnotherPlayer(t *testing.T) {
	cases := []struct {
		name string
		mark string
	}{
		{name: "another unit", mark: "cinema"},
		{name: "a Play name from an older operator", mark: "friday-film"},
		{name: "a cleared mark", mark: ""},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			events, _ := fadeTopics()
			ic, watch := fadingCommander(t, 0, map[string]string{events: ""})

			sendMark(ic, sofaFocus(), one.mark)

			noMoment(t, watch, 100*time.Millisecond)
		})
	}
}

// A fresh bus session redelivers every retained mark, so the first one on
// each topic is a catch-up again and a broker restart pulses nothing.
func TestIdleFocusABusSessionMakesTheNextMarkACatchUpAgain(t *testing.T) {
	events, _ := fadeTopics()
	ic, watch := fadingCommander(t, 0, map[string]string{events: ""})

	sendMark(ic, sofaFocus(), idleTestPlayer)
	mustMatch(t, nextPulse(t, watch), 0)
	awaitCatchUp(ic)
	sendMark(ic, sofaFocus(), idleTestPlayer)

	noMoment(t, watch, 100*time.Millisecond)
}

// A press named cycle-focus, from a controller that holds this unit, asks
// the operator to move the mark. It is the same request the translator
// publishes from a Play, so focus moves from an idle screen too.
func TestIdleCycleFocusPublishesTheCycleRequest(t *testing.T) {
	events, keymap := fadeTopics()
	ic, watch := fadingCommander(t, 0, map[string]string{events: keymap})
	bindCycle(t, ic, keymap)

	sendActivity(t, ic, playerIdle)
	sendPress(t, ic, events)

	publish := nextPublish(t, watch)
	mustMatch(t, publish.topic, sofaFocus()+"/cycle")
	mustMatch(t, len(publish.payload), 0)
	mustMatch(t, publish.retained, false)
	noPublish(t, watch, 100*time.Millisecond)
}

// A cycle press from a controller that holds another unit publishes
// nothing, because the translator that holds the mark answers it there.
func TestIdleCycleFocusPublishesNothingFromAnUnfocusedRemote(t *testing.T) {
	events, keymap := fadeTopics()
	ic, watch := fadingCommander(t, 0, map[string]string{events: keymap})
	bindCycle(t, ic, keymap)
	sendMark(ic, sofaFocus(), "cinema")

	sendActivity(t, ic, playerIdle)
	sendPress(t, ic, events)

	noPublish(t, watch, 100*time.Millisecond)
}

// The cycle press is the request and nothing else, so it leaves a sleeping
// screen asleep. The mark the operator writes back wakes it, when the cycle
// comes around to this unit.
func TestIdleCycleFocusDoesNotWakeTheScreen(t *testing.T) {
	events, keymap := fadeTopics()
	ic, watch := fadingCommander(t, 20*time.Millisecond, map[string]string{events: keymap})
	bindCycle(t, ic, keymap)

	sendActivity(t, ic, playerIdle)
	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)
	sendPress(t, ic, events)

	noMoment(t, watch, 100*time.Millisecond)
}

// A Play owns the presses on the unit it runs on, and its own translator
// publishes the cycle, so the idle sidecar publishes none while one runs.
func TestIdleCycleFocusPublishesNothingWhileAPlayRuns(t *testing.T) {
	events, keymap := fadeTopics()
	ic, watch := fadingCommander(t, 0, map[string]string{events: keymap})
	bindCycle(t, ic, keymap)

	sendActivity(t, ic, playerPlaying)
	sendPress(t, ic, events)

	noPublish(t, watch, 100*time.Millisecond)
}
