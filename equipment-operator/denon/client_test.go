// The client that holds one receiver's control connection open.

package denon

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/equipment"
)

// clientHarness is the client under test, the fake receiver it talks
// to, and every event its listener saw.
type clientHarness struct {
	receiver *fakeReceiver
	client   *Client
	events   chan equipment.Event
}

// startClient runs a client against one address until the test ends,
// and waits for its goroutines to stop before any earlier cleanup runs.
func startClient(t *testing.T, address string) *clientHarness {
	t.Helper()
	events := make(chan equipment.Event, 256)
	client := NewClient(address, func(event equipment.Event) {
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
	return &clientHarness{client: client, events: events}
}

func startHarness(t *testing.T) *clientHarness {
	t.Helper()
	receiver := startFakeReceiver(t)
	harness := startClient(t, receiver.address())
	harness.receiver = receiver
	return harness
}

// waitForField waits for the next event that names one field, and fails
// the test instead of hanging when none arrives.
func waitForField(t *testing.T, events <-chan equipment.Event, field string) equipment.State {
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
			return equipment.State{}
		}
	}
}

// drainQueries reads until the client has sent the last of its connect
// queries, so a test that follows sees only its own command.
func drainQueries(t *testing.T, harness *clientHarness) {
	t.Helper()
	harness.receiver.waitForCommands(t, Queries[len(Queries)-1])
}

