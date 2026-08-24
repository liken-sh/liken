package main

// These tests cover the idle sidecar's hardware half: the second
// window that writes the panel dark, the wake that writes it back up,
// the bounded ladder behind a wire that fails, and the panel state
// each transition publishes.

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// vcpWrite is one write the sidecar made on the wire.
type vcpWrite struct {
	Code  byte
	Value uint16
}

// scriptedWire is a stand-in for the panel's DDC wire. It answers
// every read with the value the test gave it, hands each write to the
// test, and fails every write from the moment the test says so.
type scriptedWire struct {
	mutex   sync.Mutex
	value   uint16
	readErr error
	failing bool
	made    []vcpWrite
	writes  chan vcpWrite
}

func newScriptedWire(value uint16) *scriptedWire {
	return &scriptedWire{value: value, writes: make(chan vcpWrite, 64)}
}

// unreadableWire is a wire whose reads fail, the panel that answers
// no brightness.
func unreadableWire() *scriptedWire {
	wire := newScriptedWire(0)
	wire.readErr = ErrDDCUnsupportedVCP
	return wire
}

func (w *scriptedWire) GetVCP(code byte) (uint16, error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.value, w.readErr
}

func (w *scriptedWire) SetVCP(code byte, value uint16) error {
	write := vcpWrite{Code: code, Value: value}
	w.mutex.Lock()
	w.made = append(w.made, write)
	failing := w.failing
	w.mutex.Unlock()
	w.writes <- write
	if failing {
		return ErrDDCNoAnswer
	}
	return nil
}

// failWrites makes every write from here on fail, the panel that
// stopped answering while the screen slept.
func (w *scriptedWire) failWrites() {
	w.mutex.Lock()
	w.failing = true
	w.mutex.Unlock()
}

// countWrites is how many times the sidecar made exactly this write,
// which is how a test counts one ladder's tries and no other write.
func (w *scriptedWire) countWrites(want vcpWrite) int {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	count := 0
	for _, write := range w.made {
		if write == want {
			count++
		}
	}
	return count
}

// next returns the next write the sidecar made, on a bounded wait,
// so a sidecar that writes nothing fails the test instead of hanging
// it.
func (w *scriptedWire) next(t *testing.T) vcpWrite {
	t.Helper()
	return w.nextWithin(t, 2*time.Second)
}

func (w *scriptedWire) nextWithin(t *testing.T, window time.Duration) vcpWrite {
	t.Helper()
	select {
	case write := <-w.writes:
		return write
	case <-time.After(window):
		t.Fatalf("the sidecar wrote nothing on the wire inside %s", window)
		return vcpWrite{}
	}
}

// quiet fails when the sidecar touches the wire at all inside this
// window.
func (w *scriptedWire) quiet(t *testing.T, window time.Duration) {
	t.Helper()
	select {
	case write := <-w.writes:
		t.Fatalf("the sidecar wrote %+v, and should have written nothing", write)
	case <-time.After(window):
	}
}

// panelPublish is one retained publish of the panel state.
type panelPublish struct {
	topic    string
	state    string
	retained bool
}

// panelSetup is the sidecar's panel policy for one test.
type panelSetup struct {
	fade    time.Duration
	off     time.Duration
	mode    string
	wire    panelWire
	remotes map[string]string
}

// panelCommander builds one idle sidecar that holds a panel wire,
// with both windows in milliseconds so the fade and the off land
// inside a test. The channel it returns carries every panel state the
// sidecar published.
func panelCommander(t *testing.T, setup panelSetup) (*idleCommander, chan panelPublish) {
	t.Helper()
	useWakeInterval(t, time.Millisecond)
	ic := fadingCommander(t, setup.fade, setup.remotes)
	ic.wire = setup.wire
	ic.offAfter = setup.off
	ic.offMode = setup.mode
	ic.panelTopic = playerPanelTopic(defaultTopicBase, "house", "theater")
	published := make(chan panelPublish, 32)
	ic.publish = func(topic string, payload []byte, retained bool) {
		var report panelReport
		mustSucceed(t, json.Unmarshal(payload, &report))
		published <- panelPublish{topic: topic, state: report.State, retained: retained}
	}
	return ic, published
}

// useWakeInterval runs the wake ladder in milliseconds rather than
// the twenty seconds it takes on the metal.
func useWakeInterval(t *testing.T, interval time.Duration) {
	t.Helper()
	was := panelWakeInterval
	t.Cleanup(func() { panelWakeInterval = was })
	panelWakeInterval = interval
}

// nextPanel returns the next panel state the sidecar published, on a
// bounded wait.
func nextPanel(t *testing.T, published chan panelPublish) panelPublish {
	t.Helper()
	select {
	case publish := <-published:
		return publish
	case <-time.After(2 * time.Second):
		t.Fatal("the sidecar published no panel state inside 2s")
		return panelPublish{}
	}
}

