package main

// The release fetcher runs one background download at a time, and
// never blocks the reconcile loop.
//
// Reconcile passes never download anything themselves; they ask the
// fetcher. Ensure records what the machine currently wants (an ask:
// version, digest, source, destination slot), starts the download on
// its own goroutine if one isn't already running, and returns
// immediately with the current state. The pass that started a
// download and the pass that finds it verified are different passes,
// minutes apart, and every pass in between kept the heartbeat fresh.
// The lease must never wait on a socket.
//
// Downloads are resumable by re-verification rather than by byte
// ranges: each run first verifies whatever the slot already holds
// against the release document and fetches only what fails. A torn
// download (a power cut, a killed server) leaves either a .partial
// file, which no verification ever counts, or a final file that
// either verifies or doesn't; the next run converges either way. FAT
// has no journal, so every file lands the way the installer's copies
// do (temp file, fsync, rename) and is re-read and verified after
// writing, because bytes sitting in the page cache are not durable
// until they are synced and re-read.
//
// Failure comes in two kinds, and the distinction runs through
// everything here. A *transient* failure means the server is down or
// the network dropped; it is retried every pass, forever. A *corrupt*
// failure means the bytes don't match the digests the catalog
// promised; it is held, without retrying, until the ask itself
// changes, because refetching cannot change what the server
// publishes. Corruption is why the whole chain exists: the API named
// the document, the document named the artifacts, and a mismatch
// anywhere means someone's bytes are wrong. A corrupt release is
// abandoned, never patched: the recovery is to publish a corrected
// release under a new version and point the catalog at it.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/liken-sh/liken/machine"
)

// A fetchAsk is one reconcile decision's work order: fetch this
// version, vouched for by this digest, from this source, onto this
// slot. Asks compare by value, so a changed catalog digest is a
// different ask, and a different ask is what clears a corruption
// hold.
type fetchAsk struct {
	version string
	digest  string // the catalog's "sha256:<hex>" over release.yaml
	source  string // the base URL releases are served under
	slot    string // "A" or "B", for the humans reading conditions
	slotDir string // the slot's mounted filesystem
}

type fetchState string

const (
	fetchIdle     fetchState = "Idle"     // nothing started yet
	fetchRunning  fetchState = "Running"  // a goroutine is downloading
	fetchVerified fetchState = "Verified" // every artifact on the slot checks out
	fetchFailed   fetchState = "Failed"   // transient; the next pass retries
	fetchRejected fetchState = "Rejected" // corrupt; held until the ask changes
)

// A fetchSnapshot is what a reconcile pass sees: the ask the state
// describes, the state, and a human sentence for condition messages.
type fetchSnapshot struct {
	ask    fetchAsk
	state  fetchState
	detail string
}

type fetcher struct {
	mu   sync.Mutex
	snap fetchSnapshot
	busy bool
}

// errCorrupt distinguishes verification failures from transport
// failures. It is wrapped into any error that should stop the
// retries, and an error carrying it is what holds the fetcher at
// Rejected.
var errCorrupt = errors.New("the bytes do not match the release's digests")

// Ensure records the ask, starts a download when one is needed and
// none is running, and returns the current state. It never blocks:
// the heaviest thing here is starting a goroutine.
func (f *fetcher) Ensure(ask fetchAsk) fetchSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.snap.ask != ask {
		// A different release, digest, or destination: everything
		// known so far was about the old ask, including a Rejected
		// hold. A changed catalog is precisely the event that should
		// clear the hold, so the state resets with the ask.
		f.snap = fetchSnapshot{ask: ask, state: fetchIdle, detail: "waiting to start"}
	}
	if f.busy || f.snap.state == fetchVerified || f.snap.state == fetchRejected {
		return f.snap
	}

	// A restart after a transient failure keeps the failure's reason.
	// A Failed state only exists between passes, and the pass that
	// reads it is the same pass that restarts the download, so the
	// restarted Running state is the only one any condition will ever
	// see. If that state didn't carry the reason the last attempt
	// failed, the reason would never be observable at all.
	detail := "starting"
	if f.snap.state == fetchFailed {
		detail = "retrying after: " + f.snap.detail
	}
	f.busy = true
	f.snap.state = fetchRunning
	f.snap.detail = detail
	go f.run(ask)
	return f.snap
}

