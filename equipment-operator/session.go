package main

// The session a Player holds on a receiver: the one-shot power and
// input, the owner mark on the bus, the level the operator applies
// while the mark stands, and the knob turn it publishes back.

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liken-sh/equipment-operator/denon"
	"github.com/liken-sh/equipment-operator/equipment"
)

// How long the operator waits for the receiver to answer PWON before it
// selects the input anyway.
const sessionPowerWait = 10 * time.Second

// How often the adopt looks again for a ceiling. The adopt waits on
// channels for the receiver's first volume and for the broker, and
// ticks only after both, while no ceiling maps the volume to a level:
// no spec.volume.max is declared, and the receiver reports no stable
// limit, as a Denon does not. A spec that states the ceiling can land
// after the session starts, so the adopt waits and does not give up.
// Each tick reads the session's own memory, the declared rule and the
// driver's last state, and sends nothing to the receiver or the API
// server. For a Denon with no spec.volume.max the tick runs for the
// whole session, which costs two reads of memory a second.
const sessionAdoptRetry = 500 * time.Millisecond

// How long a stop waits for the cleared owner mark to reach the broker
// before it closes the connection.
const sessionStopGrace = 200 * time.Millisecond

// session is one live session: the connection to the broker, the level
// it last sent the receiver, and the marks that tell an echo of its own
// write from a hand on the equipment.
type session struct {
	receiver string
	spec     ReceiverSession
	driver   equipment.Driver
	bus      *Bus
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
	// is awake. They gate the one-shots and nothing else. The level path
	// runs whatever they say.
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

	completeOnce sync.Once
	complete     chan struct{}

	connectedOnce sync.Once
	connected     chan struct{}

	// scale is read on every press and never held, so a spec edit reaches
	// a standing session with no restart.
	scale func() ReceiverVolume
	// inputSoundMode answers the sound mode a declared input names, read
	// when the session selects it.
	inputSoundMode func(input string) string

	mutex      sync.Mutex
	latest     volumeState
	haveLatest bool

	// awaiting holds every position the session put on the topic before
	// it adopted. The adopt publishes one, and a report before the adopt
	// publishes another, and the broker returns each in its own time, so
	// the first of them to come back is the adopt line.
	awaiting []volumeState
	adopted  bool

	// pending is the volume the session last sent and the receiver has
	// not reported yet (session_target.go).
	pending pendingVolume
}

// startSession opens the session's own broker connection and claims the
// level with a retained owner mark, for a session that appears while
// the operator runs.
//
// The power and input step runs once for a session that starts with
// either flag on, and once only when both are on at the start. A session
// that starts with both off owns the level and sends the equipment
// nothing.
func startSession(ctx context.Context, receiver string, spec ReceiverSession, driver equipment.Driver, readings *metrics, log *receiverLog, busAddress string, dial dialFunc, scale func() ReceiverVolume, inputSoundMode func(input string) string, applyPower func(power equipment.Power), room roomEvents) *session {
	s := newSession(ctx, receiver, spec, driver, readings, log, busAddress, dial, scale, inputSoundMode, applyPower, room)
	s.start(spec.Active, spec.Awake, false)
	return s
}