// A command sent after the writer has stopped is reported, not left in
// a queue nobody drains. The writer nils out under the send mutex
// before it stops, so once Run has returned there is no live queue.
func TestSendAfterTheWriterStopsReportsFailure(t *testing.T) {
	receiver := startFakeReceiver(t)
	client := NewClient(receiver.address(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		client.Run(ctx)
	}()

	receiver.waitForCommands(t, Queries[len(Queries)-1])
	cancel()
	<-stopped

	client.mutex.Lock()
	live := client.out
	client.mutex.Unlock()
	if live != nil {
		t.Fatal("the writer stopped but the queue is still named")
	}
	if err := client.send("PSBAS 53"); err == nil {
		t.Fatal("a send after the writer stopped did not error")
	}
}

// connectedState is the state the fake receiver reports once it has
// answered the queries. The fake never answers a sleep query, so the
// timer reads as unknown.
func connectedState() equipment.State {
	return equipment.State{
		Reachable: equipment.ConditionTrue,
		Zones: map[string]equipment.ZoneState{
			equipment.MainZone: {
				Power:     equipment.PowerStandby,
				Input:     "MPLAY",
				SoundMode: "MULTI CH IN",
				Volume:    100,
				VolumeMax: 139,
				Sleep:     equipment.Unknown,
			},
		},
	}
}

func TestTheClientAsksEveryQueryOnConnect(t *testing.T) {
	harness := startHarness(t)

	sent := harness.receiver.waitForCommands(t, Queries[len(Queries)-1])

	mustMatch(t, strings.Join(sent, " "), strings.Join(Queries, " "))
}

func TestTheClientReportsWhatTheReceiverSaid(t *testing.T) {
	harness := startHarness(t)

	waitForField(t, harness.events, equipment.EventSoundMode)
	state := harness.client.State()

	mustMatchState(t, state, connectedState())
	zone, _ := state.Zone(equipment.MainZone)
	mustMatch(t, FormatHalfSteps(zone.Volume), "50")
	mustMatch(t, FormatHalfSteps(zone.VolumeMax), "69.5")
}

// The noise arrives behind the sound mode and ahead of the mute the
// test asks for, so a state that still reads right after the mute is a
// state the noise left alone.
func TestTheNoiseLinesChangeNothing(t *testing.T) {
	harness := startHarness(t)
	waitForField(t, harness.events, equipment.EventSoundMode)

	harness.receiver.setMute(true)
	waitForField(t, harness.events, equipment.EventMute)

	want := connectedState()
	zone := want.Zones[equipment.MainZone]
	zone.Mute = true
	want.Zones[equipment.MainZone] = zone
	mustMatchState(t, harness.client.State(), want)
}

func TestReachableIsUnknownBeforeTheReceiverAnswers(t *testing.T) {
	client := NewClient("192.0.2.1", nil)

	mustMatch(t, client.State().Reachable, equipment.ConditionUnknown)
}

// shortenBackoff shortens the wait between sessions so a reconnect
// happens inside a test, and restores it once the client has stopped.
func shortenBackoff(t *testing.T) {
	t.Helper()
	minimum, maximum := minBackoff, maxBackoff
	t.Cleanup(func() {
		minBackoff, maxBackoff = minimum, maximum
	})
	minBackoff = 5 * time.Millisecond
	maxBackoff = 20 * time.Millisecond
}

func TestTheClientReconnectsAfterTheConnectionDrops(t *testing.T) {
	shortenBackoff(t)
	harness := startHarness(t)
	waitForField(t, harness.events, equipment.EventSoundMode)

	harness.receiver.dropConnections()
	mustMatch(t, waitForField(t, harness.events, equipment.EventReachable).Reachable, equipment.ConditionFalse)

	mustMatchState(t, waitForField(t, harness.events, equipment.EventSoundMode), connectedState())
	mustMatchState(t, harness.client.State(), connectedState())
}

func TestAKnobTurnReachesTheStateAndTheListener(t *testing.T) {
	harness := startHarness(t)
	waitForField(t, harness.events, equipment.EventSoundMode)

	harness.receiver.turnKnob(111)

	event := waitForField(t, harness.events, equipment.EventVolume)
	zone, _ := event.Zone(equipment.MainZone)
	mustMatch(t, zone.Volume, 111)
	state := harness.client.State()
	zone, _ = state.Zone(equipment.MainZone)
	mustMatch(t, zone.Volume, 111)
	mustMatch(t, FormatHalfSteps(zone.Volume), "55.5")
}

// mustStaySilent fails the test if any event arrives inside the window.
func mustStaySilent(t *testing.T, events <-chan equipment.Event, window time.Duration) {
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
	shortenBackoff(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	mustSucceed(t, err)
	address := listener.Addr().String()
	mustSucceed(t, listener.Close())

	harness := startClient(t, address)

	mustMatch(t, waitForField(t, harness.events, equipment.EventReachable).Reachable, equipment.ConditionFalse)
	mustStaySilent(t, harness.events, 100*time.Millisecond)
	mustMatch(t, harness.client.State().Reachable, equipment.ConditionFalse)
}

// shortenHeartbeat shortens the idle query so the heartbeat lands
// inside a test, and restores it once the client has stopped.
func shortenHeartbeat(t *testing.T) {
	t.Helper()
	hb := heartbeat
	t.Cleanup(func() { heartbeat = hb })
	heartbeat = 20 * time.Millisecond
}

func TestTheHeartbeatKeepsAskingOnAnIdleConnection(t *testing.T) {
	shortenHeartbeat(t)
	harness := startHarness(t)
	drainQueries(t, harness)

	mustMatch(t, harness.receiver.waitForCommand(t), "PW?")
}

func TestACommandOnADisconnectedClientDropsIt(t *testing.T) {
	receiver := startFakeReceiver(t)
	client := NewClient(receiver.address(), nil)

	client.SetVolume(equipment.MainZone, 101)

	mustMatch(t, client.State().Reachable, equipment.ConditionUnknown)
}

// The wire carries at most the top of the receiver's own scale, so a
// count above it is held to the top.
func TestSetVolumeHoldsTheReceiverToItsOwnScale(t *testing.T) {
	cases := []struct {
		name   string
		halves int
		want   string
	}{
		{"above the top", 400, "MV98"},
		{"below the floor", -4, "MV00"},
		{"at the top", 196, "MV98"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			harness := startHarness(t)
			drainQueries(t, harness)

			harness.client.SetVolume(equipment.MainZone, one.halves)

			mustMatch(t, harness.receiver.waitForCommand(t), one.want)
		})
	}
}

// The survey is complete once the connect replies have stopped
// arriving, so a declared setting is compared against what the receiver
// reported and not against an empty state.
func TestASurveyCompletesAfterTheConnectReplies(t *testing.T) {
	harness := startHarness(t)

	mustMatch(t, harness.client.Surveyed(), false)
	drainQueries(t, harness)
	deadline := time.After(testTimeout)
	for !harness.client.Surveyed() {
		select {
		case <-deadline:
			t.Fatal("the receiver was never surveyed")
		case <-time.After(time.Millisecond):
		}
	}
}
