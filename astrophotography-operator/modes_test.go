package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/astrophotography-operator/solver"
	"github.com/liken-sh/liken/astrophotography-operator/worker"
)

// Each mode serves its paths until its context ends, the kubelet's
// SIGTERM.
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
			synctest.Test(t, func(t *testing.T) {
				client := listenInMemory(t)
				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan error)
				go func() { done <- modes[c.mode](ctx, c.args(t), io.Discard) }()
				synctest.Wait()
				response, err := client.Get("http://127.0.0.1:8080" + c.path)
				if err != nil {
					t.Fatal(err)
				}
				var answer any
				err = json.NewDecoder(response.Body).Decode(&answer)
				_ = response.Body.Close()
				cancel()
				if err != nil || response.StatusCode != http.StatusOK || <-done != nil {
					t.Errorf("answered %d and %v", response.StatusCode, err)
				}
			})
		})
	}
}

func TestAModeThatCannotStartSaysWhy(t *testing.T) {
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
				t.Error("no error")
			}
		})
	}
}

// A port in use fails the start, so the pod restarts.
func TestAModeWhosePortIsTakenFails(t *testing.T) {
	restore := listen
	listen = func(string, string) (net.Listener, error) { return nil, errors.New("address already in use") }
	t.Cleanup(func() { listen = restore })
	if err := serveMode(t.Context(), ":8080", http.NotFoundHandler()); err == nil {
		t.Error("no error")
	}
}
