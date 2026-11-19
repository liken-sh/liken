package main

// Tests of the download against an in-memory HTTP server that serves a
// small release whose release.yaml has the real digests of its
// artifacts. fetch_test.go serves the same releases.

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

	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/liken/machine"
)

// fakeRelease is a release as the server publishes it. digest is the
// document's digest, the value a catalog entry would list.
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

// serveRelease serves each fake release under /releases/<version>/,
// and counts the requests.
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

// activeSlot builds the slot the machine runs from, with only the files
// that the fetcher reads: the deployment layer and its sidecar.
func activeSlot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	layer := []byte("the deployment layer")
	if err := os.WriteFile(filepath.Join(dir, machine.LayerName), layer, 0o644); err != nil {
		t.Fatalf("writing the layer: %v", err)
	}
	sum := sha256.Sum256(layer)
	sidecar := machine.FormatLayerSidecar(hex.EncodeToString(sum[:]))
	if err := os.WriteFile(filepath.Join(dir, machine.LayerSidecarName), sidecar, 0o644); err != nil {
		t.Fatalf("writing the layer sidecar: %v", err)
	}
	return dir
}

func fetchFor(r *fakeRelease, slotDir, activeSlotDir string) releaseFetch {
	return releaseFetch{
		version:       r.version,
		digest:        r.digest,
		source:        apiservertest.Host + "/releases",
		slot:          "B",
		slotDir:       slotDir,
		activeSlotDir: activeSlotDir,
	}
}

func fetcherFor(server *apiservertest.Server) *fetcher {
	return &fetcher{client: server.Client()}
}

// awaitSettled waits until every goroutine in the bubble is blocked or
// done, and returns the fetcher's state.
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
		snap := f.Ensure(fetchFor(release, slot, activeSlot(t)))
		if snap.state != fetchRunning {
			t.Fatalf("Ensure returned %+v, want a running download", snap)
		}
		snap = awaitSettled(f)
		if snap.state != fetchVerified {
			t.Fatalf("the fetch state is %s (%s), want Verified", snap.state, snap.detail)
		}

		for name, contents := range release.artifacts {
			got, err := os.ReadFile(filepath.Join(slot, name))
			if err != nil || string(got) != string(contents) {
				t.Errorf("%s in the slot is %q (error %v), want the release artifact", name, got, err)
			}
		}
		doc, err := os.ReadFile(filepath.Join(slot, "release.yaml"))
		if err != nil || string(doc) != string(release.document) {
			t.Errorf("the slot has no matching release.yaml (error %v), want the release document", err)
		}
	})
}

func TestCopiesTheLayerToTheInactiveSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := makeRelease("0.2.0")
		server := serveRelease(t, new(atomic.Int64), release)
		slot := t.TempDir()
		active := activeSlot(t)

		f := fetcherFor(server)
		f.Ensure(fetchFor(release, slot, active))
		if snap := awaitSettled(f); snap.state != fetchVerified {
			t.Fatalf("the fetch state is %s (%s), want Verified", snap.state, snap.detail)
		}

		for _, name := range []string{machine.LayerName, machine.LayerSidecarName} {
			got, err := os.ReadFile(filepath.Join(slot, name))
			if err != nil {
				t.Fatalf("reading %s from the inactive slot: %v", name, err)
			}
			want, err := os.ReadFile(filepath.Join(active, name))
			if err != nil {
				t.Fatalf("reading the active slot file: %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("%s in the inactive slot differs from the active slot's, want a copy", name)
			}
		}
	})
}

func TestAMissingActiveLayerIsRejectedAndNotRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := makeRelease("0.2.0")
		var hits atomic.Int64
		server := serveRelease(t, &hits, release)
		slot := t.TempDir()

		// The running slot has no layer, after damage or an install in
		// an older format. A retry cannot create the layer.
		f := fetcherFor(server)
		fetch := fetchFor(release, slot, t.TempDir())
		f.Ensure(fetch)
		snap := awaitSettled(f)
		if snap.state != fetchRejected {
			t.Fatalf("the fetch state is %s (%s), want Rejected", snap.state, snap.detail)
		}
		if !strings.Contains(snap.detail, "layer") {
			t.Errorf("the rejection detail is %q, want it to name the layer", snap.detail)
		}
		if strings.Contains(snap.detail, "Publish a corrected release") {
			t.Errorf("the rejection detail is %q, want no advice to publish a corrected release because the repair is local", snap.detail)
		}
		if _, err := os.Stat(filepath.Join(slot, "release.yaml")); !os.IsNotExist(err) {
			t.Error("the slot has release.yaml, want none because it has no layer")
		}

		before := hits.Load()
		if snap := f.Ensure(fetch); snap.state != fetchRejected {
			t.Errorf("after the retry the fetch state is %+v, want Rejected", snap)
		}
		if hits.Load() != before {
			t.Error("the fetcher made a request after a rejection, want none")
		}
	})
}

func TestATornActiveSidecarIsRejected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := makeRelease("0.2.0")
		server := serveRelease(t, new(atomic.Int64), release)
		active := activeSlot(t)
		// A crash can leave a sidecar that exists but is empty.
		if err := os.WriteFile(filepath.Join(active, machine.LayerSidecarName), nil, 0o644); err != nil {
			t.Fatalf("writing the torn sidecar: %v", err)
		}

		f := fetcherFor(server)
		f.Ensure(fetchFor(release, t.TempDir(), active))
		if snap := awaitSettled(f); snap.state != fetchRejected {
			t.Errorf("with an empty sidecar the fetch state is %+v, want Rejected", snap)
		}
	})
}

