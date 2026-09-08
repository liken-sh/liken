package main

// The session against a fake receiver and a fake broker, both on real
// sockets.

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// The topic every session test holds, and the window a test watches for
// a message that must not arrive.
const (
	testVolumeTopic = "liken/players/theater/volume"
	quietPeriod     = 300 * time.Millisecond
)

// sessionHolder is the receiverUnit's half of the wiring. It replays
// what arrived while the session was being started.
type sessionHolder struct {
	mutex  sync.Mutex
	held   *session
	missed []denonEvent
}

func (h *sessionHolder) observe(event denonEvent) {
	h.mutex.Lock()
	held := h.held
	if held == nil {
		h.missed = append(h.missed, event)
		h.mutex.Unlock()
		return
	}
	h.mutex.Unlock()
	held.observe(event)
}

func (h *sessionHolder) forget() {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.missed = nil
}

func (h *sessionHolder) set(started *session) {
	h.mutex.Lock()
	missed := h.missed
	h.held, h.missed = started, nil
	h.mutex.Unlock()
	for _, event := range missed {
		started.observe(event)
	}
}

// sessionHarness is a running connection to a fake receiver and a fake
// broker, ready for a session.
type sessionHarness struct {
	equipment *fakeDenon
	brokers   *fakeBrokerServer
	denon     *denonClient
	holder    *sessionHolder
	rule      ReceiverVolume
}

func newSessionHarness(t *testing.T) *sessionHarness {
	return newSessionHarnessWith(t, ReceiverVolume{Max: 69.5})
}

// newSessionHarnessWith names the ceiling and the step the session
// presses against, which a person states and the receiver never reports.
func newSessionHarnessWith(t *testing.T, rule ReceiverVolume) *sessionHarness {
	t.Helper()
	h := &sessionHarness{
		rule:      rule,
		equipment: startFakeDenon(t),
		brokers:   startFakeBrokerServer(t),
		holder:    &sessionHolder{},
	}
	h.denon = newDenonClient(h.equipment.address(), h.holder.observe)
	go h.denon.Run(t.Context())
	h.waitUntil(t, func(state denonState) bool {
		return state.Power != "" && state.VolumeMax != unknownHalves
	})
	return h
}

// waitUntil polls the state the operator holds, bounded by testTimeout.
func (h *sessionHarness) waitUntil(t *testing.T, ready func(denonState) bool) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		if ready(h.denon.State()) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the receiver never reached the state the test was waiting for")
}

func (h *sessionHarness) drainCommands() {
	for {
		select {
		case <-h.equipment.commands:
		default:
			return
		}
	}
}

// begin starts the session with the connect queries already read, so
// the next command is the session's own.
func (h *sessionHarness) begin(t *testing.T, input string) *session {
	t.Helper()
	h.drainCommands()
	h.holder.forget()
	spec := ReceiverSession{Player: "theater", Input: input, VolumeTopic: testVolumeTopic}
	started := startSession(t.Context(), "theater", spec, h.denon, h.brokers.address(), func() ReceiverVolume { return h.rule })
	h.holder.set(started)
	return started
}

// powerOn turns the receiver on before any session stands.
func (h *sessionHarness) powerOn(t *testing.T) {
	t.Helper()
	h.denon.Send(denonPowerOnCommand)
	h.waitUntil(t, func(state denonState) bool { return state.Power == powerOn })
}

// refuseCommands fails the test if any named command goes out within
// the window.
func (h *sessionHarness) refuseCommands(t *testing.T, within time.Duration, unwanted ...string) {
	t.Helper()
	refused := map[string]bool{}
	for _, command := range unwanted {
		refused[command] = true
	}
	deadline := time.After(within)
	for {
		select {
		case command := <-h.equipment.commands:
			if refused[command] {
				t.Fatalf("the operator sent %q", command)
			}
		case <-deadline:
			return
		}
	}
}

func TestTheSessionPowersOnThenSelectsTheInput(t *testing.T) {
	h := newSessionHarness(t)

	h.begin(t, "GAME")

	mustMatch(t, h.equipment.waitForCommand(t), denonPowerOnCommand)
	mustMatch(t, h.equipment.waitForCommand(t), "SIGAME")
}

func TestTheSessionSelectsTheInputOnAReceiverAlreadyOn(t *testing.T) {
	h := newSessionHarness(t)
	h.powerOn(t)

	h.begin(t, "GAME")

	mustMatch(t, h.equipment.waitForCommand(t), "SIGAME")
}

