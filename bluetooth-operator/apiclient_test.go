package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// testClient returns a client for a test server that sends the token
// "test-token".
func testClient(t *testing.T, handler http.Handler) *apiclient.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "token"), []byte("test-token"), 0o600); err != nil {
		t.Fatalf("writing the token file: %v", err)
	}
	return apiclient.New(server.URL, server.Client(), credentials)
}

// The `ResourceSlice` is owned by the `Node`, so the owner reference
// needs the UID that the API server returns for it.
func TestNodeOwnerReturnsTheNodeWithItsUID(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("the Authorization header is %q, want %q", got, "Bearer test-token")
		}
		_, _ = w.Write([]byte(`{"metadata":{"name":"liken-1","uid":"abc-123"}}`))
	}))

	owner, err := NodeOwner(client, "liken-1")
	if err != nil {
		t.Fatalf("reading the owner of Node liken-1: %v", err)
	}
	if owner != (OwnerReference{APIVersion: "v1", Kind: "Node", Name: "liken-1", UID: "abc-123"}) {
		t.Fatalf("the owner is %+v, want Node liken-1 with UID abc-123", owner)
	}
}
