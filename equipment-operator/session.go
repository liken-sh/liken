package main

// A session is the operator's state for one Player on one receiver.
// media-operator writes two flags in status.session: active while a Play
// runs on the Player, and awake while the Player's screen is awake. When
// a flag turns on, the session runs its one-shot: it powers the receiver
// on if it is off and selects the Player's input. The one-shot does not
// correct the receiver afterward, so a person can still switch the input
// with the receiver's own remote. session_asks.go handles the other
// requests in status.session.

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liken-sh/liken/equipment-operator/denon"
	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// sessionPowerWait is how long the one-shot waits for the receiver to
// report power On before it sends the input anyway.
const sessionPowerWait = 10 * time.Second

type session struct {
	receiver string
	spec     ReceiverSession
	driver   equipment.Driver
	// readings records a command timeout when the power wait runs out.
	readings *metrics
	// log is the receiver's log, shared with the unit so that the lines
	// of both appear in order.
	log *receiverLog
	// ctx outlives start, because a flag can turn on and start a one-shot
	// long after the session started. stop cancels it.
	ctx    context.Context
	cancel context.CancelFunc

	active atomic.Bool
	awake  atomic.Bool

	// powered is closed when the receiver reports power On. armPower
	// replaces it before each input selection, so a later selection waits
	// for a new power-on report.
	powerMutex sync.Mutex
	powered    chan struct{}

	// oneShot is held by everything that sends the receiver power or
	// input commands: the one-shot, a power request, and an ensure.
	// Without it, a standby that arrives during a power-on could leave the
	// receiver off while the flags are on, and nothing would send power On
	// again.
	oneShot sync.Mutex
	// applyPower writes the power that a power request settled on to the
	// Receiver spec. It is nil in a test that sends no power request.
	applyPower func(power equipment.Power)
	// room is nil in a test with no TV.
	room roomEvents

	// reached closes when the receiver first reports reachable, which
	// means the connection is open for commands.
	reachedOnce sync.Once
	reached     chan struct{}

	// firstReadDone closes when the driver has finished its first read of the
	// receiver's state. The one-shot compares against that state.
	firstReadOnce sync.Once
	firstReadDone chan struct{}

	// inputSoundMode returns the sound mode declared for an input, or ""
	// when there is none.
	inputSoundMode func(input string) string
}

// newSession returns a session that has read and sent nothing. The
// caller must route the receiver's events to it before it calls start,
// because start reads the driver's state only once. An event lost after
// that read could be the one that reports the receiver reachable, and
// the one-shot would then wait until the session ends.
func newSession(ctx context.Context, receiver string, spec ReceiverSession, driver equipment.Driver, readings *metrics, log *receiverLog, inputSoundMode func(input string) string, applyPower func(power equipment.Power), room roomEvents) *session {
	ctx, cancel := context.WithCancel(ctx)
	if inputSoundMode == nil {
		inputSoundMode = func(string) string { return "" }
	}
	return &session{
		receiver:       receiver,
		spec:           spec.withoutFlags(),
		driver:         driver,
		readings:       readings,
		log:            log,
		ctx:            ctx,
		cancel:         cancel,
		inputSoundMode: inputSoundMode,
		applyPower:     applyPower,
		room:           room,
		powered:        make(chan struct{}),
		reached:        make(chan struct{}),
		firstReadDone:  make(chan struct{}),
	}
}

// start reads the driver's state and applies the flags. When adopting
// is true, the session existed before this operator process started:
// the previous process already ran the one-shot for these flags, and a
// person may have changed the receiver since. So an adopted session only
// records the flags, and the one-shot waits for the next flag to turn on.
func (s *session) start(active, awake, adopting bool) {
	s.releaseWaits(s.driver.State())
	if !adopting {
		s.applyFlags(active, awake, true)
		return
	}
	s.active.Store(active)
	s.awake.Store(awake)
	if s.room != nil {
		s.room.opened(active || awake, s.flagWords(active, awake))
	}
}

