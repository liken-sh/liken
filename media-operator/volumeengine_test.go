package main

// These tests cover the volume engine: the pending target, the pacing
// of the writes, the wait for the device's report, and the publishing
// on the Player's topic. Each test runs on the fake clock of a synctest
// bubble, so waiting one pacing interval or one settle wait takes no
// real time.

import (
	"io"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// volumeWriteRecorder records what the engine wrote and published. The engine
// writes from timer goroutines, so the recorder has its own mutex.
type volumeWriteRecorder struct {
	mutex     sync.Mutex
	asks      []string
	published []string
}

func (r *volumeWriteRecorder) write(_ string, device volumeDevice, ask VolumeAsk) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.asks = append(r.asks, device.name+" "+describeLevel(ask.Level, ask.Mute))
	return nil
}

func (r *volumeWriteRecorder) publish(_ string, payload []byte) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.published = append(r.published, string(payload))
}

// clearPublished discards the levels published so far, so a test sees
// only the ones that follow.
func (r *volumeWriteRecorder) clearPublished() {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.published = nil
}

func (r *volumeWriteRecorder) written() []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return append([]string(nil), r.asks...)
}

func (r *volumeWriteRecorder) relayed() []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return append([]string(nil), r.published...)
}

// enginePlayer is the Player these tests press. denon builds its receiver
// with max 72 and a step of 0.5, reporting the given level.
const enginePlayer = "house/theater"

func denon(level float64) volumeDevice {
	return volumeDevice{kind: deviceReceiver, name: "den-receiver", max: 72, step: 0.5, level: level, reported: true}
}

// engineFor builds an engine whose Player has the given devices and has
// already published its level once, so a test sees only what its own
// presses and reports publish.
func engineFor(devices ...volumeDevice) (*volumeEngine, *volumeWriteRecorder) {
	recorder := &volumeWriteRecorder{}
	engine := newVolumeEngine(recorder.write, recorder.publish, io.Discard)
	engine.observe(enginePlayer, devices, true)
	recorder.clearPublished()
	return engine, recorder
}

var stepDown = volumeChange{step: -1}

// A held key moves the target one step for each press and repeat, from
// the target and not from the report. The first write happens at once,
// and the presses inside the pacing interval make one write at the end
// of the interval, with the latest target.
func TestAHeldKeyStepsFromThePendingTargetAndPacesTheWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		engine, recorder := engineFor(denon(45.5))

		engine.press(enginePlayer, stepDown, "press", false)
		synctest.Wait()
		time.Sleep(40 * time.Millisecond)
		engine.press(enginePlayer, stepDown, "repeat", true)
		time.Sleep(40 * time.Millisecond)
		engine.press(enginePlayer, stepDown, "repeat", true)
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

// The receiver reports the earlier requests after later presses have
// moved the target. The engine publishes none of these reports, so the
// indicator does not move up while a volume-down key is held. The report
// of the target ends the wait.
func TestReportsOfEarlierRequestsAreNotPublished(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		engine, recorder := engineFor(denon(45.5))
		engine.press(enginePlayer, stepDown, "press", false)
		engine.press(enginePlayer, stepDown, "repeat", true)
		recorder.clearPublished()

		engine.observe(enginePlayer, []volumeDevice{denon(45)}, true)
		engine.observe(enginePlayer, []volumeDevice{denon(44.5)}, true)
		time.Sleep(2 * volumeSettleWait)
		synctest.Wait()

		mustMatch(t, len(recorder.relayed()), 0)
	})
}

// When the device never reports the target, as when a receiver's own
// limit is below it, the target stops being pending after
// volumeSettleWait.
// The engine then publishes the device's reported level, and the next
// press steps from that level.
func TestAnUnreportedTargetIsReplacedByTheDeviceLevelAfterOneSecond(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		engine, recorder := engineFor(denon(45.5))
		engine.press(enginePlayer, volumeChange{step: 1}, "press", false)
		engine.observe(enginePlayer, []volumeDevice{denon(45.5)}, true)
		recorder.clearPublished()

		time.Sleep(volumeSettleWait)
		synctest.Wait()
		mustMatchAll(t, recorder.relayed(), []string{`{"level":0.6319,"muted":false}`})

		time.Sleep(volumePacing)
		engine.press(enginePlayer, stepDown, "press", false)
		synctest.Wait()
		mustMatch(t, recorder.written()[len(recorder.written())-1], "den-receiver 45")
	})
}

// A report that arrives while no target is pending is a change a person
// made at the device, such as a turn of the receiver's knob. The engine
// publishes it at once, and the next press steps from it.
func TestAKnobTurnIsPublishedImmediately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		engine, recorder := engineFor(denon(45.5))

		engine.observe(enginePlayer, []volumeDevice{denon(46.5)}, true)
		engine.observe(enginePlayer, []volumeDevice{denon(46.5)}, true)
		mustMatchAll(t, recorder.relayed(), []string{`{"level":0.6458,"muted":false}`})

		engine.press(enginePlayer, volumeChange{step: 1}, "press", false)
		synctest.Wait()
		mustMatchAll(t, recorder.written(), []string{"den-receiver 47"})
	})
}

