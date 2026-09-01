package main

// The idle command pod under a delegate: the presses it forwards,
// what this operator's own controller keeps doing, and the sleep a
// client requests.

import (
	"testing"
	"time"
)

// delegateCommander builds one idle command pod whose screen a delegate
// draws, with a volume topic and no fade window, so nothing but the
// test drives it.
func delegateCommander(t *testing.T, remotes map[string]string) (*idleCommander, *idleWatch) {
	t.Helper()
	ic, watch := volumeCommander(t, remotes)
	ic.delegated = true
	return ic, watch
}

// bindNavigation hands the sidecar a table that names three navigation
// presses and one volume step, the way the operator publishes it on the
// retained keymap topic.
func bindNavigation(t *testing.T, ic *idleCommander, keymap string) {
	t.Helper()
	ic.handle(keymap, mustEncode(t, []compiledBinding{
		{EventType: evKey, Code: buttonCodes["BTN_DPAD_UP"], Value: 1, Action: actionUp},
		{EventType: evKey, Code: buttonCodes["BTN_SOUTH"], Value: 1, Action: actionSelect},
		{EventType: evKey, Code: buttonCodes["BTN_EAST"], Value: 1, Action: actionBack},
		{EventType: evKey, Code: buttonCodes["BTN_NORTH"], Value: 1, Action: actionVolume, Amount: 5},
	}))
}

// Under a delegate every navigation press reaches the client on the
// Player's commands topic, in the shape a translator publishes on a
// Play's commands topic. It is not retained, because a press is an
// event and not a state.
func TestADelegateForwardsTheNavigationPress(t *testing.T) {
	cases := []struct {
		name   string
		button string
		action string
	}{
		{name: "an arrow", button: "BTN_DPAD_UP", action: actionUp},
		{name: "select", button: "BTN_SOUTH", action: actionSelect},
		{name: "back", button: "BTN_EAST", action: actionBack},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			events, keymap := fadeTopics()
			ic, watch := delegateCommander(t, map[string]string{events: keymap})
			bindNavigation(t, ic, keymap)
			sendActivity(t, ic, playerIdle)

			pressButton(t, ic, events, one.button)

			publish := nextPublish(t, watch)
			mustMatch(t, publish.topic, ic.commandsTopic)
			mustMatch(t, publish.retained, false)
			mustMatch(t, string(publish.payload), `{"action":"`+one.action+`"}`)
		})
	}
}

// Back under a delegate leaves the shade up, because the client has
// levels and only the client reads whether back has anywhere to go.
func TestADelegateBackLeavesTheShadeUp(t *testing.T) {
	events, keymap := fadeTopics()
	ic, watch := delegateCommander(t, map[string]string{events: keymap})
	bindNavigation(t, ic, keymap)
	sendActivity(t, ic, playerIdle)

	pressButton(t, ic, events, "BTN_EAST")

	mustMatch(t, string(nextPublish(t, watch).payload), `{"action":"back"}`)
	noMoment(t, watch, 100*time.Millisecond)
}

// A press on a sleeping screen is a wake and nothing more, so the first
// press after the shade comes down reaches no client.
func TestADelegatePressOnASleepingScreenOnlyWakesIt(t *testing.T) {
	events, keymap := fadeTopics()
	ic, watch := delegateCommander(t, map[string]string{events: keymap})
	bindNavigation(t, ic, keymap)
	sendActivity(t, ic, playerIdle)
	ic.mu.Lock()
	ic.asleep = true
	ic.mu.Unlock()

	pressButton(t, ic, events, "BTN_DPAD_UP")

	mustMatch(t, nextMoment(t, watch).Event, screenWakeEvent)
	noPublish(t, watch, 100*time.Millisecond)
}

// The forward reads the same gate every other press reads, so a
// controller whose mark names another unit reaches this client with
// nothing.
func TestADelegateForwardsNothingFromAnUnfocusedRemote(t *testing.T) {
	events, keymap := fadeTopics()
	ic, watch := delegateCommander(t, map[string]string{events: keymap})
	bindNavigation(t, ic, keymap)
	sendMark(ic, sofaFocus(), "cinema")
	sendActivity(t, ic, playerIdle)

	pressButton(t, ic, events, "BTN_DPAD_UP")

	noPublish(t, watch, 100*time.Millisecond)
	noMoment(t, watch, 100*time.Millisecond)
}

// bindRepeatingArrow compiles a table whose arrow repeats, with a
// repeat quick enough to land inside a test.
func bindRepeatingArrow(t *testing.T, ic *idleCommander, keymap string) {
	t.Helper()
	ic.handle(keymap, mustEncode(t, []compiledBinding{{
		EventType: evKey, Code: buttonCodes["BTN_DPAD_UP"], Value: 1,
		Action: actionUp, RepeatDelay: 10, RepeatInterval: 10,
	}}))
}

