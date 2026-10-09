package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

// noJitter is the jitter of a schedule whose delays a test reads
// exactly.
func noJitter() float64 { return 0 }

// failingPasses runs n passes through the schedule, each with the
// failures fail adds, and answers the delay after each one.
func failingPasses(s *retrySchedule, n int, fail func(*passOutcome)) []time.Duration {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	var delays []time.Duration
	for range n {
		out := &passOutcome{}
		fail(out)
		at, _ := s.next(out, now)
		delays = append(delays, at.Sub(now))
		now = at
	}
	return delays
}

func transientFailure(out *passOutcome) { out.fail("hosts", unix.EIO) }
func lastingFailure(out *passOutcome)   { out.fail("sysctls", unix.ENOENT) }

func TestRetryDelaysDoubleToTheirCeilings(t *testing.T) {
	cases := []struct {
		name string
		fail func(*passOutcome)
		want []time.Duration
	}{
		{"a failure a retry can clear", transientFailure,
			[]time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second, 10 * time.Second}},
		{"a failure that will not clear by itself", lastingFailure,
			[]time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second, 160 * time.Second, 5 * time.Minute, 5 * time.Minute}},
		{"both kinds, where the sooner retry wins", func(out *passOutcome) { transientFailure(out); lastingFailure(out) },
			[]time.Duration{time.Second, 2 * time.Second, 4 * time.Second}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &retrySchedule{jitter: noJitter}
			got := failingPasses(s, len(c.want), c.fail)
			if !equalDurations(got, c.want) {
				t.Errorf("delays = %v, want %v", got, c.want)
			}
		})
	}
}

func equalDurations(a, b []time.Duration) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAPassWithNoFailuresStopsTheRetryAndResetsTheDelay(t *testing.T) {
	s := &retrySchedule{jitter: noJitter}
	failingPasses(s, 3, transientFailure)

	_, retry := s.next(&passOutcome{}, time.Now())
	again := failingPasses(s, 1, transientFailure)

	if retry || again[0] != time.Second {
		t.Errorf("a clean pass asked for a retry: %v; the next failure waited %s, want 1s", retry, again[0])
	}
}

// A step's wake does not bring a pass in before a 429's Retry-After
// allows it, because that pass would send the same requests to a
// throttled API server.
func TestAWakeWaitsForA429sRetryAfter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		out := &passOutcome{}
		out.observe(apiclient.Outcome{Method: http.MethodPut, Status: http.StatusTooManyRequests, Err: throttledAfter(t, 30)})
		out.wakeBy(time.Now().Add(5 * time.Second))
		s := &retrySchedule{jitter: noJitter}

		at, ok := s.next(out, time.Now())

		if !ok || time.Until(at) != 30*time.Second {
			t.Errorf("the pass came in %s (%v), want the 30s the server asked for", time.Until(at), ok)
		}
	})
}

func TestRetryJitterAddsUpToATenthOfTheDelay(t *testing.T) {
	s := &retrySchedule{jitter: func() float64 { return 0.999 }}
	got := failingPasses(s, 5, transientFailure)

	if got[4] <= 10*time.Second || got[4] > 11*time.Second {
		t.Errorf("the jittered ceiling waited %s, want just under 11s", got[4])
	}
}

func TestAPassWakesAtTheTimeAStepAskedFor(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		out  func() *passOutcome
		want time.Duration
	}{
		{"a wake with no failures", func() *passOutcome {
			out := &passOutcome{}
			out.wakeBy(now.Add(3 * time.Second))
			return out
		}, 3 * time.Second},
		{"a wake sooner than the retry", func() *passOutcome {
			out := &passOutcome{}
			out.fail("sysctls", unix.ENOENT)
			out.wakeBy(now.Add(3 * time.Second))
			return out
		}, 3 * time.Second},
		{"a retry sooner than the wake", func() *passOutcome {
			out := &passOutcome{}
			out.fail("hosts", errors.New("a failure with no errno"))
			out.wakeBy(now.Add(time.Minute))
			return out
		}, time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &retrySchedule{jitter: noJitter}
			at, ok := s.next(c.out(), now)
			if !ok || at.Sub(now) != c.want {
				t.Errorf("wake in %s (%v), want %s", at.Sub(now), ok, c.want)
			}
		})
	}
}

// A 429 asks the client to wait, and liken's client answers it at once
// with no wait, so the retry waits at least as long as the API server
// asked.
func TestARetryAfterA429WaitsAsLongAsTheServerAsked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		out := &passOutcome{}
		out.observe(apiclient.Outcome{Method: http.MethodPut, Status: http.StatusTooManyRequests, Err: throttledAfter(t, 3)})
		s := &retrySchedule{jitter: noJitter}

		at, ok := s.next(out, time.Now())

		if !ok || time.Until(at) != 3*time.Second {
			t.Errorf("the retry came in %s (%v), want the 3s the server asked for", time.Until(at), ok)
		}
	})
}

// throttledAfter answers the error liken's client returns for a 429
// that asked for seconds of wait.
func throttledAfter(t *testing.T, seconds int) error {
	t.Helper()
	client := apiclient.New(apiservertest.Host, apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		http.Error(w, "slow down", http.StatusTooManyRequests)
	})).Client(), "")
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	return client.WithWaitContext(ended).RequestJSON(http.MethodPut, "/api/v1/nodes/node-1", []byte(`{}`), nil)
}
