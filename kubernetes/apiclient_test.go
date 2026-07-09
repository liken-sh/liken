package kubernetes

// Tests for the API client against a real HTTP server
// (net/http/httptest) rather than mocks: the client's whole job is
// HTTP, so the tests exercise real requests and responses.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chrisguidry/liken/machine"
)

// testClient wires a Client to a test server, with a credentials
// directory holding a token the way kubelet would have mounted one.
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

func TestRequestJSONDecodesAndAuthenticates(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("authorization: got %q", got)
		}
		_ = json.NewEncoder(w).Encode(&machine.Machine{
			Kind:     "Machine",
			Metadata: machine.ObjectMeta{Name: "node-1"},
		})
	}))
	m, err := GetMachine(client, "node-1")
	if err != nil {
		t.Fatal(err)
	}
	if m.Metadata.Name != "node-1" {
		t.Errorf("name: got %q", m.Metadata.Name)
	}
}

func TestRequestJSONDistinguishesTheOrdinaryFailures(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   error
	}{
		{"absent objects are a state, not a failure", http.StatusNotFound, ErrNotFound},
		{"losing a write race is a state, not a failure", http.StatusConflict, ErrConflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
			}))
			if err := client.RequestJSON(http.MethodGet, "/x", nil, nil); err != c.want {
				t.Errorf("got %v, want %v", err, c.want)
			}
		})
	}
}

func TestRequestJSONCarriesTheServersWords(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "machines.liken.sh is forbidden", http.StatusForbidden)
	}))
	err := client.RequestJSON(http.MethodGet, "/x", nil, nil)
	if err == nil {
		t.Fatal("expected an error for 403")
	}
	for _, want := range []string{"403", "forbidden"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

func TestListClustersReadsTheCollection(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kind": "ClusterList",
			"items": []machine.Cluster{
				{Kind: "Cluster", Metadata: machine.ObjectMeta{Name: "lab"}},
			},
		})
	}))
	clusters, err := ListClusters(client)
	if err != nil {
		t.Fatal(err)
	}
	if len(clusters) != 1 || clusters[0].Metadata.Name != "lab" {
		t.Errorf("got %+v", clusters)
	}
}

func TestDoNeedsAServiceAccountToken(t *testing.T) {
	// The token is re-read from disk on every request (kubelet
	// refreshes it as it approaches expiry); a missing token is a
	// broken pod, reported as one.
	client := NewClient("http://unreachable", http.DefaultClient, t.TempDir())
	if _, err := client.Do(http.MethodGet, "/x", "", nil); err == nil {
		t.Error("a missing token must fail the request")
	}
}
