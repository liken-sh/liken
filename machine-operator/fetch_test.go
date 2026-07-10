package main

// The fetcher, exercised against a real HTTP server (httptest)
// serving a real, tiny release: two artifacts and a release.yaml
// whose digests are computed from their actual bytes, exactly the way
// the releases package computes them at publish time.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liken-sh/liken/machine"
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

// serveRelease publishes the fake release the way `make serve` does,
// counting requests per path so tests can assert what was actually
// fetched.
func serveRelease(t *testing.T, r *fakeRelease, hits *atomic.Int64) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits.Add(1)
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
		http.NotFound(w, req)
	}))
	t.Cleanup(server.Close)
	return server
}

// activeSlot builds the slot this machine is running from, as far as
// the fetcher cares: the deployment layer and the sidecar vouching
// for it, which every fetch must carry to the inactive slot.
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

func askFor(r *fakeRelease, serverURL, slotDir, activeSlotDir string) fetchAsk {
	return fetchAsk{
		version:       r.version,
		digest:        r.digest,
		source:        serverURL + "/releases",
		slot:          "B",
		slotDir:       slotDir,
		activeSlotDir: activeSlotDir,
	}
}

// awaitSettled polls until the fetcher leaves Running/Idle, the async
// test's stand-in for "the goroutine finished".
func awaitSettled(t *testing.T, f *fetcher) fetchSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		snap := f.snap
		f.mu.Unlock()
		if snap.state != fetchRunning && snap.state != fetchIdle {
			return snap
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the fetch never settled")
	return fetchSnapshot{}
}

func TestFetchesAndVerifiesARelease(t *testing.T) {
	release := makeRelease("0.2.0")
	var hits atomic.Int64
	server := serveRelease(t, release, &hits)
	slot := t.TempDir()

	f := &fetcher{}
	snap := f.Ensure(askFor(release, server.URL, slot, activeSlot(t)))
	if snap.state != fetchRunning {
		t.Fatalf("Ensure should start the download: %+v", snap)
	}
	snap = awaitSettled(t, f)
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
}

