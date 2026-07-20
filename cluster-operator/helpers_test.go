package main

// This file provides a shared test fixture: a client wired to an
// httptest server. The kubernetes package's own tests use the same
// arrangement.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/kubernetes"
)

// testClient wires a client to a test server. The client has a
// credentials directory holding a token, the same way kubelet would
// mount one.
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