// noPanel fails when the sidecar publishes any panel state inside
// this window.
func noPanel(t *testing.T, published chan panelPublish, window time.Duration) {
	t.Helper()
	select {
	case publish := <-published:
		t.Fatalf("the sidecar published %+v, and should have published nothing", publish)
	case <-time.After(window):
	}
}

// A backlight panel goes to zero at the off window, and the state
// stands retained on the unit's panel topic.
func TestIdlePanelBacklightGoesDarkAtTheOffWindow(t *testing.T) {
	fake := startFakeMPV(t)
	wire := newScriptedWire(70)
	ic, published := panelCommander(t, panelSetup{
		fade: 20 * time.Millisecond, off: 40 * time.Millisecond,
		mode: offModeBacklight, wire: wire,
	})

	sendActivity(t, ic, fake, playerIdle)
	mustMatch(t, fake.next(t), sleepCommand)

	mustMatch(t, wire.next(t), vcpWrite{Code: vcpBrightness, Value: 0})
	mustMatch(t, nextPanel(t, published), panelPublish{
		topic:    playerPanelTopic(defaultTopicBase, "house", "theater"),
		state:    panelBacklightOff,
		retained: true,
	})
}

// The power mode writes DPM off, which is deeper than the backlight
// and which a Player states only for a drilled panel.
func TestIdlePanelPowerModeWritesDPMOff(t *testing.T) {
	fake := startFakeMPV(t)
	wire := newScriptedWire(70)
	ic, published := panelCommander(t, panelSetup{
		fade: 20 * time.Millisecond, off: 40 * time.Millisecond,
		mode: offModePower, wire: wire,
	})

	sendActivity(t, ic, fake, playerIdle)
	mustMatch(t, fake.next(t), sleepCommand)

	mustMatch(t, wire.next(t), vcpWrite{Code: vcpPowerMode, Value: powerModeOff})
	mustMatch(t, nextPanel(t, published).state, panelOff)
}

// The wake writes pixels first and the panel second, and it puts
// back the brightness the sidecar read before it wrote zero.
func TestIdlePanelWakeRestoresTheRememberedBrightness(t *testing.T) {
	events, _ := fadeTopics()
	fake := startFakeMPV(t)
	wire := newScriptedWire(70)
	ic, published := panelCommander(t, panelSetup{
		fade: 20 * time.Millisecond, off: 320 * time.Millisecond,
		mode: offModeBacklight, wire: wire, remotes: map[string]string{events: ""},
	})

	sendActivity(t, ic, fake, playerIdle)
	mustMatch(t, fake.next(t), sleepCommand)
	mustMatch(t, wire.next(t), vcpWrite{Code: vcpBrightness, Value: 0})
	mustMatch(t, nextPanel(t, published).state, panelBacklightOff)

	sendPress(t, ic, events)

	mustMatch(t, fake.next(t), wakeCommand)
	mustMatch(t, wire.next(t), vcpWrite{Code: vcpBrightness, Value: 70})
	mustMatch(t, nextPanel(t, published).state, panelOn)
	// The quiet window starts over on the press, so the shade comes
	// down again and the test reads it.
	mustMatch(t, fake.next(t), sleepCommand)
}

// A panel that answers no brightness comes back at full, because a
// lit screen is what the person pressed a button for.
func TestIdlePanelWakeWritesFullWhenNoBrightnessWasRead(t *testing.T) {
	events, _ := fadeTopics()
	fake := startFakeMPV(t)
	wire := unreadableWire()
	ic, _ := panelCommander(t, panelSetup{
		fade: 20 * time.Millisecond, off: 320 * time.Millisecond,
		mode: offModeBacklight, wire: wire, remotes: map[string]string{events: ""},
	})

	sendActivity(t, ic, fake, playerIdle)
	mustMatch(t, fake.next(t), sleepCommand)
	mustMatch(t, wire.next(t), vcpWrite{Code: vcpBrightness, Value: 0})
	sendPress(t, ic, events)

	mustMatch(t, fake.next(t), wakeCommand)
	mustMatch(t, wire.next(t), vcpWrite{Code: vcpBrightness, Value: defaultPanelBrightness})
	mustMatch(t, fake.next(t), sleepCommand)
}

// A status that leaves Idle wakes the panel the same way a press
// does, so a Play started from another room lights the screen.
func TestIdlePanelPowerModeWakesOnAPlay(t *testing.T) {
	fake := startFakeMPV(t)
	wire := newScriptedWire(70)
	ic, published := panelCommander(t, panelSetup{
		fade: 20 * time.Millisecond, off: 40 * time.Millisecond,
		mode: offModePower, wire: wire,
	})

	sendActivity(t, ic, fake, playerIdle)
	mustMatch(t, fake.next(t), sleepCommand)
	mustMatch(t, wire.next(t), vcpWrite{Code: vcpPowerMode, Value: powerModeOff})
	mustMatch(t, nextPanel(t, published).state, panelOff)

	sendActivity(t, ic, fake, playerStarting)

	mustMatch(t, fake.next(t), wakeCommand)
	mustMatch(t, wire.next(t), vcpWrite{Code: vcpPowerMode, Value: powerModeOn})
	mustMatch(t, nextPanel(t, published).state, panelOn)
}

