package main

// The fetcher, tested against an in-memory HTTP server serving a
// real, tiny release: two artifacts and a release.yaml whose digests
// are computed from their actual bytes, exactly the way the releases
// package computes them at publish time. Each test runs in a synctest
// bubble, so a download that stalls for a minute costs no real time.

import (
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/liken-sh/liken/liken/machine"
	"github.com/liken-sh/liken/liken/releases"
)

// A fake published release: contents by artifact name, plus the
// release.yaml derived from them and its catalog digest.
type fakeRelease struct {
	version   string
	artifacts map[string][]byte
	document  []byte
	digest    string
}

func makeRelease(version string) *fakeRelease {
	r := &fakeRelease{
		version: version,
		artifacts: map[string][]byte{
			"vmlinuz":    []byte("pretend kernel " + version),
			"liken.cpio": []byte("pretend initramfs " + version),
		},
	}
	doc := "apiVersion: liken.sh/v1alpha1\nkind: Release\nmetadata:\n  name: " + version + "\nartifacts:\n"
	for _, name := range []string{"vmlinuz", "liken.cpio"} {
		sum := sha256.Sum256(r.artifacts[name])
		doc += fmt.Sprintf("  - name: %s\n    sha256: %s\n    size: %d\n",
			name, hex.EncodeToString(sum[:]), len(r.artifacts[name]))
	}
	r.document = []byte(doc)
	sum := sha256.Sum256(r.document)
	r.digest = "sha256:" + hex.EncodeToString(sum[:])
	return r
}

// serveRelease publishes fake releases the way `make serve` does,
// counting requests so tests can assert what was actually fetched.
func serveRelease(t *testing.T, hits *atomic.Int64, published ...*fakeRelease) *apiservertest.Server {
	t.Helper()
	return apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits.Add(1)
		for _, r := range published {
			if req.URL.Path == "/releases/"+r.version+"/release.yaml" {
				w.Write(r.document)
				return
			}
			for name, contents := range r.artifacts {
				if req.URL.Path == "/releases/"+r.version+"/"+name {
					w.Write(contents)
					return
				}
			}
		}
		http.NotFound(w, req)
	}))
}

// activeSlot builds the slot this machine is running from, as far
// as the fetcher cares: the deployment layer and the sidecar that
// confirms it, which every fetch must carry to the inactive slot.
func activeSlot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	layer := []byte("the deployment layer")
	if err := os.WriteFile(filepath.Join(dir, machine.LayerName), layer, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(layer)
	sidecar := machine.FormatLayerSidecar(hex.EncodeToString(sum[:]))
	if err := os.WriteFile(filepath.Join(dir, machine.LayerSidecarName), sidecar, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func askFor(r *fakeRelease, slotDir, activeSlotDir string) fetchAsk {
	return fetchAsk{
		version:       r.version,
		digest:        r.digest,
		source:        apiservertest.Host + "/releases",
		slot:          "B",
		slotDir:       slotDir,
		activeSlotDir: activeSlotDir,
	}
}

// fetcherFor is a fetcher whose downloads reach the server.
func fetcherFor(server *apiservertest.Server) *fetcher {
	return &fetcher{client: server.Client()}
}

// awaitSettled waits until every goroutine in the bubble is blocked
// or done, and answers the fetcher's state.
func awaitSettled(f *fetcher) fetchSnapshot {
	synctest.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap
}

func TestFetchesAndVerifiesARelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := makeRelease("0.2.0")
		var hits atomic.Int64
		server := serveRelease(t, &hits, release)
		slot := t.TempDir()

		f := fetcherFor(server)
		snap := f.Ensure(askFor(release, slot, activeSlot(t)))
		if snap.state != fetchRunning {
			t.Fatalf("Ensure should start the download: %+v", snap)
		}
		snap = awaitSettled(f)
		if snap.state != fetchVerified {
			t.Fatalf("wanted Verified, got %s (%s)", snap.state, snap.detail)
		}

		for name, contents := range release.artifacts {
			got, err := os.ReadFile(filepath.Join(slot, name))
			if err != nil || string(got) != string(contents) {
				t.Errorf("%s on the slot: %q, %v", name, got, err)
			}
		}
		doc, err := os.ReadFile(filepath.Join(slot, "release.yaml"))
		if err != nil || string(doc) != string(release.document) {
			t.Errorf("the slot should carry the release document: %v", err)
		}
	})
}

func TestCarriesTheLayerToTheInactiveSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := makeRelease("0.2.0")
		server := serveRelease(t, new(atomic.Int64), release)
		slot := t.TempDir()
		active := activeSlot(t)

		f := fetcherFor(server)
		f.Ensure(askFor(release, slot, active))
		if snap := awaitSettled(f); snap.state != fetchVerified {
			t.Fatalf("wanted Verified, got %s (%s)", snap.state, snap.detail)
		}

		for _, name := range []string{machine.LayerName, machine.LayerSidecarName} {
			got, err := os.ReadFile(filepath.Join(slot, name))
			if err != nil {
				t.Fatalf("%s must be carried to the inactive slot: %v", name, err)
			}
			want, err := os.ReadFile(filepath.Join(active, name))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Errorf("%s on the inactive slot differs from the active slot's", name)
			}
		}
	})
}

func TestAMissingActiveLayerIsRejectedAndHeld(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := makeRelease("0.2.0")
		var hits atomic.Int64
		server := serveRelease(t, &hits, release)
		slot := t.TempDir()

		// The active slot has no layer at all: an old-format install, or
		// real damage. Either way, no retry can produce the layer, so
		// this holds the way corruption does.
		f := fetcherFor(server)
		ask := askFor(release, slot, t.TempDir())
		f.Ensure(ask)
		snap := awaitSettled(f)
		if snap.state != fetchRejected {
			t.Fatalf("wanted Rejected, got %s (%s)", snap.state, snap.detail)
		}
		if !strings.Contains(snap.detail, "layer") {
			t.Errorf("the hold must name the layer as the problem: %s", snap.detail)
		}
		if strings.Contains(snap.detail, "publish a corrected release") {
			t.Errorf("the remedy is local, not a republish: %s", snap.detail)
		}
		if _, err := os.Stat(filepath.Join(slot, "release.yaml")); !os.IsNotExist(err) {
			t.Error("a slot without its layer is not bootable and must not carry the release document")
		}

		before := hits.Load()
		if snap := f.Ensure(ask); snap.state != fetchRejected {
			t.Errorf("an unusable active layer holds: %+v", snap)
		}
		if hits.Load() != before {
			t.Error("a held ask must not touch the network again")
		}
	})
}

func TestATornActiveSidecarIsRejected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := makeRelease("0.2.0")
		server := serveRelease(t, new(atomic.Int64), release)
		active := activeSlot(t)
		// The crash-tear shape: the sidecar exists but is empty.
		if err := os.WriteFile(filepath.Join(active, machine.LayerSidecarName), nil, 0o644); err != nil {
			t.Fatal(err)
		}

		f := fetcherFor(server)
		f.Ensure(askFor(release, t.TempDir(), active))
		if snap := awaitSettled(f); snap.state != fetchRejected {
			t.Errorf("a torn sidecar makes the layer unverifiable: %+v", snap)
		}
	})
}

func TestAStaleInactiveLayerIsReplaced(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := makeRelease("0.2.0")
		server := serveRelease(t, new(atomic.Int64), release)
		slot := t.TempDir()
		active := activeSlot(t)

		// The inactive slot still carries an older install's layer,
		// with a sidecar that confirms those older bytes. The carry
		// must replace both with the running slot's.
		stale := []byte("a previous deployment layer")
		sum := sha256.Sum256(stale)
		if err := os.WriteFile(filepath.Join(slot, machine.LayerName), stale, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(slot, machine.LayerSidecarName),
			machine.FormatLayerSidecar(hex.EncodeToString(sum[:])), 0o644); err != nil {
			t.Fatal(err)
		}

		f := fetcherFor(server)
		f.Ensure(askFor(release, slot, active))
		if snap := awaitSettled(f); snap.state != fetchVerified {
			t.Fatalf("wanted Verified, got %s (%s)", snap.state, snap.detail)
		}
		got, err := os.ReadFile(filepath.Join(slot, machine.LayerName))
		if err != nil || string(got) != "the deployment layer" {
			t.Errorf("the stale layer must be replaced by the active slot's: %q, %v", got, err)
		}
	})
}

