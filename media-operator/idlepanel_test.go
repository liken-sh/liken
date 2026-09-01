package main

// These tests cover the idle command pod's panel half: the second window
// that states the off desire, the wake that states the on desire, and
// the retained topic both stand on. The sidecar writes no hardware,
// so nothing here holds a wire.

import (
	"encoding/json"
	"testing"
	"time"
)

// panelPublish is one retained publish of the panel desire.
type panelPublish struct {
	topic    string
	desire   string
	retained bool
}

// panelSetup is the sidecar's panel policy for one test.
type panelSetup struct {
	fade    time.Duration
	off     time.Duration
	remotes map[string]string
}

// panelCommander builds one idle command pod with both windows in
// milliseconds, so the fade and the off window land inside a test.
func panelCommander(t *testing.T, setup panelSetup) (*idleCommander, *idleWatch) {
	t.Helper()
	ic, watch := fadingCommander(t, setup.fade, setup.remotes)
	ic.offAfter = setup.off
	ic.panelTopic = playerPanelTopic(defaultTopicBase, "house", "theater")
	return ic, watch
}

// nextPanel returns the next desire the sidecar published, on a
// bounded wait.
func nextPanel(t *testing.T, watch *idleWatch) panelPublish {
	t.Helper()
	publish := nextPublish(t, watch)
	var desire panelDesire
	mustSucceed(t, json.Unmarshal(publish.payload, &desire))
	return panelPublish{topic: publish.topic, desire: desire.Desire, retained: publish.retained}
}

// noPanel fails when the sidecar publishes any desire inside this
// window.
func noPanel(t *testing.T, watch *idleWatch, window time.Duration) {
	t.Helper()
	noPublish(t, watch, window)
}

// The off window states the off desire, retained on the unit's panel
// topic, and that message is the whole of what the sidecar does about
// the panel.
func TestIdlePanelStatesTheOffDesireAtTheOffWindow(t *testing.T) {
	ic, watch := panelCommander(t, panelSetup{
		fade: 20 * time.Millisecond, off: 40 * time.Millisecond,
	})

	sendActivity(t, ic, playerIdle)
	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)

	mustMatch(t, nextPanel(t, watch), panelPublish{
		topic:    playerPanelTopic(defaultTopicBase, "house", "theater"),
		desire:   panelDesireOff,
		retained: true,
	})
}

// A press states the on desire, and the operator lifts the override
// the off desire put on the screen's Display.
func TestIdlePanelStatesTheOnDesireOnAPress(t *testing.T) {
	events, _ := fadeTopics()
	ic, watch := panelCommander(t, panelSetup{
		fade: 20 * time.Millisecond, off: 320 * time.Millisecond,
		remotes: map[string]string{events: ""},
	})

	sendActivity(t, ic, playerIdle)
	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)
	mustMatch(t, nextPanel(t, watch).desire, panelDesireOff)

	sendPress(t, ic, events)

	mustMatch(t, nextMoment(t, watch).Event, screenWakeEvent)
	mustMatch(t, nextPanel(t, watch).desire, panelDesireOn)
	// The quiet window starts over on the press, so the shade comes
	// down again and the test reads it.
	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)
}

// A status that leaves Idle states the on desire the same way a press
// does, so a Play started from another room lights the screen.
func TestIdlePanelStatesTheOnDesireOnAPlay(t *testing.T) {
	ic, watch := panelCommander(t, panelSetup{
		fade: 20 * time.Millisecond, off: 40 * time.Millisecond,
	})

	sendActivity(t, ic, playerIdle)
	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)
	mustMatch(t, nextPanel(t, watch).desire, panelDesireOff)

	sendActivity(t, ic, playerStarting)

	mustMatch(t, nextMoment(t, watch).Event, screenWakeEvent)
	mustMatch(t, nextPanel(t, watch).desire, panelDesireOn)
}

// Every bus session states the desire the sidecar holds now. A pod
// that returns while the panel is dark states on, which is the
// failure a sidecar that remembered brightness in its own memory
// could not survive.
func TestIdlePanelStatesItsDesireOnEveryBusSession(t *testing.T) {
	ic, watch := panelCommander(t, panelSetup{fade: 20 * time.Millisecond, off: 40 * time.Millisecond})

	ic.onBusConnect(nil)

	mustMatch(t, nextPanel(t, watch), panelPublish{
		topic:    playerPanelTopic(defaultTopicBase, "house", "theater"),
		desire:   panelDesireOn,
		retained: true,
	})
}

// A window of zero is the panel never going dark, whatever the unit
// does, so the sidecar states nothing at all.
func TestIdlePanelNeverDarkensAtZero(t *testing.T) {
	ic, watch := panelCommander(t, panelSetup{fade: 20 * time.Millisecond})

	sendActivity(t, ic, playerIdle)
	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)

	noPanel(t, watch, 200*time.Millisecond)
}

// The panel goes dark behind a black screen and never in front of a
// lit one. The fade lands at 40ms and the off window at 200ms, so no
// desire changes in the 120ms between them.
func TestIdlePanelWaitsForTheFade(t *testing.T) {
	ic, watch := panelCommander(t, panelSetup{
		fade: 40 * time.Millisecond, off: 200 * time.Millisecond,
	})

	sendActivity(t, ic, playerIdle)
	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)

	noPanel(t, watch, 120*time.Millisecond)
	mustMatch(t, nextPanel(t, watch).desire, panelDesireOff)
}

// A press inside the second window cancels it and starts both windows
// again, so a screen a person touched keeps its panel lit. The window
// was 50ms from running out at the press, and the read allows twice
// that.
func TestIdlePanelAPressBeforeTheWindowKeepsThePanelLit(t *testing.T) {
	events, _ := fadeTopics()
	ic, watch := panelCommander(t, panelSetup{
		fade: 20 * time.Millisecond, off: 220 * time.Millisecond,
		remotes: map[string]string{events: ""},
	})

	sendActivity(t, ic, playerIdle)
	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)
	time.Sleep(150 * time.Millisecond)
	sendPress(t, ic, events)
	mustMatch(t, nextMoment(t, watch).Event, screenWakeEvent)
	mustMatch(t, nextMoment(t, watch).Event, screenSleepEvent)

	noPanel(t, watch, 100*time.Millisecond)
}

// The off window reads the seconds the operator resolved, and it
// never lands before the fade, so the clamp is what keeps a dark
// panel behind a black screen.
func TestIdleOffAfterReadsTheSecondsAndClamps(t *testing.T) {
	cases := []struct {
		name  string
		value string
		fade  time.Duration
		want  time.Duration
	}{
		{name: "half an hour past a ten-minute fade", value: "1800",
			fade: 600 * time.Second, want: 1800 * time.Second},
		{name: "a minute clamped up to the fade", value: "60",
			fade: 600 * time.Second, want: 600 * time.Second},
		{name: "the same window as the fade", value: "600",
			fade: 600 * time.Second, want: 600 * time.Second},
		{name: "the panel turned off", value: "0", fade: 600 * time.Second},
		{name: "nothing at all", value: "", fade: 600 * time.Second},
		{name: "a word", value: "soon", fade: 600 * time.Second},
		{name: "a negative", value: "-5", fade: 600 * time.Second},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, idleOffAfter(one.value, one.fade), one.want)
		})
	}
}
