package main

// These tests cover the idle command pod's fade: the quiet window it arms
// only while the unit plays nothing, the press that restarts it and
// lifts the shade, the status that leaves Idle, and the back press a
// person sleeps the screen with by hand.

import (
	"encoding/json"
	"testing"
	"time"
)

// idleWatch is everything one sidecar states, split by topic: the
// moments a client reads off the unit's screen topic, and every other
// message the sidecar publishes. Two channels, because a test about the
// shade and a test about the level read different halves, and neither
// should wait out the other's traffic.
type idleWatch struct {
	moments   chan brokerPublish
	published chan brokerPublish
}

// nextMoment returns the next moment the sidecar stated, on a bounded
// wait, and fails the test for a retained one, which playerScreenTopic
// rules out.
func nextMoment(t *testing.T, watch *idleWatch) screenMessage {
	t.Helper()
	return momentWithin(t, watch, 2*time.Second)
}

// momentWithin returns the next moment, and fails when none arrives
// inside the window. A test that proves a quiet window was not
// restarted reads with an allowance shorter than a restart would need.
//
// Every read checks the retain flag against the rule for the
// moment it carries, so no test can state a shade the broker drops or a
// moment the broker replays.
func momentWithin(t *testing.T, watch *idleWatch, window time.Duration) screenMessage {
	t.Helper()
	select {
	case publish := <-watch.moments:
		var message screenMessage
		mustSucceed(t, json.Unmarshal(publish.payload, &message))
		shade := message.Event == screenSleepEvent || message.Event == screenWakeEvent
		if publish.retained != shade {
			t.Errorf("the sidecar published %s with retained %v, and only the shade is retained",
				publish.payload, publish.retained)
		}
		return message
	case <-time.After(window):
		t.Fatalf("the sidecar stated no screen moment inside %s", window)
		return screenMessage{}
	}
}

// noMoment fails when the sidecar states any moment inside this window.
func noMoment(t *testing.T, watch *idleWatch, window time.Duration) {
	t.Helper()
	select {
	case publish := <-watch.moments:
		t.Fatalf("the sidecar stated %s, and should have stated nothing", publish.payload)
	case <-time.After(window):
	}
}

// nextPublish returns the next message the sidecar published anywhere but
// the screen topic, on a bounded wait.
func nextPublish(t *testing.T, watch *idleWatch) brokerPublish {
	t.Helper()
	select {
	case publish := <-watch.published:
		return publish
	case <-time.After(2 * time.Second):
		t.Fatal("the sidecar published nothing inside 2s")
		return brokerPublish{}
	}
}

// noPublish fails when the sidecar publishes anything inside this window.
func noPublish(t *testing.T, watch *idleWatch, window time.Duration) {
	t.Helper()
	select {
	case publish := <-watch.published:
		t.Fatalf("the sidecar published %+v, and should have published nothing", publish)
	case <-time.After(window):
	}
}

// fadeTopics builds the events topic of the one controller these tests
// press. The pod reads no other topic of that controller's on a press.
func fadeTopics() string {
	return remoteEventsTopic(defaultTopicBase, "house", "sofa")
}

// idleTestPlayer is the Player every idle command pod in these tests
// stands for, the same name its commands and status topics carry.
const idleTestPlayer = "theater"

// focusedRemotes turns the list of events topics a test states into the
// sidecar's own records, and marks every controller focused on this
// Player with its mark already caught up. That is the state a running
// pod holds once the retained marks arrive, so a test about the fade
// presses a controller that points here and says nothing about focus.
// The list's own order is the index the focus pulse carries, the way
// the operator's list is.
func focusedRemotes(t *testing.T, remotes []string) (map[string]idleRemote, map[string]focusMark) {
	t.Helper()
	records := map[string]idleRemote{}
	marks := map[string]focusMark{}
	for index, events := range remotes {
		namespace, name, ok := parseRemoteTopic(defaultTopicBase, events, "events")
		mustMatch(t, ok, true)
		records[events] = idleRemote{
			focus: remoteFocusTopic(defaultTopicBase, namespace, name),
			index: index,
		}
		marks[events] = focusMark{player: idleTestPlayer, caughtUp: true}
	}
	return records, marks
}

