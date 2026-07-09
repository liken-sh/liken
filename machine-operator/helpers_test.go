package main

// Shared test fixtures: a client wired to an httptest server, and a
// fixed instant so time-sensitive decisions are reproducible.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chrisguidry/liken/kubernetes"
)

var testNow = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

// testClient wires a client to a test server, with a credentials
// directory holding a token the way kubelet would have mounted one.
func testClient(t *testing.T, handler http.Handler) *kubernetes.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "token"), []byte("test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	return kubernetes.NewClient(server.URL, server.Client(), credentials)
}

// TestMain silences the retry pause for the whole test binary: the
// seeding loops retry forever by design, and no test wants the real
// five-second wait.
func TestMain(m *testing.M) {
	kubernetes.RetryPause = func() {}
	os.Exit(m.Run())
}
