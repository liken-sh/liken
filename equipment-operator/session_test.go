package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/denon"
	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// quietPeriod is how long a test watches for a command that must not
// arrive.
const quietPeriod = 300 * time.Millisecond

// sessionHolder routes the driver's events to a session as
// receiverUnit does. It keeps the events that arrive before set and
// replays them to the session.
type sessionHolder struct {
	lines  *receiverLog
	mutex  sync.Mutex
	held   *session
	missed []equipment.Event
}

func (h *sessionHolder) observe(event equipment.Event) {
	h.lines.observe()
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

// sessionHarness is a driver connected to a fake receiver, ready for a
// session.
type sessionHarness struct {
	equipment *fakeDenon
	denon     *denon.Client
	holder    *sessionHolder
	readings  *metrics
	// log is the buffer that lines writes to, and lines is the receiver
	// log that the session writes its lines to.
	log        *logBuffer
	lines      *receiverLog
	soundModes map[string]string
	applyPower func(power equipment.Power)
	room       roomEvents
}

func (h *sessionHarness) inputSoundMode(input string) string {
	return h.soundModes[input]
}

func newSessionHarness(t *testing.T) *sessionHarness {
	t.Helper()
	h := &sessionHarness{
		equipment: startFakeDenon(t),
		log:       &logBuffer{},
	}
	h.lines = newReceiverLog(h.log, "theater")
	h.holder = &sessionHolder{lines: h.lines}
	h.denon = denon.NewClient(h.equipment.address(), h.holder.observe)
	h.denon.Dial = testNetwork.dial
	go h.denon.Run(t.Context())
	h.waitUntil(t, func(state equipment.State) bool {
		return mainZone(state).Power != "" && mainZone(state).VolumeMax != equipment.Unknown
	})
	// The connect queries keep arriving behind the volume, so the
	// harness reads to the last of them before a test sends its own.
	h.equipment.waitForCommands(t, denon.Queries[len(denon.Queries)-1])
	return h
}

// waitUntil polls the driver's state until ready returns true, and
// fails the test after testTimeout.
func (h *sessionHarness) waitUntil(t *testing.T, ready func(equipment.State) bool) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		if ready(h.denon.State()) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("the receiver did not reach the wanted state within %s", testTimeout)
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

// begin starts a session with a Play running. It first discards the
// commands the receiver has received, so the next command a test reads
// is the session's.
func (h *sessionHarness) begin(t *testing.T, input string) *session {
	t.Helper()
	return h.beginSession(t, input, true, false)
}

// beginIdle starts a session with both flags off, as media-operator
// writes it while the Player shows its idle screen.
func (h *sessionHarness) beginIdle(t *testing.T, input string) *session {
	t.Helper()
	return h.beginSession(t, input, false, false)
}

func (h *sessionHarness) beginSession(t *testing.T, input string, active, awake bool) *session {
	t.Helper()
	h.drainCommands()
	h.holder.forget()
	spec := ReceiverSession{Player: "theater", Input: input, Active: active, Awake: awake}
	started := newSession(t.Context(), "theater", spec, h.denon, h.readings, h.lines, h.inputSoundMode, h.applyPower, h.room)
	h.holder.set(started)
	started.start(active, awake, false)
	return started
}

// startSession starts a session that is not adopted, as the unit does
// for a session that appears while the operator runs.
func startSession(ctx context.Context, receiver string, spec ReceiverSession, driver equipment.Driver, log *receiverLog, applyPower func(power equipment.Power), room roomEvents) *session {
	started := newSession(ctx, receiver, spec, driver, nil, log, nil, applyPower, room)
	started.start(spec.Active, spec.Awake, false)
	return started
}

// powerAsk sends the session a power request, as the unit does when
// status.session.powerAsk changes.
func powerAsk(held *session, action string) {
	goWork(held.ctx, func() { held.power(action, "status.session.powerAsk") })
}

// powerOn turns the receiver on before a session starts.
func (h *sessionHarness) powerOn(t *testing.T) {
	t.Helper()
	h.denon.SetPower(equipment.MainZone, true)
	h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Power == equipment.PowerOn })
}

// refuseCommands fails the test if the receiver receives any of the
// unwanted commands within the given time.
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
				t.Fatalf("the receiver received the command %q within %s, want none of the refused commands", command, within)
			}
		case <-deadline:
			return
		}
	}
}

func TestTheSessionPowersOnThenSelectsTheInput(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newSessionHarness(t)

		h.begin(t, "GAME")

		mustMatch(t, h.equipment.waitForCommand(t), denon.PowerOnCommand)
		mustMatch(t, h.equipment.waitForCommand(t), "SIGAME")
	})
}

func TestTheSessionSelectsTheInputOnAReceiverAlreadyOn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newSessionHarness(t)
		h.powerOn(t)

		h.begin(t, "GAME")

		mustMatch(t, h.equipment.waitForCommand(t), "SIGAME")
	})
}