// fadingCommander builds one idle command pod for a Player, with its quiet
// window in milliseconds so the fade lands inside a test rather than
// ten minutes after it. The watch it returns holds both halves of what
// the sidecar sends: every moment on the screen topic, and every other
// message it publishes. A sidecar runs as long as its pod, so nothing
// stops its timer in production and the test stops it here: a window
// still armed when the test ends would otherwise state a moment into
// the next test's watch.
func fadingCommander(t *testing.T, fade time.Duration, remotes []string) (*idleCommander, *idleWatch) {
	t.Helper()
	records, marks := focusedRemotes(t, remotes)
	ic := &idleCommander{
		commandsTopic: playerCommandsTopic(defaultTopicBase, "house", idleTestPlayer),
		statusTopic:   playerStatusTopic(defaultTopicBase, "house", idleTestPlayer),
		screenTopic:   playerScreenTopic(defaultTopicBase, "house", idleTestPlayer),
		playerName:    idleTestPlayer,
		remotes:       records,
		marks:         marks,
		fadeAfter:     fade,
		// A pod that starts holds the on desire, the same value
		// the sidecar starts with on the metal.
		desire: panelDesireOn,
	}
	watch := &idleWatch{
		moments:   make(chan brokerPublish, 32),
		published: make(chan brokerPublish, 32),
	}
	ic.publish = func(topic string, payload []byte, retained bool) {
		message := brokerPublish{topic: topic, payload: append([]byte(nil), payload...), retained: retained}
		if topic == ic.screenTopic {
			watch.moments <- message
			return
		}
		watch.published <- message
	}
	t.Cleanup(func() {
		ic.mu.Lock()
		defer ic.mu.Unlock()
		ic.idle = false
		ic.rearmLocked()
	})
	return ic, watch
}

// sendActivity hands the sidecar one status off the unit's retained
// status topic, the way the operator publishes it.
func sendActivity(t *testing.T, ic *idleCommander, activity string) {
	t.Helper()
	ic.handle(ic.statusTopic, []byte(`{"activity":"`+activity+`"}`))
}

// sendEvent publishes one controller event on the events topic, the
// way the standing remote pod does.
func sendEvent(t *testing.T, ic *idleCommander, events string, event keyEvent) {
	t.Helper()
	payload, err := json.Marshal(event)
	mustSucceed(t, err)
	ic.handle(events, payload)
}

// sendPress presses play-pause, a key the idle command pod holds no
// row for. The tests about the fade press this one because it wakes
// the screen and restarts the quiet window and does nothing else, so a
// test reads the fade alone.
func sendPress(t *testing.T, ic *idleCommander, events string) {
	t.Helper()
	sendEvent(t, ic, events, keyEvent{Key: "KEY_PLAYPAUSE", Value: 1})
}

// sendBack presses back, one of the three keys that bring the shade
// down under this operator's own client.
func sendBack(t *testing.T, ic *idleCommander, events string) {
	t.Helper()
	sendEvent(t, ic, events, keyEvent{Key: "KEY_BACK", Value: 1})
}

// sendBackRelease lets back back up. The standing remote pod publishes
// this edge too, so the pod reads it and must do nothing with it.
func sendBackRelease(t *testing.T, ic *idleCommander, events string) {
	t.Helper()
	sendEvent(t, ic, events, keyEvent{Key: "KEY_BACK", Value: 0})
}

// A unit that plays nothing arms the quiet window, and the window running
// out states the sleep moment the client draws the shade on.
func TestIdleFadeSleepsAfterTheQuietWindow(t *testing.T) {
	ic, watch := fadingCommander(t, 20*time.Millisecond, nil)

	sendActivity(t, ic, playerIdle)

	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)
}

