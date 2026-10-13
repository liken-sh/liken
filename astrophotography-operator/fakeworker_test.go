package main

// A fake of a reservation's worker (plan 02): it answers GET /outbox
// with the free bytes a test sets and the frames the fake camera saved,
// and records the host of each request, which names the worker's
// Service.

import (
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/liken-sh/liken/astrophotography-operator/worker"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

type fakeWorker struct {
	server *apiservertest.Server

	mu     sync.Mutex
	free   int64
	frames []string
	hosts  []string
	// down answers 503, as a worker whose pod is not ready does, and
	// garbled answers a body that is no JSON.
	down, garbled bool
}

func startFakeWorker(t *testing.T) *fakeWorker {
	f := &fakeWorker{free: 1 << 40}
	f.server = apiservertest.Start(t, f)
	return f
}

func (f *fakeWorker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hosts = append(f.hosts, r.Host)
	switch {
	case f.down || r.Method != http.MethodGet || r.URL.Path != worker.PathOutbox:
		http.Error(w, "the worker is not ready", http.StatusServiceUnavailable)
	case f.garbled:
		_, _ = w.Write([]byte("<html>"))
	default:
		_ = json.NewEncoder(w).Encode(worker.OutboxAnswer{FreeBytes: f.free, Frames: slices.Sorted(slices.Values(f.frames))})
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