// A binding whose keymap repeats it publishes the same press again
// while the control is held, on the clock the volume repeat runs on.
func TestAHeldDelegateArrowRepeats(t *testing.T) {
	events, keymap := fadeTopics()
	ic, watch := delegateCommander(t, map[string]string{events: keymap})
	bindRepeatingArrow(t, ic, keymap)
	sendActivity(t, ic, playerIdle)

	pressButton(t, ic, events, "BTN_DPAD_UP")

	for range 3 {
		mustMatch(t, string(nextPublish(t, watch).payload), `{"action":"up"}`)
	}
	releaseButton(t, ic, events, "BTN_DPAD_UP")
}

// A volume press still steps the unit's level under a delegate, because
// the level is the command pod's own and no client draws it.
func TestADelegateVolumePressStillStepsTheLevel(t *testing.T) {
	events, keymap := fadeTopics()
	ic, watch := delegateCommander(t, map[string]string{events: keymap})
	bindNavigation(t, ic, keymap)
	sendActivity(t, ic, playerIdle)
	ic.handle(ic.volumeTopic, []byte(`{"level":40,"muted":false}`))

	pressButton(t, ic, events, "BTN_NORTH")

	publish := nextPublish(t, watch)
	mustMatch(t, publish.topic, ic.volumeTopic)
	mustMatch(t, string(publish.payload), `{"level":45,"muted":false}`)
}

// Under this operator's own controller a navigation press reaches no
// one, because the stock client draws no list.
func TestTheOwnControllerForwardsNoNavigationPress(t *testing.T) {
	events, keymap := fadeTopics()
	ic, watch := volumeCommander(t, map[string]string{events: keymap})
	bindNavigation(t, ic, keymap)
	sendActivity(t, ic, playerIdle)

	pressButton(t, ic, events, "BTN_DPAD_UP")

	noPublish(t, watch, 100*time.Millisecond)
	noMoment(t, watch, 100*time.Millisecond)
}

// Under this operator's own controller back is still sleep, so the
// keymap that names the navigation actions changes nothing there.
func TestTheOwnControllerBackStillSleepsTheScreen(t *testing.T) {
	events, keymap := fadeTopics()
	ic, watch := volumeCommander(t, map[string]string{events: keymap})
	bindNavigation(t, ic, keymap)
	sendActivity(t, ic, playerIdle)

	pressButton(t, ic, events, "BTN_EAST")

	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)
	noPublish(t, watch, 100*time.Millisecond)
}

// A client at its top level asks for sleep, and the shade comes down
// the way it does under this operator's own back press.
func TestASleepRequestBringsTheShadeDown(t *testing.T) {
	ic, watch := fadingCommander(t, 0, nil)
	sendActivity(t, ic, playerIdle)

	ic.handle(ic.commandsTopic, mustEncode(t, mediaCommand{Action: actionSleep}))

	mustMatch(t, nextScreenPublish(t, watch), screenPublish{event: screenSleepEvent, retained: true})
}

// The shade coming down on a request starts the off window, so the
// panel darkens on the same schedule a back press sets.
func TestASleepRequestArmsTheOffWindow(t *testing.T) {
	ic, watch := fadingCommander(t, 0, nil)
	ic.panelTopic = playerPanelTopic(defaultTopicBase, "house", idleTestPlayer)
	ic.offAfter = 20 * time.Millisecond
	sendActivity(t, ic, playerIdle)

	ic.handle(ic.commandsTopic, mustEncode(t, mediaCommand{Action: actionSleep}))

	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)
	publish := nextPublish(t, watch)
	mustMatch(t, publish.topic, ic.panelTopic)
	mustMatch(t, string(publish.payload), `{"desire":"off"}`)
}

// A sleep request on a screen that already sleeps, and one that arrives
// while a Play runs, state nothing.
func TestASleepRequestIsIgnored(t *testing.T) {
	cases := []struct {
		name     string
		activity string
		asleep   bool
	}{
		{name: "the screen already sleeps", activity: playerIdle, asleep: true},
		{name: "a Play runs", activity: playerPlaying},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			ic, watch := fadingCommander(t, 0, nil)
			sendActivity(t, ic, one.activity)
			ic.mu.Lock()
			ic.asleep = one.asleep
			ic.mu.Unlock()

			ic.handle(ic.commandsTopic, mustEncode(t, mediaCommand{Action: actionSleep}))

			noMoment(t, watch, 100*time.Millisecond)
		})
	}
}

// The navigation actions are the six a delegate's client answers, and
// nothing else on the commands topic is one of them.
func TestIsNavigationAction(t *testing.T) {
	cases := []struct {
		name   string
		action string
		want   bool
	}{
		{name: "up", action: actionUp, want: true},
		{name: "down", action: actionDown, want: true},
		{name: "left", action: actionLeft, want: true},
		{name: "right", action: actionRight, want: true},
		{name: "select", action: actionSelect, want: true},
		{name: "back", action: actionBack, want: true},
		{name: "a volume step", action: actionVolume},
		{name: "cycle-focus", action: actionCycleFocus},
		{name: "a sleep request", action: actionSleep},
		{name: "nothing at all", action: ""},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, isNavigationAction(one.action), one.want)
		})
	}
}
