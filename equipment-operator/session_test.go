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
}

func newSessionHarness(t *testing.T) *sessionHarness {
	t.Helper()
	h := &sessionHarness{
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
	started := startSession(t.Context(), "theater", spec, h.denon, h.brokers.address())
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
	startSession(t.Context(), "theater", spec, newDenonClient("127.0.0.1:1", nil), listener.Addr().String())

	will := connectWill(t, waitForFrame(t, frames))
	mustMatch(t, will.Topic, ownerTopic(testVolumeTopic))
	mustMatch(t, will.Retained, true)
	mustMatch(t, len(will.Payload), 0)
}

// listening is a harness whose session has selected its input and
// marked itself the owner, which is where every level test starts.
func listening(t *testing.T) (*sessionHarness, *fakeBroker, *session) {
	t.Helper()
	h := newSessionHarness(t)
	held := h.begin(t, "GAME")
	broker := h.brokers.waitForSession(t)
	broker.waitForTopic(t, ownerTopic(testVolumeTopic))
	// The session publishes where the receiver stands and waits for that
	// message to come back before it applies anything, so every test
	// below starts from the other side of that boundary.
	adopted := broker.waitForTopic(t, testVolumeTopic)
	mustMatch(t, string(adopted.payload), `{"level":72,"muted":false}`)
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

func TestTheLevelOnTheTopicReachesTheReceiver(t *testing.T) {
	cases := []struct {
		name   string
		state  string
		volume string
		mute   string
	}{
		{"the top of the scale", `{"level":100,"muted":false}`, "MV695", denonMuteOffCommand},
		{"half way", `{"level":50}`, "MV35", denonMuteOffCommand},
		{"muted", `{"level":50,"muted":true}`, "MV35", denonMuteOnCommand},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			h, broker, _ := listening(t)

			broker.push(testVolumeTopic, []byte(one.state))

			mustMatch(t, h.equipment.waitForCommand(t), one.volume)
			mustMatch(t, h.equipment.waitForCommand(t), one.mute)
		})
	}
}

func TestABurstOfLevelsSendsOneVolume(t *testing.T) {
	h, broker, _ := listening(t)

	broker.push(testVolumeTopic, []byte(`{"level":10}`))
	broker.push(testVolumeTopic, []byte(`{"level":60}`))
	broker.push(testVolumeTopic, []byte(`{"level":100}`))

	mustMatch(t, len(h.equipment.waitForCommands(t, "MV695")), 1)
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
// topic within the window.
func refusePublish(t *testing.T, broker *fakeBroker, topic string, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case published := <-broker.pubs:
			if published.topic == topic {
				t.Fatalf("the operator published %q on %q", published.payload, topic)
			}
		case <-deadline:
			return
		}
	}
}

func TestTheEchoOfTheOperatorsOwnWritePublishesNothingBack(t *testing.T) {
	h, broker, _ := listening(t)

	broker.push(testVolumeTopic, []byte(`{"level":100,"muted":true}`))
	h.equipment.waitForCommands(t, denonMuteOnCommand)

	refusePublish(t, broker, testVolumeTopic, quietPeriod)
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

			h.equipment.refuseCommand(t, "MV695", 4*sessionVolumeDebounce)
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

	h.equipment.waitForCommands(t, "MV695")
	h.waitUntil(t, func(state denonState) bool { return state.Volume == 139 })
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

	mustMatch(t, h.equipment.waitForCommand(t), "MV695")
	mustMatch(t, h.equipment.waitForCommand(t), denonMuteOffCommand)
}
