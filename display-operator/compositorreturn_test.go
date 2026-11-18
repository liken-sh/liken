package main

// The compositor can return late: when weston crashes during a mode
// switch, the container exits and the kubelet's crash backoff delays
// the next start by up to five minutes. modeSwitchTimeout starts only
// when the new compositor first responds, so a late compositor is not
// counted as declining the mode.

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestAModeSwitchWaitsForTheCompositorToComeBack(t *testing.T) {
	cases := []struct {
		name     string
		backoff  time.Duration
		gone     bool
		declines bool
		declined bool
		fails    bool
		waited   time.Duration
	}{
		{
			name:    "the compositor comes back at once",
			backoff: 0,
		},
		{
			name:    "the compositor waits out a crash backoff",
			backoff: 23 * time.Second,
			waited:  23 * time.Second,
		},
		{
			name:    "the compositor waits out the longest crash backoff",
			backoff: 5 * time.Minute,
			waited:  5 * time.Minute,
		},
		{
			name:     "the compositor comes back late at another mode",
			backoff:  23 * time.Second,
			declines: true,
			declined: true,
			fails:    true,
			waited:   23*time.Second + modeSwitchTimeout,
		},
		{
			name:   "the compositor never comes back",
			gone:   true,
			fails:  true,
			waited: compositorReturnLimit,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				plugin, compositor := modeSwitchBench(t)
				plugin.switchTimeout = modeSwitchTimeout
				compositor.backoff, compositor.gone, compositor.declines = c.backoff, c.gone, c.declines
				start := time.Now()

				err := plugin.applyMode(t.Context(), Output{Connector: "HDMI-A-2"}, "1280x720@60")

				if (err != nil) != c.fails {
					t.Fatalf("applyMode returned %v, want an error: %v", err, c.fails)
				}
				if errors.Is(err, errModeDeclined) != c.declined {
					t.Errorf("applyMode returned %v, want errModeDeclined: %v", err, c.declined)
				}
				// The bench sets switchFallback to a millisecond, so
				// modeSwitchFallback is a loose upper bound on the delay.
				if waited := time.Since(start); waited < c.waited || waited > c.waited+modeSwitchFallback {
					t.Errorf("applyMode waited %v, want %v", waited, c.waited)
				}
				if compositor.ended() != 1 {
					t.Errorf("applyMode ended the compositor %d times, want once", compositor.ended())
				}
			})
		})
	}
}

// When the kubelet's call ends while the compositor is still in its
// crash backoff, the switch has not been declined, and the retry finds
// the screen at the mode without another restart.
func TestARetryAfterTheCallEndsRestartsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		plugin, compositor := modeSwitchBench(t)
		plugin.switchTimeout = modeSwitchTimeout
		compositor.backoff = 2 * time.Minute
		call, end := context.WithTimeout(t.Context(), 45*time.Second)
		defer end()

		err := plugin.applyMode(call, Output{Connector: "HDMI-A-2"}, "1280x720@60")
		if err == nil || errors.Is(err, errModeDeclined) {
			t.Fatalf("applyMode returned %v, want the context deadline error and not errModeDeclined", err)
		}
		time.Sleep(2 * time.Minute)

		if err := plugin.applyMode(t.Context(), Output{Connector: "HDMI-A-2"}, "1280x720@60"); err != nil {
			t.Fatalf("retrying applyMode after the backoff: %v", err)
		}
		if compositor.ended() != 1 {
			t.Errorf("applyMode and its retry ended the compositor %d times, want once", compositor.ended())
		}
	})
}
