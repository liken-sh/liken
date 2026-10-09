package main

// The fetcher, tested against the in-memory release server of
// download_test.go: the hold, the backoff, and the retarget. Each test
// runs in a synctest bubble, so a download that stalls for a minute
// costs no real time.

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

func TestAChangedAskClearsTheHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bad := makeRelease("0.2.0")
		bad.artifacts["liken.cpio"] = []byte("corrupted")
		// The corrected release is published under a new version. This
		// is the recovery the design calls for, and the new ask fetches.
		good := makeRelease("0.2.1")
		server := serveRelease(t, new(atomic.Int64), bad, good)
		slot := t.TempDir()

		f := fetcherFor(server)
		f.Ensure(askFor(bad, slot, activeSlot(t)))
		awaitSettled(f)

		f.Ensure(askFor(good, slot, activeSlot(t)))
		if snap := awaitSettled(f); snap.state != fetchVerified {
			t.Errorf("a new ask starts fresh: %+v", snap)
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
		ask := askFor(release, slot, activeSlot(t))
		f.Ensure(ask)
		if snap := awaitSettled(f); snap.state != fetchFailed {
			t.Fatalf("a down server is a transient failure: %+v", snap)
		}

		// The retry must carry the failure's reason, so a condition
		// written while the retry runs still says what failed.
		broken.Store(false)
		time.Sleep(fetchFirstRetry * 11 / 10)
		if snap := f.Ensure(ask); snap.state != fetchRunning || !strings.Contains(snap.detail, "retrying after") {
			t.Errorf("a retry should say what it's retrying after: %+v", snap)
		}
		if snap := awaitSettled(f); snap.state != fetchVerified {
			t.Errorf("recovery: %+v", snap)
		}
	})
}

// brokenServer answers every request with a 500 and counts them.
func brokenServer(t *testing.T, hits *atomic.Int64) *apiservertest.Server {
	return apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits.Add(1)
		http.Error(w, "the server is broken", http.StatusInternalServerError)
	}))
}

// A transient failure waits out its backoff: a pass before the retry
// time starts no download, and says when the retry comes.
func TestATransientFailureWaitsOutItsBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var hits atomic.Int64
		f := &fetcher{client: brokenServer(t, &hits).Client(), jitter: noJitter}
		ask := askFor(makeRelease("0.2.0"), t.TempDir(), activeSlot(t))
		f.Ensure(ask)
		failed := awaitSettled(f)
		sent := hits.Load()

		early := f.Ensure(ask)
		synctest.Wait()

		if early.state != fetchFailed || hits.Load() != sent || !early.retryAt.Equal(failed.retryAt) ||
			time.Until(failed.retryAt) != fetchFirstRetry {
			t.Errorf("a pass before the retry time answered %+v and sent %d more requests; want Failed, retry in %s, and none",
				early, hits.Load()-sent, fetchFirstRetry)
		}
	})
}

// The backoff doubles from ten seconds up to two minutes.
func TestTheDownloadBackoffDoublesToTwoMinutes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var hits atomic.Int64
		f := &fetcher{client: brokenServer(t, &hits).Client(), jitter: noJitter}
		ask := askFor(makeRelease("0.2.0"), t.TempDir(), activeSlot(t))
		var delays []time.Duration
		for range 6 {
			f.Ensure(ask)
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

// A new ask is a new release, so it starts at once, whatever the
// last ask's backoff.
func TestANewAskStartsAtOnceAfterAFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var hits atomic.Int64
		f := &fetcher{client: brokenServer(t, &hits).Client(), jitter: noJitter}
		f.Ensure(askFor(makeRelease("0.2.0"), t.TempDir(), activeSlot(t)))
		awaitSettled(f)

		snap := f.Ensure(askFor(makeRelease("0.3.0"), t.TempDir(), activeSlot(t)))

		if snap.state != fetchRunning {
			t.Errorf("a new ask after a failure answered %+v, want Running", snap)
		}
		awaitSettled(f)
	})
}

func TestEnsureNeverBlocksOnTheDownload(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := makeRelease("0.2.0")
		server := apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			<-req.Context().Done() // the server never answers
		}))

		// This is a small-scale version of the heartbeat's guarantee.
		// Ensure must return while the server still has not answered a
		// byte, with no time passing on the bubble's clock.
		f := fetcherFor(server)
		start := time.Now()
		snap := f.Ensure(askFor(release, t.TempDir(), activeSlot(t)))
		if snap.state != fetchRunning || time.Since(start) != 0 {
			t.Errorf("Ensure must start the download and return at once: %+v after %v", snap, time.Since(start))
		}
	})
}

