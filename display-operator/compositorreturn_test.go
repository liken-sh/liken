package main

// These tests cover a mode switch whose compositor comes back late.
// The kubelet treats each restart the operator orders as a crash, so a
// second restart within ten minutes waits in its crash backoff, and the
// backoff doubles up to five minutes. The readback's budget starts when
// the new compositor answers, so a late compositor is not a decline.

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
			// The drill on stick-1: the second restart within a
			// minute waited 23 seconds in the crash backoff.
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
					t.Fatalf("the switch answered %v, want a failure: %v", err, c.fails)
				}
				if errors.Is(err, errModeDeclined) != c.declined {
					t.Errorf("the switch answered %v, want a decline: %v", err, c.declined)
				}
				// The fallback look runs each millisecond, so the switch
				// ends within one of the moment the answer arrives.
				if waited := time.Since(start); waited < c.waited || waited > c.waited+modeSwitchFallback {
					t.Errorf("the switch waited %v, want %v", waited, c.waited)
				}
				if compositor.ended() != 1 {
					t.Errorf("the switch ended the compositor %d times, want once", compositor.ended())
				}
			})
		})
	}
}

// A compositor that is still in its crash backoff when the kubelet's
// call ends is not a decline either, so the retry reads the screen
// again and restarts nothing.
func TestARetryAfterTheCallEndsRestartsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		plugin, compositor := modeSwitchBench(t)
		plugin.switchTimeout = modeSwitchTimeout
		compositor.backoff = 2 * time.Minute
		call, end := context.WithTimeout(t.Context(), 45*time.Second)
		defer end()

		err := plugin.applyMode(call, Output{Connector: "HDMI-A-2"}, "1280x720@60")
		if err == nil || errors.Is(err, errModeDeclined) {
			t.Fatalf("the switch answered %v, want the call's end and no decline", err)
		}
		time.Sleep(2 * time.Minute)

		if err := plugin.applyMode(t.Context(), Output{Connector: "HDMI-A-2"}, "1280x720@60"); err != nil {
			t.Fatalf("the retry answered %v", err)
		}
		if compositor.ended() != 1 {
			t.Errorf("the switch and its retry ended the compositor %d times, want once", compositor.ended())
		}
	})
}
