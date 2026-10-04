package main

// The volume engine: the pending target, the pacing of the asks, the
// wait for the device's report, and the relay onto the unit's topic.
// Each test runs on the fake clock of a synctest bubble, so a wait of
// one pacing interval or one second takes no real time. The engine's
// two outputs are recorded by the deskRecorder below: every ask it
// writes and every level it publishes.

import (
	"io"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// deskRecorder keeps what the engine wrote and published. The engine
// writes from its timers, so the record takes a lock of its own.
type deskRecorder struct {
	mutex     sync.Mutex
	asks      []string
	published []string
}

func (r *deskRecorder) write(_ string, device volumeDevice, ask VolumeAsk) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.asks = append(r.asks, device.name+" "+describeLevel(ask.Level, ask.Mute))
	return nil
}

func (r *deskRecorder) publish(_ string, payload []byte) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.published = append(r.published, string(payload))
}

// forget drops what the recorder has published so far, so a test reads
// what follows.
func (r *deskRecorder) forget() {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.published = nil
}

func (r *deskRecorder) written() []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return append([]string(nil), r.asks...)
}

func (r *deskRecorder) relayed() []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return append([]string(nil), r.published...)
}

// The unit these tests press, and its Denon: max 72, a half step, and
// a report of 45.5.
const engineUnit = "house/theater"

func denon(level float64) volumeDevice {
	return volumeDevice{kind: deviceReceiver, name: "den-receiver", max: 72, step: 0.5, level: level, reported: true}
}

// engineFor builds an engine whose unit has the given devices and has
// published its level once, so each test reads only what its own
// presses and reports publish.
func engineFor(devices ...volumeDevice) (*volumeEngine, *deskRecorder) {
	recorder := &deskRecorder{}
	engine := newVolumeEngine(recorder.write, recorder.publish, io.Discard)
	engine.observe(engineUnit, devices, true)
	recorder.forget()
	return engine, recorder
}

var stepDown = volumeChange{step: -1}

// A held key moves the target one step for each press and repeat,
// from the target and not from the report. The first ask goes out at
// once, and the presses inside the pacing interval write one ask at its
// end, with the last target.
func TestAHeldKeyStepsFromThePendingTargetAndPacesTheAsks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		engine, recorder := engineFor(denon(45.5))

		engine.press(engineUnit, stepDown, "press", false)
		synctest.Wait()
		time.Sleep(40 * time.Millisecond)
		engine.press(engineUnit, stepDown, "repeat", true)
		time.Sleep(40 * time.Millisecond)
		engine.press(engineUnit, stepDown, "repeat", true)
		synctest.Wait()
		mustMatchAll(t, recorder.written(), []string{"den-receiver 45"})

		time.Sleep(volumePacing)
		synctest.Wait()

		mustMatchAll(t, recorder.written(), []string{"den-receiver 45", "den-receiver 44"})
		mustMatchAll(t, recorder.relayed(), []string{
			`{"level":0.625,"muted":false}`,
			`{"level":0.6181,"muted":false}`,
			`{"level":0.6111,"muted":false}`,
		})
	})
}

// The receiver's reports of the earlier asks arrive after the later
// presses moved the target. None of them goes on the topic, so the
// indicator never moves up while the key is held down. The report of the
// target ends the wait, and the next press steps from the target.
func TestReportsOfEarlierAsksAreNotPublished(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		engine, recorder := engineFor(denon(45.5))
		engine.press(engineUnit, stepDown, "press", false)
		engine.press(engineUnit, stepDown, "repeat", true)
		recorder.forget()

		engine.observe(engineUnit, []volumeDevice{denon(45)}, true)
		engine.observe(engineUnit, []volumeDevice{denon(44.5)}, true)
		time.Sleep(2 * volumeSettleWait)
		synctest.Wait()

		mustMatch(t, len(recorder.relayed()), 0)
	})
}

// A target the device never reports, as when a receiver's own limit is
// below it, is dropped after one second. The topic then carries what
// the device reports, and the next press steps from that report.
func TestATargetTheDeviceNeverReportsGivesUpAfterOneSecond(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		engine, recorder := engineFor(denon(45.5))
		engine.press(engineUnit, volumeChange{step: 1}, "press", false)
		engine.observe(engineUnit, []volumeDevice{denon(45.5)}, true)
		recorder.forget()

		time.Sleep(volumeSettleWait)
		synctest.Wait()
		mustMatchAll(t, recorder.relayed(), []string{`{"level":0.6319,"muted":false}`})

		time.Sleep(volumePacing)
		engine.press(engineUnit, stepDown, "press", false)
		synctest.Wait()
		mustMatch(t, recorder.written()[len(recorder.written())-1], "den-receiver 45")
	})
}

