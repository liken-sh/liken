package indi

import (
	"context"
	"errors"
	"net"
	"regexp"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// testTimeout bounds every wait in the tests that replay transcripts.
const testTimeout = 10 * time.Second

// run starts the client's Run in the background until the test ends,
// and returns the channel that Run's result arrives on.
func run(t *testing.T, c *Client) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	t.Cleanup(cancel)
	return done
}

// waitFor waits for a condition on the store and fails the test when
// it does not hold within testTimeout.
func waitFor(t *testing.T, c *Client, what string, condition func(*Store) bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if err := c.WaitFor(ctx, condition); err != nil {
		t.Fatalf("waiting for %s: %v", what, err)
	}
}

func defined(device, property string) func(*Store) bool {
	return func(s *Store) bool {
		_, ok := s.Property(device, property)
		return ok
	}
}

var definition = regexp.MustCompile(`<def(?:Number|Switch|Text|Light|BLOB)Vector device="[^"]+" name="([^"]+)"`)

// definedNames lists the properties that a transcript defines, in
// order, read with a regular expression rather than with the client. A
// driver can define one property more than once, and each definition
// is in the list.
func definedNames(data []byte) []string {
	var names []string
	for _, match := range definition.FindAllSubmatch(data, -1) {
		names = append(names, string(match[1]))
	}
	return names
}

// distinct keeps the first of each name.
func distinct(names []string) []string {
	var out []string
	for _, name := range names {
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// connectedBaseline starts a client against the simulator's replay
// server and waits until the client has applied every definition in
// the baseline.
func connectedBaseline(t *testing.T, simulator string) (*Client, *replayServer) {
	t.Helper()
	server := startReplay(t, simulator)
	c := server.newClient()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	events := c.Subscribe(ctx)
	run(t, c)
	awaitDefinitions(t, events, len(definedNames(transcript(t, simulator, "baseline"))))
	return c, server
}

// awaitDefinitions reads events until n Defined events have arrived.
func awaitDefinitions(t *testing.T, events <-chan Event, n int) {
	t.Helper()
	for got := 0; got < n; {
		event, ok := <-events
		if !ok {
			t.Fatalf("%d of %d definitions arrived before the wait ended", got, n)
		}
		if event.Kind == Defined {
			got++
		}
	}
}

func TestBaselineOfEverySimulator(t *testing.T) {
	for _, simulator := range simulators {
		t.Run(simulator, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, server := connectedBaseline(t, simulator)
				want := distinct(definedNames(transcript(t, simulator, "baseline")))
				var got []string
				for _, p := range c.Properties(server.device) {
					got = append(got, p.Name)
				}
				if !slices.Equal(got, want) {
					t.Errorf("properties in the store:\n%v\nwant the baseline's definitions in the order of their first definition:\n%v", got, want)
				}
				if devices := c.Devices(); !slices.Equal(devices, []string{server.device}) {
					t.Errorf("devices = %v, want [%s]", devices, server.device)
				}
			})
		})
	}
}

func TestBaselineKeepsEachDefinition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _ := connectedBaseline(t, "telescope")
		got, _ := c.Property("Telescope Simulator", "CONNECTION")
		want := Property{
			Device: "Telescope Simulator", Name: "CONNECTION", Label: "Connection",
			Group: "Main Control", Type: SwitchType, Perm: ReadWrite, State: Idle,
			Rule: OneOfMany, Timeout: 60 * time.Second, Timestamp: got.Timestamp,
			Members: []Member{
				{Name: "CONNECT", Label: "Connect", Switch: false},
				{Name: "DISCONNECT", Label: "Disconnect", Switch: true},
			},
		}
		if !equalProperty(got, want) {
			t.Errorf("CONNECTION =\n%+v\nwant\n%+v", got, want)
		}
		if got.Timestamp.IsZero() || got.Timestamp.Location() != time.UTC {
			t.Errorf("timestamp = %v, want a time in UTC", got.Timestamp)
		}
	})
}