// A wake write that keeps failing runs the bounded ladder and then
// stops. Twenty tries and no more, and the panel reads Unresponsive,
// so a person reads in kubectl that the wire went quiet.
func TestIdlePanelAFailedWakeEndsOnTheBoundedLadder(t *testing.T) {
	events, _ := fadeTopics()
	fake := startFakeMPV(t)
	wire := newScriptedWire(70)
	ic, published := panelCommander(t, panelSetup{
		fade: 20 * time.Millisecond, off: 320 * time.Millisecond,
		mode: offModeBacklight, wire: wire, remotes: map[string]string{events: ""},
	})

	sendActivity(t, ic, fake, playerIdle)
	mustMatch(t, fake.next(t), sleepCommand)
	mustMatch(t, wire.next(t), vcpWrite{Code: vcpBrightness, Value: 0})
	mustMatch(t, nextPanel(t, published).state, panelBacklightOff)

	wire.failWrites()
	sendPress(t, ic, events)
	mustMatch(t, fake.next(t), wakeCommand)

	mustMatch(t, nextPanel(t, published).state, panelUnresponsive)
	mustMatch(t, wire.countWrites(vcpWrite{Code: vcpBrightness, Value: 70}), panelWakeAttempts)
	mustMatch(t, fake.next(t), sleepCommand)
}

// The wire is the gate. A Player that states no control device holds
// none, so the fade runs and the sidecar writes no hardware and
// publishes no panel state.
func TestIdlePanelWritesNothingWithoutAWire(t *testing.T) {
	fake := startFakeMPV(t)
	ic, published := panelCommander(t, panelSetup{
		fade: 20 * time.Millisecond, off: 40 * time.Millisecond,
		mode: offModeBacklight,
	})

	sendActivity(t, ic, fake, playerIdle)
	mustMatch(t, fake.next(t), sleepCommand)

	noPanel(t, published, 200*time.Millisecond)
}

// A window of zero is the panel never going dark, whatever the unit
// does, because darkening hardware is opt-in twice.
func TestIdlePanelNeverDarkensAtZero(t *testing.T) {
	fake := startFakeMPV(t)
	wire := newScriptedWire(70)
	ic, _ := panelCommander(t, panelSetup{
		fade: 20 * time.Millisecond, mode: offModeBacklight, wire: wire,
	})

	sendActivity(t, ic, fake, playerIdle)
	mustMatch(t, fake.next(t), sleepCommand)

	wire.quiet(t, 200*time.Millisecond)
}

// The panel goes dark behind a black screen and never in front of a
// lit one. The fade lands at 40ms and the panel at 200ms, so nothing
// reaches the wire in the 120ms between them.
func TestIdlePanelWaitsForTheFade(t *testing.T) {
	fake := startFakeMPV(t)
	wire := newScriptedWire(70)
	ic, _ := panelCommander(t, panelSetup{
		fade: 40 * time.Millisecond, off: 200 * time.Millisecond,
		mode: offModeBacklight, wire: wire,
	})

	sendActivity(t, ic, fake, playerIdle)
	mustMatch(t, fake.next(t), sleepCommand)

	wire.quiet(t, 120*time.Millisecond)
	mustMatch(t, wire.next(t), vcpWrite{Code: vcpBrightness, Value: 0})
}

// A press inside the second window cancels it and starts both
// windows again, so a screen a person touched keeps its panel lit.
// The panel was 50ms from going dark at the press, and the read
// allows twice that.
func TestIdlePanelAPressBeforeTheWindowKeepsThePanelLit(t *testing.T) {
	events, _ := fadeTopics()
	fake := startFakeMPV(t)
	wire := newScriptedWire(70)
	ic, _ := panelCommander(t, panelSetup{
		fade: 20 * time.Millisecond, off: 220 * time.Millisecond,
		mode: offModeBacklight, wire: wire, remotes: map[string]string{events: ""},
	})

	sendActivity(t, ic, fake, playerIdle)
	mustMatch(t, fake.next(t), sleepCommand)
	time.Sleep(150 * time.Millisecond)
	sendPress(t, ic, events)
	mustMatch(t, fake.next(t), wakeCommand)
	mustMatch(t, fake.next(t), sleepCommand)

	wire.quiet(t, 100*time.Millisecond)
}

// The hardware window reads the seconds the operator resolved, and
// it never lands before the fade, so the clamp is what keeps a dark
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

// Only power is deeper than the backlight, and every other value
// takes the backlight, the state that always answers DDC.
func TestIdleOffModeReadsTheMode(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{name: "the deeper mode", value: offModePower, want: offModePower},
		{name: "the stated backlight", value: offModeBacklight, want: offModeBacklight},
		{name: "nothing at all", value: "", want: offModeBacklight},
		{name: "a word the API does not carry", value: "dark", want: offModeBacklight},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			mustMatch(t, idleOffMode(one.value), one.want)
		})
	}
}
