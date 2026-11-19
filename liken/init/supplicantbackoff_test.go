package main

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

// The restart delay doubles from backoff and stops at maxBackoff, so a
// supplicant that dies again and again is started at least once in
// each maxBackoff, plus the jitter.
func TestTheSupplicantDelayDoublesUpToItsLimit(t *testing.T) {
	p := &supplicantProcess{backoff: time.Second, maxBackoff: 30 * time.Second}
	for _, tc := range []struct {
		name  string
		delay time.Duration
		want  time.Duration
	}{
		{"a delay far below the limit", time.Second, 2 * time.Second},
		{"a delay that doubles past the limit", 16 * time.Second, 30 * time.Second},
		{"a delay at the limit", 30 * time.Second, 30 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.next(tc.delay); got != tc.want {
				t.Errorf("next(%s) = %s, want %s", tc.delay, got, tc.want)
			}
		})
	}
}

// The first restart after a death waits backoff, with up to half of it
// again as jitter, and not twice backoff.
func TestTheFirstRestartWaitsTheFirstDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &supplicantProcess{
			ifname: "wlan0",
			stop:   make(chan struct{}), done: make(chan struct{}),
			backoff: 10 * time.Second, maxBackoff: 30 * time.Second,
		}
		var startedAt time.Time
		orig := startSupplicant
		startSupplicant = func(string, string) (runningSupplicant, error) {
			startedAt = time.Now()
			return runningSupplicant{}, nil
		}
		t.Cleanup(func() { startSupplicant = orig })

		begin := time.Now()
		backoff := p.backoff
		p.restart(context.Background(), "", &backoff)

		if waited := startedAt.Sub(begin); waited < p.backoff || waited >= p.backoff*3/2 {
			t.Errorf("the first restart waited %s, want from %s to %s because backoff is the first delay", waited, p.backoff, p.backoff*3/2)
		}
	})
}