// newSession builds a session that reads nothing and sends nothing
// yet. The unit holds it before start, so every line the receiver sends
// from start on reaches it. A line that reached no session could be the
// one that says the receiver is reachable, and a session that missed it
// would never run its one-shot.
func newSession(ctx context.Context, receiver string, spec ReceiverSession, driver equipment.Driver, readings *metrics, log *receiverLog, busAddress string, dial dialFunc, scale func() ReceiverVolume, inputSoundMode func(input string) string, applyPower func(power equipment.Power), room roomEvents) *session {
	ctx, cancel := context.WithCancel(ctx)
	if inputSoundMode == nil {
		inputSoundMode = func(string) string { return "" }
	}
	s := &session{
		receiver:       receiver,
		spec:           spec.withoutFlags(),
		driver:         driver,
		readings:       readings,
		log:            log,
		ctx:            ctx,
		cancel:         cancel,
		scale:          scale,
		inputSoundMode: inputSoundMode,
		applyPower:     applyPower,
		room:           room,
		powered:        make(chan struct{}),
		reached:        make(chan struct{}),
		surveyed:       make(chan struct{}),
		complete:       make(chan struct{}),
		connected:      make(chan struct{}),
	}
	// The will clears the mark, so an operator that dies hands the level
	// back to the pods that were leaving it alone. A session with no
	// volume topic sets the level only from status.session.volumeAsk, so
	// it claims nothing on the bus and names no will.
	var will *busWill
	var claim func(*Bus)
	if spec.VolumeTopic != "" {
		will, claim = &busWill{Topic: ownerTopic(spec.VolumeTopic), Retained: true}, s.claim
	}
	s.bus = newBus(busAddress, dial, "equipment-operator-"+receiver, will, claim, s.receive)
	if spec.VolumeTopic != "" {
		s.bus.Subscribe(spec.VolumeTopic)
	}
	// The remote's power button publishes its toggle on the power topic,
	// and the session subscribes to it only when the media operator
	// named one. An absent topic subscribes the session to nothing.
	if spec.PowerTopic != "" {
		s.bus.Subscribe(spec.PowerTopic)
	}
	return s
}

// start reads the receiver's state as it stands, opens the broker
// connection, and takes the flags. A session that appears while the
// operator runs is a change a person made, so its flags run the power
// and input step. A session the operator finds when it starts is not:
// the last operator already ran the step for those flags, and a person
// may have changed the receiver since. So adopting stores the flags and
// runs no step, and only a later flip runs one.
func (s *session) start(active, awake, adopting bool) {
	s.mark(s.driver.State())
	// A session that names no topic has nothing to read or publish on the
	// bus, so it opens no broker connection.
	if s.spec.VolumeTopic != "" || s.spec.PowerTopic != "" {
		goWork(s.ctx, func() { s.bus.Run(s.ctx) })
	}
	if s.spec.VolumeTopic != "" {
		goWork(s.ctx, func() { s.adopt(s.ctx) })
	}
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

// stop clears the owner mark, waits for it to reach the broker, and
// closes the connection. It never powers the receiver off, because the
// room may still be listening to something else.
func (s *session) stop() {
	if s.spec.VolumeTopic == "" {
		s.cancel()
		return
	}
	s.publishOwner(nil)
	s.log.printf("cleared the owner mark on %s", ownerTopic(s.spec.VolumeTopic))
	time.Sleep(sessionStopGrace)
	s.cancel()
}

// handOver closes the connection and leaves the owner mark on the
// broker, for an operator that shuts down or a unit that a new wiring
// replaces. The next operator, or the new unit, starts the same session
// and publishes the same mark, so the playback pods leave the level
// alone through the change. The bus closes with DISCONNECT, so the
// broker drops the will; an operator that dies sends none, and the will
// clears the mark.
func (s *session) handOver() {
	if s.spec.VolumeTopic == "" {
		s.cancel()
		return
	}
	s.log.printf("kept the owner mark on %s for the session that takes over", ownerTopic(s.spec.VolumeTopic))
	s.cancel()
}

// claim publishes the mark on every fresh broker session, because a
// broker that restarted holds none of it.
func (s *session) claim(*Bus) {
	mark, err := ownerMark(s.receiver)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marking the owner of %s: %v\n", s.spec.VolumeTopic, err)
		return
	}
	s.publishOwner(mark)
	s.log.printf("published the owner mark %s on %s", mark, ownerTopic(s.spec.VolumeTopic))
	s.connectedOnce.Do(func() { close(s.connected) })
}

func (s *session) publishOwner(payload []byte) {
	s.bus.Publish(ownerTopic(s.spec.VolumeTopic), payload, true)
}

