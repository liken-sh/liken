package main

// The volume engine turns volume key presses, and messages on a Player's
// volume/commands topic, into a VolumeAsk for each device that sets the
// Player's level. It writes the VolumeAsk into the device's object, and
// the device's operator applies it (sessionwriter in receiverasks.go,
// and sink.go). Each pass reads the levels that the devices report and
// gives them to the engine (volumedevices.go). The engine is the only
// writer of the Player's volume topic. It publishes the target each time
// a press moves it, and each change that a person makes at the device.
//
// A held remote key sends a press about every 42 ms. A Denon reports a
// new volume tens of milliseconds after it applies it, because the
// request goes through one apply and one watch event in each direction.
// If each press stepped from the reported level, several presses in a
// row would compute the same target, and the device would move one step
// for all of them. So a press steps from the pending target, and from
// the reported level only when no target is pending.
//
// While a volume-down key is held, the device reports each earlier
// request after later presses have moved the target lower, so a screen
// that drew those reports would show the level going up. So the engine
// publishes no report while a target is pending. The target stops being
// pending when the device reports it, or after volumeSettleWait.

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// volumePacing is the shortest time between two requests for one
// device. Sonos tells a controller to send at most one volume command
// every 100 ms (docs.sonos.com/docs/volume), and a held key repeats
// faster than that. The first request is written at once. A press inside
// the interval moves the target and writes nothing, and the write at the
// end of the interval sends the latest target.
const volumePacing = 100 * time.Millisecond

// volumeSettleWait is how long a target stays pending while the device
// has not reported it. Some devices never report the target: a receiver
// whose own limit is below it, or one whose equipment operator restarted
// and skipped the request in its first pass. After this wait the engine
// publishes the level that the device reports, and the next press steps
// from that level.
const volumeSettleWait = time.Second

const (
	deviceReceiver = "receiver"
	deviceSink     = "sink"
)

// volumeDevice is one device that sets a Player's level, as the last pass
// read it. max, step, and level are in the device's own units. reported
// is false until the device reports a level, and until then a press has
// no level to step from.
type volumeDevice struct {
	kind     string
	name     string
	max      float64
	step     float64
	level    float64
	mute     bool
	reported bool
	// indicator is the indicator field of the topic payload for this
	// device. It is empty when the Player's screens draw the volume bar,
	// and relayIndicatorReceiver for a Receiver that draws its own
	// overlay.
	indicator string
}

func (d volumeDevice) key() string { return d.kind + "/" + d.name }

func (d volumeDevice) sameReport(other volumeDevice) bool {
	return d.reported == other.reported && d.level == other.level && d.mute == other.mute
}

// volumeChange is the change that one press or one message makes to a
// Player: a step up or down, or a change of the mute.
type volumeChange struct {
	step int
	mute muteChange
}

type muteChange int

const (
	muteKeep muteChange = iota
	muteToggle
	muteOn
	muteOff
)

// relayLevel is the payload of the Player's volume topic: the level as
// a fraction of the device's max, the mute, and which component draws
// the indicator. Without an indicator, the screens draw the bar.
type relayLevel struct {
	Level     float64 `json:"level"`
	Muted     bool    `json:"muted"`
	Indicator string  `json:"indicator,omitempty"`
}

// relayIndicatorReceiver marks a level that the receiver draws on the TV
// itself. The screens track the level and draw no bar for it, so the TV
// shows one indicator.
const relayIndicatorReceiver = "receiver"

// volumeTarget is the target level for one device, and the state of the
// writes that send it to the device's object.
type volumeTarget struct {
	device  volumeDevice
	unit    string
	level   float64
	mute    bool
	pending bool

	// Each move increments generation and restarts expiry. Stopping a
	// timer does not stop a callback that has already started, so the
	// callback compares the generation it captured with this one and
	// does nothing when they differ.
	generation int
	expiry     *time.Timer

	// Write pacing. scheduled is true while a write waits for its
	// interval. writing is true until the API server responds to a write.
	// A press during a write sets again, and flush then schedules one
	// more write.
	lastWrite time.Time
	lastAt    string
	scheduled bool
	writing   bool
	again     bool
}

