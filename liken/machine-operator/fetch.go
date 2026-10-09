package main

// The release fetcher runs one background download at a time and
// never blocks the reconcile loop.
//
// One download at a time is a rule about the slot, not about the
// network. The download writes the inactive slot, so a second writer
// for another release would interleave its files with the first one's.
// When the ask changes while a download runs, Ensure cancels that
// download and starts the new one only after the old goroutine
// returns. The cancelled download removes its partial file on the
// way out, and the next run verifies whatever already landed.
//
// Reconcile passes never download anything themselves. They ask the
// fetcher instead. Ensure records what the machine currently needs
// (an ask: version, digest, source, and destination slot), starts
// the download on its own goroutine if one is not already running,
// and returns immediately with the current state. The pass that
// starts a download and the pass that finds it verified are
// different passes, minutes apart, and every pass in between keeps
// the heartbeat fresh. The lease must never wait on a socket. The
// download wakes the loop when it ends (fetcher.wake), so the pass
// that reads the result runs at once.
//
// Failure comes in two kinds, and the distinction matters
// throughout this file. A transient failure means the server is
// down or the network dropped. The fetcher retries a transient
// failure forever, after a backoff that starts at ten seconds and
// doubles up to two minutes (fetchRetryLimit). A release server that
// fails usually stays down for minutes, and every pass would
// otherwise download again the moment it saw the failure. A corrupt
// failure means the bytes do not match the digests the catalog
// promised. The fetcher holds a corrupt failure, without retrying,
// until the ask itself changes, because refetching cannot change what
// the server publishes.
// Corruption is the reason the chain of checks in download.go
// exists: the API names the document, the document names the
// artifacts, and a mismatch anywhere means someone's bytes are wrong. The fetcher
// abandons a corrupt release rather than patching it. The recovery
// is to publish a corrected release under a new version and point
// the catalog at it.

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"
)

// A fetchAsk is one reconcile decision's request: fetch this
// version, confirmed by this digest, from this source, onto this
// slot. Asks compare by value, so a changed catalog digest produces
// a different ask, and a different ask is what clears a corruption
// hold.
type fetchAsk struct {
	version       string
	digest        string // the catalog's "sha256:<hex>" over release.yaml
	source        string // the base URL releases are served under
	slot          string // "A" or "B", for the humans reading conditions
	slotDir       string // the slot's mounted filesystem
	activeSlotDir string // the running slot's, which lends its layer
}

type fetchState string

const (
	fetchIdle     fetchState = "Idle"     // nothing started yet
	fetchRunning  fetchState = "Running"  // a goroutine is downloading
	fetchVerified fetchState = "Verified" // every artifact on the slot checks out
	fetchFailed   fetchState = "Failed"   // transient; retried at retryAt
	fetchRejected fetchState = "Rejected" // corrupt; held until the ask changes
)

// A fetchSnapshot is what a reconcile pass sees: the ask the state
// describes, the state, a human sentence for condition messages, and,
// for a Failed state, when the retry starts.
type fetchSnapshot struct {
	ask     fetchAsk
	state   fetchState
	detail  string
	retryAt time.Time
}

const (
	fetchFirstRetry = 10 * time.Second
	fetchRetryLimit = 2 * time.Minute
)

type fetcher struct {
	mu   sync.Mutex
	snap fetchSnapshot
	busy bool

	// cancel ends the running download. It is nil while no download
	// runs.
	cancel context.CancelCauseFunc

	// client sends the downloads. A nil client is
	// http.DefaultClient; a test gives one that reaches its server.
	client *http.Client

	// retryDelay is the backoff after the ask's last transient
	// failure, and zero before the first. A new ask resets it, and so
	// does a verified download.
	retryDelay time.Duration

	// wake wakes the reconcile loop when a download ends, whatever
	// the end: verified, failed, rejected, or stopped because the ask
	// changed. The pass it starts reads the result, or starts the
	// download the new ask needs. A failed download is safe to wake on,
	// because its backoff (retryAt) gives the next attempt its time; a
	// download that fails in milliseconds, such as one that meets a
	// 404, would otherwise start again at once. Nil wakes nothing.
	wake func()

	// jitter answers a number in [0, 1), for the tenth of the delay
	// that each retry adds at random, so a fleet that failed together
	// does not retry together. Nil means math/rand.
	jitter func() float64

	// The fetcher's running totals, kept beside the snapshot because
	// they outlive every ask. The snapshot describes one release; a
	// total describes what this machine has spent on downloads since
	// the operator started, which is what a graph of staging progress
	// and of a download that keeps failing is drawn from
	// (metrics.go).
	downloaded int64
	failures   int
}

// DownloadedBytes is how many release artifact bytes this machine
// has downloaded and verified onto a slot. Bytes count when the
// artifact lands, so a torn download adds nothing until its retry
// completes the file.
func (f *fetcher) DownloadedBytes() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.downloaded
}

