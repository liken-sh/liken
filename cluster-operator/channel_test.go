package main

// Tests for the channel poller. The fetch edge is injected, so these
// drive Observe the way the sweep does and watch what the poller
// asks for and remembers; the real HTTP fetch gets its own test
// against a local server.

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liken-sh/liken/machine"
)

func channelDocument(latest string) []byte {
	return []byte(fmt.Sprintf("apiVersion: liken.sh/v1alpha1\nkind: Channel\nmetadata:\n  name: liken\nlatest: %s\n", latest))
}

// pollerWith returns a poller whose fetches serve the given latest
// version, counting calls.
func pollerWith(latest string, calls *atomic.Int64) *channelPoller {
	p := newChannelPoller()
	p.fetch = func(url string) ([]byte, error) {
		calls.Add(1)
		return channelDocument(latest), nil
	}
	return p
}

// awaitAvailable spins until the poller's background poll lands, the
// same wait-for-the-goroutine shape the supervisor's registry tests
// use, bounded so a broken poller fails instead of hanging.
func awaitAvailable(t *testing.T, p *channelPoller, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if p.Available() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("available = %q, want %q", p.Available(), want)
}

// awaitCalls spins until the fake fetch has been asked n times: polls
// run on their own goroutine, so a test that counts them has to wait
// for them, bounded the same way awaitAvailable is.
func awaitCalls(t *testing.T, calls *atomic.Int64, n int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if calls.Load() == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("fetches: %d, want %d", calls.Load(), n)
}

var channelSpec = machine.ClusterReleasesSpec{Source: "https://releases.example/"}

func TestPollerLearnsTheChannelsLatest(t *testing.T) {
	var calls atomic.Int64
	p := pollerWith("2026.07.13-002", &calls)

	p.Observe(channelSpec, time.Now())
	awaitAvailable(t, p, "2026.07.13-002")
}

func TestPollerIsLazyBetweenSweeps(t *testing.T) {
	var calls atomic.Int64
	p := pollerWith("2026.07.13-002", &calls)

	// Sweeps run every ten seconds; the channel is asked once per
	// interval, not once per sweep.
	now := time.Now()
	p.Observe(channelSpec, now)
	awaitAvailable(t, p, "2026.07.13-002")
	p.Observe(channelSpec, now.Add(10*time.Second))
	p.Observe(channelSpec, now.Add(20*time.Second))
	if calls.Load() != 1 {
		t.Errorf("fetches: %d, want 1", calls.Load())
	}
}

func TestPollerReasksWhenTheAnswerAgesOut(t *testing.T) {
	var calls atomic.Int64
	p := pollerWith("2026.07.13-002", &calls)

	now := time.Now()
	p.Observe(channelSpec, now)
	awaitAvailable(t, p, "2026.07.13-002")
	p.Observe(channelSpec, now.Add(channelPollInterval))
	awaitCalls(t, &calls, 2)
	awaitAvailable(t, p, "2026.07.13-002")
}

func TestCheckEditForcesAnImmediatePoll(t *testing.T) {
	var calls atomic.Int64
	p := pollerWith("2026.07.13-002", &calls)

	now := time.Now()
	p.Observe(channelSpec, now)
	awaitAvailable(t, p, "2026.07.13-002")

	// One sweep later the spec carries a new check value: the poll
	// happens now, not at the interval.
	nudged := channelSpec
	nudged.Check = "again please"
	p.Observe(nudged, now.Add(10*time.Second))
	awaitCalls(t, &calls, 2)

	// The same check value on later sweeps is not a new nudge.
	p.Observe(nudged, now.Add(20*time.Second))
	if calls.Load() != 2 {
		t.Errorf("fetches after a repeated check: %d, want 2", calls.Load())
	}
}

func TestANewSourceDropsTheOldAnswer(t *testing.T) {
	var calls atomic.Int64
	p := newChannelPoller()
	p.fetch = func(url string) ([]byte, error) {
		calls.Add(1)
		// Block the second channel's answer so the gap shows: the
		// old channel's latest must not linger while the new one is
		// still being asked.
		if calls.Load() > 1 {
			return nil, errors.New("unreachable")
		}
		return channelDocument("2026.07.13-002"), nil
	}

	now := time.Now()
	p.Observe(channelSpec, now)
	awaitAvailable(t, p, "2026.07.13-002")

	moved := machine.ClusterReleasesSpec{Source: "https://elsewhere.example"}
	p.Observe(moved, now.Add(10*time.Second))
	awaitAvailable(t, p, "")
}

func TestAFailedPollKeepsTheLastAnswer(t *testing.T) {
	var calls atomic.Int64
	p := newChannelPoller()
	p.fetch = func(url string) ([]byte, error) {
		if calls.Add(1) > 1 {
			return nil, errors.New("the channel is down")
		}
		return channelDocument("2026.07.13-002"), nil
	}

	now := time.Now()
	p.Observe(channelSpec, now)
	awaitAvailable(t, p, "2026.07.13-002")
	p.Observe(channelSpec, now.Add(channelPollInterval))
	// The failed poll must complete and leave the answer standing.
	awaitCalls(t, &calls, 2)
	awaitAvailable(t, p, "2026.07.13-002")
}

func TestNoSourceMeansNothingAvailable(t *testing.T) {
	var calls atomic.Int64
	p := pollerWith("2026.07.13-002", &calls)

	now := time.Now()
	p.Observe(channelSpec, now)
	awaitAvailable(t, p, "2026.07.13-002")

	// The channel leaves the spec entirely: the stale answer goes
	// with it, and nothing is fetched.
	p.Observe(machine.ClusterReleasesSpec{}, now.Add(10*time.Second))
	awaitAvailable(t, p, "")
	if calls.Load() != 1 {
		t.Errorf("fetches: %d, want 1", calls.Load())
	}
}

func TestFetchChannelDocumentSpeaksHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/releases/channel.yaml" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(channelDocument("2026.07.13-002"))
	}))
	defer server.Close()

	raw, err := fetchChannelDocument(server.URL + "/releases/channel.yaml")
	if err != nil {
		t.Fatal(err)
	}
	channel, err := machine.ParseChannel(raw)
	if err != nil {
		t.Fatal(err)
	}
	if channel.Latest != "2026.07.13-002" {
		t.Errorf("latest: %q", channel.Latest)
	}

	if _, err := fetchChannelDocument(server.URL + "/absent/channel.yaml"); err == nil {
		t.Error("a 404 must be an error, not an empty document")
	}
}
