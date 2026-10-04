package main

// The volume engine turns presses into asks. A press of a volume key,
// or a message on a Player's volume/commands topic, moves a pending
// target for each device that sets the unit's level. The engine writes
// the target into the device's object as an ask, and the device's
// operator applies it (sessionwriter in receiverasks.go, and sink.go).
// The device's operator reports the level it reads back, and the pass
// hands each report to the engine (volumedevices.go). The engine is the
// only writer of the Player's volume topic, and it publishes there the
// target as it moves and each report a person made at the device.
//
// A held remote key sends a press about every 42 ms, and a Denon reports
// a volume tens of milliseconds after it takes it, after one apply and
// one watch event in each direction. A press that stepped from the
// device's report would compute the same target as the press before it,
// and the device would move one step for several presses. So each press
// steps from the target that is still pending, and from the report only
// when no target is pending.
//
// The reports of the earlier asks arrive after the later presses moved
// the target. Each of them is above the target on a held volume-down
// key, and a screen that drew them would show the level going up. So the
// engine publishes no report while a target is pending, and the target
// stops being pending when the device reports it.

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

// volumePacing is the shortest time between two asks for one device.
// Sonos tells a controller to throttle its volume commands to one every
// 100 ms (docs.sonos.com/docs/volume), and a held key repeats faster
// than that. The first ask goes out at once. A press inside the
// interval moves the target and writes nothing, and the write at the
// end of the interval carries the last target.
const volumePacing = 100 * time.Millisecond

// volumeSettleWait is how long a target stays pending without a report
// of it. A Denon reports within a fraction of a second. A device that
// never reports the target, such as a receiver whose own limit is below
// it, or an equipment operator that restarted and skipped the ask in its
// first pass, has its report published after this wait, and the next
// press steps from that report.
const volumeSettleWait = time.Second

// The kinds of device that set a unit's level.
const (
	deviceReceiver = "receiver"
	deviceSink     = "sink"
)

// volumeDevice is one device that sets a unit's level, as the pass last
// read it: its scale in its own units, and its report. A device that has
// reported no level has reported false, and a press cannot step from it.
type volumeDevice struct {
	kind     string
	name     string
	max      float64
	step     float64
	level    float64
	mute     bool
	reported bool
	// indicator is the relay's indicator field for this device: empty
	// while the Player's screens draw the bar, relayIndicatorReceiver
	// for a Receiver that draws its own overlay.
	indicator string
}

func (d volumeDevice) key() string { return d.kind + "/" + d.name }

// sameReport answers whether two reads of a device report one level.
func (d volumeDevice) sameReport(other volumeDevice) bool {
	return d.reported == other.reported && d.level == other.level && d.mute == other.mute
}

// volumeChange is what one press or one message asks of a unit: a step
// up or down, or a change of the mute.
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
// a fraction of the device's max, the mute, and who draws the
// indicator. An absent indicator means the screens draw the bar.
type relayLevel struct {
	Level     float64 `json:"level"`
	Muted     bool    `json:"muted"`
	Indicator string  `json:"indicator,omitempty"`
}

// relayIndicatorReceiver marks a level that the receiver draws on the
// TV itself. The screens track the level and draw no bar for it, so the
// TV shows one indicator and not two.
const relayIndicatorReceiver = "receiver"

// volumeTarget is the pending target for one device, and the state of
// the asks that carry it.
type volumeTarget struct {
	device  volumeDevice
	unit    string
	level   float64
	mute    bool
	pending bool

	// generation tells an expiry that fires late from the expiry of the
	// target pending now.
	generation int
	expiry     *time.Timer

	// The pacing. scheduled is a write waiting for its interval, and
	// writing is a write the API server has not answered yet. again
	// asks for one more write when that answer arrives.
	lastWrite time.Time
	lastAt    string
	scheduled bool
	writing   bool
	again     bool
}

// volumeUnit is what the engine holds for one unit: its devices, the
// device whose level the topic carries, and whether the engine has
// published the unit's level in this broker session.
type volumeUnit struct {
	devices   []volumeDevice
	relayed   string
	announced bool
}

// volumeEngine holds every unit's devices and every device's target.
// The pass, the bus reader, and the timers all reach it, so one mutex
// covers it. Writes to the API server go out with the mutex released.
type volumeEngine struct {
	mutex   sync.Mutex
	units   map[string]*volumeUnit
	targets map[string]*volumeTarget
	// heard is the payload the broker holds on each unit's topic, as
	// the operator's own subscription delivered it.
	heard map[string]string

	write   func(unit string, device volumeDevice, ask VolumeAsk) error
	publish func(unit string, payload []byte)
	log     io.Writer
}

func newVolumeEngine(write func(string, volumeDevice, VolumeAsk) error, publish func(string, []byte), log io.Writer) *volumeEngine {
	return &volumeEngine{
		units:   map[string]*volumeUnit{},
		targets: map[string]*volumeTarget{},
		heard:   map[string]string{},
		write:   write,
		publish: publish,
		log:     log,
	}
}