// A press never moves the target above the device's max or below zero.
// A press at the end of the scale writes nothing, but the engine
// publishes the level again, so the screens draw the indicator as
// feedback for the press.
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

				engine.press(enginePlayer, each.change, "press", false)
				synctest.Wait()

				mustMatchAll(t, recorder.written(), each.written)
				mustMatch(t, len(recorder.relayed()), 1)
			})
		})
	}
}

// The mute keys and messages change the mute and leave the level.
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

				engine.press(enginePlayer, each.change, "press", false)
				synctest.Wait()

				mustMatchAll(t, recorder.written(), []string{each.want})
			})
		})
	}
}

// A Player with several sinks steps each sink in its own units, and the
// topic carries the first sink's level.
func TestEachSinkStepsInItsOwnUnits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		front := volumeDevice{kind: deviceSink, name: "front", max: 100, step: 5, level: 40, reported: true}
		rear := volumeDevice{kind: deviceSink, name: "rear", max: 80, step: 2, level: 30, reported: true}
		engine, recorder := engineFor(front, rear)

		engine.press(enginePlayer, volumeChange{step: 1}, "press", false)
		synctest.Wait()

		written := recorder.written()
		slices.Sort(written)
		mustMatchAll(t, written, []string{"front 45", "rear 32"})
		mustMatchAll(t, recorder.relayed(), []string{`{"level":0.45,"muted":false}`})
	})
}

// A press writes nothing for a device that has reported no level, or
// for a receiver with no max, because there is no scale to step in.
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

				engine.press(enginePlayer, volumeChange{step: 1}, "press", false)
				synctest.Wait()

				mustMatch(t, len(recorder.written()), 0)
				mustMatch(t, len(recorder.relayed()), 0)
			})
		})
	}
}

// The engine publishes each Player's level once per broker session, after
// the session's retained messages have had time to arrive, and skips
// the publish when the topic already has that payload. A new broker
// session publishes the level again.
func TestTheLevelIsPublishedOncePerBrokerSession(t *testing.T) {
	recorder := &volumeWriteRecorder{}
	engine := newVolumeEngine(recorder.write, recorder.publish, io.Discard)

	engine.observe(enginePlayer, []volumeDevice{denon(36)}, false)
	mustMatch(t, len(recorder.relayed()), 0)

	engine.observe(enginePlayer, []volumeDevice{denon(36)}, true)
	engine.observe(enginePlayer, []volumeDevice{denon(36)}, true)
	mustMatchAll(t, recorder.relayed(), []string{`{"level":0.5,"muted":false}`})

	engine.newSession()
	engine.recordBrokerPayload(enginePlayer, []byte(`{"level":0.5,"muted":false}`))
	engine.observe(enginePlayer, []volumeDevice{denon(36)}, true)
	mustMatch(t, len(recorder.relayed()), 1)

	engine.newSession()
	engine.observe(enginePlayer, []volumeDevice{denon(36)}, true)
	mustMatch(t, len(recorder.relayed()), 2)
}

// When the Player's devices change, as when its receiver stops responding
// and the sinks set the level, the engine publishes the level of the new
// first device.
func TestANewFirstDeviceIsPublished(t *testing.T) {
	engine, recorder := engineFor(denon(36))
	sink := volumeDevice{kind: deviceSink, name: "front", max: 100, step: 5, level: 40, reported: true}

	engine.observe(enginePlayer, []volumeDevice{sink}, true)

	mustMatchAll(t, recorder.relayed(), []string{`{"level":0.4,"muted":false}`})
}

// A message on a Player's volume/commands topic parses into the same
// change that a key makes. Any other payload does not parse.
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

// When the API server takes longer than the pacing interval, the engine
// has one write in flight for a device. Presses during the write move
// the target, and when the write returns the engine makes one more write
// with the latest target. Two writes for one device therefore never run
// at the same time.
func TestASlowWriteIsFollowedByOneWriteOfTheLatestTarget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		recorder := &volumeWriteRecorder{}
		answered := make(chan struct{})
		slow := func(unit string, device volumeDevice, ask VolumeAsk) error {
			<-answered
			return recorder.write(unit, device, ask)
		}
		engine := newVolumeEngine(slow, recorder.publish, io.Discard)
		engine.observe(enginePlayer, []volumeDevice{denon(45.5)}, true)

		engine.press(enginePlayer, stepDown, "press", false)
		synctest.Wait()
		time.Sleep(volumePacing)
		engine.press(enginePlayer, stepDown, "repeat", true)
		time.Sleep(volumePacing)
		engine.press(enginePlayer, stepDown, "repeat", true)
		synctest.Wait()
		mustMatch(t, len(recorder.written()), 0)

		answered <- struct{}{}
		synctest.Wait()
		answered <- struct{}{}
		synctest.Wait()

		mustMatchAll(t, recorder.written(), []string{"den-receiver 45", "den-receiver 44"})
	})
}