func TestCarriesTheLayerToTheInactiveSlot(t *testing.T) {
	release := makeRelease("0.2.0")
	server := serveRelease(t, release, new(atomic.Int64))
	slot := t.TempDir()
	active := activeSlot(t)

	f := &fetcher{}
	f.Ensure(askFor(release, server.URL, slot, active))
	if snap := awaitSettled(t, f); snap.state != fetchVerified {
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
}

func TestAMissingActiveLayerIsRejectedAndHeld(t *testing.T) {
	release := makeRelease("0.2.0")
	var hits atomic.Int64
	server := serveRelease(t, release, &hits)
	slot := t.TempDir()

	// The active slot has no layer at all: an old-format install, or
	// real damage. Either way no retry can conjure the layer, so this
	// holds like corruption does.
	f := &fetcher{}
	ask := askFor(release, server.URL, slot, t.TempDir())
	f.Ensure(ask)
	snap := awaitSettled(t, f)
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
}

func TestATornActiveSidecarIsRejected(t *testing.T) {
	release := makeRelease("0.2.0")
	server := serveRelease(t, release, new(atomic.Int64))
	active := activeSlot(t)
	// The crash-tear shape: the sidecar exists but is empty.
	if err := os.WriteFile(filepath.Join(active, machine.LayerSidecarName), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	f := &fetcher{}
	f.Ensure(askFor(release, server.URL, t.TempDir(), active))
	if snap := awaitSettled(t, f); snap.state != fetchRejected {
		t.Errorf("a torn sidecar makes the layer unverifiable: %+v", snap)
	}
}

func TestAStaleInactiveLayerIsReplaced(t *testing.T) {
	release := makeRelease("0.2.0")
	server := serveRelease(t, release, new(atomic.Int64))
	slot := t.TempDir()
	active := activeSlot(t)

	// The inactive slot still carries an older install's layer, with
	// a sidecar that vouches for those older bytes. The carry must
	// replace both with the running slot's.
	stale := []byte("a previous deployment layer")
	sum := sha256.Sum256(stale)
	if err := os.WriteFile(filepath.Join(slot, machine.LayerName), stale, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slot, machine.LayerSidecarName),
		machine.FormatLayerSidecar(hex.EncodeToString(sum[:])), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &fetcher{}
	f.Ensure(askFor(release, server.URL, slot, active))
	if snap := awaitSettled(t, f); snap.state != fetchVerified {
		t.Fatalf("wanted Verified, got %s (%s)", snap.state, snap.detail)
	}
	got, err := os.ReadFile(filepath.Join(slot, machine.LayerName))
	if err != nil || string(got) != "the deployment layer" {
		t.Errorf("the stale layer must be replaced by the active slot's: %q, %v", got, err)
	}
}

func TestALayerWithoutItsSidecarIsRecompleted(t *testing.T) {
	release := makeRelease("0.2.0")
	server := serveRelease(t, release, new(atomic.Int64))
	slot := t.TempDir()
	active := activeSlot(t)

	// A previous carry died between the layer's rename and the
	// sidecar's write. The layer is already right; the next pass must
	// finish the job rather than reject or recopy blindly.
	layer, err := os.ReadFile(filepath.Join(active, machine.LayerName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slot, machine.LayerName), layer, 0o644); err != nil {
		t.Fatal(err)
	}

	f := &fetcher{}
	f.Ensure(askFor(release, server.URL, slot, active))
	if snap := awaitSettled(t, f); snap.state != fetchVerified {
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
}

func TestResumesByVerificationNotRefetching(t *testing.T) {
	release := makeRelease("0.2.0")
	var hits atomic.Int64
	server := serveRelease(t, release, &hits)
	slot := t.TempDir()

	// One artifact already landed (a previous run, interrupted after
	// vmlinuz): only the other should be fetched.
	if err := os.WriteFile(filepath.Join(slot, "vmlinuz"), release.artifacts["vmlinuz"], 0o644); err != nil {
		t.Fatal(err)
	}

	f := &fetcher{}
	f.Ensure(askFor(release, server.URL, slot, activeSlot(t)))
	awaitSettled(t, f)

	// release.yaml + liken.cpio, and nothing else.
	if got := hits.Load(); got != 2 {
		t.Errorf("expected 2 requests (the document and the missing artifact), saw %d", got)
	}
}

func TestVerifiedIsIdempotentAcrossPasses(t *testing.T) {
	release := makeRelease("0.2.0")
	var hits atomic.Int64
	server := serveRelease(t, release, &hits)
	slot := t.TempDir()

	f := &fetcher{}
	ask := askFor(release, server.URL, slot, activeSlot(t))
	f.Ensure(ask)
	awaitSettled(t, f)
	before := hits.Load()

	if snap := f.Ensure(ask); snap.state != fetchVerified {
		t.Errorf("a verified ask stays verified: %+v", snap)
	}
	if hits.Load() != before {
		t.Error("re-ensuring a verified ask must not touch the network")
	}
}

func TestCorruptArtifactIsRejectedAndHeld(t *testing.T) {
	release := makeRelease("0.2.0")
	// The server's copy of liken.cpio is damaged after publish: the
	// document still promises the original digest (make corrupt).
	release.artifacts["liken.cpio"] = []byte("pretend initramfs 0.2.0 with a flipped bit")
	var hits atomic.Int64
	server := serveRelease(t, release, &hits)
	slot := t.TempDir()

	f := &fetcher{}
	ask := askFor(release, server.URL, slot, activeSlot(t))
	f.Ensure(ask)
	snap := awaitSettled(t, f)
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
}

func TestCorruptDocumentIsRejected(t *testing.T) {
	release := makeRelease("0.2.0")
	release.digest = "sha256:" + hex.EncodeToString(make([]byte, 32)) // the catalog promises different bytes
	server := serveRelease(t, release, new(atomic.Int64))

	f := &fetcher{}
	f.Ensure(askFor(release, server.URL, t.TempDir(), activeSlot(t)))
	if snap := awaitSettled(t, f); snap.state != fetchRejected {
		t.Errorf("a document that fails the catalog digest is corrupt: %+v", snap)
	}
}

func TestAChangedAskClearsTheHold(t *testing.T) {
	bad := makeRelease("0.2.0")
	bad.artifacts["liken.cpio"] = []byte("corrupted")
	server := serveRelease(t, bad, new(atomic.Int64))
	slot := t.TempDir()

	f := &fetcher{}
	f.Ensure(askFor(bad, server.URL, slot, activeSlot(t)))
	awaitSettled(t, f)

	// The corrected release is published under a new version, which
	// is the recovery the design prescribes, and the new ask fetches.
	good := makeRelease("0.2.1")
	goodServer := serveRelease(t, good, new(atomic.Int64))
	f.Ensure(askFor(good, goodServer.URL, slot, activeSlot(t)))
	if snap := awaitSettled(t, f); snap.state != fetchVerified {
		t.Errorf("a new ask starts fresh: %+v", snap)
	}
}

func TestServerFailuresAreTransientAndRetried(t *testing.T) {
	release := makeRelease("0.2.0")
	var broken atomic.Bool
	broken.Store(true)
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits.Add(1)
		if broken.Load() {
			http.Error(w, "the server is having a day", http.StatusInternalServerError)
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
	t.Cleanup(server.Close)
	slot := t.TempDir()

	f := &fetcher{}
	ask := askFor(release, server.URL, slot, activeSlot(t))
	f.Ensure(ask)
	if snap := awaitSettled(t, f); snap.state != fetchFailed {
		t.Fatalf("a down server is a transient failure: %+v", snap)
	}

	// The retry must carry the failure's reason: the restarted state
	// is the only one a reconcile pass ever reads, so the reason the
	// last attempt failed has to appear in it.
	broken.Store(false)
	if snap := f.Ensure(ask); snap.state != fetchRunning || !strings.Contains(snap.detail, "retrying after") {
		t.Errorf("a retry should say what it's retrying after: %+v", snap)
	}
	if snap := awaitSettled(t, f); snap.state != fetchVerified {
		t.Errorf("recovery: %+v", snap)
	}
}

func TestEnsureNeverBlocksOnTheDownload(t *testing.T) {
	release := makeRelease("0.2.0")
	gate := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		<-gate // the download hangs until the test releases it
		w.Write(release.document)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(gate) })

	f := &fetcher{}
	ask := askFor(release, server.URL, t.TempDir(), activeSlot(t))
	done := make(chan fetchSnapshot, 1)
	go func() { done <- f.Ensure(ask) }()

	// This is the heartbeat's guarantee in miniature: Ensure must
	// return while the server still hasn't answered a byte.
	select {
	case snap := <-done:
		if snap.state != fetchRunning {
			t.Errorf("expected the download to be running: %+v", snap)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ensure blocked on the download; the heartbeat would starve")
	}
}
