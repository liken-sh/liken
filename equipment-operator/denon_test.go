package main

// The Denon control protocol as the AVR-X1700H speaks it, and the
// client that holds one receiver's connection open.

import (
	"net"
	"strings"
	"testing"
	"time"
)

// foldedInto is the state before the receiver has said anything, with
// the one field a line changes set to what that line produces.
func foldedInto(change func(*denonState)) denonState {
	state := newDenonState()
	change(&state)
	return state
}

func TestApplyDenonLineFoldsEveryLineTheReceiverSends(t *testing.T) {
	cases := []struct {
		line  string
		field string
		want  denonState
	}{
		{line: "PWON", field: denonPowerField, want: foldedInto(func(s *denonState) { s.Power = powerOn })},
		{line: "PWSTANDBY", field: denonPowerField, want: foldedInto(func(s *denonState) { s.Power = powerStandby })},
		{line: "MV50", field: denonVolumeField, want: foldedInto(func(s *denonState) { s.Volume = 100 })},
		{line: "MV505", field: denonVolumeField, want: foldedInto(func(s *denonState) { s.Volume = 101 })},
		{line: "MVMAX 695", field: denonVolumeMaxField, want: foldedInto(func(s *denonState) { s.VolumeMax = 139 })},
		{line: "MUON", field: denonMuteField, want: foldedInto(func(s *denonState) { s.Mute = true })},
		{line: "MUOFF", field: denonMuteField, want: foldedInto(func(s *denonState) { s.Mute = false })},
		{line: "SIMPLAY", field: denonInputField, want: foldedInto(func(s *denonState) { s.Input = "MPLAY" })},
		{line: "SICBL/SAT", field: denonInputField, want: foldedInto(func(s *denonState) { s.Input = "CBL/SAT" })},
		{line: "MSMULTI CH IN", field: denonSoundModeField, want: foldedInto(func(s *denonState) { s.SoundMode = "MULTI CH IN" })},
		{line: "MSSTEREO", field: denonSoundModeField, want: foldedInto(func(s *denonState) { s.SoundMode = "STEREO" })},
	}
	for _, test := range cases {
		t.Run(test.line, func(t *testing.T) {
			state, field, known := applyDenonLine(newDenonState(), test.line)
			mustMatch(t, known, true)
			mustMatch(t, field, test.field)
			mustMatch(t, state, test.want)
		})
	}
}

func TestApplyDenonLineIgnoresTheLinesTheOperatorDoesNotRead(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{name: "the video select setting", line: "SVOFF"},
		{name: "the dynamic range setting", line: "PSDRC OFF"},
		{name: "the low frequency setting", line: "PSLFE 00"},
		{name: "a volume that is not digits", line: "MVFOO"},
		{name: "an input with no name", line: "SI"},
		{name: "a sound mode with no name", line: "MS"},
		{name: "an empty line", line: ""},
		{name: "the second zone", line: "Z2ON"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			state, field, known := applyDenonLine(newDenonState(), test.line)
			mustMatch(t, known, false)
			mustMatch(t, field, "")
			mustMatch(t, state, newDenonState())
		})
	}
}

// MVMAX carries the limit set in the receiver's own menu, and it
// arrives right behind the volume. Reading it as a volume would put the
// receiver's loudest setting on the bus.
func TestTheVolumeLimitIsNotTheVolume(t *testing.T) {
	state, _, _ := applyDenonLine(newDenonState(), "MV50")
	state, field, known := applyDenonLine(state, "MVMAX 695")

	mustMatch(t, known, true)
	mustMatch(t, field, denonVolumeMaxField)
	mustMatch(t, state.Volume, 100)
	mustMatch(t, state.VolumeMax, 139)
}

func TestTheVolumeCommandCarriesTheHalfSteps(t *testing.T) {
	cases := []struct {
		name   string
		halves int
		want   string
	}{
		{name: "a whole step", halves: 100, want: "MV50"},
		{name: "a half step", halves: 101, want: "MV505"},
		{name: "a single digit step", halves: 10, want: "MV05"},
		{name: "the limit", halves: 139, want: "MV695"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			mustMatch(t, denonVolumeCommand(test.halves), test.want)
		})
	}
}