// DownloadFailures is how many downloads ended without a complete
// slot, whether the network dropped or the bytes did not match the
// catalog's digests.
func (f *fetcher) DownloadFailures() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.failures
}

// errCorrupt distinguishes verification failures from transport
// failures. The code wraps it into any error that should stop the
// retries, and an error carrying it holds the fetcher at Rejected.
var errCorrupt = errors.New("the bytes do not match the release's digests")

// errLayer marks a failure in the machine's own deployment layer.
// It holds the fetcher the same way corruption does, because no
// retry can repair the slot the machine is running on. But the
// remedy is local: repair or reinstall this machine. So the
// condition must not send a person off to republish a release that
// was never the problem.
var errLayer = errors.New("this machine's deployment layer is unusable")

// errSuperseded is the cause of a download that Ensure cancelled
// because the ask changed. It is not a failure of the download.
var errSuperseded = errors.New("the machine no longer asks for this release")

// Ensure records the ask, starts a download when one is needed and
// none is running, and returns the current state. It never blocks:
// the heaviest thing it does is start a goroutine.
func (f *fetcher) Ensure(ask fetchAsk) fetchSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.snap.ask != ask {
		// This is a different release, digest, or destination.
		// Everything known so far, including a Rejected hold, was
		// about the old ask. A changed catalog is exactly the event
		// that should clear the hold, so the state resets with the
		// ask.
		f.snap = fetchSnapshot{ask: ask, state: fetchIdle, detail: "waiting to start"}
		f.retryDelay = 0
		if f.busy {
			// The running download is for the old ask, and it
			// writes the same slot this one will. It stops at
			// once, and this ask waits until its goroutine
			// returns, so that two writers never overlap.
			f.cancel(errSuperseded)
			f.snap.detail = "waiting for the previous download to stop"
		}
	}
	if f.busy || f.snap.state == fetchVerified || f.snap.state == fetchRejected {
		return f.snap
	}
	if f.snap.state == fetchFailed && time.Now().Before(f.snap.retryAt) {
		return f.snap
	}

	// A restart after a transient failure keeps the failure's reason,
	// so a condition written while the retry runs still says what the
	// last attempt met.
	detail := "starting"
	if f.snap.state == fetchFailed {
		detail = "retrying after: " + f.snap.detail
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	f.busy, f.cancel = true, cancel
	f.snap.state = fetchRunning
	f.snap.detail = detail
	go f.run(ctx, ask)
	return f.snap
}

// run is the goroutine. It runs the fetch, then records the
// verdict. If the ask changed while the fetch ran, the verdict
// describes a release the machine no longer needs, so the function
// discards it.
func (f *fetcher) run(ctx context.Context, ask fetchAsk) {
	client := f.client
	if client == nil {
		client = http.DefaultClient
	}
	fetched, downloaded, err := fetchRelease(ctx, client, ask)

	// The wake runs after the unlock below, because deferred calls run
	// in reverse, so the pass it starts finds the fetcher idle.
	if f.wake != nil {
		defer f.wake()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancel(nil)
	f.busy, f.cancel = false, nil
	// The totals count what this run did, even when the ask has
	// moved on. The bytes reached the slot and the failure happened,
	// whatever the machine wants now. A download that Ensure
	// cancelled did not fail.
	f.downloaded += downloaded
	if err != nil && !errors.Is(context.Cause(ctx), errSuperseded) {
		f.failures++
	}
	// A download that Ensure cancelled describes no verdict, even when
	// the ask has changed back to it since: Ensure reset the state to
	// Idle for the new ask, and the next pass starts the download.
	if f.snap.ask != ask || errors.Is(context.Cause(ctx), errSuperseded) {
		return
	}
	switch {
	case err == nil:
		f.snap.state = fetchVerified
		f.snap.detail = fmt.Sprintf("%d artifacts fetched, the rest already verified in place", fetched)
		f.retryDelay = 0
	case errors.Is(err, errLayer):
		f.snap.state = fetchRejected
		f.snap.detail = err.Error()
	case errors.Is(err, errCorrupt):
		f.snap.state = fetchRejected
		f.snap.detail = fmt.Sprintf("release %s at %s is corrupt (%v); publish a corrected release under a new version", ask.version, ask.source, err)
	default:
		f.snap.state = fetchFailed
		f.snap.detail = err.Error()
		f.retryDelay = grow(f.retryDelay, true, fetchFirstRetry, fetchRetryLimit)
		f.snap.retryAt = time.Now().Add(f.retryDelay + time.Duration(float64(f.retryDelay)*f.random()/10))
	}
}

func (f *fetcher) random() float64 {
	if f.jitter == nil {
		return rand.Float64()
	}
	return f.jitter()
}
