package main

// Tests of the fetcher against the in-memory release server of
// download_test.go. They cover the Rejected state, the retry backoff,
// and a new request during a download. Each test runs in a synctest
// bubble, so a download that stalls for a minute takes no real time.

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/liken/releases"
)

func TestANewRequestClearsARejectedState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bad := makeRelease("0.2.0")
		bad.artifacts["liken.cpio"] = []byte("corrupted")
		// A corrected release is published under a new version.
		good := makeRelease("0.2.1")
		server := serveRelease(t, new(atomic.Int64), bad, good)
		slot := t.TempDir()

		f := fetcherFor(server)
		f.Ensure(fetchFor(bad, slot, activeSlot(t)))
		awaitSettled(f)

		f.Ensure(fetchFor(good, slot, activeSlot(t)))
		if snap := awaitSettled(f); snap.state != fetchVerified {
			t.Errorf("after the request changed from a rejected release, the state is %+v, want Verified", snap)
		}
	})
}

func TestServerFailuresAreTransientAndRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := makeRelease("0.2.0")
		var broken atomic.Bool
		broken.Store(true)
		var hits atomic.Int64
		server := apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			hits.Add(1)
			if broken.Load() {
				http.Error(w, "the server is broken", http.StatusInternalServerError)
				return
			}
			if req.URL.Path == "/releases/0.2.0/release.yaml" {
				w.Write(release.document)
				return
			}
			for name, contents := range release.artifacts {
				if req.URL.Path == "/releases/0.2.0/"+name {
					w.Write(contents)
				}
			}
		}))
		slot := t.TempDir()

		f := fetcherFor(server)
		fetch := fetchFor(release, slot, activeSlot(t))
		f.Ensure(fetch)
		if snap := awaitSettled(f); snap.state != fetchFailed {
			t.Fatalf("with a server that answers 500, the state is %+v, want Failed", snap)
		}

		// The retry keeps the last failure's detail and stays marked as
		// a retry. Otherwise a condition written while it runs would
		// lose the failure, and its reason would change to Downloading
		// on each attempt.
		broken.Store(false)
		time.Sleep(fetchFirstRetry * 11 / 10)
		if snap := f.Ensure(fetch); snap.state != fetchRunning || !snap.retrying || !strings.Contains(snap.detail, "retrying after") {
			t.Errorf("the retry is %+v, want Running, retrying, and a detail that contains \"retrying after\"", snap)
		}
		if snap := awaitSettled(f); snap.state != fetchVerified || snap.retrying {
			t.Errorf("after the server recovered, the state is %+v, want Verified and not retrying", snap)
		}
	})
}

// brokenServer responds to every request with a 500 and counts them.
func brokenServer(t *testing.T, hits *atomic.Int64) *apiservertest.Server {
	return apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits.Add(1)
		http.Error(w, "the server is broken", http.StatusInternalServerError)
	}))
}

// Ensure does not start a download before the retry time of a failed
// one, and it returns the same retryAt.
func TestATransientFailureWaitsOutItsBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var hits atomic.Int64
		f := &fetcher{client: brokenServer(t, &hits).Client(), jitter: noJitter}
		fetch := fetchFor(makeRelease("0.2.0"), t.TempDir(), activeSlot(t))
		f.Ensure(fetch)
		failed := awaitSettled(f)
		sent := hits.Load()

		early := f.Ensure(fetch)
		synctest.Wait()

		if early.state != fetchFailed || hits.Load() != sent || !early.retryAt.Equal(failed.retryAt) ||
			time.Until(failed.retryAt) != fetchFirstRetry {
			t.Errorf("Ensure before the retry time returned %+v and sent %d more requests; want Failed, retry in %s, and none",
				early, hits.Load()-sent, fetchFirstRetry)
		}
	})
}

// The backoff doubles from ten seconds up to two minutes.
func TestTheDownloadBackoffDoublesToTwoMinutes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var hits atomic.Int64
		f := &fetcher{client: brokenServer(t, &hits).Client(), jitter: noJitter}
		fetch := fetchFor(makeRelease("0.2.0"), t.TempDir(), activeSlot(t))
		var delays []time.Duration
		for range 6 {
			f.Ensure(fetch)
			snap := awaitSettled(f)
			delays = append(delays, time.Until(snap.retryAt))
			time.Sleep(time.Until(snap.retryAt))
		}

		want := []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second, 2 * time.Minute, 2 * time.Minute}
		if !equalDurations(delays, want) {
			t.Errorf("delays = %v, want %v", delays, want)
		}
	})
}

