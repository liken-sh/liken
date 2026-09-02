package main

// The idle command pod under a delegate: the presses it forwards,
// what this operator's own controller keeps doing, and the sleep a
// client requests.

import (
	"strconv"
	"testing"
	"time"
)

// delegateCommander builds one idle command pod whose screen a delegate
// draws, with a volume topic and no fade window, so nothing but the
// test drives it.
func delegateCommander(t *testing.T, remotes []string) (*idleCommander, *idleWatch) {
	t.Helper()
	ic, watch := volumeCommander(t, remotes)
	ic.delegated = true
	return ic, watch
}

// sendKey presses one named key at one value, for the tests that name
// the key they press rather than what it means.
func sendKey(t *testing.T, ic *idleCommander, events, key string, value int32) {
	t.Helper()
	sendEvent(t, ic, events, keyEvent{Key: key, Value: value})
}

// Under a delegate every navigation key reaches the client on the
// Player's commands topic, as the same JSON the events topic carried,
// so the client reads the kernel's name and holds its own table. The
// press and the repeat both travel, because a person holds an arrow to
// run down a list. The topic is not retained, because a press is an
// event and not a state.
func TestADelegateForwardsEveryNavigationKey(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value int32
	}{
		{name: "up", key: "KEY_UP", value: 1},
		{name: "down", key: "KEY_DOWN", value: 1},
		{name: "left", key: "KEY_LEFT", value: 1},
		{name: "right", key: "KEY_RIGHT", value: 1},
		{name: "enter", key: "KEY_ENTER", value: 1},
		{name: "ok", key: "KEY_OK", value: 1},
		{name: "select", key: "KEY_SELECT", value: 1},
		{name: "the keypad's enter", key: "KEY_KPENTER", value: 1},
		{name: "back", key: "KEY_BACK", value: 1},
		{name: "escape", key: "KEY_ESC", value: 1},
		{name: "exit", key: "KEY_EXIT", value: 1},
		{name: "a held arrow repeating", key: "KEY_UP", value: 2},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			events := fadeTopics()
			ic, watch := delegateCommander(t, []string{events})
			sendActivity(t, ic, playerIdle)

			sendKey(t, ic, events, one.key, one.value)

			publish := nextPublish(t, watch)
			mustMatch(t, publish.topic, ic.commandsTopic)
			mustMatch(t, publish.retained, false)
			mustMatch(t, string(publish.payload),
				`{"key":"`+one.key+`","value":`+strconv.Itoa(int(one.value))+`}`)
		})
	}
}

// A release reaches no client. The client reads the press and the
// repeat, and a release it answered would move the list a second time
// on one act.
func TestADelegateForwardsNoRelease(t *testing.T) {
	events := fadeTopics()
	ic, watch := delegateCommander(t, []string{events})
	sendActivity(t, ic, playerIdle)

	sendKey(t, ic, events, "KEY_UP", 0)

	noPublish(t, watch, 100*time.Millisecond)
	noMoment(t, watch, 100*time.Millisecond)
}

// The pod forwards the navigation keys and nothing else, so a key it
// holds no row for restarts the quiet window and reaches no client.
func TestADelegateForwardsNothingForAKeyItHoldsNoRowFor(t *testing.T) {
	events := fadeTopics()
	ic, watch := delegateCommander(t, []string{events})
	sendActivity(t, ic, playerIdle)

	sendPress(t, ic, events)

	noPublish(t, watch, 100*time.Millisecond)
	noMoment(t, watch, 100*time.Millisecond)
}

// Back under a delegate leaves the shade up, because the client has
// levels and only the client reads whether back has anywhere to go.
func TestADelegateBackLeavesTheShadeUp(t *testing.T) {
	events := fadeTopics()
	ic, watch := delegateCommander(t, []string{events})
	sendActivity(t, ic, playerIdle)

	sendBack(t, ic, events)

	mustMatch(t, string(nextPublish(t, watch).payload), `{"key":"KEY_BACK","value":1}`)
	noMoment(t, watch, 100*time.Millisecond)
}

// A press on a sleeping screen is a wake and nothing more, so the first
// press after the shade comes down reaches no client.
func TestADelegatePressOnASleepingScreenOnlyWakesIt(t *testing.T) {
	events := fadeTopics()
	ic, watch := delegateCommander(t, []string{events})
	sendActivity(t, ic, playerIdle)
	ic.mu.Lock()
	ic.asleep = true
	ic.mu.Unlock()

	sendKey(t, ic, events, "KEY_UP", 1)

	mustMatch(t, nextMoment(t, watch).Event, screenWakeEvent)
	noPublish(t, watch, 100*time.Millisecond)
}

// The forward reads the same gate every other press reads, so a
// controller whose mark names another unit reaches this client with
// nothing.
func TestADelegateForwardsNothingFromAnUnfocusedRemote(t *testing.T) {
	events := fadeTopics()
	ic, watch := delegateCommander(t, []string{events})
	sendMark(ic, sofaFocus(), "cinema")
	sendActivity(t, ic, playerIdle)

	sendKey(t, ic, events, "KEY_UP", 1)

	noPublish(t, watch, 100*time.Millisecond)
	noMoment(t, watch, 100*time.Millisecond)
}

// A volume press still steps the unit's level under a delegate, because
// the level is the command pod's own and no client draws it.
func TestADelegateVolumePressStillStepsTheLevel(t *testing.T) {
	events := fadeTopics()
	ic, watch := delegateCommander(t, []string{events})
	sendActivity(t, ic, playerIdle)
	ic.handle(ic.volumeTopic, []byte(`{"level":40,"muted":false}`))

	sendVolumeUp(t, ic, events)

	publish := nextPublish(t, watch)
	mustMatch(t, publish.topic, ic.volumeTopic)
	mustMatch(t, string(publish.payload), `{"level":45,"muted":false}`)
}

// Under this operator's own controller a navigation press reaches no
// one, because the stock client draws no list.
func TestTheOwnControllerForwardsNoNavigationPress(t *testing.T) {
	events := fadeTopics()
	ic, watch := volumeCommander(t, []string{events})
	sendActivity(t, ic, playerIdle)

	sendKey(t, ic, events, "KEY_UP", 1)

	noPublish(t, watch, 100*time.Millisecond)
	noMoment(t, watch, 100*time.Millisecond)
}

// Under this operator's own client each of the three back keys brings
// the shade down, because a shell sends whichever one it was built
// with.
func TestTheOwnControllerBackKeysSleepTheScreen(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{name: "back", key: "KEY_BACK"},
		{name: "escape", key: "KEY_ESC"},
		{name: "exit", key: "KEY_EXIT"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			events := fadeTopics()
			ic, watch := volumeCommander(t, []string{events})
			sendActivity(t, ic, playerIdle)

			sendKey(t, ic, events, one.key, 1)

			mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)
			noPublish(t, watch, 100*time.Millisecond)
		})
	}
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
