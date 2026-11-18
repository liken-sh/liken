package main

// A fake of a reservation's worker. It responds to GET /outbox with the
// free bytes a test sets and the frames the fake camera saved, deletes
// a centering frame on DELETE /centering/<FRAMEID>, and records the
// host of each request, which is the worker's Service name. It passes
// every other request, such as a star extraction or a solve, on to the
// fake peers, because the operator sends all requests through one
// client.

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
	// deleted lists the FRAMEID of each centering frame the operator
	// deleted, in order.
	deleted []string
	// refuseDeletes makes each delete of a centering frame return 500,
	// as a worker that cannot write the outbox does.
	refuseDeletes bool
	// down makes requests return 503, as a worker whose pod is not ready
	// does. garbled makes them return a body that is not JSON.
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
		// The worker lists the frames in the targets' directories and
		// omits the centering frames.
		frames := slices.DeleteFunc(slices.Clone(f.frames), func(p string) bool { return strings.Contains(p, "/"+outbox.Centering+"/") })
		_ = json.NewEncoder(w).Encode(worker.OutboxResponse{FreeBytes: f.free, Frames: slices.Sorted(slices.Values(frames))})
	}
}

// set applies a change to the fake while holding its lock.
func (f *fakeWorker) set(change func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change()
}

// save adds a frame to the outbox, by its path relative to the outbox
// root.
func (f *fakeWorker) save(path string) { f.set(func() { f.frames = append(f.frames, path) }) }

func (f *fakeWorker) setFree(free int64) { f.set(func() { f.free = free }) }

func (f *fakeWorker) outboxFrames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.frames)
}

func (f *fakeWorker) requestHosts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.hosts)
}

func (f *fakeWorker) deletions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.deleted)
}