func TestTheMuteAndInputCommands(t *testing.T) {
	mustMatch(t, denonMuteCommand(true), "MUON")
	mustMatch(t, denonMuteCommand(false), "MUOFF")
	mustMatch(t, denonInputCommand("MPLAY"), "SIMPLAY")
	mustMatch(t, denonInputCommand("CBL/SAT"), "SICBL/SAT")
}

func TestDenonAddressNamesTheControlPort(t *testing.T) {
	cases := []struct {
		name    string
		address string
		want    string
	}{
		{name: "a bare address", address: "192.168.1.5", want: "192.168.1.5:23"},
		{name: "an address with a port", address: "192.168.1.5:8023", want: "192.168.1.5:8023"},
		{name: "a bare host name", address: "receiver.local", want: "receiver.local:23"},
		{name: "a bare ipv6 literal", address: "fd00::1", want: "[fd00::1]:23"},
		{name: "an ipv6 literal with a port", address: "[fd00::1]:8023", want: "[fd00::1]:8023"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			mustMatch(t, denonAddress(test.address), test.want)
		})
	}
}

// denonHarness is the client under test, the fake receiver it talks to,
// and every event its listener saw.
type denonHarness struct {
	receiver *fakeDenon
	client   *denonClient
	events   chan denonEvent
}

// startDenonClient runs a client against one address until the test
// ends, and waits for its goroutines to stop before any earlier cleanup
// runs.
func startDenonClient(t *testing.T, address string) *denonHarness {
	t.Helper()
	events := make(chan denonEvent, 256)
	client := newDenonClient(address, func(event denonEvent) {
		select {
		case events <- event:
		default:
		}
	})
	stopped := make(chan struct{})
	ctx := t.Context()
	go func() {
		defer close(stopped)
		client.Run(ctx)
	}()
	t.Cleanup(func() {
		select {
		case <-stopped:
		case <-time.After(testTimeout):
			t.Error("the client did not stop")
		}
	})
	return &denonHarness{client: client, events: events}
}

func startDenonHarness(t *testing.T) *denonHarness {
	t.Helper()
	receiver := startFakeDenon(t)
	harness := startDenonClient(t, receiver.address())
	harness.receiver = receiver
	return harness
}

// waitForField waits for the next event that names one field, and fails
// the test instead of hanging when none arrives.
func waitForField(t *testing.T, events <-chan denonEvent, field string) denonState {
	t.Helper()
	deadline := time.After(testTimeout)
	for {
		select {
		case event := <-events:
			if event.Field == field {
				return event.State
			}
		case <-deadline:
			t.Fatalf("no %s event arrived", field)
			return denonState{}
		}
	}
}

// connectedState is the state the fake receiver reports once it has
// answered all five queries.
func connectedState() denonState {
	return denonState{
		Power:     powerStandby,
		Input:     "MPLAY",
		SoundMode: "MULTI CH IN",
		Volume:    100,
		VolumeMax: 139,
		Reachable: ConditionTrue,
	}
}

func TestTheClientAsksTheFiveQueriesOnConnect(t *testing.T) {
	harness := startDenonHarness(t)

	sent := harness.receiver.waitForCommands(t, "MS?")

	mustMatch(t, strings.Join(sent, " "), strings.Join(denonQueries, " "))
}

func TestTheClientReportsWhatTheReceiverSaid(t *testing.T) {
	harness := startDenonHarness(t)

	waitForField(t, harness.events, denonSoundModeField)
	state := harness.client.State()

	mustMatch(t, state, connectedState())
	mustMatch(t, formatHalfSteps(state.Volume), "50")
	mustMatch(t, formatHalfSteps(state.VolumeMax), "69.5")
}

