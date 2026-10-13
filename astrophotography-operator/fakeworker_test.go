package main

// A fake of a reservation's worker (plan 02): it answers GET /outbox
// with the free bytes a test sets and the frames the fake camera saved,
// deletes a centering frame on DELETE /centering/<FRAMEID>, and records
// the host of each request, which names the worker's Service. Every
// other request, an extraction or a solver's, goes to the fake peers,
// because the operator sends all of them through one client.

import (
	"encoding/json"
	"net/http"
	"path"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/liken-sh/liken/astrophotography-operator/outbox"
	"github.com/liken-sh/liken/astrophotography-operator/worker"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

type fakeWorker struct {
	server *apiservertest.Server
	peers  *fakePeers

	mu     sync.Mutex
	free   int64
	frames []string
	hosts  []string
	// deleted holds the FRAMEID of each centering frame the operator
	// deleted, in order.
	deleted []string
	// refuseDeletes answers each delete of a centering frame with 500,
	// as a worker that cannot write the outbox does.
	refuseDeletes bool
	// down answers 503, as a worker whose pod is not ready does, and
	// garbled answers a body that is no JSON.
	down, garbled bool
}

func startFakeWorker(t *testing.T) *fakeWorker {
	f := &fakeWorker{free: 1 << 40, peers: newFakePeers()}
	f.server = apiservertest.Start(t, f)
	return f
}

func (f *fakeWorker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	frameID, deleting := strings.CutPrefix(r.URL.Path, worker.PathCentering)
	if r.URL.Path != worker.PathOutbox && !deleting {
		f.peers.ServeHTTP(w, r)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hosts = append(f.hosts, r.Host)
	switch {
	case deleting && f.refuseDeletes:
		http.Error(w, "removing the frame: read-only file system", http.StatusInternalServerError)
	case deleting && r.Method == http.MethodDelete:
		f.deleted = append(f.deleted, frameID)
		f.frames = slices.DeleteFunc(f.frames, func(p string) bool { return path.Base(p) == frameID+outbox.Extension })
		w.WriteHeader(http.StatusNoContent)
	case f.down || r.Method != http.MethodGet:
		http.Error(w, "the worker is not ready", http.StatusServiceUnavailable)
	case f.garbled:
		_, _ = w.Write([]byte("<html>"))
	default:
		// The worker lists the frames of the targets' directories, and
		// leaves out the centering frames.
		frames := slices.DeleteFunc(slices.Clone(f.frames), func(p string) bool { return strings.Contains(p, "/"+outbox.Centering+"/") })
		_ = json.NewEncoder(w).Encode(worker.OutboxAnswer{FreeBytes: f.free, Frames: slices.Sorted(slices.Values(frames))})
	}
}

// set changes the fake under its lock.
func (f *fakeWorker) set(change func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change()
}

// save adds a frame to the outbox, by its path relative to the root.
func (f *fakeWorker) save(path string) { f.set(func() { f.frames = append(f.frames, path) }) }

func (f *fakeWorker) setFree(free int64) { f.set(func() { f.free = free }) }

func (f *fakeWorker) held() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.frames)
}

func (f *fakeWorker) asked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.hosts)
}

func (f *fakeWorker) deletions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.deleted)
}