// handOnTheRemote sends one line to the receiver over a second
// connection, to stand for a person using the receiver's own remote.
// The receiver sends its events to every connection, so the second
// connection reads and discards them until the test ends.
func handOnTheRemote(t *testing.T, equipment *fakeDenon, line string) {
	t.Helper()
	conn, err := testNetwork.dial(t.Context(), "tcp", equipment.address())
	mustSucceed(t, err)
	t.Cleanup(func() { conn.Close() })
	go func() { _, _ = io.Copy(io.Discard, conn) }()
	_, err = conn.Write([]byte(line + string(denon.Terminator)))
	mustSucceed(t, err)
}

func TestTheSessionSelectsTheInputOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newSessionHarness(t)
		h.begin(t, "GAME")
		h.equipment.waitForCommands(t, "SIGAME")

		handOnTheRemote(t, h.equipment, "SIDVD")
		h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Input == "DVD" })

		h.refuseCommands(t, quietPeriod, "SIGAME")
	})
}

// A toggle on a standby receiver powers it on and selects the input and
// its sound mode.
func TestAToggleOnAStandbyReceiverPowersOnAndSelectsTheInput(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newSessionHarness(t)
		h.soundModes = map[string]string{"GAME": "STEREO"}
		held := h.beginIdle(t, "GAME")

		powerAsk(held, "toggle")

		mustMatch(t, h.equipment.waitForCommand(t), denon.PowerOnCommand)
		mustMatch(t, h.equipment.waitForCommand(t), "SIGAME")
		mustMatch(t, h.equipment.waitForCommand(t), denon.SoundModeCommand("STEREO"))
		h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Power == equipment.PowerOn })
	})
}

// A toggle on a receiver that is on sends standby and selects no
// input.
func TestAToggleOnAReceiverAlreadyOnGoesToStandby(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newSessionHarness(t)
		h.powerOn(t)
		held := h.beginIdle(t, "GAME")

		powerAsk(held, "toggle")

		mustMatch(t, h.equipment.waitForCommand(t), "PWSTANDBY")
		h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Power == equipment.PowerStandby })
		h.refuseCommands(t, quietPeriod, "SIGAME")
	})
}

// A second toggle powers on the receiver that the first toggle put in
// standby and selects the input again.
func TestASecondTogglePowersTheReceiverBackOn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newSessionHarness(t)
		h.powerOn(t)
		held := h.beginIdle(t, "GAME")

		powerAsk(held, "toggle")
		mustMatch(t, h.equipment.waitForCommand(t), "PWSTANDBY")
		h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Power == equipment.PowerStandby })

		powerAsk(held, "toggle")
		mustMatch(t, h.equipment.waitForCommand(t), denon.PowerOnCommand)
		mustMatch(t, h.equipment.waitForCommand(t), "SIGAME")
		h.waitUntil(t, func(state equipment.State) bool { return mainZone(state).Power == equipment.PowerOn })
	})
}

// A power request with an action other than toggle, on, or off sends the
// receiver nothing.
func TestAPowerAskWithAnotherActionSendsNothing(t *testing.T) {
	cases := []struct {
		name   string
		action string
	}{
		{"a different action", "dim"},
		{"an ask for the screen", "wake"},
		{"an empty action", ""},
	}
	t.Parallel()
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newSessionHarness(t)
				held := h.beginIdle(t, "GAME")

				powerAsk(held, one.action)

				h.refuseCommands(t, quietPeriod, denon.PowerOnCommand, "PWSTANDBY")
			})
		})
	}
}

// A toggle writes the receiver's new power to the Receiver spec, so the
// next reconcile finds no difference to correct.
func TestAToggleWritesTheSpecsPower(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := &cannedAPI{answers: map[string]any{
			"PATCH /apis/equipment.liken.sh/v1alpha1/receivers/theater": Receiver{Metadata: ObjectMeta{Name: "theater"}},
		}}
		client := testAPIClient(t, api.handler())
		h := newSessionHarness(t)
		applied := make(chan equipment.Power, 1)
		h.applyPower = func(power equipment.Power) {
			_, err := ApplyReceiverPower(client, "theater", power)
			mustSucceed(t, err)
			applied <- power
		}
		held := h.beginIdle(t, "GAME")

		powerAsk(held, "toggle")
		mustMatch(t, h.equipment.waitForCommand(t), denon.PowerOnCommand)

		mustMatch(t, <-applied, equipment.PowerOn)

		if len(api.requests) != 1 {
			t.Fatalf("the API server received %d requests, want 1: %+v", len(api.requests), api.requests)
		}
		sent := api.requests[0]
		mustMatch(t, sent.Method, http.MethodPatch)
		mustMatch(t, sent.Path, "/apis/equipment.liken.sh/v1alpha1/receivers/theater")
		mustMatch(t, sent.Query.Get("fieldManager"), fieldManager)
		mustMatch(t, sent.Query.Get("force"), "true")
		body := map[string]any{}
		mustSucceed(t, json.Unmarshal(sent.Body, &body))
		spec, _ := body["spec"].(map[string]any)
		mustMatch(t, spec["power"], any("On"))
	})
}
