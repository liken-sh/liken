package main

// The session a Player holds on a receiver: the one-shot power and
// input that its flags run. The asks in status.session reach the same
// session (session_asks.go).

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liken-sh/equipment-operator/denon"
	"github.com/liken-sh/equipment-operator/equipment"
)

// How long the operator waits for the receiver to answer PWON before it
// selects the input anyway.
const sessionPowerWait = 10 * time.Second

// session is one live session: the Player's input, the flags the media
// operator wrote, and the waits its one-shots stand on.
type session struct {
	receiver string
	spec     ReceiverSession
	driver   equipment.Driver
	// readings counts a timeout this session's own power wait produced.
	readings *metrics
	// log is the receiver's log, which the unit shares with the session,
	// so a command's line waits for the same reports either way.
	log *receiverLog
	// The session's own context, held because a flip of either flag starts
	// the one-shots long after the session started, and they stop when it
	// does.
	ctx    context.Context
	cancel context.CancelFunc

	// The two flags: whether a Play stands, and whether the room's screen
	// is awake. They gate the one-shots and nothing else.
	active atomic.Bool
	awake  atomic.Bool

	// The gate the input selection waits on, closed when the receiver
	// reports itself on. It is armed again for each selection, so a second
	// Play waits for its own power-on.
	powerMutex sync.Mutex
	powered    chan struct{}

	// oneShot serializes the power and input one-shots. A toggle and a
	// flag flip could otherwise drive the receiver at the same moment,
	// and a standby landing mid power-on would leave the receiver off
	// while the flags still say on, with nothing left to re-assert.
	oneShot sync.Mutex
	// applyPower records the power the session settled on and writes it
	// to the spec, wired to the unit that owns the receiver. It is nil in
	// a test that never toggles.
	applyPower func(power equipment.Power)
	// room hears each wake and each sleep of the room, so the TV of the
	// room wakes with the receiver. It is nil in a test with no TV.
	room roomEvents

	reachedOnce sync.Once
	reached     chan struct{}

	// surveyed closes when the driver has read the receiver's own facts,
	// which the power and input step compares with before it sends.
	surveyedOnce sync.Once
	surveyed     chan struct{}

	// inputSoundMode answers the sound mode a declared input names, read
	// when the session selects it.
	inputSoundMode func(input string) string
}

// newSession builds a session that reads nothing and sends nothing
// yet. The unit holds it before start, so every line the receiver sends
// from start on reaches it. A line that reached no session could be the
// one that says the receiver is reachable, and a session that missed it
// would never run its one-shot.
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
		surveyed:       make(chan struct{}),
	}
}

// start reads the receiver's state as it stands and takes the flags. A
// session that appears while the operator runs is a change a person
// made, so its flags run the power and input step. A session the
// operator finds when it starts is not: the last operator already ran
// the step for those flags, and a person may have changed the receiver
// since. So adopting stores the flags and runs no step, and only a
// later flip runs one.
func (s *session) start(active, awake, adopting bool) {
	s.mark(s.driver.State())
	if !adopting {
		s.flags(active, awake, true)
		return
	}
	s.active.Store(active)
	s.awake.Store(awake)
	if s.room != nil {
		s.room.opened(active || awake, s.flagWords(active, awake))
	}
}

// roomEvents is how a session tells the TV of its room what it did to
// the receiver. opened reports a session that starts, with whether its
// flags hold the room awake and the words that name them; the room
// decides whether the start is a change a person caused. woke reports a
// wake the session saw happen: a flag that turns on, or the remote's
// power button that turns the room on. slept reports both flags turning
// off, and asks the TV for nothing. television answers the TV of the
// room and the power it reports, empty when the room has none, and
// standby asks that TV to go to standby: only the remote's power button
// calls it. show asks that TV to show the session's Display when it is
// on: only a home press calls it.
type roomEvents interface {
	opened(awake bool, trigger string)
	woke(trigger string)
	slept()
	television() (name, power string)
	standby(trigger string)
	show(trigger string)
}

// setFlags takes both flags as the media operator wrote them: active
// when a Play starts or ends, awake when the room's screen wakes or
// sleeps. Either one turning on runs the one-shots once, and both
// turning on in one write runs them once and not twice. A screen that
// wakes under a standing Play selects the input again, which is what a
// person who reached for the receiver's own power button needs.
func (s *session) setFlags(active, awake bool) {
	s.flags(active, awake, false)
}

