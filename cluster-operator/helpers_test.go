package main

// Shared test fixture: a client wired to an httptest server, the
// same arrangement the kubernetes package's own tests use.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/chrisguidry/liken/kubernetes"
)

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