// handOnTheRemote writes one line the way a person's own remote does,
// with nothing in the cluster having asked.
func handOnTheRemote(t *testing.T, equipment *fakeDenon, line string) {
	t.Helper()
	conn, err := net.Dial("tcp", equipment.address())
	mustSucceed(t, err)
	t.Cleanup(func() { conn.Close() })
	_, err = conn.Write([]byte(line + string(denonTerminator)))
	mustSucceed(t, err)
}

func TestTheSessionSelectsTheInputOnce(t *testing.T) {
	h := newSessionHarness(t)
	h.begin(t, "GAME")
	h.equipment.waitForCommands(t, "SIGAME")

	handOnTheRemote(t, h.equipment, "SIDVD")
	h.waitUntil(t, func(state denonState) bool { return state.Input == "DVD" })

	h.refuseCommands(t, quietPeriod, "SIGAME")
}

func TestTheSessionMarksItselfTheOwnerOfTheLevel(t *testing.T) {
	h := newSessionHarness(t)

	h.begin(t, "GAME")

	broker := h.brokers.waitForSession(t)
	mustMatch(t, waitForString(t, broker.subs), testVolumeTopic)
	mark := broker.waitForTopic(t, ownerTopic(testVolumeTopic))
	mustMatch(t, string(mark.payload), `{"owner":"receiver/theater"}`)
	mustMatch(t, mark.retained, true)
}

// connectWill reads the will out of an MQTT CONNECT body. The payload
// holds the client identifier, the will topic, and the will payload,
// each length-prefixed.
func connectWill(t *testing.T, body []byte) busWill {
	t.Helper()
	const flagsAt, payloadAt = 7, 10
	identifier, _, ok := readTopicFilter(body[payloadAt:])
	mustMatch(t, ok, true)
	topicLength, topic, ok := readTopicFilter(body[payloadAt+identifier:])
	mustMatch(t, ok, true)
	_, payload, ok := readTopicFilter(body[payloadAt+identifier+topicLength:])
	mustMatch(t, ok, true)
	return busWill{
		Topic:    topic,
		Payload:  []byte(payload),
		Retained: body[flagsAt]&connectWillRetain != 0,
	}
}

func waitForFrame(t *testing.T, frames <-chan []byte) []byte {
	t.Helper()
	select {
	case frame := <-frames:
		return frame
	case <-time.After(testTimeout):
		t.Fatal("the session sent no CONNECT")
		return nil
	}
}

func TestTheSessionNamesAWillThatClearsTheOwnerMark(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	mustSucceed(t, err)
	t.Cleanup(func() { listener.Close() })
	frames := make(chan []byte, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, body, err := readPacket(bufio.NewReader(conn))
		if err != nil {
			return
		}
		frames <- body
	}()

	spec := ReceiverSession{Player: "theater", Input: "GAME", VolumeTopic: testVolumeTopic}
	startSession(t.Context(), "theater", spec, newDenonClient("127.0.0.1:1", nil), listener.Addr().String(),
		func() ReceiverVolume { return ReceiverVolume{Max: 69.5} })

	will := connectWill(t, waitForFrame(t, frames))
	mustMatch(t, will.Topic, ownerTopic(testVolumeTopic))
	mustMatch(t, will.Retained, true)
	mustMatch(t, len(will.Payload), 0)
}

// listening is a harness whose session has selected its input and
// marked itself the owner, which is where every level test starts.
func listening(t *testing.T) (*sessionHarness, *fakeBroker, *session) {
	return listeningWith(t, ReceiverVolume{Max: 69.5}, 72)
}

// listeningWith names the ceiling and the step, and the bus level the
// receiver's own position lands on under them.
func listeningWith(t *testing.T, rule ReceiverVolume, adoptLevel int) (*sessionHarness, *fakeBroker, *session) {
	t.Helper()
	h := newSessionHarnessWith(t, rule)
	held := h.begin(t, "GAME")
	broker := h.brokers.waitForSession(t)
	broker.waitForTopic(t, ownerTopic(testVolumeTopic))
	// The session publishes where the receiver stands and waits for that
	// message to come back before it applies anything, so every test
	// below starts from the other side of that boundary.
	adopted := broker.waitForTopic(t, testVolumeTopic)
	mustMatch(t, positionOf(t, adopted), volumeState{Level: adoptLevel})
	broker.push(testVolumeTopic, adopted.payload)
	waitUntilAdopted(t, held)
	h.equipment.waitForCommands(t, "SIGAME")
	return h, broker, held
}