// roomEvents connects a session to the Television in its room.
//
//   - opened reports that a session started, whether either of its
//     flags is true, and the text that describes them. The
//     implementation decides whether a person caused that start.
//   - woke reports that a flag turned on or that a power request turned
//     the room on.
//   - slept reports that both flags turned off, or that a power request
//     turned off a room with no TV that reports power. It sends the TV
//     no command.
//   - television returns the TV's name and power, both "" when the room
//     has no TV.
//   - standby turns the TV off, for a power request (session_power.go).
//   - show asks a TV that is on to show the session's Display, for a
//     home press (session_ensure.go).
type roomEvents interface {
	opened(awake bool, trigger string)
	woke(trigger string)
	slept()
	television() (name, power string)
	standby(trigger string)
	show(trigger string)
}

// setFlags applies the flags that media-operator wrote. Each flag that
// turns on runs the one-shot, and both turning on in one write run it
// once. So a screen that wakes during a Play selects the input again,
// which turns the receiver back on after a person switched it off with
// its own power button.
func (s *session) setFlags(active, awake bool) {
	s.applyFlags(active, awake, false)
}

// applyFlags is setFlags, with opening true on the session's first
// call. When both flags turn off, it tells the room and sends the
// receiver nothing, because the room may still be playing sound from
// another source.
func (s *session) applyFlags(active, awake, opening bool) {
	before := s.active.Load() || s.awake.Load()
	played := s.storeFlag(&s.active, active)
	woke := s.storeFlag(&s.awake, awake)
	// Tell the room here, in the order the flags change, and not from the
	// one-shot's goroutine. Otherwise a sleep right after a wake could
	// reach the TV first, and the TV would wake in a dark room. The TV
	// wake also does not wait for the receiver to respond.
	words := s.flagWords(played, woke)
	switch {
	case s.room == nil:
	case opening:
		s.room.opened(active || awake, words)
	case played || woke:
		s.room.woke(words)
	case before && !active && !awake:
		s.room.slept()
	}
	if played || woke {
		goWork(s.ctx, func() { s.selectInput(s.ctx, words) })
	}
}

// flagWords describes which flags turned on, as text for the log lines
// of the one-shot they run.
func (s *session) flagWords(played, woke bool) string {
	switch {
	case played && woke:
		return fmt.Sprintf("a Play started on Player %s and its screen woke", s.spec.Player)
	case played:
		return fmt.Sprintf("a Play started on Player %s", s.spec.Player)
	}
	return fmt.Sprintf("the screen of Player %s woke", s.spec.Player)
}

// storeFlag stores one flag and reports whether it changed from false
// to true.
func (s *session) storeFlag(flag *atomic.Bool, on bool) bool {
	if !on {
		flag.Store(false)
		return false
	}
	return flag.CompareAndSwap(false, true)
}

// stop ends the session's one-shots and leaves the receiver as it is.
func (s *session) stop() {
	s.cancel()
}

// observe receives each event from the driver.
func (s *session) observe(event equipment.Event) {
	s.releaseWaits(event.State)
}

// mainZone returns the main zone of a state. A session drives only the
// main zone.
func mainZone(state equipment.State) equipment.ZoneState {
	zone, _ := state.Zone(equipment.MainZone)
	return zone
}

// releaseWaits closes reached, firstReadDone, and powered when the state
// satisfies them.
func (s *session) releaseWaits(state equipment.State) {
	if state.Reachable == ConditionTrue {
		s.reachedOnce.Do(func() { close(s.reached) })
	}
	if s.driver.FirstReadDone() {
		s.firstReadOnce.Do(func() { close(s.firstReadDone) })
	}
	main := mainZone(state)
	if main.Power == equipment.PowerOn {
		s.markPowered()
	}
}

func (s *session) markPowered() {
	s.powerMutex.Lock()
	defer s.powerMutex.Unlock()
	select {
	case <-s.powered:
	default:
		close(s.powered)
	}
}