// volumeUnit is the engine's state for one Player: its devices, the key of
// the device whose level the topic carries, and whether the engine has
// published the Player's level in this broker session.
type volumeUnit struct {
	devices   []volumeDevice
	relayed   string
	announced bool
}

// volumeEngine holds every Player's devices and every device's target.
// The pass, the bus reader, and the timers all call it, so one mutex
// guards it. Writes to the API server run with the mutex released.
type volumeEngine struct {
	mutex   sync.Mutex
	units   map[string]*volumeUnit
	targets map[string]*volumeTarget
	// brokerPayload is the last payload on each Player's topic, from the
	// operator's own subscription or its own publish.
	brokerPayload map[string]string

	write   func(unit string, device volumeDevice, ask VolumeAsk) error
	publish func(unit string, payload []byte)
	log     io.Writer
}

func newVolumeEngine(write func(string, volumeDevice, VolumeAsk) error, publish func(string, []byte), log io.Writer) *volumeEngine {
	return &volumeEngine{
		units:         map[string]*volumeUnit{},
		targets:       map[string]*volumeTarget{},
		brokerPayload: map[string]string{},
		write:         write,
		publish:       publish,
		log:           log,
	}
}

// press moves the target of each of the Player's devices and schedules the
// writes. A quiet press is a repeat of a held key. It does the same work
// and writes no log line, because the first press wrote one.
func (e *volumeEngine) press(unit string, change volumeChange, trigger string, quiet bool) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	state := e.units[unit]
	if state == nil || len(state.devices) == 0 {
		if !quiet {
			logLine(e.log, "%s, set no level, because the unit has no reachable receiver and no sinks", trigger)
		}
		return
	}
	var moves []string
	for index, device := range state.devices {
		moves = append(moves, e.move(unit, device, change, index == 0))
	}
	if !quiet {
		logLine(e.log, "%s, %s", trigger, joinMoves(moves))
	}
}

// move steps one device's target and returns the log text for it. When
// first is true, move publishes the new target on the Player's topic. It
// publishes even when a press at the end of the scale changes nothing,
// so the screens draw the indicator as feedback for the press.
func (e *volumeEngine) move(unit string, device volumeDevice, change volumeChange, first bool) string {
	if device.max <= 0 {
		return fmt.Sprintf("changed nothing on %s %s, because spec.volume.max is not set and its driver has no fixed scale", device.kind, device.name)
	}
	target := e.targets[device.key()]
	level, mute := device.level, device.mute
	switch {
	case target != nil && target.pending:
		level, mute = target.level, target.mute
	case !device.reported:
		return fmt.Sprintf("changed nothing on %s %s, because it has reported no level", device.kind, device.name)
	}
	nextLevel := clampLevel(roundLevel(level+float64(change.step)*device.step), device.max)
	nextMute := changeMute(mute, change.mute)
	if first {
		e.relay(unit, device, nextLevel, nextMute)
	}
	if nextLevel == level && nextMute == mute {
		return fmt.Sprintf("left %s %s at %s", device.kind, device.name, describeLevel(level, mute))
	}
	if target == nil {
		target = &volumeTarget{}
		e.targets[device.key()] = target
	}
	target.device, target.unit = device, unit
	target.level, target.mute = nextLevel, nextMute
	e.startSettleWait(device.key(), target)
	e.schedule(device.key(), target)
	return fmt.Sprintf("moved %s %s from %s to %s", device.kind, device.name,
		describeLevel(level, mute), describeLevel(nextLevel, nextMute))
}

