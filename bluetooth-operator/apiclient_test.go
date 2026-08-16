package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// testClient points a client at a test server, with a credentials
// directory the test owns.
func testClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "token"), []byte("test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	return NewClient(server.URL, server.Client(), credentials)
}

func TestRequestJSONAuthenticatesAndDecodes(t *testing.T) {
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

func TestRequestJSONSeparatesAbsenceFromFailure(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{status: http.StatusNotFound, want: ErrNotFound},
		{status: http.StatusConflict, want: ErrConflict},
	}
	for _, c := range cases {
		client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
		}))
		if err := client.RequestJSON(http.MethodGet, "/anything", nil, nil); err != c.want {
			t.Errorf("status %d gave %v, want %v", c.status, err, c.want)
		}
	}
}