func equalProperty(a, b Property) bool {
	members := slices.Equal(a.Members, b.Members)
	a.Members, b.Members = nil, nil
	return members && a.Timestamp.Equal(b.Timestamp) && a.Device == b.Device &&
		a.Name == b.Name && a.Label == b.Label && a.Group == b.Group &&
		a.Type == b.Type && a.Perm == b.Perm && a.State == b.State &&
		a.Rule == b.Rule && a.Timeout == b.Timeout
}

func TestBaselineReadsEachType(t *testing.T) {
	cases := []struct {
		simulator, property, member string
		want                        Member
	}{
		{"telescope", "POLLING_PERIOD", "PERIOD_MS", Member{Name: "PERIOD_MS", Label: "Period (ms)", Number: 250, Format: "%.f", Min: 10, Max: 600000, Step: 1000}},
		{"telescope", "ALIGNMENT_POINT_MANDATORY_NUMBERS", "ALIGNMENT_POINT_ENTRY_RA", Member{Name: "ALIGNMENT_POINT_ENTRY_RA", Label: "Right Ascension (hh:mm:ss)", Format: "%010.6m", Min: 0, Max: 24}},
		{"telescope", "DRIVER_INFO", "DRIVER_EXEC", Member{Name: "DRIVER_EXEC", Label: "Exec", Text: "indi_simulator_telescope"}},
		{"telescope", "DEBUG", "DISABLE", Member{Name: "DISABLE", Label: "Disable", Switch: true}},
		{"telescope", "ALIGNMENT_POINT_OPTIONAL_BINARY_BLOB", "ALIGNMENT_POINT_ENTRY_PRIVATE", Member{Name: "ALIGNMENT_POINT_ENTRY_PRIVATE", Label: "Private binary data"}},
	}
	for _, c := range cases {
		t.Run(c.property, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client, server := connectedBaseline(t, c.simulator)
				p, _ := client.Property(server.device, c.property)
				got, ok := p.Member(c.member)
				if !ok || got != c.want {
					t.Errorf("%s.%s = %+v, want %+v", c.property, c.member, got, c.want)
				}
			})
		})
	}
}

func TestLightProperty(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, server := connectedDevice(t, "weather")
		p, _ := c.Property(server.device, "WEATHER_STATUS")
		if p.Type != LightType || p.Perm != ReadOnly || len(p.Members) == 0 {
			t.Fatalf("WEATHER_STATUS = %+v, want a read-only Light with members", p)
		}
		for _, m := range p.Members {
			if !slices.Contains([]State{Idle, Ok, Busy, Alert}, m.Light) {
				t.Errorf("light %s = %q, want a state", m.Name, m.Light)
			}
		}
	})
}

func connectDevice(t *testing.T, c *Client, device string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	return c.ConnectDevice(ctx, device)
}

func disconnectDevice(t *testing.T, c *Client, device string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	return c.DisconnectDevice(ctx, device)
}

func TestFirstMessageIsGetProperties(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, server := connectedBaseline(t, "focus")
		first := server.received()[0]
		if first.XMLName.Local != "getProperties" || first.Version != "1.7" || first.Device != "" {
			t.Errorf("first message = %+v, want getProperties version 1.7 for every device", first)
		}
	})
}