// waitUntilAdopted waits for the session to take the broker's delivery
// of its own adopt message, which is the point from which a press moves
// the receiver.
func waitUntilAdopted(t *testing.T, held *session) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		held.mutex.Lock()
		adopted := held.adopted
		held.mutex.Unlock()
		if adopted {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the session never took its own adopt message back")
}

// positionOf reads the state a message on the volume topic carries.
func positionOf(t *testing.T, published brokerPublish) volumeState {
	t.Helper()
	state, ok := parseVolumeState(published.payload)
	mustMatch(t, ok, true)
	return state
}

// pressFrom publishes the state one press produces from a level the way
// the sidecar does, and answers the position the session put back on the
// topic once the receiver had answered.
func pressFrom(t *testing.T, h *sessionHarness, broker *fakeBroker, from, by int, want string) volumeState {
	t.Helper()
	payload, err := marshalVolumeState(volumeState{Level: from + by})
	mustSucceed(t, err)
	broker.push(testVolumeTopic, payload)
	h.equipment.waitForCommands(t, want)
	return positionOf(t, broker.waitForTopic(t, testVolumeTopic))
}

// A held key arrives as one message per press, and each one moves the
// receiver a single step from where it actually stands.
func TestThreePressesMoveTheReceiverThreeSteps(t *testing.T) {
	h, broker, _ := listeningWith(t, ReceiverVolume{Max: 69.5, Step: 1}, 72)

	first := pressFrom(t, h, broker, 72, 5, "MV51")
	mustMatch(t, first, volumeState{Level: 73})
	second := pressFrom(t, h, broker, first.Level, 5, "MV52")
	mustMatch(t, second, volumeState{Level: 75})
	third := pressFrom(t, h, broker, second.Level, 5, "MV53")
	mustMatch(t, third, volumeState{Level: 76})

	h.waitUntil(t, func(state denonState) bool { return state.Volume == 106 })
}

// A press down moves the receiver down by the step a person declared.
func TestAPressDownMovesTheReceiverDownOneStep(t *testing.T) {
	h, broker, _ := listeningWith(t, ReceiverVolume{Max: 69.5, Step: 0.5}, 72)

	mustMatch(t, pressFrom(t, h, broker, 72, -5, "MV495"), volumeState{Level: 71})
}

// A step of two moves the receiver two units of its own scale.
func TestAStepOfTwoMovesTheReceiverTwoUnits(t *testing.T) {
	h, broker, _ := listeningWith(t, ReceiverVolume{Max: 69.5, Step: 2}, 72)

	mustMatch(t, pressFrom(t, h, broker, 72, 5, "MV52"), volumeState{Level: 75})
}

// Presses stop at the ceiling a person declared, and the topic then
// carries the top of the bus scale.
func TestPressesStopAtTheDeclaredCeiling(t *testing.T) {
	h, broker, _ := listeningWith(t, ReceiverVolume{Max: 52, Step: 1}, 96)

	mustMatch(t, pressFrom(t, h, broker, 96, 5, "MV51"), volumeState{Level: 98})

	// The press that reaches the ceiling already carries the top of the
	// bus scale, so the session has nothing further to say about it.
	broker.push(testVolumeTopic, []byte(`{"level":100,"muted":false}`))
	h.equipment.waitForCommands(t, "MV52")
	h.waitUntil(t, func(state denonState) bool { return state.Volume == 104 })

	broker.push(testVolumeTopic, []byte(`{"level":100,"muted":false}`))
	refuseVolumeSets(t, h.equipment, quietPeriod)
}

// A ceiling above the top of a Denon's own scale is that top, so a
// person cannot ask for a volume the receiver does not have.
func TestACeilingAboveTheDenonScaleIsTheScaleTop(t *testing.T) {
	h, broker, _ := listeningWith(t, ReceiverVolume{Max: 120, Step: 1}, 51)

	mustMatch(t, pressFrom(t, h, broker, 51, 5, "MV51"), volumeState{Level: 52})
}

