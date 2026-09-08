package main

// The session a Player holds on a receiver: the one-shot power and
// input, the owner mark on the bus, the level the operator applies
// while the mark stands, and the knob turn it publishes back.

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"
)

// How long the operator waits for the receiver to answer PWON before it
// selects the input anyway.
var sessionPowerWait = 10 * time.Second

// How often the adopt looks again for what it needs. The spec that
// states the ceiling can land after the session starts, so an adopt
// that cannot map a level yet waits and does not give up.
var sessionAdoptRetry = 500 * time.Millisecond

// How long a stop waits for the cleared owner mark to reach the broker
// before it closes the connection.
var sessionStopGrace = 200 * time.Millisecond

// session is one live session: the connection to the broker, the level
// it last sent the receiver, and the marks that tell an echo of its own
// write from a hand on the equipment.
type session struct {
	receiver string
	spec     ReceiverSession
	denon    *denonClient
	bus      *Bus
	cancel   context.CancelFunc

	powerOnce sync.Once
	powerOn   chan struct{}

	reachedOnce sync.Once
	reached     chan struct{}

	completeOnce sync.Once
	complete     chan struct{}

	connectedOnce sync.Once
	connected     chan struct{}

	// scale is read on every press and never held, so a spec edit reaches
	// a standing session with no restart.
	scale func() ReceiverVolume

	mutex      sync.Mutex
	latest     volumeState
	haveLatest bool

	awaiting     volumeState
	haveAwaiting bool
	adopted      bool
}

// startSession opens the session's own broker connection, claims the
// level with a retained owner mark, and drives power and input once.
func startSession(ctx context.Context, receiver string, spec ReceiverSession, denon *denonClient, busAddress string, scale func() ReceiverVolume) *session {
	ctx, cancel := context.WithCancel(ctx)
	s := &session{
		receiver:  receiver,
		spec:      spec,
		denon:     denon,
		cancel:    cancel,
		scale:     scale,
		powerOn:   make(chan struct{}),
		reached:   make(chan struct{}),
		complete:  make(chan struct{}),
		connected: make(chan struct{}),
	}
	s.mark(denon.State())

	// The will clears the mark, so an operator that dies hands the level
	// back to the pods that were leaving it alone.
	will := &busWill{Topic: ownerTopic(spec.VolumeTopic), Retained: true}
	s.bus = newBus(busAddress, "equipment-operator-"+receiver, will, s.claim, s.receive)
	s.bus.Subscribe(spec.VolumeTopic)
	go s.bus.Run(ctx)
	go s.adopt(ctx)
	go s.selectInput(ctx)
	return s
}

// stop clears the owner mark, waits for it to reach the broker, and
// closes the connection. It never powers the receiver off, because the
// room may still be listening to something else.
func (s *session) stop() {
	s.publishOwner(nil)
	time.Sleep(sessionStopGrace)
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
	s.connectedOnce.Do(func() { close(s.connected) })
}

func (s *session) publishOwner(payload []byte) {
	s.bus.Publish(ownerTopic(s.spec.VolumeTopic), payload, true)
}

// receive reads one message off the topic. A message that differs from
// the state the session holds is a press, and the session moves the
// receiver one step in its direction.
func (s *session) receive(topic string, payload []byte) {
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
		if s.haveAwaiting && s.awaiting == state {
			s.latest, s.haveLatest, s.adopted = state, true, true
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
// two scales. Mute is absolute.
func (s *session) press(previous, state volumeState) {
	reading := s.denon.State()
	sent := false
	if state.Muted != reading.Mute {
		s.denon.Send(denonMuteCommand(state.Muted))
		sent = true
	}
	if state.Level != previous.Level {
		if target, moves := s.nextPosition(reading, state.Level > previous.Level); moves {
			s.denon.Send(denonVolumeCommand(target))
			sent = true
		}
	}
	// A press the receiver answers is reported when its answer arrives.
	// One that moves nothing has no answer coming, so the position goes
	// back to the topic now.
	if !sent {
		s.report(reading)
	}
}

// nextPosition answers where one press puts the receiver, and whether
// it moves at all.
func (s *session) nextPosition(reading denonState, up bool) (int, bool) {
	ceiling := ceilingHalves(s.scale())
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
	step := pressHalves(s.scale())
	if !up {
		target := max(reading.Volume-step, 0)
		return target, target != reading.Volume
	}
	target := min(reading.Volume+step, ceiling)
	return target, target != reading.Volume
}

// report puts where the receiver actually stands back on the topic, so
// the sidecar's next press counts from a value that matches the
// equipment.
func (s *session) report(reading denonState) {
	level, ok := levelForHalves(reading.Volume, ceilingHalves(s.scale()))
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
}

// observe is the session's half of every line the receiver sends. It
// releases the waits the one-shots stand on, and it reports a position
// the operator did not ask for, such as a knob turn.
func (s *session) observe(event denonEvent) {
	s.mark(event.State)
	switch event.Field {
	case denonVolumeField, denonMuteField:
		s.report(event.State)
	}
}

// mark releases the three waits a session stands on: the connection the
// commands go out over, the power the input selection follows, and the
// volume reading the adopt needs.
func (s *session) mark(state denonState) {
	if state.Reachable == ConditionTrue {
		s.reachedOnce.Do(func() { close(s.reached) })
	}
	if state.Power == powerOn {
		s.powerOnce.Do(func() { close(s.powerOn) })
	}
	if state.Reachable == ConditionTrue && state.Volume != unknownHalves {
		s.completeOnce.Do(func() { close(s.complete) })
	}
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
	state := s.denon.State()
	level, ok := levelForHalves(state.Volume, ceilingHalves(s.scale()))
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
	s.awaiting, s.haveAwaiting = held, true
	return true
}

// selectInput powers the receiver on, waits for it to say so, and
// selects the input once. The input is never re-asserted: a hand on the
// equipment outranks the cluster.
func (s *session) selectInput(ctx context.Context) {
	// A command sent before the connection is open is dropped, and a one-
	// shot is never re-asserted, so the wait for the connection is what
	// makes the one-shot land. The session's own lifetime is the bound: a
	// receiver that never answers has nothing to select.
	select {
	case <-ctx.Done():
		return
	case <-s.reached:
	}
	if s.denon.State().Power != powerOn {
		s.denon.Send(denonPowerOnCommand)
	}
	select {
	case <-ctx.Done():
		return
	case <-s.powerOn:
	case <-time.After(sessionPowerWait):
	}
	if ctx.Err() != nil {
		return
	}
	s.denon.Send(denonInputCommand(s.spec.Input))
}
