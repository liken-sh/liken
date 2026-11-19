package main

// Test fixtures: a client for a fake API server, and a fixed time.
// apiservertest serves the fake server over in-memory connections, so a
// test inside a synctest bubble can use the client.

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

// testClient returns a client for a test server that uses handler. Its
// credentials directory holds a token file, as the kubelet mounts one
// in a pod.
func testClient(t *testing.T, handler http.Handler) *apiclient.Client {
	t.Helper()
	server := apiservertest.Start(t, handler)

	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "token"), []byte("test-token"), 0o600); err != nil {
		t.Fatalf("writing the token file: %v", err)
	}
	return apiclient.New(apiservertest.Host, server.Client(), credentials)
}

// readOnly makes a directory read-only until the test ends.
func readOnly(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("making %s read-only: %v", dir, err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
}