// armPower replaces powered with an open channel and returns it. The
// one-shot calls it before it reads the receiver's power, so a power-on
// report that arrives between the two still releases the wait.
func (s *session) armPower() <-chan struct{} {
	s.powerMutex.Lock()
	defer s.powerMutex.Unlock()
	s.powered = make(chan struct{})
	return s.powered
}

func (s *session) selectInput(ctx context.Context, trigger string) {
	s.oneShot.Lock()
	defer s.oneShot.Unlock()
	s.selectInputLocked(ctx, trigger)
}

// selectInputLocked is the one-shot, and the caller holds oneShot.
// trigger says what started it, for the log lines. It sends only the
// power, input, and sound mode that differ from what the receiver
// reports.
//
// It first waits for the first full read, with no timeout. The driver drops a
// command sent before the connection opens, and the one-shot never
// runs again, so a command sent early would be lost. Until the first full read
// ends, the driver also has no state to compare with.
func (s *session) selectInputLocked(ctx context.Context, trigger string) {
	if !s.waitForFirstRead(ctx) {
		return
	}
	powered := s.armPower()
	if mainZone(s.driver.State()).Power != equipment.PowerOn {
		line := trigger + "; sent power On"
		began := time.Now()
		if err := s.driver.SetPower(equipment.MainZone, true); err != nil {
			s.log.refused(line, err)
			return
		}
		select {
		case <-ctx.Done():
			s.log.printf("%s; the session ended before the receiver reported power On", line)
			return
		case <-powered:
			s.log.printf("%s; the receiver reported power On after %s", line, elapsed(time.Since(began)))
		case <-time.After(sessionPowerWait):
			s.readings.reportCommand(denon.CommandTimeout)
			s.log.printf("%s; the receiver did not report power On in %s, so the input goes out anyway", line, elapsed(sessionPowerWait))
		}
	}
	if ctx.Err() != nil {
		return
	}
	s.selectTheInput(trigger)
}

// waitForFirstRead waits until the receiver is reachable and has finished its first full read. It
// returns false when the session ends first.
func (s *session) waitForFirstRead(ctx context.Context) bool {
	for _, wait := range []chan struct{}{s.reached, s.firstReadDone} {
		select {
		case <-ctx.Done():
			return false
		case <-wait:
		}
	}
	return true
}

// selectTheInput sends the session's input and the input's sound mode,
// each only when the receiver reports a different value. The driver
// compares sound modes, because a receiver can report a mode in other
// words than the command that selects it. The one-shot and ensure
// (session_ensure.go) both call it, so both send the sound mode with
// the input.
func (s *session) selectTheInput(trigger string) {
	state := mainZone(s.driver.State())
	mode := s.inputSoundMode(s.spec.Input)
	sendInput := state.Input != s.spec.Input
	sendMode := mode != "" && !s.driver.SameSoundMode(mode, state.SoundMode)
	switch {
	case !sendInput && !sendMode:
		reports := []string{powerWords(state, 0), inputWords(state, 0)}
		if mode != "" {
			reports = append(reports, soundModeWords(state, 0))
		}
		s.log.printf("%s; sent nothing, because the receiver reports %s", trigger, wordList(reports))
		return
	case !sendInput:
		line := fmt.Sprintf("%s; sent sound mode %s", trigger, mode)
		began := time.Now()
		if err := s.driver.SetSoundMode(equipment.MainZone, mode); err != nil {
			s.log.refused(line, err)
			return
		}
		s.log.confirm(line, began, soundModeCheck(s.driver, mode))
		return
	}
	line := fmt.Sprintf("%s; sent input %s", trigger, s.spec.Input)
	began := time.Now()
	if err := s.driver.SetInput(equipment.MainZone, s.spec.Input); err != nil {
		s.log.refused(line, err)
		return
	}
	if sendMode {
		line += " and sound mode " + mode
		if err := s.driver.SetSoundMode(equipment.MainZone, mode); err != nil {
			s.log.refused(line, err)
			return
		}
	}
	s.log.confirm(line, began, mainZoneCheck(s.driver, "input "+s.spec.Input, inputWords))
}
