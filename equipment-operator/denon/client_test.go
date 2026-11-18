// Tests of the client that keeps a receiver's control connection open.

package denon

import (
	"context"
	"net"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// clientHarness is the client under test, the fake receiver it talks
// to, and the events that its listener received.
type clientHarness struct {
	receiver *fakeReceiver
	client   *Client
	events   chan equipment.Event
}

// startClient runs a client against address until the test ends, and
// the cleanup waits for the client to stop. Each configure function
// runs on the client before it starts.
func startClient(t *testing.T, address string, configure ...func(*Client)) *clientHarness {
	t.Helper()
	events := make(chan equipment.Event, 256)
	client := NewClient(address, func(event equipment.Event) {
		select {
		case events <- event:
		default:
		}
	})
	client.Dial = testNetwork.dial
	for _, apply := range configure {
		apply(client)
	}
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
			t.Error("the client did not stop after its context ended")
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

// waitForField returns the state of the next event for field, and fails
// the test when none arrives within testTimeout.
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
			t.Fatalf("no %s event arrived within %v", field, testTimeout)
			return equipment.State{}
		}
	}
}

// drainQueries waits until the receiver has read the last connect
// query, so that the next command the receiver reads is the test's own.
func drainQueries(t *testing.T, harness *clientHarness) {
	t.Helper()
	harness.receiver.waitForCommands(t, Queries[len(Queries)-1])
}

// After Run returns the client has no queue, so send returns an error
// instead of queuing a command that nothing writes.
func TestSendAfterTheWriterStopsReportsFailure(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		receiver := startFakeReceiver(t)
		client := NewClient(receiver.address(), nil)
		client.Dial = testNetwork.dial
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
			t.Fatal("the client has a send queue after the writer stopped, want none")
		}
		if err := client.send("PSBAS 53"); err == nil {
			t.Fatal("send after the writer stopped returned nil, want an error")
		}
	})
}

// connectedState is the state that the fake receiver reports after it
// answers the queries. The fake does not answer a sleep query, so the
// sleep timer is unknown.
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

func TestTheClientSendsEveryQueryOnConnect(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		harness := startHarness(t)

		sent := harness.receiver.waitForCommands(t, Queries[len(Queries)-1])

		mustMatch(t, strings.Join(sent, " "), strings.Join(Queries, " "))
	})
}

func TestTheClientReportsWhatTheReceiverSaid(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		harness := startHarness(t)

		waitForField(t, harness.events, equipment.EventSoundMode)
		state := harness.client.State()

		mustMatchState(t, state, connectedState())
		zone, _ := state.Zone(equipment.MainZone)
		mustMatch(t, FormatHalfSteps(zone.Volume), "50")
		mustMatch(t, FormatHalfSteps(zone.VolumeMax), "69.5")
	})
}

// The fake receiver sends lines that the client does not recognize
// after the sound mode and before the mute that the test sets. If the
// state is right after the mute, those lines changed nothing.
func TestUnrecognizedLinesChangeNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		harness := startHarness(t)
		waitForField(t, harness.events, equipment.EventSoundMode)

		harness.receiver.setMute(true)
		waitForField(t, harness.events, equipment.EventMute)

		want := connectedState()
		zone := want.Zones[equipment.MainZone]
		zone.Mute = true
		want.Zones[equipment.MainZone] = zone
		mustMatchState(t, harness.client.State(), want)
	})
}

func TestReachableIsUnknownBeforeTheReceiverAnswers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		client := NewClient("192.0.2.1", nil)

		mustMatch(t, client.State().Reachable, equipment.ConditionUnknown)
	})
}

func TestTheClientReconnectsAfterTheConnectionDrops(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		harness := startHarness(t)
		waitForField(t, harness.events, equipment.EventSoundMode)

		harness.receiver.dropConnections()
		mustMatch(t, waitForField(t, harness.events, equipment.EventReachable).Reachable, equipment.ConditionFalse)

		mustMatchState(t, waitForField(t, harness.events, equipment.EventSoundMode), connectedState())
		mustMatchState(t, harness.client.State(), connectedState())
	})
}