// A report that arrives while no target is pending is a change a person
// made at the device, such as a turn of the receiver's knob. The topic
// carries it at once, and the next press steps from it.
func TestAKnobTurnIsPublishedLive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		engine, recorder := engineFor(denon(45.5))

		engine.observe(engineUnit, []volumeDevice{denon(46.5)}, true)
		engine.observe(engineUnit, []volumeDevice{denon(46.5)}, true)
		mustMatchAll(t, recorder.relayed(), []string{`{"level":0.6458,"muted":false}`})

		engine.press(engineUnit, volumeChange{step: 1}, "press", false)
		synctest.Wait()
		mustMatchAll(t, recorder.written(), []string{"den-receiver 47"})
	})
}

// An ask never moves the target past the device's max or below zero.
// A press at the end of the scale writes nothing, and the topic carries
// the level again, so the screens draw the indicator as the feedback.
func TestAPressAtTheEndOfTheScaleStaysThere(t *testing.T) {
	cases := []struct {
		name    string
		level   float64
		change  volumeChange
		written []string
	}{
		{name: "up at max", level: 72, change: volumeChange{step: 1}},
		{name: "up to max", level: 71.8, change: volumeChange{step: 1}, written: []string{"den-receiver 72"}},
		{name: "down at zero", level: 0, change: stepDown},
		{name: "down to zero", level: 0.2, change: stepDown, written: []string{"den-receiver 0"}},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				engine, recorder := engineFor(denon(each.level))

				engine.press(engineUnit, each.change, "press", false)
				synctest.Wait()

				mustMatchAll(t, recorder.written(), each.written)
				mustMatch(t, len(recorder.relayed()), 1)
			})
		})
	}
}

// The mute keys and asks change the mute and leave the level.
func TestTheMuteChanges(t *testing.T) {
	cases := []struct {
		name   string
		change volumeChange
		muted  bool
		want   string
	}{
		{name: "toggle on", change: volumeChange{mute: muteToggle}, want: "den-receiver 45.5, muted"},
		{name: "toggle off", change: volumeChange{mute: muteToggle}, muted: true, want: "den-receiver 45.5"},
		{name: "unmute", change: volumeChange{mute: muteOff}, muted: true, want: "den-receiver 45.5"},
		{name: "mute", change: volumeChange{mute: muteOn}, want: "den-receiver 45.5, muted"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				device := denon(45.5)
				device.mute = each.muted
				engine, recorder := engineFor(device)

				engine.press(engineUnit, each.change, "press", false)
				synctest.Wait()

				mustMatchAll(t, recorder.written(), []string{each.want})
			})
		})
	}
}

// A unit with several sinks steps each one in its own units, and the
// topic carries the first sink's level.
func TestEachSinkStepsInItsOwnUnits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		front := volumeDevice{kind: deviceSink, name: "front", max: 100, step: 5, level: 40, reported: true}
		rear := volumeDevice{kind: deviceSink, name: "rear", max: 80, step: 2, level: 30, reported: true}
		engine, recorder := engineFor(front, rear)

		engine.press(engineUnit, volumeChange{step: 1}, "press", false)
		synctest.Wait()

		written := recorder.written()
		slices.Sort(written)
		mustMatchAll(t, written, []string{"front 45", "rear 32"})
		mustMatchAll(t, recorder.relayed(), []string{`{"level":0.45,"muted":false}`})
	})
}

// A device that has reported no level, and a receiver that states no
// max, give a press nothing to step in, so it writes nothing.
func TestAPressNeedsAReportAndAMax(t *testing.T) {
	cases := []struct {
		name   string
		device volumeDevice
	}{
		{name: "no report", device: volumeDevice{kind: deviceSink, name: "front", max: 100, step: 5}},
		{name: "no max", device: volumeDevice{kind: deviceReceiver, name: "den-receiver", step: 1, level: 40, reported: true}},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				engine, recorder := engineFor(each.device)

				engine.press(engineUnit, volumeChange{step: 1}, "press", false)
				synctest.Wait()

				mustMatch(t, len(recorder.written()), 0)
				mustMatch(t, len(recorder.relayed()), 0)
			})
		})
	}
}