// press moves the target of each of the unit's devices and schedules
// the asks. A quiet press is the repeat of a held key: it acts the same
// and writes no line, because the press wrote one.
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

// move steps one device's target. first says the device's level is the
// one the topic carries, so the target goes on the topic. A press at the
// end of the scale moves nothing, and the topic carries the level again,
// so the screens draw the indicator as the press's feedback.
func (e *volumeEngine) move(unit string, device volumeDevice, change volumeChange, first bool) string {
	if device.max <= 0 {
		return fmt.Sprintf("set nothing on %s %s, because it states no spec.volume.max and its driver has no fixed scale", device.kind, device.name)
	}
	target := e.targets[device.key()]
	level, mute := device.level, device.mute
	switch {
	case target != nil && target.pending:
		level, mute = target.level, target.mute
	case !device.reported:
		return fmt.Sprintf("set nothing on %s %s, because it has reported no level", device.kind, device.name)
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
	e.aim(device.key(), target)
	e.schedule(device.key(), target)
	return fmt.Sprintf("moved %s %s from %s to %s", device.kind, device.name,
		describeLevel(level, mute), describeLevel(nextLevel, nextMute))
}

// aim marks the target pending, and starts the wait for the device to
// report it. Each move starts the wait again, so the wait runs from the
// last ask.
func (e *volumeEngine) aim(key string, target *volumeTarget) {
	if target.expiry != nil {
		target.expiry.Stop()
	}
	target.generation++
	generation := target.generation
	target.pending = true
	target.expiry = time.AfterFunc(volumeSettleWait, func() { e.giveUp(key, generation) })
}

// schedule arranges the write that carries the target. A write that is
// waiting for its interval reads the target when it goes, so nothing
// more is needed for it. A write that is in flight asks for one more
// when it is answered.
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

// flush writes the target as it stands now.
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

// giveUp ends the wait for a target the device never reported, and
// publishes what the device reports.
func (e *volumeEngine) giveUp(key string, generation int) {
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

// observe takes one pass's read of a unit's devices. A report of the
// pending target ends the wait. A report that changed while no target
// is pending is a change a person made at the device, such as a turn of
// a receiver's knob, and the topic carries it. announce says the
// broker's retained values have had time to arrive, so the engine can
// publish the unit's level once in this broker session, unless the
// broker already holds it.
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
			if e.heard[unit] != string(relayPayload(device, device.level, device.mute)) {
				e.relay(unit, device, device.level, device.mute)
			}
		case !state.announced:
		case state.relayed != device.key(), !seen || !before.sameReport(device),
			e.heardIndicator(unit) != device.indicator:
			state.relayed = device.key()
			e.relay(unit, device, device.level, device.mute)
		}
	}
}

// heardIndicator is the indicator field the broker holds on a unit's
// topic. A change of the indicator alone republishes the level, and the
// comparison reads the broker's payload and not the last pass, so a
// change that arrived while a target was pending is still published.
func (e *volumeEngine) heardIndicator(unit string) string {
	level, _ := parseRelayLevel([]byte(e.heard[unit]))
	return level.Indicator
}

// heardLevel records the payload the broker delivered on a unit's
// topic.
func (e *volumeEngine) heardLevel(unit string, payload []byte) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	e.heard[unit] = string(payload)
}

// newSession forgets what the last broker session held, so the next
// pass publishes each unit's level again. The bus calls it at each
// connect. A broker that restarted holds no retained value.
func (e *volumeEngine) newSession() {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	e.heard = map[string]string{}
	for _, state := range e.units {
		state.announced = false
	}
}

// retain drops the units the cluster no longer holds, and the targets
// no unit names that wait for nothing.
func (e *volumeEngine) retain(live map[string]bool) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	named := map[string]bool{}
	for unit, state := range e.units {
		if !live[unit] {
			delete(e.units, unit)
			delete(e.heard, unit)
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

// relay publishes one level of a device on the unit's topic. The caller
// holds the mutex, so two publishes for one unit leave in the order the
// engine made them.
func (e *volumeEngine) relay(unit string, device volumeDevice, level float64, mute bool) {
	payload := relayPayload(device, level, mute)
	e.heard[unit] = string(payload)
	e.publish(unit, payload)
}

// relayPayload is the topic's payload for a level in a device's units,
// with the device's indicator.
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

// roundLevel holds a level to a thousandth, so a step such as 0.1 added
// many times does not drift away from the receiver's own steps.
func roundLevel(level float64) float64 {
	return math.Round(level*1000) / 1000
}

// clampLevel holds a level between zero and the device's max. An ask
// never goes past max. A person at the device can.
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

// describeLevel names a level the way every volume line does.
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