func TestAVolumeKnobTurnUpdatesTheStateAndNotifiesTheListener(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
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
	})
}

// mustStaySilent fails the test if an event arrives within window.
func mustStaySilent(t *testing.T, events <-chan equipment.Event, window time.Duration) {
	t.Helper()
	select {
	case event := <-events:
		t.Fatalf("an event arrived: %+v, want none within %v", event, window)
	case <-time.After(window):
	}
}

// The listener hears about Reachable false once, not once per retry,
// so an address that never answers produces one status write and not a
// stream of them.
func TestAnAddressThatAnswersNothingIsNeverReachable(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		harness := startClient(t, "127.0.0.1:1")

		mustMatch(t, waitForField(t, harness.events, equipment.EventReachable).Reachable, equipment.ConditionFalse)
		mustStaySilent(t, harness.events, 3*maxBackoff)
		mustMatch(t, harness.client.State().Reachable, equipment.ConditionFalse)
	})
}

func TestTheClientSendsAHeartbeatQueryOnAnIdleConnection(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		harness := startHarness(t)
		drainQueries(t, harness)
		asked := time.Now()

		mustMatch(t, harness.receiver.waitForCommandWithin(t, 2*heartbeat), "PW?")
		mustMatch(t, time.Since(asked), heartbeat)
	})
}

func TestACommandOnADisconnectedClientIsNotSent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		receiver := startFakeReceiver(t)
		client := NewClient(receiver.address(), nil)
		client.Dial = testNetwork.dial

		client.SetVolume(equipment.MainZone, 101)

		mustMatch(t, client.State().Reachable, equipment.ConditionUnknown)
	})
}

func TestSetVolumeClampsToTheReceiversScale(t *testing.T) {
	cases := []struct {
		name   string
		halves int
		want   string
	}{
		{"above the top", 400, "MV98"},
		{"below the floor", -4, "MV00"},
		{"at the top", 196, "MV98"},
	}
	t.Parallel()
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				harness := startHarness(t)
				drainQueries(t, harness)

				harness.client.SetVolume(equipment.MainZone, one.halves)

				mustMatch(t, harness.receiver.waitForCommand(t), one.want)
			})
		})
	}
}

// No line marks the end of the first full read, so the client sends
// EventFirstReadDone for it.
func TestTheFirstFullReadCompletesAfterTheConnectReplies(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		harness := startHarness(t)

		mustMatch(t, harness.client.FirstReadDone(), false)
		drainQueries(t, harness)

		waitForField(t, harness.events, equipment.EventFirstReadDone)
		mustMatch(t, harness.client.FirstReadDone(), true)
	})
}

// peerConn wraps a connection so that RemoteAddr returns a TCP address,
// as the socket to a real receiver does.
type peerConn struct {
	net.Conn
	peer *net.TCPAddr
}

func (c peerConn) RemoteAddr() net.Addr { return c.peer }

// A client with no Dial connects over TCP. The test uses the real
// clock, because it opens a real socket.
func TestAClientWithNoDialDialsOverTCP(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	mustSucceed(t, err)
	t.Cleanup(func() { listener.Close() })
	address := listener.Addr().String()

	conn, err := NewClient(address, nil).dialer()(t.Context(), "tcp", address)

	mustSucceed(t, err)
	mustMatch(t, conn.RemoteAddr().String(), address)
	conn.Close()
}

// Address returns the remote address of the connection, which can
// differ from the address that the declared name resolves to.
func TestTheAddressIsThePeer(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		receiver := startFakeReceiver(t)
		peer := &net.TCPAddr{IP: net.ParseIP("192.0.2.30"), Port: 23}
		harness := startClient(t, receiver.address(), func(client *Client) {
			client.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
				conn, err := testNetwork.dial(ctx, network, address)
				if err != nil {
					return nil, err
				}
				return peerConn{Conn: conn, peer: peer}, nil
			}
		})
		harness.receiver = receiver
		drainQueries(t, harness)

		mustMatch(t, harness.client.Address(), "192.0.2.30")
	})
}
