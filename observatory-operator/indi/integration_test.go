// The test against a real indiserver, with the focuser simulator
// behind it in the topology of plan 03. It covers what the transcripts
// cannot: a server that restarts, and a driver that restarts behind
// the server. It skips itself when Docker or the simulators image is
// missing, and in -short mode.

package indi

import (
	"context"
	"slices"
	"testing"
	"time"
)

// dockerWait bounds each wait on the real server. A restart of the
// server's container takes a few seconds, and a pull is not part of
// it.
const dockerWait = time.Minute

func TestAgainstIndiserver(t *testing.T) {
	image := simulatorImage(t)
	r := startRig(t, image, "indi_simulator_focus")
	const device = "Focuser Simulator"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := NewClient(r.address())
	events := c.Subscribe(ctx)
	go keepRunning(ctx, c)

	wait := func(t *testing.T, what string, condition func(*Store) bool) {
		t.Helper()
		waitCtx, cancel := context.WithTimeout(ctx, dockerWait)
		defer cancel()
		if err := c.WaitFor(waitCtx, condition); err != nil {
			t.Fatalf("waiting for %s: %v", what, err)
		}
	}
	bounded := func(t *testing.T) context.Context {
		waitCtx, cancel := context.WithTimeout(ctx, dockerWait)
		t.Cleanup(cancel)
		return waitCtx
	}
	disconnected := func(s *Store) bool {
		p, ok := s.Property(device, "CONNECTION")
		return ok && slices.Equal(p.On(), []string{"DISCONNECT"})
	}

	t.Run("baseline", func(t *testing.T) {
		wait(t, "the baseline", disconnected)
	})

	t.Run("connect", func(t *testing.T) {
		if err := c.ConnectDevice(bounded(t), device); err != nil {
			t.Fatal(err)
		}
		wait(t, "the focuser's position", defined(device, "ABS_FOCUS_POSITION"))
	})

	t.Run("an update after a change", func(t *testing.T) {
		sent, err := c.SetNumbers(device, "ABS_FOCUS_POSITION", map[string]float64{"FOCUS_ABSOLUTE_POSITION": 52000})
		if err != nil {
			t.Fatal(err)
		}
		p, err := c.Settle(bounded(t), sent)
		if err != nil {
			t.Fatal(err)
		}
		if m, _ := p.Member("FOCUS_ABSOLUTE_POSITION"); p.State != Ok || m.Number != 52000 {
			t.Errorf("ABS_FOCUS_POSITION = %s at %v, want Ok at 52000", p.State, m.Number)
		}
	})

	t.Run("disconnect", func(t *testing.T) {
		if err := c.DisconnectDevice(bounded(t), device); err != nil {
			t.Fatal(err)
		}
		wait(t, "the focuser's position to go", func(s *Store) bool {
			_, ok := s.Property(device, "ABS_FOCUS_POSITION")
			return !ok
		})
	})

	t.Run("a driver that restarts behind the server", func(t *testing.T) {
		if err := c.ConnectDevice(bounded(t), device); err != nil {
			t.Fatal(err)
		}
		r.restartDriver("indi_simulator_focus")
		// indiserver deletes the device when the shim exits, then
		// restarts the shim, and the new driver defines its properties
		// again, disconnected.
		next(t, events, func(e Event) bool { return e.Kind == Deleted && e.Device == device && e.Property == "" })
		wait(t, "the restarted driver", disconnected)
		if err := c.ConnectDevice(bounded(t), device); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a server that restarts", func(t *testing.T) {
		r.restartServer()
		next(t, events, kind(Disconnected))
		next(t, events, kind(Connected))
		wait(t, "the new baseline", disconnected)
		if err := c.ConnectDevice(bounded(t), device); err != nil {
			t.Fatal(err)
		}
	})
}

// keepRunning runs the client again each time its connection ends,
// until ctx ends. The pause between two runs is a clock: it spaces the
// dials while the server's container restarts and refuses them.
func keepRunning(ctx context.Context, c *Client) {
	for {
		c.Run(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}
