package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/solver"
	"github.com/liken-sh/liken/astrophotography-operator/worker"
)

// The test runs outside a synctest bubble because the solver's watch
// of its index directory waits on a file descriptor, and a bubble does
// not count that as blocked. A dial of the in-memory listener waits
// until the mode is serving, so the test needs no other wait.
func TestEachModeServesUntilItsContextEnds(t *testing.T) {
	cases := []struct {
		mode string
		args func(t *testing.T) []string
		path string
	}{
		{"solver", func(t *testing.T) []string { return []string{"--indexes", t.TempDir(), "--work", t.TempDir()} }, solver.PathIndexes},
		{"worker", func(t *testing.T) []string { return []string{"--outbox", t.TempDir(), "--work", t.TempDir()} }, worker.PathOutbox},
	}
	for _, c := range cases {
		t.Run(c.mode, func(t *testing.T) {
			client := listenInMemory(t)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error)
			go func() { done <- modes[c.mode](ctx, c.args(t), io.Discard) }()
			response, err := client.Get("http://127.0.0.1:8080" + c.path)
			if err != nil {
				t.Fatalf("requesting %s from the %s mode: %v", c.path, c.mode, err)
			}
			var body any
			err = json.NewDecoder(response.Body).Decode(&body)
			_ = response.Body.Close()
			cancel()
			if err != nil || response.StatusCode != http.StatusOK || <-done != nil {
				t.Errorf("the %s mode responded %d with decode error %v, want 200 with JSON and a clean stop", c.mode, response.StatusCode, err)
			}
		})
	}
}

// When the index directory is replaced by a file, the solver cannot
// watch it any more, so the mode ends with an error and the kubelet
// restarts the container.
func TestASolverThatCannotWatchItsIndexesEnds(t *testing.T) {
	client := listenInMemory(t)
	indexes := filepath.Join(t.TempDir(), "2mass")
	if err := os.Mkdir(indexes, 0o700); err != nil {
		t.Fatalf("creating the index directory: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := make(chan error)
	go func() {
		done <- modes["solver"](ctx, []string{"--indexes", indexes, "--work", t.TempDir()}, io.Discard)
	}()
	response, err := client.Get("http://127.0.0.1:8080" + solver.PathIndexes)
	if err != nil {
		t.Fatalf("requesting the indexes from the solver: %v", err)
	}
	_ = response.Body.Close()
	if err := os.Remove(indexes); err != nil {
		t.Fatalf("removing the index directory: %v", err)
	}
	if err := os.WriteFile(indexes, nil, 0o600); err != nil {
		t.Fatalf("replacing the index directory with a file: %v", err)
	}
	if err := <-done; err == nil || !strings.Contains(err.Error(), "watching the index files") {
		t.Errorf("the mode ended with %v, want the watch's error", err)
	}
}

func TestAModeThatCannotStartReturnsAnError(t *testing.T) {
	cases := []struct {
		name string
		mode string
		args []string
	}{
		{"a solver flag it does not take", "solver", []string{"--no-such-flag"}},
		{"a solver with no work directory", "solver", []string{"--indexes", "/nonexistent", "--work", "/nonexistent/work"}},
		{"a worker flag it does not take", "worker", []string{"--no-such-flag"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := modes[c.mode](t.Context(), c.args, io.Discard); err == nil {
				t.Errorf("%s returned no error, want one", c.name)
			}
		})
	}
}

// A port that is already in use fails the start, so the pod restarts.
func TestAModeWhosePortIsInUseFails(t *testing.T) {
	restore := listen
	listen = func(string, string) (net.Listener, error) { return nil, errors.New("address already in use") }
	t.Cleanup(func() { listen = restore })
	cases := []struct {
		name  string
		serve func(t *testing.T) error
	}{
		{"a handler", func(t *testing.T) error { return serveMode(t.Context(), ":8080", http.NotFoundHandler()) }},
		{"the solver", func(t *testing.T) error {
			return modes["solver"](t.Context(), []string{"--indexes", t.TempDir(), "--work", t.TempDir()}, io.Discard)
		}},
		{"the worker", func(t *testing.T) error {
			return modes["worker"](t.Context(), []string{"--outbox", t.TempDir(), "--work", t.TempDir()}, io.Discard)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.serve(t); err == nil {
				t.Errorf("%s returned no error, want the listen error", c.name)
			}
		})
	}
}
