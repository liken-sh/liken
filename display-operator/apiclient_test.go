package main

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

// testClient points the operator's API client at a test server, with a
// service account token on disk the way the kubelet mounts one. The
// server answers over the in-memory connections of apiservertest, so a
// test that calls it can run in a synctest bubble.
func testClient(t *testing.T, handler http.Handler) *apiclient.Client {
	t.Helper()
	server := apiservertest.Start(t, handler)

	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "token"), []byte("test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	return apiclient.New(apiservertest.Host, server.Client(), credentials)
}