// receive reads one message off the topic. A message that differs from
// the state the session holds is a press, and the session moves the
// receiver one step in its direction. A message on the power topic is a
// toggle, and the session flips the receiver's power. The power topic is
// routed first, so a power topic that happens to equal the volume topic
// still reads as a toggle.
func (s *session) receive(topic string, payload []byte) {
	if topic == s.spec.PowerTopic {
		goWork(s.ctx, func() { s.togglePower(payload) })
		return
	}
	if topic != s.spec.VolumeTopic {
		return
	}
	state, ok := parseVolumeState(payload)
	if !ok {
		return
	}
	s.mutex.Lock()
	// The broker delivers a client's own publish back to it, and it sends
	// the topic's old retained state ahead of it. So the session's own
	// adopt message coming back is the line between the level a previous
	// session left and a press meant for this one. Everything before that
	// line was written for mpv's scale and moves nothing.
	if !s.adopted {
		if slices.Contains(s.awaiting, state) {
			s.latest, s.haveLatest, s.adopted = state, true, true
			s.awaiting = nil
		}
		s.mutex.Unlock()
		return
	}
	// A state the session already holds is its own message coming back.
	// Applying it again would map the level onto a half step the equipment
	// is not on.
	if s.haveLatest && s.latest == state {
		s.mutex.Unlock()
		return
	}
	previous := s.latest
	s.latest, s.haveLatest = state, true
	s.mutex.Unlock()

	s.press(previous, state)
}

// press reads the message as a direction and moves the receiver one
// step from where it actually stands, never to a level mapped through
// two scales. Mute is absolute. A press is a person's command, so each
// one is a line, and a press that moves nothing says why.
func (s *session) press(previous, state volumeState) {
	reading, _ := s.driver.State().Zone(equipment.MainZone)
	trigger := fmt.Sprintf("the volume topic went from %s to %s", previous, state)
	var sent []string
	var words []func(equipment.ZoneState, int) string
	began := time.Now()
	if state.Muted != reading.Mute {
		if err := s.driver.SetMute(equipment.MainZone, state.Muted); err != nil {
			s.log.refused(fmt.Sprintf("%s; sent mute %s", trigger, onOff(state.Muted)), err)
		} else {
			sent = append(sent, "mute "+onOff(state.Muted))
			words = append(words, muteWords)
		}
	}
	if state.Level != previous.Level {
		if target, moves := s.nextPosition(s.position(reading), state.Level > previous.Level); moves {
			volume := volumeWords(equipment.ZoneState{Volume: target}, s.driver.VolumeResolution())
			s.aim(target)
			if err := s.driver.SetVolume(equipment.MainZone, target); err != nil {
				s.release()
				s.log.refused(fmt.Sprintf("%s; sent %s", trigger, volume), err)
			} else {
				sent = append(sent, volume)
				words = append(words, volumeWords)
			}
		}
	}
	// A press the receiver answers is reported when its answer arrives.
	// One that moves nothing has no answer coming, so the position goes
	// back to the topic now.
	if len(sent) == 0 {
		s.log.printf("%s; sent nothing, because the receiver reports %s and %s, and the ceiling is %s",
			trigger, volumeWords(reading, s.driver.VolumeResolution()), muteWords(reading, 0), s.ceilingWords())
		s.report(s.position(reading))
		return
	}
	s.log.confirm(fmt.Sprintf("%s; sent %s", trigger, strings.Join(sent, " and ")), began, mainZoneCheck(s.driver, strings.Join(sent, " and "), words...))
}

// ceilingWords names the ceiling a press is measured against.
func (s *session) ceilingWords() string {
	ceiling := s.ceiling()
	if ceiling <= 0 {
		return "not known"
	}
	return volumeWords(equipment.ZoneState{Volume: ceiling}, s.driver.VolumeResolution())
}

// nextPosition answers where one press puts the receiver, and whether
// it moves at all.
func (s *session) nextPosition(reading equipment.ZoneState, up bool) (int, bool) {
	resolution := s.driver.VolumeResolution()
	ceiling := s.ceiling()
	if ceiling <= 0 || reading.Volume < 0 {
		return 0, false
	}
	// A hand can leave the receiver above the ceiling. A press up then
	// moves nothing, and never drops the receiver to the ceiling.
	if up && reading.Volume >= ceiling {
		return 0, false
	}
	// The ceiling bounds the way up and never the way down, so a receiver
	// above it steps down one press at a time.
	step := pressSteps(s.scale(), resolution)
	if !up {
		target := max(reading.Volume-step, 0)
		return target, target != reading.Volume
	}
	target := min(reading.Volume+step, ceiling)
	return target, target != reading.Volume
}

