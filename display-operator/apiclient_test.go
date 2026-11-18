package main

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

// testClient returns an API client for a test server. The server uses
// the in-memory connections of apiservertest, so a test that calls it
// can run in a synctest bubble.
func testClient(t *testing.T, handler http.Handler) *apiclient.Client {
	t.Helper()
	server := apiservertest.Start(t, handler)

	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "token"), []byte("test-token"), 0o600); err != nil {
		t.Fatalf("writing the token file: %v", err)
	}
	return apiclient.New(apiservertest.Host, server.Client(), credentials)
}