// startSettleWait marks the target pending and restarts its
// volumeSettleWait timer, so the wait runs from the latest move.
func (e *volumeEngine) startSettleWait(key string, target *volumeTarget) {
	if target.expiry != nil {
		target.expiry.Stop()
	}
	target.generation++
	generation := target.generation
	target.pending = true
	target.expiry = time.AfterFunc(volumeSettleWait, func() { e.publishUnsettled(key, generation) })
}

// schedule makes sure the target gets written. A write that waits for
// its interval reads the target when it runs, so it needs nothing more.
// When a write is in flight, schedule sets again, so flush schedules one
// more write when the API server responds.
func (e *volumeEngine) schedule(key string, target *volumeTarget) {
	switch {
	case target.writing:
		target.again = true
		return
	case target.scheduled:
		return
	}
	target.scheduled = true
	wait := time.Until(target.lastWrite.Add(volumePacing))
	if wait <= 0 {
		go e.flush(key)
		return
	}
	time.AfterFunc(wait, func() { e.flush(key) })
}

// flush writes the target as it is when flush runs. It writes nothing
// when the target is no longer pending, because the device has reported
// it or the settle wait has ended.
func (e *volumeEngine) flush(key string) {
	e.mutex.Lock()
	target := e.targets[key]
	if target == nil {
		e.mutex.Unlock()
		return
	}
	target.scheduled = false
	if !target.pending {
		e.mutex.Unlock()
		return
	}
	now := time.Now()
	ask := VolumeAsk{Level: target.level, Mute: target.mute, At: nextAskTime(target.lastAt, now)}
	target.lastAt, target.lastWrite, target.writing = ask.At, now, true
	unit, device := target.unit, target.device
	e.mutex.Unlock()

	err := e.write(unit, device, ask)

	e.mutex.Lock()
	defer e.mutex.Unlock()
	target.writing = false
	if err != nil {
		fmt.Fprintf(os.Stderr, "player %s: writing volumeAsk %s on %s %s: %v\n",
			unit, describeLevel(ask.Level, ask.Mute), device.kind, device.name, err)
	}
	if target.again {
		target.again = false
		e.schedule(key, target)
	}
}

// publishUnsettled runs when the device has not reported its target
// within volumeSettleWait. It ends the wait and, when the device is the
// Player's first device, publishes the level that the device last
// reported.
func (e *volumeEngine) publishUnsettled(key string, generation int) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	target := e.targets[key]
	if target == nil || !target.pending || target.generation != generation {
		return
	}
	target.pending = false
	state := e.units[target.unit]
	if state == nil || len(state.devices) == 0 || state.devices[0].key() != key {
		return
	}
	device := state.devices[0]
	if !device.reported {
		return
	}
	logLine(e.log, "player %s: %s %s did not report %s within %s, published its report of %s",
		target.unit, device.kind, device.name, describeLevel(target.level, target.mute), volumeSettleWait,
		describeLevel(device.level, device.mute))
	e.relay(target.unit, device, device.level, device.mute)
}

// observe records one pass's read of a Player's devices. A report that
// equals the pending target ends the wait. When no target is pending, a
// changed report is a change that a person made at the device, such as a
// turn of a receiver's knob. observe publishes the first device's level
// when its report changed, when the first device is a different device,
// or when its indicator differs from the topic's.
//
// announce is true once the broker's retained messages have had time to
// arrive. Until then observe publishes nothing. After that it publishes
// the Player's level once in each broker session, unless the topic already
// has that payload.
func (e *volumeEngine) observe(unit string, devices []volumeDevice, announce bool) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	state := e.units[unit]
	if state == nil {
		state = &volumeUnit{}
		e.units[unit] = state
	}
	previous := map[string]volumeDevice{}
	for _, device := range state.devices {
		previous[device.key()] = device
	}
	state.devices = devices
	for index, device := range devices {
		target := e.targets[device.key()]
		if target != nil && target.pending {
			if device.reported && device.level == target.level && device.mute == target.mute {
				target.pending = false
				target.expiry.Stop()
			}
			continue
		}
		if index != 0 || !device.reported || device.max <= 0 {
			continue
		}
		before, seen := previous[device.key()]
		switch {
		case !state.announced && announce:
			state.announced, state.relayed = true, device.key()
			if e.brokerPayload[unit] != string(relayPayload(device, device.level, device.mute)) {
				e.relay(unit, device, device.level, device.mute)
			}
		case !state.announced:
		case state.relayed != device.key(), !seen || !before.sameReport(device),
			e.brokerIndicator(unit) != device.indicator:
			state.relayed = device.key()
			e.relay(unit, device, device.level, device.mute)
		}
	}
}