func TestALayerWithoutItsSidecarIsRecompleted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := makeRelease("0.2.0")
		server := serveRelease(t, new(atomic.Int64), release)
		slot := t.TempDir()
		active := activeSlot(t)

		// A previous carry died between the layer's rename and the
		// sidecar's write. The layer is already correct. The next pass
		// must finish the job, instead of rejecting it or blindly
		// copying it again.
		layer, err := os.ReadFile(filepath.Join(active, machine.LayerName))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(slot, machine.LayerName), layer, 0o644); err != nil {
			t.Fatal(err)
		}

		f := fetcherFor(server)
		f.Ensure(askFor(release, slot, active))
		if snap := awaitSettled(f); snap.state != fetchVerified {
			t.Fatalf("wanted Verified, got %s (%s)", snap.state, snap.detail)
		}
		sidecar, err := os.ReadFile(filepath.Join(slot, machine.LayerSidecarName))
		if err != nil {
			t.Fatal(err)
		}
		digest, err := machine.ParseLayerSidecar(sidecar)
		if err != nil {
			t.Fatal(err)
		}
		if err := machine.VerifyLayer(digest, strings.NewReader(string(layer))); err != nil {
			t.Errorf("the recompleted sidecar must vouch for the layer: %v", err)
		}
	})
}

func TestResumesByVerificationNotRefetching(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := makeRelease("0.2.0")
		var hits atomic.Int64
		server := serveRelease(t, &hits, release)
		slot := t.TempDir()

		// One artifact already landed, from a previous run interrupted
		// after vmlinuz. Only the other artifact should be fetched.
		if err := os.WriteFile(filepath.Join(slot, "vmlinuz"), release.artifacts["vmlinuz"], 0o644); err != nil {
			t.Fatal(err)
		}

		f := fetcherFor(server)
		f.Ensure(askFor(release, slot, activeSlot(t)))
		awaitSettled(f)

		// release.yaml + liken.cpio, and nothing else.
		if got := hits.Load(); got != 2 {
			t.Errorf("expected 2 requests (the document and the missing artifact), saw %d", got)
		}
	})
}

func TestVerifiedIsIdempotentAcrossPasses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := makeRelease("0.2.0")
		var hits atomic.Int64
		server := serveRelease(t, &hits, release)
		slot := t.TempDir()

		f := fetcherFor(server)
		ask := askFor(release, slot, activeSlot(t))
		f.Ensure(ask)
		awaitSettled(f)
		before := hits.Load()

		if snap := f.Ensure(ask); snap.state != fetchVerified {
			t.Errorf("a verified ask stays verified: %+v", snap)
		}
		if hits.Load() != before {
			t.Error("re-ensuring a verified ask must not touch the network")
		}
	})
}

func TestCorruptArtifactIsRejectedAndHeld(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := makeRelease("0.2.0")
		// The server's copy of liken.cpio is damaged after publish. The
		// document still promises the original digest (make corrupt).
		release.artifacts["liken.cpio"] = []byte("pretend initramfs 0.2.0 with a flipped bit")
		var hits atomic.Int64
		server := serveRelease(t, &hits, release)
		slot := t.TempDir()

		f := fetcherFor(server)
		ask := askFor(release, slot, activeSlot(t))
		f.Ensure(ask)
		snap := awaitSettled(f)
		if snap.state != fetchRejected {
			t.Fatalf("wanted Rejected, got %s (%s)", snap.state, snap.detail)
		}
		if _, err := os.Stat(filepath.Join(slot, "liken.cpio")); !os.IsNotExist(err) {
			t.Error("a corrupt artifact must never land under its final name")
		}
		if _, err := os.Stat(filepath.Join(slot, "release.yaml")); !os.IsNotExist(err) {
			t.Error("an incomplete slot must not carry the release document")
		}

		// The hold: the same ask never refetches.
		before := hits.Load()
		if snap := f.Ensure(ask); snap.state != fetchRejected {
			t.Errorf("a rejected ask holds: %+v", snap)
		}
		if hits.Load() != before {
			t.Error("a rejected ask must not touch the network again")
		}
	})
}

func TestCorruptDocumentIsRejected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := makeRelease("0.2.0")
		release.digest = "sha256:" + hex.EncodeToString(make([]byte, 32)) // the catalog promises different bytes
		server := serveRelease(t, new(atomic.Int64), release)

		f := fetcherFor(server)
		f.Ensure(askFor(release, t.TempDir(), activeSlot(t)))
		if snap := awaitSettled(f); snap.state != fetchRejected {
			t.Errorf("a document that fails the catalog digest is corrupt: %+v", snap)
		}
	})
}

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

		// The retry must carry the failure's reason. The restarted
		// state is the only one a reconcile pass ever reads, so the
		// reason the last attempt failed has to appear in it.
		broken.Store(false)
		if snap := f.Ensure(ask); snap.state != fetchRunning || !strings.Contains(snap.detail, "retrying after") {
			t.Errorf("a retry should say what it's retrying after: %+v", snap)
		}
		if snap := awaitSettled(f); snap.state != fetchVerified {
			t.Errorf("recovery: %+v", snap)
		}
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
// limit, and the next pass retries it.
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