// run is the goroutine: do the fetch, then record the verdict. If
// the ask changed while the fetch ran, the verdict describes a
// release the machine no longer wants, so it is discarded.
func (f *fetcher) run(ask fetchAsk) {
	fetched, err := fetchRelease(ask)

	f.mu.Lock()
	defer f.mu.Unlock()
	f.busy = false
	if f.snap.ask != ask {
		return
	}
	switch {
	case err == nil:
		f.snap.state = fetchVerified
		f.snap.detail = fmt.Sprintf("%d artifacts fetched, the rest already verified in place", fetched)
	case errors.Is(err, errCorrupt):
		f.snap.state = fetchRejected
		f.snap.detail = fmt.Sprintf("release %s at %s is corrupt (%v); publish a corrected release under a new version", ask.version, ask.source, err)
	default:
		f.snap.state = fetchFailed
		f.snap.detail = err.Error()
	}
}

// fetchRelease does one complete pass: fetch and vet the release
// document, verify or fetch each artifact, and leave the document
// itself on the slot last, so a slot carrying release.yaml is a slot
// whose artifacts were complete when it was written. Returns how many
// artifacts were actually downloaded (zero is the idempotent case:
// everything already verified in place).
func fetchRelease(ask fetchAsk) (int, error) {
	base := strings.TrimSuffix(ask.source, "/") + "/" + ask.version

	raw, err := fetchBytes(base + "/release.yaml")
	if err != nil {
		return 0, fmt.Errorf("fetching the release document: %w", err)
	}

	// The trust chain's first link: the document's bytes must hash to
	// exactly what the catalog promised. Until that holds, nothing
	// the document says can be trusted.
	sum := sha256.Sum256(raw)
	if digest := "sha256:" + hex.EncodeToString(sum[:]); digest != ask.digest {
		return 0, fmt.Errorf("the release document's digest %s does not match the catalog's %s: %w", digest, ask.digest, errCorrupt)
	}
	release, err := machine.ParseRelease(raw)
	if err != nil {
		return 0, fmt.Errorf("the release document does not parse: %v: %w", err, errCorrupt)
	}
	if release.Metadata.Name != ask.version {
		return 0, fmt.Errorf("the release document names version %s, not %s: %w", release.Metadata.Name, ask.version, errCorrupt)
	}

	fetched := 0
	for _, artifact := range release.Artifacts {
		dest := filepath.Join(ask.slotDir, artifact.Name)
		if verifySlotFile(artifact, dest) == nil {
			continue // already here from an earlier, interrupted run
		}
		if err := fetchArtifact(base, artifact, dest); err != nil {
			return fetched, err
		}
		fetched++
	}

	// The document lands after the artifacts it describes, durably,
	// which makes the slot self-describing: it records which release
	// it holds, byte for byte, without asking the network.
	if err := writeDurably(filepath.Join(ask.slotDir, "release.yaml"), raw); err != nil {
		return fetched, fmt.Errorf("writing the release document to the slot: %w", err)
	}
	return fetched, nil
}

// fetchArtifact streams one artifact onto the slot: temp file, fsync,
// verify the durable bytes by re-reading them, then rename into
// place. The verify-before-rename order means a final-looking name
// never points at unverified bytes.
func fetchArtifact(base string, artifact machine.ReleaseArtifact, dest string) error {
	resp, err := http.Get(base + "/" + artifact.Name)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", artifact.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetching %s: the server answered %s", artifact.Name, resp.Status)
	}

	// The size cap protects the slot: an artifact that runs past its
	// declared size is already wrong, and there is no reason to fill
	// a 512Mi filesystem with the rest of it before finding out.
	tmp, err := spillDurably(dest, io.LimitReader(resp.Body, artifact.Size+1))
	if err != nil {
		return fmt.Errorf("writing %s: %w", artifact.Name, err)
	}

	if err := verifySlotFile(artifact, tmp); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("%s from the server does not verify: %v: %w", artifact.Name, err, errCorrupt)
	}
	return os.Rename(tmp, dest)
}

// verifySlotFile checks one file on the slot against its artifact's
// digest and size, returning an error for any reason the file fails,
// including that it doesn't exist, which is the common case on a
// first run.
func verifySlotFile(artifact machine.ReleaseArtifact, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return artifact.Verify(f)
}

// fetchBytes GETs a small document whole. The 1MiB limit is far
// larger than any reasonable release.yaml and small enough to read
// into memory without concern.
func fetchBytes(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the server answered %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// spillDurably lands a stream next to its destination with the same
// discipline the installer applies to file copies: write to a
// .partial temp file, fsync, close. On any failure the temp file is
// removed and never seen again. On success the temp path comes back
// for the caller to finish: verify first and then rename
// (fetchArtifact), or rename immediately (writeDurably).
func spillDurably(dest string, r io.Reader) (string, error) {
	tmp := dest + ".partial"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, r)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// writeDurably writes bytes already in memory with the same
// discipline: temp file, fsync, rename.
func writeDurably(dest string, contents []byte) error {
	tmp, err := spillDurably(dest, bytes.NewReader(contents))
	if err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}