// flags is setFlags, with opening for the session's first call. Both
// flags going off puts the room to sleep for its TV, and sends the
// receiver nothing.
func (s *session) flags(active, awake, opening bool) {
	before := s.active.Load() || s.awake.Load()
	played := s.raise(&s.active, active)
	woke := s.raise(&s.awake, awake)
	// The room hears the flags here, in the order they change, and not
	// from the one-shot, which runs later on its own goroutine: a sleep
	// that follows at once must reach the TV after the wake, or the TV
	// would wake in a room that is going dark. The TV is reached over
	// another path than the receiver, so its wake does not wait for the
	// receiver to answer. The room decides whether the flags a session
	// starts with are a change a person caused.
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

// flagWords names what raised a flag, for the lines of the one-shots
// it runs.
func (s *session) flagWords(played, woke bool) string {
	switch {
	case played && woke:
		return fmt.Sprintf("a Play started on Player %s and its screen woke", s.spec.Player)
	case played:
		return fmt.Sprintf("a Play started on Player %s", s.spec.Player)
	}
	return fmt.Sprintf("the screen of Player %s woke", s.spec.Player)
}

// raise stores one flag and answers whether this is the false to true
// that runs the one-shots. True to false sends nothing, because the
// room may still be listening.
func (s *session) raise(flag *atomic.Bool, on bool) bool {
	if !on {
		flag.Store(false)
		return false
	}
	return flag.CompareAndSwap(false, true)
}

// stop ends the session's one-shots. It never powers the receiver off,
// because the room may still be listening to something else.
func (s *session) stop() {
	s.cancel()
}

// observe is the session's half of every line the receiver sends: it
// releases the waits the one-shots stand on.
func (s *session) observe(event equipment.Event) {
	s.mark(event.State)
}

// mainZone reads the zone a session drives out of one state.
func mainZone(state equipment.State) equipment.ZoneState {
	zone, _ := state.Zone(equipment.MainZone)
	return zone
}

// mark releases the three waits a session stands on: the connection the
// commands go out over, the survey the power and input step compares
// with, and the power the input selection follows.
func (s *session) mark(state equipment.State) {
	if state.Reachable == ConditionTrue {
		s.reachedOnce.Do(func() { close(s.reached) })
	}
	if s.driver.Surveyed() {
		s.surveyedOnce.Do(func() { close(s.surveyed) })
	}
	main := mainZone(state)
	if main.Power == equipment.PowerOn {
		s.notePower()
	}
}

// notePower releases whoever waits for the receiver to come on.
func (s *session) notePower() {
	s.powerMutex.Lock()
	defer s.powerMutex.Unlock()
	select {
	case <-s.powered:
	default:
		close(s.powered)
	}
}

// armPower answers the gate that closes when the receiver next reports
// itself on. A selection arms it before it reads the power, so a
// receiver that answers in between still releases the wait.
func (s *session) armPower() <-chan struct{} {
	s.powerMutex.Lock()
	defer s.powerMutex.Unlock()
	s.powered = make(chan struct{})
	return s.powered
}

// selectInput powers the receiver on, waits for it to say so, and
// selects the input once for whoever asked. The power one-shot is
// serialized against a toggle, so the two never drive the receiver at
// the same moment.
func (s *session) selectInput(ctx context.Context, trigger string) {
	s.oneShot.Lock()
	defer s.oneShot.Unlock()
	s.selectInputLocked(ctx, trigger)
}

// selectInputLocked is the power-and-input one-shot, called with oneShot
// held by either a flag flip or a toggle. trigger names what asked for
// it, for the lines it writes. It waits for the survey, because before
// it the receiver has reported nothing to compare with, and then it
// sends only the power, the input, and the sound mode that the receiver
// reports at another value.
func (s *session) selectInputLocked(ctx context.Context, trigger string) {
	// A command sent before the connection is open is dropped, and a one-
	// shot is never re-asserted, so the wait for the survey, which comes
	// after the connection, is what makes the one-shot land. The session's
	// own lifetime is the bound: a receiver that never answers has nothing
	// to select.
	if !s.waitForSurvey(ctx) {
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

// waitForSurvey waits until the receiver is reachable and surveyed, and
// answers false when the session ends first.
func (s *session) waitForSurvey(ctx context.Context) bool {
	for _, wait := range []chan struct{}{s.reached, s.surveyed} {
		select {
		case <-ctx.Done():
			return false
		case <-wait:
		}
	}
	return true
}

// selectTheInput sends the session's input and the sound mode that
// travels with it, each only when the receiver reports another value.
// The driver compares the sound mode, because a receiver can report the
// mode it runs in other words than the command that selects it. The
// power one-shot and the ensure both reach it, so the mode and the
// input can never drift apart.
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
