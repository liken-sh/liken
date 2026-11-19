package main

import (
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

func channelDocument(latest string) []byte {
	return []byte(fmt.Sprintf("apiVersion: liken.sh/v1alpha1\nkind: Channel\nmetadata:\n  name: liken\nlatest: %s\n", latest))
}

// pollerWith returns a poller whose fetch function returns a channel
// document with the given latest version and counts its calls.
func pollerWith(latest string, calls *atomic.Int64) *channelPoller {
	p := newChannelPoller()
	p.fetch = func(url string) ([]byte, error) {
		calls.Add(1)
		return channelDocument(latest), nil
	}
	return p
}

// awaitAvailable waits for the background poll to finish and checks the
// version the poller reports. synctest.Wait returns when every
// goroutine in the bubble is blocked or has ended.
func awaitAvailable(t *testing.T, p *channelPoller, want string) {
	t.Helper()
	synctest.Wait()
	if got := p.Available(); got != want {
		t.Fatalf("available = %q, want %q", got, want)
	}
}

// awaitCalls waits for the background poll to finish and checks that
// the fake fetch function ran n times.
func awaitCalls(t *testing.T, calls *atomic.Int64, n int64) {
	t.Helper()
	synctest.Wait()
	if got := calls.Load(); got != n {
		t.Fatalf("the poller fetched %d times, want %d", got, n)
	}
}

var channelSpec = cluster.ClusterReleasesSpec{Source: "https://releases.example/"}

func TestPollerReportsTheChannelsLatestVersion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		p := pollerWith("2026.07.13-002", &calls)

		p.Observe(channelSpec, "", time.Now())
		awaitAvailable(t, p, "2026.07.13-002")
	})
}

func TestPollerFetchesOncePerInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		p := pollerWith("2026.07.13-002", &calls)

		// Passes run every ten seconds, but the poller fetches once per
		// interval.
		now := time.Now()
		p.Observe(channelSpec, "", now)
		awaitAvailable(t, p, "2026.07.13-002")
		p.Observe(channelSpec, "", now.Add(10*time.Second))
		p.Observe(channelSpec, "", now.Add(20*time.Second))
		if calls.Load() != 1 {
			t.Errorf("the poller fetched %d times, want 1", calls.Load())
		}
	})
}

func TestPollerFetchesAgainAfterTheInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		p := pollerWith("2026.07.13-002", &calls)

		now := time.Now()
		p.Observe(channelSpec, "", now)
		awaitAvailable(t, p, "2026.07.13-002")
		p.Observe(channelSpec, "", now.Add(channelPollInterval))
		awaitCalls(t, &calls, 2)
		awaitAvailable(t, p, "2026.07.13-002")
	})
}

func TestChangedCheckAnnotationFetchesAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		p := pollerWith("2026.07.13-002", &calls)

		now := time.Now()
		p.Observe(channelSpec, "", now)
		awaitAvailable(t, p, "2026.07.13-002")

		// A later pass sees a new check-releases value, and the poller
		// fetches at once instead of waiting for the interval.
		p.Observe(channelSpec, "again please", now.Add(10*time.Second))
		awaitCalls(t, &calls, 2)

		// The same value on a later pass does not fetch again.
		p.Observe(channelSpec, "again please", now.Add(20*time.Second))
		if calls.Load() != 2 {
			t.Errorf("after a repeated check, the poller fetched %d times, want 2", calls.Load())
		}
	})
}

func TestANewSourceDropsTheOldSourcesVersion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		p := newChannelPoller()
		p.fetch = func(url string) ([]byte, error) {
			calls.Add(1)
			// The second source fails, so the test can check that the
			// old source's version is gone before the new source
			// returns one.
			if calls.Load() > 1 {
				return nil, errors.New("unreachable")
			}
			return channelDocument("2026.07.13-002"), nil
		}

		now := time.Now()
		p.Observe(channelSpec, "", now)
		awaitAvailable(t, p, "2026.07.13-002")

		moved := cluster.ClusterReleasesSpec{Source: "https://elsewhere.example"}
		p.Observe(moved, "", now.Add(10*time.Second))
		awaitAvailable(t, p, "")
	})
}

func TestAFailedFetchKeepsTheLastVersion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		p := newChannelPoller()
		p.fetch = func(url string) ([]byte, error) {
			if calls.Add(1) > 1 {
				return nil, errors.New("the channel is down")
			}
			return channelDocument("2026.07.13-002"), nil
		}

		now := time.Now()
		p.Observe(channelSpec, "", now)
		awaitAvailable(t, p, "2026.07.13-002")
		p.Observe(channelSpec, "", now.Add(channelPollInterval))
		// The failed fetch leaves the version unchanged.
		awaitCalls(t, &calls, 2)
		awaitAvailable(t, p, "2026.07.13-002")
	})
}

func TestNoSourceClearsTheVersionAndFetchesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		p := pollerWith("2026.07.13-002", &calls)

		now := time.Now()
		p.Observe(channelSpec, "", now)
		awaitAvailable(t, p, "2026.07.13-002")

		p.Observe(cluster.ClusterReleasesSpec{}, "", now.Add(10*time.Second))
		awaitAvailable(t, p, "")
		if calls.Load() != 1 {
			t.Errorf("the poller fetched %d times, want 1", calls.Load())
		}
	})
}

func TestFetchChannelDocumentRequestsTheDocumentOverHTTP(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/releases/channel.yaml" {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(channelDocument("2026.07.13-002"))
		}))

		raw, err := fetchChannelDocumentVia(server, apiservertest.Host+"/releases/channel.yaml")
		if err != nil {
			t.Fatalf("fetching the channel document: %v", err)
		}
		channel, err := machine.ParseChannel(raw)
		if err != nil {
			t.Fatalf("parsing the channel document: %v", err)
		}
		if channel.Latest != "2026.07.13-002" {
			t.Errorf("the channel's latest version is %q, want 2026.07.13-002", channel.Latest)
		}

		if _, err := fetchChannelDocumentVia(server, apiservertest.Host+"/absent/channel.yaml"); err == nil {
			t.Error("fetching a channel document that returns 404 returned no error, want one")
		}
	})
}