// A Play never sleeps the screen, so the window stays off while one runs.
func TestIdleFadeNeverArmsWhileAPlayRuns(t *testing.T) {
	ic, watch := fadingCommander(t, 20*time.Millisecond, nil)

	sendActivity(t, ic, playerPlaying)

	noMoment(t, watch, 100*time.Millisecond)
}

// A quiet window of zero is the cluster stating that this screen never dims
// on its own, so the window never arms whatever the unit does.
func TestIdleFadeNeverArmsAtZero(t *testing.T) {
	ic, watch := fadingCommander(t, 0, nil)

	sendActivity(t, ic, playerIdle)

	noMoment(t, watch, 100*time.Millisecond)
}

// A press restarts the window from the moment of the press, so a screen a
// person touches keeps the whole window again rather than the remainder of
// the last one.
func TestIdleFadeAPressResetsTheWindow(t *testing.T) {
	events := fadeTopics()
	ic, watch := fadingCommander(t, 100*time.Millisecond, []string{events})

	sendActivity(t, ic, playerIdle)
	time.Sleep(60 * time.Millisecond)
	sendPress(t, ic, events)

	// The first window ran out at 100ms. Nothing arrives through 130ms, so the
	// press moved it.
	noMoment(t, watch, 70*time.Millisecond)
	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)
}

// A status the operator republishes with the same activity
// leaves the window where it stands, so bus churn never holds the screen
// awake. The window runs 200ms and the republish lands at 120ms: the read
// allows 140ms, which the remaining 80ms fits and a restarted window does
// not.
func TestIdleFadeARepublishedStatusDoesNotRestartTheWindow(t *testing.T) {
	ic, watch := fadingCommander(t, 200*time.Millisecond, nil)

	sendActivity(t, ic, playerIdle)
	time.Sleep(120 * time.Millisecond)
	sendActivity(t, ic, playerIdle)

	mustMatch(t, momentWithin(t, watch, 140*time.Millisecond).Event, screenSleepEvent)
}

// ScreenPublish is one moment with the retain flag it went out
// under, which momentWithin reads but does not return.
type screenPublish struct {
	event    string
	retained bool
}

// NextScreenPublish returns the next moment and the flag it
// carried, on a bounded wait.
func nextScreenPublish(t *testing.T, watch *idleWatch) screenPublish {
	t.Helper()
	select {
	case publish := <-watch.moments:
		var message screenMessage
		mustSucceed(t, json.Unmarshal(publish.payload, &message))
		return screenPublish{event: message.Event, retained: publish.retained}
	case <-time.After(2 * time.Second):
		t.Fatal("the sidecar stated no screen moment inside 2s")
		return screenPublish{}
	}
}

// The shade is a state, so it goes out retained and a client that
// restarts reads the shade it left rather than waking lit. The focus and
// the present are moments, so they do not, and a restart replays neither.
func TestIdleScreenRetainsTheShadeAndNothingElse(t *testing.T) {
	events := fadeTopics()
	ic, watch := fadingCommander(t, time.Hour, []string{events})
	sendActivity(t, ic, playerIdle)

	sendBack(t, ic, events)
	mustMatch(t, nextScreenPublish(t, watch), screenPublish{event: screenSleepEvent, retained: true})

	sendBack(t, ic, events)
	mustMatch(t, nextScreenPublish(t, watch), screenPublish{event: screenWakeEvent, retained: true})

	sendMark(ic, sofaFocus(), idleTestPlayer)
	mustMatch(t, nextScreenPublish(t, watch), screenPublish{event: screenFocusEvent})

	ic.handle(ic.commandsTopic, mustEncode(t, mediaCommand{Action: actionRePresent}))
	mustMatch(t, nextScreenPublish(t, watch), screenPublish{event: screenPresentEvent})
}