// brokerIndicator returns the indicator of the last payload on a Player's
// topic. observe compares the device's indicator with it, and not with
// the previous pass, so an indicator change that arrives while a target
// is pending is published after the wait ends.
func (e *volumeEngine) brokerIndicator(unit string) string {
	level, _ := parseRelayLevel([]byte(e.brokerPayload[unit]))
	return level.Indicator
}

// lastLevel records a payload that the operator's subscription received
// on a Player's topic.
func (e *volumeEngine) recordBrokerPayload(unit string, payload []byte) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	e.brokerPayload[unit] = string(payload)
}

// newSession clears the recorded topic payloads and the announced flags,
// so the next pass publishes each Player's level again. The bus calls it
// on each connect, because a broker that restarted has no retained
// messages.
func (e *volumeEngine) newSession() {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	e.brokerPayload = map[string]string{}
	for _, state := range e.units {
		state.announced = false
	}
}

// retain deletes the Players that are not in live, and the targets whose
// device is on no remaining Player and that have nothing pending, scheduled,
// or in flight.
func (e *volumeEngine) retain(live map[string]bool) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	named := map[string]bool{}
	for unit, state := range e.units {
		if !live[unit] {
			delete(e.units, unit)
			delete(e.brokerPayload, unit)
			continue
		}
		for _, device := range state.devices {
			named[device.key()] = true
		}
	}
	for key, target := range e.targets {
		if !named[key] && !target.pending && !target.writing && !target.scheduled {
			delete(e.targets, key)
		}
	}
}

// relay publishes a level of a device on the Player's topic. The caller
// holds the mutex, so publishes for one Player go out in the order the
// engine made them.
func (e *volumeEngine) relay(unit string, device volumeDevice, level float64, mute bool) {
	payload := relayPayload(device, level, mute)
	e.brokerPayload[unit] = string(payload)
	e.publish(unit, payload)
}

// relayPayload returns the topic payload for a level in a device's
// units, as a fraction of the device's max, with the device's indicator.
func relayPayload(device volumeDevice, level float64, mute bool) []byte {
	fraction := 0.0
	if device.max > 0 {
		fraction = math.Round(level/device.max*10000) / 10000
	}
	fraction = math.Min(math.Max(fraction, 0), 1)
	payload, err := json.Marshal(relayLevel{Level: fraction, Muted: mute, Indicator: device.indicator})
	if err != nil {
		return nil
	}
	return payload
}

// roundLevel rounds a level to a thousandth. Adding a step such as 0.1
// many times in floating point drifts from the receiver's own steps.
func roundLevel(level float64) float64 {
	return math.Round(level*1000) / 1000
}

// clampLevel limits a level to zero through the device's max. A person
// at the device can set a level above max, and a press from that level
// then requests max.
func clampLevel(level, max float64) float64 {
	return math.Min(math.Max(level, 0), max)
}

func changeMute(mute bool, change muteChange) bool {
	switch change {
	case muteToggle:
		return !mute
	case muteOn:
		return true
	case muteOff:
		return false
	}
	return mute
}

func describeLevel(level float64, mute bool) string {
	text := strconv.FormatFloat(level, 'f', -1, 64)
	if mute {
		return text + ", muted"
	}
	return text
}

func joinMoves(moves []string) string {
	return strings.Join(moves, ", and ")
}
