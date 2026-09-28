package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// testClient points a client at a test server, with a credentials
// directory the test owns.
func testClient(t *testing.T, handler http.Handler) *apiclient.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "token"), []byte("test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	return apiclient.New(server.URL, server.Client(), credentials)
}

// The owner of a ResourceSlice is the Node the API server names, with
// its UID.
func TestTheNodeOwnerIsTheNodeTheAPIServerNames(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"metadata":{"name":"liken-1","uid":"abc-123"}}`))
	}))

	owner, err := NodeOwner(client, "liken-1")
	if err != nil {
		t.Fatal(err)
	}
	if owner != (OwnerReference{APIVersion: "v1", Kind: "Node", Name: "liken-1", UID: "abc-123"}) {
		t.Fatalf("owner = %+v", owner)
	}
}