// A hand can leave the receiver above the ceiling. The topic then reads
// the top of the scale, a press up moves nothing, and a press down still
// steps.
func TestAReceiverAboveTheCeilingStaysUntilAPressDown(t *testing.T) {
	h, broker, _ := listeningWith(t, ReceiverVolume{Max: 45, Step: 1}, 100)

	// The topic already reads the top of the scale, so a press up has
	// nothing above it to ask for and the receiver stays where it is.
	broker.push(testVolumeTopic, []byte(`{"level":100,"muted":false}`))
	refuseVolumeSets(t, h.equipment, quietPeriod)

	broker.push(testVolumeTopic, []byte(`{"level":95,"muted":false}`))
	h.equipment.waitForCommands(t, "MV49")
	h.waitUntil(t, func(state denonState) bool { return state.Volume == 98 })
}

// The limit a Denon reports wanders while the room is playing, so the
// session reads none of it and a new figure moves nothing.
func TestAWanderingReportedLimitMovesNothing(t *testing.T) {
	h, broker, _ := listeningWith(t, ReceiverVolume{Max: 69.5, Step: 1}, 72)

	h.equipment.driftLimit(141)
	h.equipment.driftLimit(129)

	refuseVolumeSets(t, h.equipment, quietPeriod)
	h.waitUntil(t, func(state denonState) bool { return state.Volume == 100 })
	broker.refuseTopic(t, testVolumeTopic, quietPeriod)
}

func TestAKnobTurnPublishesTheLevelBack(t *testing.T) {
	h, broker, _ := listening(t)

	h.equipment.turnKnob(120)

	published := broker.waitForTopic(t, testVolumeTopic)
	mustMatch(t, string(published.payload), `{"level":86,"muted":false}`)
	mustMatch(t, published.retained, true)
}

func TestAMuteOnTheEquipmentPublishesBack(t *testing.T) {
	h, broker, _ := listening(t)

	h.equipment.setMute(true)

	published := broker.waitForTopic(t, testVolumeTopic)
	mustMatch(t, string(published.payload), `{"level":72,"muted":true}`)
	mustMatch(t, published.retained, true)
}

// refusePublish fails the test if anything reaches the broker on the
// The position the session publishes comes back to it from the broker,
// and moves neither the receiver nor the topic again.
func TestThePositionTheSessionPublishesMovesNothingWhenItComesBack(t *testing.T) {
	h, broker, _ := listeningWith(t, ReceiverVolume{Max: 69.5, Step: 1}, 72)

	position := pressFrom(t, h, broker, 72, 5, "MV51")
	payload, err := marshalVolumeState(position)
	mustSucceed(t, err)

	broker.push(testVolumeTopic, payload)

	refuseVolumeSets(t, h.equipment, quietPeriod)
}

func TestStoppingTheSessionClearsTheOwnerMarkAndLeavesThePowerAlone(t *testing.T) {
	h, broker, held := listening(t)
	h.drainCommands()

	held.stop()

	cleared := broker.waitForTopic(t, ownerTopic(testVolumeTopic))
	mustMatch(t, len(cleared.payload), 0)
	mustMatch(t, cleared.retained, true)
	h.refuseCommands(t, quietPeriod, "PWSTANDBY", "PWOFF")
}

// A message the session cannot read leaves the receiver alone, so
// another program's traffic on the broker never moves the room's level.
func TestAMessageTheSessionCannotReadMovesNothing(t *testing.T) {
	cases := []struct {
		name    string
		topic   string
		payload string
	}{
		{"another topic", testVolumeTopic + "/owner", `{"level":100}`},
		{"a payload that is not a state", testVolumeTopic, `not json`},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			h, broker, _ := listening(t)

			broker.push(one.topic, []byte(one.payload))

			h.equipment.refuseCommand(t, "MV695", quietPeriod)
		})
	}
}

// Two neighbouring half steps map to one bus level, so the level a knob
// turn publishes maps back onto the wrong half step, and the session
// must not push the equipment there when the broker delivers its own
// message back to it.
func TestTheSessionsOwnWriteBackDoesNotMoveTheEquipment(t *testing.T) {
	const knob, level, wrong = 119, 86, 120
	h, broker, _ := listening(t)

	h.equipment.turnKnob(knob)
	published := broker.waitForTopic(t, testVolumeTopic)
	mustMatch(t, string(published.payload), `{"level":86,"muted":false}`)

	broker.push(testVolumeTopic, published.payload)

	mustMatch(t, halvesForLevelOrZero(level), wrong)
	h.equipment.refuseCommand(t, denonVolumeCommand(wrong), quietPeriod)
	mustMatch(t, h.denon.State().Volume, knob)
}