// report puts where the receiver actually stands back on the topic, so
// the sidecar's next press counts from a value that matches the
// equipment. A position reported before the adopt, such as the volume
// the receiver states when its connection opens, is the same message
// the adopt publishes, so the session takes its return as the adopt
// line as well. The broker returns the two in its own order, and the
// session must not wait for the second when the first has come back.
func (s *session) report(reading equipment.ZoneState) {
	level, ok := levelForSteps(reading.Volume, s.ceiling())
	if !ok {
		return
	}
	position := volumeState{Level: level, Muted: reading.Mute}.clamped()
	payload, err := marshalVolumeState(position)
	if err != nil {
		fmt.Fprintf(os.Stderr, "publishing the level of %s: %v\n", s.spec.VolumeTopic, err)
		return
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.haveLatest && s.latest == position {
		return
	}
	s.bus.Publish(s.spec.VolumeTopic, payload, true)
	s.latest, s.haveLatest = position, true
	if !s.adopted {
		s.awaiting = append(s.awaiting, position)
	}
}

// observe is the session's half of every line the receiver sends. It
// releases the waits the one-shots stand on, and it reports a position
// the operator did not ask for, such as a knob turn, on the volume
// topic when the session names one.
func (s *session) observe(event equipment.Event) {
	s.mark(event.State)
	if s.spec.VolumeTopic == "" {
		return
	}
	switch event.Field {
	case equipment.EventVolume, equipment.EventMute:
		if zone := mainZone(event.State); s.settle(zone) {
			s.report(zone)
		}
	}
}

// mainZone reads the zone a session drives out of one state.
func mainZone(state equipment.State) equipment.ZoneState {
	zone, _ := state.Zone(equipment.MainZone)
	return zone
}

// ceiling answers the loudest the bus level maps to, in the driver's
// smallest steps. A declared spec.volume.max wins. A protocol whose
// reported limit is a real ceiling, such as a WiiM at 100, provides one
// when none is declared; a Denon's reported limit moves with the volume,
// so its driver marks it unstable and the ceiling stays declared.
func (s *session) ceiling() int {
	if declared := ceilingSteps(s.scale(), s.driver.VolumeResolution()); declared > 0 {
		return declared
	}
	zone := mainZone(s.driver.State())
	if zone.VolumeMaxStable && zone.VolumeMax > 0 {
		return zone.VolumeMax
	}
	return 0
}

// mark releases the four waits a session stands on: the connection the
// commands go out over, the survey the power and input step compares
// with, the power the input selection follows, and the volume reading
// the adopt needs.
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
	if state.Reachable == ConditionTrue && main.Volume != equipment.Unknown {
		s.completeOnce.Do(func() { close(s.complete) })
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

// adopt is what makes the session willing to apply a level. The topic
// holds whatever the last session left on it, in mpv's scale and not
// this receiver's. So the session publishes where the equipment already
// stands and takes that as the state it holds. Until that message comes
// back, every message on the topic is history and moves nothing.
func (s *session) adopt(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-s.complete:
	}
	select {
	case <-ctx.Done():
		return
	case <-s.connected:
	}

	ticker := time.NewTicker(sessionAdoptRetry)
	defer ticker.Stop()
	for !s.publishPosition() {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// publishPosition puts the receiver's position on the topic and answers
// whether it went out. Everything that can stop it is something the
// next try may have: a ceiling nobody had declared yet, or a volume the
// receiver has not reported.
func (s *session) publishPosition() bool {
	state := mainZone(s.driver.State())
	level, ok := levelForSteps(state.Volume, s.ceiling())
	if !ok {
		return false
	}
	held := volumeState{Level: level, Muted: state.Mute}.clamped()
	payload, err := marshalVolumeState(held)
	if err != nil {
		fmt.Fprintf(os.Stderr, "adopting the level of %s: %v\n", s.spec.VolumeTopic, err)
		return false
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.bus.Publish(s.spec.VolumeTopic, payload, true)
	s.awaiting = append(s.awaiting, held)
	return true
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