// Every bus session restamps the shade the sidecar holds, the way
// it restamps the panel desire. A pod that rolled while the screen was
// dark starts awake and overwrites the sleep the process before it left,
// so the screen comes back lit instead of holding a shade nothing will
// lift.
func TestIdleScreenRestampsTheShadeOnEveryBusSession(t *testing.T) {
	cases := []struct {
		name   string
		asleep bool
		want   string
	}{
		{name: "a pod that starts awake", want: screenWakeEvent},
		{name: "a session that returns to a dark screen", asleep: true, want: screenSleepEvent},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			ic, watch := fadingCommander(t, time.Hour, nil)
			ic.mu.Lock()
			ic.asleep = one.asleep
			ic.mu.Unlock()

			ic.onBusConnect(nil)

			mustMatch(t, nextScreenPublish(t, watch), screenPublish{event: one.want, retained: true})
		})
	}
}

// A press on a sleeping screen states wake, whether or not the pod
// holds a row for that key. The press is the person, so it is the wake
// signal.
func TestIdleFadeAPressWakesASleepingScreen(t *testing.T) {
	events := fadeTopics()
	ic, watch := fadingCommander(t, 20*time.Millisecond, []string{events})

	sendActivity(t, ic, playerIdle)
	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)
	sendPress(t, ic, events)

	mustMatch(t, nextMoment(t, watch).Event, screenWakeEvent)
}

// A status that leaves Idle states wake, so a Play started from another
// room shows the film and not a black screen.
func TestIdleFadeAStatusThatLeavesIdleWakes(t *testing.T) {
	ic, watch := fadingCommander(t, 20*time.Millisecond, nil)

	sendActivity(t, ic, playerIdle)
	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)
	sendActivity(t, ic, playerStarting)

	mustMatch(t, nextMoment(t, watch).Event, screenWakeEvent)
	noMoment(t, watch, 100*time.Millisecond)
}

// A press named back, on a unit that plays nothing, states sleep at once
// rather than waiting out the window.
func TestIdleBackSleepsTheScreenAtOnce(t *testing.T) {
	events := fadeTopics()
	ic, watch := fadingCommander(t, time.Hour, []string{events})

	sendActivity(t, ic, playerIdle)
	sendBack(t, ic, events)

	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)
}

// The same back press states wake again, because any press wakes a
// sleeping screen. So one button works the screen from either side.
func TestIdleBackWakesTheScreenItSlept(t *testing.T) {
	events := fadeTopics()
	ic, watch := fadingCommander(t, time.Hour, []string{events})

	sendActivity(t, ic, playerIdle)
	sendBack(t, ic, events)
	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)
	sendBack(t, ic, events)

	mustMatch(t, nextMoment(t, watch).Event, screenWakeEvent)
}

// Back sleeps nothing while a Play runs. The film owns the screen, and back
// means what the display makes of it there.
func TestIdleBackSleepsNothingWhileAPlayRuns(t *testing.T) {
	events := fadeTopics()
	ic, watch := fadingCommander(t, 20*time.Millisecond, []string{events})

	sendActivity(t, ic, playerPlaying)
	sendBack(t, ic, events)

	noMoment(t, watch, 100*time.Millisecond)
}

// The pod holds one table for every controller, and a key with no row
// in it restarts the quiet window and does nothing else.
func TestIdleAKeyThePodHoldsNoRowForStatesNothing(t *testing.T) {
	events := fadeTopics()
	ic, watch := fadingCommander(t, time.Hour, []string{events})

	sendActivity(t, ic, playerIdle)
	sendPress(t, ic, events)

	noMoment(t, watch, 100*time.Millisecond)
	noPublish(t, watch, 100*time.Millisecond)
}

// The two remote lists travel one per line and stay aligned by
// position, so each events topic reads the mark of its own controller,
// and its line number is the index the focus pulse carries.
func TestIdleRemoteMapPairsTheTwoLists(t *testing.T) {
	remotes := idleRemoteMap(
		"events/sofa\nevents/armchair",
		"focus/sofa\nfocus/armchair")

	mustMatch(t, len(remotes), 2)
	mustMatch(t, remotes["events/sofa"], idleRemote{focus: "focus/sofa", index: 0})
	mustMatch(t, remotes["events/armchair"], idleRemote{focus: "focus/armchair", index: 1})
}

