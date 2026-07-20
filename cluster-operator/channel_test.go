package main

// Tests for the channel poller. Each test injects the fetch
// function, then calls Observe the way the sweep does, and checks
// what the poller asks for and remembers. A separate test checks the
// real HTTP fetch against a local server.

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
)

func channelDocument(latest string) []byte {
	return []byte(fmt.Sprintf("apiVersion: liken.sh/v1alpha1\nkind: Channel\nmetadata:\n  name: liken\nlatest: %s\n", latest))
}

// pollerWith returns a poller whose fetch function serves the given
// latest version. It counts how many times the fetch function runs.
func pollerWith(latest string, calls *atomic.Int64) *channelPoller {
	p := newChannelPoller()
	p.fetch = func(url string) ([]byte, error) {
		calls.Add(1)
		return channelDocument(latest), nil
	}
	return p
}

// awaitAvailable waits until the poller's background poll finishes.
// The supervisor's registry tests use the same wait-for-the-goroutine
// method. The wait has a limit, so a broken poller makes the test
// fail instead of hang.
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

// awaitCalls waits until the fake fetch function has run n times.
// Polls run on their own goroutine, so a test that counts them must
// wait for them. The wait has the same limit as awaitAvailable.
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

var channelSpec = cluster.ClusterReleasesSpec{Source: "https://releases.example/"}

func TestPollerLearnsTheChannelsLatest(t *testing.T) {
	var calls atomic.Int64
	p := pollerWith("2026.07.13-002", &calls)

	p.Observe(channelSpec, time.Now())
	awaitAvailable(t, p, "2026.07.13-002")
}

func TestPollerIsLazyBetweenSweeps(t *testing.T) {
	var calls atomic.Int64
	p := pollerWith("2026.07.13-002", &calls)

	// Sweeps run every ten seconds. The channel is asked once per
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

	// One sweep later, the spec carries a new check value. The poll
	// happens now, not at the next interval.
	nudged := channelSpec
	nudged.Check = "again please"
	p.Observe(nudged, now.Add(10*time.Second))
	awaitCalls(t, &calls, 2)

	// The same check value on later sweeps is not a new signal.
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
		// This blocks the second channel's answer, so the test can
		// show the gap. The old channel's latest version must not
		// stay while the poller is still asking the new one.
		if calls.Load() > 1 {
			return nil, errors.New("unreachable")
		}
		return channelDocument("2026.07.13-002"), nil
	}

	now := time.Now()
	p.Observe(channelSpec, now)
	awaitAvailable(t, p, "2026.07.13-002")

	moved := cluster.ClusterReleasesSpec{Source: "https://elsewhere.example"}
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
	// The failed poll must finish and leave the answer unchanged.
	awaitCalls(t, &calls, 2)
	awaitAvailable(t, p, "2026.07.13-002")
}

func TestNoSourceMeansNothingAvailable(t *testing.T) {
	var calls atomic.Int64
	p := pollerWith("2026.07.13-002", &calls)

	now := time.Now()
	p.Observe(channelSpec, now)
	awaitAvailable(t, p, "2026.07.13-002")

	// The channel is removed from the spec entirely. The stale
	// answer goes with it, and the poller fetches nothing.
	p.Observe(cluster.ClusterReleasesSpec{}, now.Add(10*time.Second))
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