// The noise arrives behind the sound mode and ahead of the mute the
// test asks for, so a state that still reads right after the mute is a
// state the noise left alone.
func TestTheNoiseLinesChangeNothing(t *testing.T) {
	harness := startDenonHarness(t)
	waitForField(t, harness.events, denonSoundModeField)

	harness.receiver.setMute(true)
	waitForField(t, harness.events, denonMuteField)

	want := connectedState()
	want.Mute = true
	mustMatch(t, harness.client.State(), want)
}

func TestReachableIsUnknownBeforeTheReceiverAnswers(t *testing.T) {
	client := newDenonClient("192.0.2.1", nil)

	mustMatch(t, client.State().Reachable, ConditionUnknown)
}

// shortenDenonBackoff shortens the wait between sessions so a reconnect
// happens inside a test, and restores it once the client has stopped.
func shortenDenonBackoff(t *testing.T) {
	t.Helper()
	minimum, maximum := denonMinBackoff, denonMaxBackoff
	t.Cleanup(func() {
		denonMinBackoff, denonMaxBackoff = minimum, maximum
	})
	denonMinBackoff = 5 * time.Millisecond
	denonMaxBackoff = 20 * time.Millisecond
}

func TestTheClientReconnectsAfterTheConnectionDrops(t *testing.T) {
	shortenDenonBackoff(t)
	harness := startDenonHarness(t)
	waitForField(t, harness.events, denonSoundModeField)

	harness.receiver.dropConnections()
	mustMatch(t, waitForField(t, harness.events, denonReachableField).Reachable, ConditionFalse)

	mustMatch(t, waitForField(t, harness.events, denonSoundModeField), connectedState())
	mustMatch(t, harness.client.State(), connectedState())
}

func TestAKnobTurnReachesTheStateAndTheListener(t *testing.T) {
	harness := startDenonHarness(t)
	waitForField(t, harness.events, denonSoundModeField)

	harness.receiver.turnKnob(111)

	mustMatch(t, waitForField(t, harness.events, denonVolumeField).Volume, 111)
	mustMatch(t, harness.client.State().Volume, 111)
	mustMatch(t, formatHalfSteps(harness.client.State().Volume), "55.5")
}

// mustStaySilent fails the test if any event arrives inside the window.
func mustStaySilent(t *testing.T, events <-chan denonEvent, window time.Duration) {
	t.Helper()
	select {
	case event := <-events:
		t.Fatalf("wanted no event, got %+v", event)
	case <-time.After(window):
	}
}

// The verdict is announced once and not once per retry, so an address
// that never answers writes one status and not a stream of them.
func TestAnAddressThatAnswersNothingIsNeverReachable(t *testing.T) {
	shortenDenonBackoff(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	mustSucceed(t, err)
	address := listener.Addr().String()
	mustSucceed(t, listener.Close())

	harness := startDenonClient(t, address)

	mustMatch(t, waitForField(t, harness.events, denonReachableField).Reachable, ConditionFalse)
	mustStaySilent(t, harness.events, 100*time.Millisecond)
	mustMatch(t, harness.client.State().Reachable, ConditionFalse)
}

// shortenDenonHeartbeat shortens the idle query so the heartbeat lands
// inside a test, and restores it once the client has stopped.
func shortenDenonHeartbeat(t *testing.T) {
	t.Helper()
	heartbeat := denonHeartbeat
	t.Cleanup(func() { denonHeartbeat = heartbeat })
	denonHeartbeat = 20 * time.Millisecond
}

func TestTheHeartbeatKeepsAskingOnAnIdleConnection(t *testing.T) {
	shortenDenonHeartbeat(t)
	harness := startDenonHarness(t)
	harness.receiver.waitForCommands(t, "MS?")

	mustMatch(t, harness.receiver.waitForCommand(t), "PW?")
}

func TestSendOnADisconnectedClientDropsTheCommand(t *testing.T) {
	receiver := startFakeDenon(t)
	client := newDenonClient(receiver.address(), nil)

	client.Send(denonPowerOnCommand)

	mustMatch(t, client.State(), newDenonState())
}