// halvesForLevelOrZero maps a bus level onto the fake receiver's scale,
// so the case above states the half step the mapping would land on.
func halvesForLevelOrZero(level int) int {
	halves, _ := halvesForLevel(level, 139)
	return halves
}

// A level left on the topic by an earlier player is in another unit's
// scale, so the session publishes where the receiver stands and applies
// nothing until the broker delivers that message back to it.
func TestASessionAdoptsTheReceiverBeforeItAppliesAnyLevel(t *testing.T) {
	h := newSessionHarness(t)
	h.begin(t, "GAME")
	broker := h.brokers.waitForSession(t)
	broker.waitForTopic(t, ownerTopic(testVolumeTopic))

	adopted := broker.waitForTopic(t, testVolumeTopic)
	mustMatch(t, string(adopted.payload), `{"level":72,"muted":false}`)
	mustMatch(t, adopted.retained, true)

	broker.push(testVolumeTopic, []byte(`{"level":100,"muted":false}`))
	refuseVolumeSets(t, h.equipment, quietPeriod)

	broker.push(testVolumeTopic, adopted.payload)
	broker.push(testVolumeTopic, []byte(`{"level":100,"muted":false}`))

	h.equipment.waitForCommands(t, "MV51")
	h.waitUntil(t, func(state denonState) bool { return state.Volume == 102 })
}

// refuseVolumeSets fails the test if the session sets the receiver's
// volume at all, as opposed to asking it what the volume is.
func refuseVolumeSets(t *testing.T, equipment *fakeDenon, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	var seen []string
	for {
		select {
		case command := <-equipment.commands:
			seen = append(seen, command)
			if strings.HasPrefix(command, denonVolumeCommandPrefix) && command != "MV?" {
				t.Fatalf("the session set the volume with %q (seen %v)", command, seen)
			}
		case <-deadline:
			return
		}
	}
}

// A level published after the adopt is a press, and it moves the
// receiver.
func TestAPressAfterTheAdoptMovesTheReceiver(t *testing.T) {
	h, broker, _ := listening(t)

	broker.push(testVolumeTopic, []byte(`{"level":100,"muted":false}`))

	h.equipment.waitForCommands(t, "MV51")
	h.waitUntil(t, func(state denonState) bool { return state.Volume == 102 })
}

// A press that mutes the room is not a direction: the receiver is muted
// as the message states, and the session says nothing back because the
// topic already carries what the equipment now reports.
func TestAPressThatMutesTheRoomMutesTheReceiver(t *testing.T) {
	h, broker, _ := listeningWith(t, ReceiverVolume{Max: 69.5, Step: 1}, 72)

	broker.push(testVolumeTopic, []byte(`{"level":72,"muted":true}`))

	h.equipment.waitForCommands(t, denonMuteOnCommand)
	h.waitUntil(t, func(state denonState) bool { return state.Mute })
	broker.refuseTopic(t, testVolumeTopic, quietPeriod)
}

// A press is bounded at both ends of the scale, and a receiver a hand
// left above the ceiling is not dragged back down by a press up.
func TestNextPositionIsBoundedAtBothEndsOfTheScale(t *testing.T) {
	press := func(rule ReceiverVolume, volume int, up bool) (int, bool) {
		held := &session{scale: func() ReceiverVolume { return rule }}
		return held.nextPosition(denonState{Volume: volume, VolumeMax: 139}, up)
	}
	room := ReceiverVolume{Max: 45, Step: 1}

	cases := []struct {
		name   string
		rule   ReceiverVolume
		volume int
		up     bool
		want   int
		moves  bool
	}{
		{"up inside the scale", room, 80, true, 82, true},
		{"up onto the ceiling", room, 89, true, 90, true},
		{"up from the ceiling", room, 90, true, 0, false},
		{"up from above the ceiling", room, 100, true, 0, false},
		{"down from above the ceiling", room, 100, false, 98, true},
		{"down onto the floor", room, 1, false, 0, true},
		{"down from the floor", room, 0, false, 0, false},
		{"no ceiling declared", ReceiverVolume{Step: 1}, 80, true, 0, false},
		{"a volume the receiver has not reported", room, unknownHalves, true, 0, false},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			target, moves := press(one.rule, one.volume, one.up)
			mustMatch(t, target, one.want)
			mustMatch(t, moves, one.moves)
		})
	}
}
