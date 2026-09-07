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

// How long a burst of volume messages is collected before one set goes
// to the receiver.
var sessionVolumeDebounce = 100 * time.Millisecond

// How long a stop waits for the cleared owner mark to reach the broker
// before it closes the connection.
var sessionStopGrace = 200 * time.Millisecond

// A mute state the operator has not sent yet.
const muteUnsent = -1

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

	pending chan struct{}

	mutex      sync.Mutex
	latest     volumeState
	haveLatest bool
	sentVolume int
	sentMute   int
}

// startSession opens the session's own broker connection, claims the
// level with a retained owner mark, and drives power and input once.
func startSession(ctx context.Context, receiver string, spec ReceiverSession, denon *denonClient, busAddress string) *session {
	ctx, cancel := context.WithCancel(ctx)
	s := &session{
		receiver:   receiver,
		spec:       spec,
		denon:      denon,
		cancel:     cancel,
		powerOn:    make(chan struct{}),
		reached:    make(chan struct{}),
		pending:    make(chan struct{}, 1),
		sentVolume: unknownHalves,
		sentMute:   muteUnsent,
	}
	s.mark(denon.State())

	// The will clears the mark, so an operator that dies hands the level
	// back to the pods that were leaving it alone.
	will := &busWill{Topic: ownerTopic(spec.VolumeTopic), Retained: true}
	s.bus = newBus(busAddress, "equipment-operator-"+receiver, will, s.claim, s.receive)
	s.bus.Subscribe(spec.VolumeTopic)
	go s.bus.Run(ctx)
	go s.selectInput(ctx)
	go s.applyLevels(ctx)
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
}

func (s *session) publishOwner(payload []byte) {
	s.bus.Publish(ownerTopic(s.spec.VolumeTopic), payload, true)
}

// receive records the latest state the topic carries and wakes the
// sender, so a burst of presses reaches the receiver as one set.
func (s *session) receive(topic string, payload []byte) {
	if topic != s.spec.VolumeTopic {
		return
	}
	state, ok := parseVolumeState(payload)
	if !ok {
		return
	}
	s.mutex.Lock()
	// A state the session already holds is its own message coming back.
	// Applying it again would map the level onto a half step the equipment
	// is not on.
	if s.haveLatest && s.latest == state {
		s.mutex.Unlock()
		return
	}
	s.latest, s.haveLatest = state, true
	s.mutex.Unlock()
	poke(s.pending)
}

// observe is the session's half of every line the receiver sends. It
// releases the wait on power, and it publishes a level the operator did
// not ask for.
func (s *session) observe(event denonEvent) {
	s.mark(event.State)
	switch event.Field {
	case denonVolumeField, denonMuteField:
		s.writeBack(event)
	case denonVolumeMaxField:
		// A level read before the receiver reported its limit could not be
		// mapped, so the limit's arrival is a second chance to send it.
		s.mutex.Lock()
		held := s.haveLatest
		s.mutex.Unlock()
		if held {
			poke(s.pending)
		}
	}
}

// mark releases the two waits the one-shots stand on: the connection
// the commands go out over, and the power the input selection follows.
func (s *session) mark(state denonState) {
	if state.Reachable == ConditionTrue {
		s.reachedOnce.Do(func() { close(s.reached) })
	}
	if state.Power == powerOn {
		s.powerOnce.Do(func() { close(s.powerOn) })
	}
}

// writeBack publishes what the receiver reports, unless it is the echo
// of this operator's own last write. While the mark stands the receiver
// owns the level, so its position is what the topic must carry.
func (s *session) writeBack(event denonEvent) {
	s.mutex.Lock()
	if event.Field == denonVolumeField && event.State.Volume == s.sentVolume {
		s.sentVolume = unknownHalves
		s.mutex.Unlock()
		return
	}
	if event.Field == denonMuteField && s.sentMute != muteUnsent && event.State.Mute == (s.sentMute == 1) {
		s.sentMute = muteUnsent
		s.mutex.Unlock()
		return
	}
	s.mutex.Unlock()

	level, ok := levelForHalves(event.State.Volume, event.State.VolumeMax)
	if !ok {
		return
	}
	payload, err := marshalVolumeState(volumeState{Level: level, Muted: event.State.Mute})
	if err != nil {
		fmt.Fprintf(os.Stderr, "publishing the level of %s: %v\n", s.spec.VolumeTopic, err)
		return
	}
	// The session subscribes to the topic it publishes on. It holds what
	// it published as the state it last applied, so the broker's delivery
	// of that same message moves nothing.
	published := volumeState{Level: level, Muted: event.State.Mute}.clamped()
	s.mutex.Lock()
	s.bus.Publish(s.spec.VolumeTopic, payload, true)
	s.latest, s.haveLatest = published, true
	s.mutex.Unlock()
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

// applyLevels sends the receiver the latest level the topic carries,
// one set per burst.
func (s *session) applyLevels(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.pending:
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(sessionVolumeDebounce):
		}
		drainPokes(s.pending)
		s.sendLevel()
	}
}

// sendLevel maps the held level onto the receiver's scale and marks
// what it sent, so the echo that follows publishes nothing back.
func (s *session) sendLevel() {
	state := s.denon.State()
	s.mutex.Lock()
	held, level := s.haveLatest, s.latest
	s.mutex.Unlock()
	if !held {
		return
	}

	halves, ok := halvesForLevel(level.Level, state.VolumeMax)
	if !ok {
		return
	}
	s.mutex.Lock()
	s.sentVolume = halves
	s.sentMute = 0
	if level.Muted {
		s.sentMute = 1
	}
	s.mutex.Unlock()

	s.denon.Send(denonVolumeCommand(halves))
	s.denon.Send(denonMuteCommand(level.Muted))
}