// The engine publishes each unit's level once a broker session's
// retained values have had time to arrive, and not when the broker
// already holds that level. A new broker session publishes it again.
func TestTheLevelIsAnnouncedOncePerBrokerSession(t *testing.T) {
	recorder := &deskRecorder{}
	engine := newVolumeEngine(recorder.write, recorder.publish, io.Discard)

	engine.observe(engineUnit, []volumeDevice{denon(36)}, false)
	mustMatch(t, len(recorder.relayed()), 0)

	engine.observe(engineUnit, []volumeDevice{denon(36)}, true)
	engine.observe(engineUnit, []volumeDevice{denon(36)}, true)
	mustMatchAll(t, recorder.relayed(), []string{`{"level":0.5,"muted":false}`})

	engine.newSession()
	engine.heardLevel(engineUnit, []byte(`{"level":0.5,"muted":false}`))
	engine.observe(engineUnit, []volumeDevice{denon(36)}, true)
	mustMatch(t, len(recorder.relayed()), 1)

	engine.newSession()
	engine.observe(engineUnit, []volumeDevice{denon(36)}, true)
	mustMatch(t, len(recorder.relayed()), 2)
}

// When the unit's devices change, as when its receiver stops answering
// and the sinks set the level, the topic carries the new first device's
// level.
func TestANewFirstDeviceIsPublished(t *testing.T) {
	engine, recorder := engineFor(denon(36))
	sink := volumeDevice{kind: deviceSink, name: "front", max: 100, step: 5, level: 40, reported: true}

	engine.observe(engineUnit, []volumeDevice{sink}, true)

	mustMatchAll(t, recorder.relayed(), []string{`{"level":0.4,"muted":false}`})
}

// The messages on a Player's volume/commands topic read as the same
// changes the keys make, and anything else reads as nothing.
func TestParseVolumeCommand(t *testing.T) {
	cases := []struct {
		payload string
		want    volumeChange
		ok      bool
	}{
		{payload: `{"step":"up"}`, want: volumeChange{step: 1}, ok: true},
		{payload: `{"step":"down"}`, want: volumeChange{step: -1}, ok: true},
		{payload: `{"mute":"toggle"}`, want: volumeChange{mute: muteToggle}, ok: true},
		{payload: `{"mute":true}`, want: volumeChange{mute: muteOn}, ok: true},
		{payload: `{"mute":false}`, want: volumeChange{mute: muteOff}, ok: true},
		{payload: `{"step":"sideways"}`},
		{payload: `{"level":0.5}`},
		{payload: `not json`},
	}
	for _, each := range cases {
		t.Run(each.payload, func(t *testing.T) {
			got, ok := parseVolumeCommand([]byte(each.payload))
			mustMatch(t, ok, each.ok)
			mustMatch(t, got, each.want)
		})
	}
}

// An API server slower than the pacing interval holds one write at a
// time. The presses during a write move the target, and the answer
// sends one more write with the last target, so two asks for one device
// never race each other to the API server.
func TestASlowWriteSendsTheLastTargetWhenItIsAnswered(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := &deskRecorder{}
		answered := make(chan struct{})
		slow := func(unit string, device volumeDevice, ask VolumeAsk) error {
			<-answered
			return recorder.write(unit, device, ask)
		}
		engine := newVolumeEngine(slow, recorder.publish, io.Discard)
		engine.observe(engineUnit, []volumeDevice{denon(45.5)}, true)

		engine.press(engineUnit, stepDown, "press", false)
		synctest.Wait()
		time.Sleep(volumePacing)
		engine.press(engineUnit, stepDown, "repeat", true)
		time.Sleep(volumePacing)
		engine.press(engineUnit, stepDown, "repeat", true)
		synctest.Wait()
		mustMatch(t, len(recorder.written()), 0)

		answered <- struct{}{}
		synctest.Wait()
		answered <- struct{}{}
		synctest.Wait()

		mustMatchAll(t, recorder.written(), []string{"den-receiver 45", "den-receiver 44"})
	})
}