// A blank line, and a focus list shorter than the events list, leave
// that controller with no focus topic rather than shifting the
// pairing.
func TestIdleRemoteMapLeavesAMissingFocusTopicBlank(t *testing.T) {
	remotes := idleRemoteMap("events/sofa\nevents/armchair", "\nfocus/armchair")

	mustMatch(t, remotes["events/sofa"].focus, "")
	mustMatch(t, remotes["events/armchair"].focus, "focus/armchair")
}

// An unset or unreadable quiet window fades nothing, because the operator
// settles this field for every Player and a sidecar that guessed would dim
// a screen the cluster never asked it to.
func TestIdleFadeAfterReadsTheSeconds(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{name: "the resolved default", value: "600", want: 600 * time.Second},
		{name: "a minute", value: "60", want: time.Minute},
		{name: "the fade turned off", value: "0", want: 0},
		{name: "nothing at all", value: "", want: 0},
		{name: "a word", value: "soon", want: 0},
		{name: "a negative", value: "-5", want: 0},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, idleFadeAfter(one.value), one.want)
		})
	}
}

// A button press and the release that follows it are one act by one person.
// The standing remote pod publishes both edges, so the release must leave
// the shade the press stated exactly where it is. Otherwise back sleeps the
// screen and its own release wakes it a tenth of a second later.
func TestIdleBackHoldsTheScreenAsleepThroughTheRelease(t *testing.T) {
	events := fadeTopics()
	ic, watch := fadingCommander(t, time.Hour, []string{events})

	sendActivity(t, ic, playerIdle)
	sendBack(t, ic, events)
	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)
	sendBackRelease(t, ic, events)

	noMoment(t, watch, 100*time.Millisecond)
}

// A release on a sleeping screen is a control coming back up, not a person
// reaching for one, so the screen stays dark.
func TestIdleFadeAReleaseDoesNotWakeASleepingScreen(t *testing.T) {
	events := fadeTopics()
	ic, watch := fadingCommander(t, 20*time.Millisecond, []string{events})

	sendActivity(t, ic, playerIdle)
	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)
	sendBackRelease(t, ic, events)

	noMoment(t, watch, 100*time.Millisecond)
}

// A release restarts nothing, so the sleep lands on the schedule the last
// press set. The window here runs 200ms and the release lands at 120ms: the
// read allows 140ms, which the remaining 80ms fits and a restarted 200ms
// window does not.
func TestIdleFadeAReleaseDoesNotRestartTheWindow(t *testing.T) {
	events := fadeTopics()
	ic, watch := fadingCommander(t, 200*time.Millisecond, []string{events})

	sendActivity(t, ic, playerIdle)
	time.Sleep(120 * time.Millisecond)
	sendBackRelease(t, ic, events)

	mustMatch(t, momentWithin(t, watch, 140*time.Millisecond).Event, screenSleepEvent)
}

// The control held down is the person: the press and the repeat the
// standing pod sends while a person holds a key. The release is
// neither. The pod reads the value alone, because the standing pod
// already named the key.
func TestIsPressEdgeReadsThePressAndTheRepeat(t *testing.T) {
	cases := []struct {
		name  string
		event keyEvent
		want  bool
	}{
		{name: "a key down", event: keyEvent{Key: "KEY_UP", Value: 1}, want: true},
		{name: "a held key repeating", event: keyEvent{Key: "KEY_UP", Value: 2}, want: true},
		{name: "a key up", event: keyEvent{Key: "KEY_UP", Value: 0}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, isPressEdge(one.event), one.want)
		})
	}
}

// A payload that does not decode is not a person, so it neither wakes the
// screen nor restarts the quiet window.
func TestIdleFadeIgnoresAnEventThatDoesNotDecode(t *testing.T) {
	events := fadeTopics()
	ic, watch := fadingCommander(t, 20*time.Millisecond, []string{events})

	sendActivity(t, ic, playerIdle)
	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)
	ic.handle(events, []byte("not json"))

	noMoment(t, watch, 100*time.Millisecond)
}