// A new request starts at once, without waiting for the backoff of
// the previous one.
func TestANewRequestStartsAtOnceAfterAFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var hits atomic.Int64
		f := &fetcher{client: brokenServer(t, &hits).Client(), jitter: noJitter}
		f.Ensure(fetchFor(makeRelease("0.2.0"), t.TempDir(), activeSlot(t)))
		awaitSettled(f)

		snap := f.Ensure(fetchFor(makeRelease("0.3.0"), t.TempDir(), activeSlot(t)))

		if snap.state != fetchRunning {
			t.Errorf("a changed request after a failure returned %+v, want Running", snap)
		}
		awaitSettled(f)
	})
}

func TestEnsureNeverBlocksOnTheDownload(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := makeRelease("0.2.0")
		server := apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			<-req.Context().Done() // the server never responds
		}))

		// Ensure must return while the server has sent nothing, with no
		// time passed on the bubble's clock, because a pass that waits
		// longer than stuckAfter stops the heartbeat (liveness.go).
		f := fetcherFor(server)
		start := time.Now()
		snap := f.Ensure(fetchFor(release, t.TempDir(), activeSlot(t)))
		if snap.state != fetchRunning || time.Since(start) != 0 {
			t.Errorf("Ensure returned %+v after %v, want Running with no wait", snap, time.Since(start))
		}
	})
}

// stallingServer serves the first half of one release's liken.cpio and
// then sends nothing until the client disconnects. It serves every
// other file whole, and counts the most requests in progress at once.
type stallingServer struct {
	*apiservertest.Server
	inFlight, mostInFlight atomic.Int64
	stalling               atomic.Bool
}

func serveStalling(t *testing.T, stalled *fakeRelease, published ...*fakeRelease) *stallingServer {
	t.Helper()
	h := &stallingServer{}
	h.stalling.Store(true)
	h.Server = apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		now := h.inFlight.Add(1)
		defer h.inFlight.Add(-1)
		for most := h.mostInFlight.Load(); now > most && !h.mostInFlight.CompareAndSwap(most, now); most = h.mostInFlight.Load() {
		}
		for _, r := range append(published, stalled) {
			if req.URL.Path == "/releases/"+r.version+"/release.yaml" {
				w.Write(r.document)
				return
			}
			for name, contents := range r.artifacts {
				if req.URL.Path != "/releases/"+r.version+"/"+name {
					continue
				}
				if r != stalled || name != "liken.cpio" || !h.stalling.Load() {
					w.Write(contents)
					return
				}
				w.Header().Set("Content-Length", fmt.Sprint(len(contents)))
				w.Write(contents[:len(contents)/2])
				w.(http.Flusher).Flush()
				<-req.Context().Done()
				return
			}
		}
		http.NotFound(w, req)
	}))
	return h
}

// When the request changes, the download of the old release stops
// before the new one starts, so only one download writes the slot at a
// time.
func TestANewRequestStopsTheOldDownloadFirst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		old, wanted := makeRelease("0.2.0"), makeRelease("0.3.0")
		server := serveStalling(t, old, wanted)
		slot, active := t.TempDir(), activeSlot(t)
		f := fetcherFor(server.Server)
		f.Ensure(fetchFor(old, slot, active))
		awaitSettled(f)

		waiting := f.Ensure(fetchFor(wanted, slot, active))
		stopped := awaitSettled(f)
		started := f.Ensure(fetchFor(wanted, slot, active))
		done := awaitSettled(f)

		if waiting.state != fetchIdle || !strings.Contains(waiting.detail, "previous download") {
			t.Errorf("the changed request returned %+v, want Idle and a wait for the old download", waiting)
		}
		if stopped.state != fetchIdle {
			t.Errorf("after the old download stopped: %+v, want the new one not started", stopped)
		}
		if started.state != fetchRunning || done.state != fetchVerified {
			t.Errorf("the next pass started %+v and ended %+v, want the new release verified", started, done)
		}
		if most := server.mostInFlight.Load(); most != 1 {
			t.Errorf("%d downloads ran at once, want 1", most)
		}
		if _, err := os.Stat(filepath.Join(slot, "liken.cpio.partial")); !os.IsNotExist(err) {
			t.Errorf("the stopped download left its partial file: %v", err)
		}
		if failures := f.DownloadFailures(); failures != 0 {
			t.Errorf("%d failures, want 0: a stopped download did not fail", failures)
		}
	})
}