func TestAStaleInactiveLayerIsReplaced(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := makeRelease("0.2.0")
		server := serveRelease(t, new(atomic.Int64), release)
		slot := t.TempDir()
		active := activeSlot(t)

		// The inactive slot has a layer from an older install, with a
		// sidecar that matches it.
		stale := []byte("a previous deployment layer")
		sum := sha256.Sum256(stale)
		if err := os.WriteFile(filepath.Join(slot, machine.LayerName), stale, 0o644); err != nil {
			t.Fatalf("writing the stale layer: %v", err)
		}
		if err := os.WriteFile(filepath.Join(slot, machine.LayerSidecarName),
			machine.FormatLayerSidecar(hex.EncodeToString(sum[:])), 0o644); err != nil {
			t.Fatalf("writing the stale layer: %v", err)
		}

		f := fetcherFor(server)
		f.Ensure(fetchFor(release, slot, active))
		if snap := awaitSettled(f); snap.state != fetchVerified {
			t.Fatalf("the fetch state is %s (%s), want Verified", snap.state, snap.detail)
		}
		got, err := os.ReadFile(filepath.Join(slot, machine.LayerName))
		if err != nil || string(got) != "the deployment layer" {
			t.Errorf("the layer in the inactive slot is %q (error %v), want the active slot's layer", got, err)
		}
	})
}

func TestALayerWithoutItsSidecarIsRecompleted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := makeRelease("0.2.0")
		server := serveRelease(t, new(atomic.Int64), release)
		slot := t.TempDir()
		active := activeSlot(t)

		// An earlier copy stopped after it renamed the layer and before
		// it wrote the sidecar. The next run writes only the sidecar.
		layer, err := os.ReadFile(filepath.Join(active, machine.LayerName))
		if err != nil {
			t.Fatalf("preparing the inactive slot: %v", err)
		}
		if err := os.WriteFile(filepath.Join(slot, machine.LayerName), layer, 0o644); err != nil {
			t.Fatalf("preparing the inactive slot: %v", err)
		}

		f := fetcherFor(server)
		f.Ensure(fetchFor(release, slot, active))
		if snap := awaitSettled(f); snap.state != fetchVerified {
			t.Fatalf("the fetch state is %s (%s), want Verified", snap.state, snap.detail)
		}
		sidecar, err := os.ReadFile(filepath.Join(slot, machine.LayerSidecarName))
		if err != nil {
			t.Fatalf("reading the layer sidecar: %v", err)
		}
		digest, err := machine.ParseLayerSidecar(sidecar)
		if err != nil {
			t.Fatalf("parsing the layer sidecar: %v", err)
		}
		if err := machine.VerifyLayer(digest, strings.NewReader(string(layer))); err != nil {
			t.Errorf("the new sidecar does not match the layer: %v, want a match", err)
		}
	})
}

func TestResumesByVerificationNotRefetching(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := makeRelease("0.2.0")
		var hits atomic.Int64
		server := serveRelease(t, &hits, release)
		slot := t.TempDir()

		// An earlier run stopped after it wrote vmlinuz.
		if err := os.WriteFile(filepath.Join(slot, "vmlinuz"), release.artifacts["vmlinuz"], 0o644); err != nil {
			t.Fatalf("preparing the inactive slot: %v", err)
		}

		f := fetcherFor(server)
		f.Ensure(fetchFor(release, slot, activeSlot(t)))
		awaitSettled(f)

		if got := hits.Load(); got != 2 {
			t.Errorf("the server got %d requests, want 2 (the release document and the missing artifact)", got)
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
		fetch := fetchFor(release, slot, activeSlot(t))
		f.Ensure(fetch)
		awaitSettled(f)
		before := hits.Load()

		if snap := f.Ensure(fetch); snap.state != fetchVerified {
			t.Errorf("after a second Ensure the fetch state is %+v, want Verified", snap)
		}
		if hits.Load() != before {
			t.Error("the second Ensure made a request, want none")
		}
	})
}

func TestCorruptArtifactIsRejectedAndNotRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := makeRelease("0.2.0")
		// The server's liken.cpio changed after the release was
		// published.
		release.artifacts["liken.cpio"] = []byte("pretend initramfs 0.2.0 with a flipped bit")
		var hits atomic.Int64
		server := serveRelease(t, &hits, release)
		slot := t.TempDir()

		f := fetcherFor(server)
		fetch := fetchFor(release, slot, activeSlot(t))
		f.Ensure(fetch)
		snap := awaitSettled(f)
		if snap.state != fetchRejected {
			t.Fatalf("the fetch state is %s (%s), want Rejected", snap.state, snap.detail)
		}
		if _, err := os.Stat(filepath.Join(slot, "liken.cpio")); !os.IsNotExist(err) {
			t.Error("the corrupt artifact is in the slot under its final name, want no file")
		}
		if _, err := os.Stat(filepath.Join(slot, "release.yaml")); !os.IsNotExist(err) {
			t.Error("the slot has release.yaml, want none because an artifact is missing")
		}

		before := hits.Load()
		if snap := f.Ensure(fetch); snap.state != fetchRejected {
			t.Errorf("after the retry the fetch state is %+v, want Rejected", snap)
		}
		if hits.Load() != before {
			t.Error("the fetcher made a request after a rejection, want none")
		}
	})
}

func TestCorruptDocumentIsRejected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := makeRelease("0.2.0")
		release.digest = "sha256:" + hex.EncodeToString(make([]byte, 32))
		server := serveRelease(t, new(atomic.Int64), release)

		f := fetcherFor(server)
		f.Ensure(fetchFor(release, t.TempDir(), activeSlot(t)))
		if snap := awaitSettled(f); snap.state != fetchRejected {
			t.Errorf("with a wrong catalog digest the fetch state is %+v, want Rejected", snap)
		}
	})
}
