package main

// The fetcher downloads a release onto the inactive slot on its own
// goroutine. A download can take minutes, and a reconcile pass that
// runs longer than stuckAfter stops the renewal of the machine's
// heartbeat lease (liveness.go).
//
// Only one download runs at a time, because every download writes the
// same inactive slot. When the requested release changes during a
// download, Ensure cancels it, and the first Ensure after the old
// goroutine returns starts the new download.
//
// A transient failure, such as a server error or a dropped connection,
// is retried forever, after a backoff that starts at fetchFirstRetry
// and doubles up to fetchRetryLimit. A retry cannot fix bytes that do
// not match the catalog's digests (errCorrupt), or a running slot whose
// deployment layer does not verify (errLayer). Those failures put the
// fetcher in the Rejected state until the request changes, for example
// when the catalog points at a corrected release.

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"
)

// A releaseFetch is the download that a pass requests: the release, the
// server, and the slot to write. Ensure compares fetches field by
// field, so a changed catalog digest is a new fetch, and a new fetch
// clears a Rejected state.
type releaseFetch struct {
	version       string
	digest        string // the catalog's "sha256:<hex>" over release.yaml
	source        string // the base URL releases are served under
	slot          string // "A" or "B", as condition messages write it
	slotDir       string // where the destination slot is mounted
	activeSlotDir string // where the running slot is mounted. The download copies its deployment layer.
}

type fetchState string

const (
	fetchIdle     fetchState = "Idle"     // no download started for this request
	fetchRunning  fetchState = "Running"  // a goroutine is downloading
	fetchVerified fetchState = "Verified" // every artifact on the slot matches its digest
	fetchFailed   fetchState = "Failed"   // transient failure, retried at retryAt
	fetchRejected fetchState = "Rejected" // a retry cannot fix it, so it stays until the request changes
)

// A fetchSnapshot is the fetcher's state for one request. detail is a
// sentence for condition messages. retrying is true from the request's
// first failure until a download of it verifies, including while a
// retry runs.
type fetchSnapshot struct {
	fetch    releaseFetch
	state    fetchState
	detail   string
	retryAt  time.Time
	retrying bool
}

const (
	fetchFirstRetry = 10 * time.Second
	fetchRetryLimit = 2 * time.Minute
)

// A fetcher downloads one release at a time. The zero value works.
type fetcher struct {
	mu   sync.Mutex
	snap fetchSnapshot
	busy bool

	// cancel ends the running download, and is nil when none runs.
	cancel context.CancelCauseFunc

	// client sends the downloads. When nil, the fetcher uses
	// http.DefaultClient.
	client *http.Client

	// retryDelay is the backoff after the last transient failure, and
	// zero before the first. A new request and a verified download
	// reset it.
	retryDelay time.Duration

	// wake runs when a download ends for any reason, including a
	// cancel by a new request. It starts a reconcile pass that reads
	// the result or starts the new download. A wake after a failure
	// does not retry at once, because Ensure waits for retryAt. When
	// nil, nothing wakes.
	wake func()

	// jitter returns a number in [0, 1). Each retry waits longer than
	// its delay by up to a tenth, scaled by jitter, so machines that
	// failed together do not retry together. When nil, the fetcher
	// uses math/rand.
	jitter func() float64

	// The totals for the metrics (metrics.go) count every request since
	// the operator started. The snapshot covers only the current one.
	downloaded int64
	failures   int
}

// DownloadedBytes returns the bytes of release artifacts downloaded
// and verified onto a slot. An artifact's bytes count when the whole
// file is in place, so a download that breaks partway adds nothing
// until a retry completes the file.
func (f *fetcher) DownloadedBytes() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.downloaded
}

// DownloadFailures returns how many downloads ended without a
// complete slot, whether the network failed or the bytes did not match
// the catalog's digests. A download that Ensure cancelled is not
// counted.
func (f *fetcher) DownloadFailures() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.failures
}

// errCorrupt marks downloaded bytes that do not match the release's
// digests. An error that wraps it puts the fetcher in the Rejected
// state.
var errCorrupt = errors.New("the bytes do not match the release's digests")

// errLayer marks a running slot whose deployment layer does not
// verify. It also puts the fetcher in the Rejected state, because a
// retry cannot repair the slot the machine runs from. Its detail tells
// a person to repair or reinstall the machine, not to republish the
// release, which is not at fault.
var errLayer = errors.New("this machine's deployment layer is unusable")

// errSuperseded is the cancellation cause of a download that Ensure
// stopped for a new request. It does not count as a failure.
var errSuperseded = errors.New("the requested release changed during the download")

// Ensure records the request, starts a download when one is needed
// and none is running, and returns the current state without waiting
// for the download.
func (f *fetcher) Ensure(fetch releaseFetch) fetchSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.snap.fetch != fetch {
		// A new request resets the state, including Rejected, because
		// a changed catalog is how a corrected release reaches the
		// machine.
		f.snap = fetchSnapshot{fetch: fetch, state: fetchIdle, detail: "waiting to start"}
		f.retryDelay = 0
		if f.busy {
			// The running download writes the same slot. The new
			// request waits until its goroutine returns, so two
			// downloads never write the slot at once.
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

	// A retry keeps the reason for the last failure, so a condition
	// written while it runs still says what went wrong.
	detail := "starting"
	if f.snap.state == fetchFailed {
		detail = "retrying after: " + f.snap.detail
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	f.busy, f.cancel = true, cancel
	f.snap.state = fetchRunning
	f.snap.detail = detail
	go f.run(ctx, fetch)
	return f.snap
}

func (f *fetcher) run(ctx context.Context, fetch releaseFetch) {
	client := f.client
	if client == nil {
		client = http.DefaultClient
	}
	fetched, downloaded, err := fetchRelease(ctx, client, fetch)

	// wake is deferred before the unlock, so it runs after it, and the
	// pass it starts finds the fetcher not busy.
	if f.wake != nil {
		defer f.wake()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancel(nil)
	f.busy, f.cancel = false, nil
	// The bytes count even when the request changed, because they
	// reached the slot.
	f.downloaded += downloaded
	if err != nil && !errors.Is(context.Cause(ctx), errSuperseded) {
		f.failures++
	}
	// A cancelled download has no result, even when the request has
	// since changed back to the same value. Ensure already reset the state to
	// Idle, and the next pass starts the download again.
	if f.snap.fetch != fetch || errors.Is(context.Cause(ctx), errSuperseded) {
		return
	}
	switch {
	case err == nil:
		f.snap.state = fetchVerified
		f.snap.retrying = false
		f.snap.detail = fmt.Sprintf("%d artifacts fetched, the rest already verified in place", fetched)
		f.retryDelay = 0
	case errors.Is(err, errLayer):
		f.snap.state = fetchRejected
		f.snap.detail = fmt.Sprintf("Release %s cannot be downloaded to slot %s: %v", fetch.version, fetch.slot, err)
	case errors.Is(err, errCorrupt):
		f.snap.state = fetchRejected
		f.snap.detail = fmt.Sprintf("Release %s at %s is corrupt: %v. Publish a corrected release under a new version", fetch.version, fetch.source, err)
	default:
		f.snap.state = fetchFailed
		f.snap.retrying = true
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