func TestReconnectReplacesTheStore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := startReplay(t, "focus")
		c := server.newClient()
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		events := c.Subscribe(ctx)

		done := run(t, c)
		waitFor(t, c, "the baseline", defined(server.device, "CONNECTION"))
		if err := connectDevice(t, c, server.device); err != nil {
			t.Fatal(err)
		}
		server.drop()
		if err := <-done; err == nil {
			t.Fatal("Run returned no error after the server closed the connection")
		}
		if c.Connected() || len(c.Devices()) != 0 {
			t.Errorf("after the connection ended: connected %v, devices %v, want no connection and no devices", c.Connected(), c.Devices())
		}

		run(t, c)
		waitFor(t, c, "the new baseline", defined(server.device, "CONNECTION"))
		p, _ := c.Property(server.device, "CONNECTION")
		if on := p.On(); !slices.Equal(on, []string{"DISCONNECT"}) {
			t.Errorf("CONNECTION after the new baseline has %v on, want the baseline's [DISCONNECT]", on)
		}
		if _, ok := c.Property(server.device, "ABS_FOCUS_POSITION"); ok {
			t.Error("ABS_FOCUS_POSITION from the old connection is still in the store")
		}

		var kinds []EventKind
		for event := range events {
			if event.Kind == Connected || event.Kind == Disconnected {
				kinds = append(kinds, event.Kind)
			}
			if len(kinds) == 3 {
				break
			}
		}
		if want := []EventKind{Connected, Disconnected, Connected}; !slices.Equal(kinds, want) {
			t.Errorf("events = %v, want %v", kinds, want)
		}
	})
}

func TestRunRefusesASecondRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _ := connectedBaseline(t, "focus")
		if err := c.Run(context.Background()); !errors.Is(err, ErrRunning) {
			t.Errorf("second Run = %v, want ErrRunning", err)
		}
	})
}

func TestRunEndsWithItsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := startReplay(t, "focus")
		c := server.newClient()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- c.Run(ctx) }()
		waitFor(t, c, "the baseline", defined(server.device, "CONNECTION"))
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("Run = %v, want context.Canceled", err)
			}
		case <-time.After(testTimeout):
			t.Fatal("Run did not return after its context ended")
		}
	})
}

func TestRunFailsToDial(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := startReplay(t, "focus")
		server.close()
		c := server.newClient()
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		if err := c.Run(ctx); err == nil {
			t.Error("Run dialed a closed port with no error")
		}
	})
}

func TestWaitForEndsWithItsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _ := connectedBaseline(t, "focus")
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		err := c.WaitFor(ctx, defined("No Such Device", "CONNECTION"))
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("WaitFor = %v, want context.DeadlineExceeded", err)
		}
	})
}

func TestSubscriptionEndsWithItsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewClient("127.0.0.1:1")
		ctx, cancel := context.WithCancel(context.Background())
		events := c.Subscribe(ctx)
		cancel()
		select {
		case _, open := <-events:
			if open {
				t.Error("the subscription delivered an event after its context ended")
			}
		case <-time.After(testTimeout):
			t.Fatal("the subscription did not close after its context ended")
		}
	})
}

func TestSubscribersReceiveEveryEventInOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := startReplay(t, "focus")
		c := server.newClient()
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		first, second := c.Subscribe(ctx), c.Subscribe(ctx)
		run(t, c)
		names := definedNames(transcript(t, "focus", "baseline"))
		for _, events := range []<-chan Event{first, second} {
			var got []string
			for event := range events {
				if event.Kind == Defined {
					got = append(got, event.Property)
				}
				if len(got) == len(names) {
					break
				}
			}
			if !slices.Equal(got, names) {
				t.Errorf("Defined events = %v, want %v", got, names)
			}
		}
	})
}

// A caller that names its own dialer reaches the server through it,
// the way the operator's tests reach a server over an in-memory pipe.
func TestADialerReplacesTheNetwork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := startReplay(t, "focus")
		var dialed []string
		dialer := DialerFunc(func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return server.DialContext(ctx, network, address)
		})
		c := NewClient("east-telescope.observatory.svc:7624", WithDialer(dialer))
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		events := c.Subscribe(ctx)
		run(t, c)
		awaitDefinitions(t, events, 1)
		if !slices.Equal(dialed, []string{"east-telescope.observatory.svc:7624"}) {
			t.Errorf("the dialer received %v, want the client's address", dialed)
		}
	})
}
