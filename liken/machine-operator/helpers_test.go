package main

// Shared test fixtures: a client wired to a fake API server that
// apiservertest serves over in-memory connections, so a test in a
// synctest bubble can use it, and a fixed instant so time-sensitive
// decisions are reproducible.

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

var testNow = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

// testClient wires a client to a test server, with a credentials
// directory holding a token the way kubelet would have mounted one.
func testClient(t *testing.T, handler http.Handler) *apiclient.Client {
	t.Helper()
	server := apiservertest.Start(t, handler)

	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "token"), []byte("test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	return apiclient.New(apiservertest.Host, server.Client(), credentials)
}