// A download of another release deletes the slot's release.yaml before
// it writes any file. So while the slot has files of two releases, it
// has no release.yaml that claims one of them.
func TestADownloadOfAnotherReleaseRemovesTheSlotsDocument(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		old, wanted := makeRelease("0.2.0"), makeRelease("0.3.0")
		server := serveStalling(t, wanted, old)
		slot, active := t.TempDir(), activeSlot(t)
		f := fetcherFor(server.Server)
		f.Ensure(fetchFor(old, slot, active))
		awaitSettled(f)

		f.Ensure(fetchFor(wanted, slot, active))
		awaitSettled(f)

		kernel, _ := os.ReadFile(filepath.Join(slot, "vmlinuz"))
		if string(kernel) != string(wanted.artifacts["vmlinuz"]) {
			t.Fatalf("the slot's kernel is %q, want the new release's, written before the server stalled the download", kernel)
		}
		if _, err := os.Stat(filepath.Join(slot, "release.yaml")); !os.IsNotExist(err) {
			t.Errorf("the slot still has a release document beside the new kernel: %v", err)
		}
	})
}

// A download fails when the server stops sending for releases.StallLimit,
// and the first Ensure after the backoff retries it.
func TestAStalledDownloadFailsAndIsRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := makeRelease("0.2.0")
		server := serveStalling(t, release)
		f := fetcherFor(server.Server)
		fetch := fetchFor(release, t.TempDir(), activeSlot(t))
		f.Ensure(fetch)
		awaitSettled(f)

		time.Sleep(releases.StallLimit)
		stalled := awaitSettled(f)
		server.stalling.Store(false)
		time.Sleep(time.Until(stalled.retryAt))
		f.Ensure(fetch)
		retried := awaitSettled(f)

		if stalled.state != fetchFailed || !strings.Contains(stalled.detail, "no bytes arrived") {
			t.Errorf("after the stall limit, the state is %+v, want a failure that names the stall", stalled)
		}
		if retried.state != fetchVerified {
			t.Errorf("after the retry, the state is %+v, want Verified", retried)
		}
	})
}

// A download that Ensure cancelled for a new request did not fail,
// even when the request changes back before the download stops.
// Ensure starts the download again with no backoff and records no
// failure.
func TestARequestThatChangesBackRestartsWithNoBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first, second := makeRelease("0.2.0"), makeRelease("0.3.0")
		server := serveStalling(t, first, second)
		f := fetcherFor(server.Server)
		slot, active := t.TempDir(), activeSlot(t)
		f.Ensure(fetchFor(first, slot, active))
		synctest.Wait()

		f.Ensure(fetchFor(second, slot, active))
		f.Ensure(fetchFor(first, slot, active))
		stopped := awaitSettled(f)
		server.stalling.Store(false)
		restarted := f.Ensure(fetchFor(first, slot, active))
		awaitSettled(f)

		if stopped.state == fetchFailed || restarted.state != fetchRunning {
			t.Errorf("after the cancelled download stopped: %+v; the next pass: %+v; want no failure and a restart", stopped, restarted)
		}
	})
}

// Every download that ends calls wake, whether it verified, failed, or
// was cancelled by a new request. The fetcher is not busy when wake
// runs, so the pass that it starts can read the result or start the
// next download.
func TestEveryDownloadThatEndsWakesTheLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		old, wanted := makeRelease("0.2.0"), makeRelease("0.3.0")
		server := serveStalling(t, old, wanted)
		slot, active := t.TempDir(), activeSlot(t)
		f := fetcherFor(server.Server)
		var wakes atomic.Int64
		var busyAtWake atomic.Bool
		f.wake = func() {
			f.mu.Lock()
			busyAtWake.Store(busyAtWake.Load() || f.busy)
			f.mu.Unlock()
			wakes.Add(1)
		}

		f.Ensure(fetchFor(old, slot, active))
		awaitSettled(f)
		f.Ensure(fetchFor(wanted, slot, active))
		awaitSettled(f)
		afterStop := wakes.Load()
		f.Ensure(fetchFor(wanted, slot, active))
		awaitSettled(f)

		if afterStop != 1 || wakes.Load() != 2 || busyAtWake.Load() {
			t.Errorf("woke %d times after the stop and %d after the verified download, busy at a wake: %v; want 1, 2, false",
				afterStop, wakes.Load(), busyAtWake.Load())
		}
	})
}