// A server that holds the release's initramfs: it sends the first
// half, then nothing until the client goes away. Every other file it
// serves whole. It counts the downloads in flight and the most that
// were ever in flight at once.
type holdingServer struct {
	*apiservertest.Server
	inFlight, mostInFlight atomic.Int64
	holding                atomic.Bool
}

func serveHolding(t *testing.T, held *fakeRelease, published ...*fakeRelease) *holdingServer {
	t.Helper()
	h := &holdingServer{}
	h.holding.Store(true)
	h.Server = apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		now := h.inFlight.Add(1)
		defer h.inFlight.Add(-1)
		for most := h.mostInFlight.Load(); now > most && !h.mostInFlight.CompareAndSwap(most, now); most = h.mostInFlight.Load() {
		}
		for _, r := range append(published, held) {
			if req.URL.Path == "/releases/"+r.version+"/release.yaml" {
				w.Write(r.document)
				return
			}
			for name, contents := range r.artifacts {
				if req.URL.Path != "/releases/"+r.version+"/"+name {
					continue
				}
				if r != held || name != "liken.cpio" || !h.holding.Load() {
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

// A retarget stops the download of the old release before the new
// one starts, so one writer at a time changes the slot.
func TestARetargetStopsTheOldDownloadFirst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		old, wanted := makeRelease("0.2.0"), makeRelease("0.3.0")
		server := serveHolding(t, old, wanted)
		slot, active := t.TempDir(), activeSlot(t)
		f := fetcherFor(server.Server)
		f.Ensure(askFor(old, slot, active))
		awaitSettled(f)

		waiting := f.Ensure(askFor(wanted, slot, active))
		stopped := awaitSettled(f)
		started := f.Ensure(askFor(wanted, slot, active))
		done := awaitSettled(f)

		if waiting.state != fetchIdle || !strings.Contains(waiting.detail, "previous download") {
			t.Errorf("the retarget answered %+v, want a wait for the old download", waiting)
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

// A download for another release removes the slot's document before
// it writes a byte, so the slot claims no release while it holds the
// files of two.
func TestADownloadOfAnotherReleaseWithdrawsTheSlotsDocument(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		old, wanted := makeRelease("0.2.0"), makeRelease("0.3.0")
		server := serveHolding(t, wanted, old)
		slot, active := t.TempDir(), activeSlot(t)
		f := fetcherFor(server.Server)
		f.Ensure(askFor(old, slot, active))
		awaitSettled(f)

		f.Ensure(askFor(wanted, slot, active))
		awaitSettled(f)

		kernel, _ := os.ReadFile(filepath.Join(slot, "vmlinuz"))
		if string(kernel) != string(wanted.artifacts["vmlinuz"]) {
			t.Fatalf("the slot's kernel is %q, want the new release's, written before the hold", kernel)
		}
		if _, err := os.Stat(filepath.Join(slot, "release.yaml")); !os.IsNotExist(err) {
			t.Errorf("the slot still carries a release document beside the new kernel: %v", err)
		}
	})
}

// A server that stops sending fails the download after the stall
// limit, and the first pass after its backoff retries it.
func TestAStalledDownloadFailsAndIsRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := makeRelease("0.2.0")
		server := serveHolding(t, release)
		f := fetcherFor(server.Server)
		ask := askFor(release, t.TempDir(), activeSlot(t))
		f.Ensure(ask)
		awaitSettled(f)

		time.Sleep(releases.StallLimit)
		stalled := awaitSettled(f)
		server.holding.Store(false)
		time.Sleep(time.Until(stalled.retryAt))
		f.Ensure(ask)
		retried := awaitSettled(f)

		if stalled.state != fetchFailed || !strings.Contains(stalled.detail, "no bytes arrived") {
			t.Errorf("after the stall limit: %+v, want a failure that names the stall", stalled)
		}
		if retried.state != fetchVerified {
			t.Errorf("the retry: %+v, want verified", retried)
		}
	})
}

// A download that a changed ask cancelled did not fail, even when the
// ask changes back before it stops. The machine starts the download
// again at once, with no backoff and no failure in its condition.
func TestAnAskThatChangesBackRestartsWithNoBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first, second := makeRelease("0.2.0"), makeRelease("0.3.0")
		server := serveHolding(t, first, second)
		f := fetcherFor(server.Server)
		slot, active := t.TempDir(), activeSlot(t)
		f.Ensure(askFor(first, slot, active))
		synctest.Wait()

		f.Ensure(askFor(second, slot, active))
		f.Ensure(askFor(first, slot, active))
		stopped := awaitSettled(f)
		server.holding.Store(false)
		restarted := f.Ensure(askFor(first, slot, active))
		awaitSettled(f)

		if stopped.state == fetchFailed || restarted.state != fetchRunning {
			t.Errorf("after the cancelled download stopped: %+v; the next pass: %+v; want no failure and a restart", stopped, restarted)
		}
	})
}
