package main

// Tests for the API client and the seeding loops, against a real HTTP
// server (net/http/httptest) rather than mocks: the client's whole
// job is HTTP, so the tests exercise real requests and responses.

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

// testClient wires an apiClient to a test server, with a credentials
// directory holding a token the way kubelet would have mounted one.
func testClient(t *testing.T, handler http.Handler) *apiClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "token"), []byte("test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &apiClient{base: server.URL, http: server.Client(), credentials: credentials}
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
	m, err := getMachine(client, "node-1")
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
		{"absent objects are a state, not a failure", http.StatusNotFound, errNotFound},
		{"losing a write race is a state, not a failure", http.StatusConflict, errConflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
			}))
			if err := client.requestJSON(http.MethodGet, "/x", nil, nil); err != c.want {
				t.Errorf("got %v, want %v", err, c.want)
			}
		})
	}
}

func TestRequestJSONCarriesTheServersWords(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "machines.liken.sh is forbidden", http.StatusForbidden)
	}))
	err := client.requestJSON(http.MethodGet, "/x", nil, nil)
	if err == nil {
		t.Fatal("expected an error for 403")
	}
	for _, want := range []string{"403", "forbidden"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

// clusterAPI is a miniature API server for the seeding loop: it
// remembers whether the Cluster exists and can be told to answer the
// first create with a conflict, as the real server would when another
// machine's operator created the object first.
type clusterAPI struct {
	exists   bool
	conflict bool
	creates  int
}

func (api *clusterAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			if !api.exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(&machine.Cluster{
				Kind:     "Cluster",
				Metadata: machine.ObjectMeta{Name: "lab"},
			})
		case r.Method == http.MethodPost:
			api.creates++
			if api.conflict {
				// Someone else's create landed first; the object
				// exists now no matter who made it.
				api.exists = true
				w.WriteHeader(http.StatusConflict)
				return
			}
			api.exists = true
			w.WriteHeader(http.StatusCreated)
		}
	})
}

func seedCluster() *machine.Cluster {
	return &machine.Cluster{
		Kind:     "Cluster",
		Metadata: machine.ObjectMeta{Name: "lab"},
		Spec:     machine.ClusterSpec{Leaders: []string{"node-1"}},
	}
}

func TestEnsureClusterCreatesWhenAbsent(t *testing.T) {
	api := &clusterAPI{}
	client := testClient(t, api.handler())
	if err := ensureCluster(client, seedCluster()); err != nil {
		t.Fatal(err)
	}
	if api.creates != 1 {
		t.Errorf("expected one create, got %d", api.creates)
	}
}

func TestEnsureClusterLeavesAnExistingClusterAlone(t *testing.T) {
	api := &clusterAPI{exists: true}
	client := testClient(t, api.handler())
	if err := ensureCluster(client, seedCluster()); err != nil {
		t.Fatal(err)
	}
	if api.creates != 0 {
		t.Errorf("an existing cluster should never be re-created; got %d creates", api.creates)
	}
}

func TestEnsureClusterTreatsLosingTheRaceAsSuccess(t *testing.T) {
	api := &clusterAPI{conflict: true}
	client := testClient(t, api.handler())
	if err := ensureCluster(client, seedCluster()); err != nil {
		t.Fatalf("a lost race is a seeded cluster: %v", err)
	}
}

func TestDoNeedsAServiceAccountToken(t *testing.T) {
	// The token is re-read from disk on every request (kubelet
	// refreshes it as it approaches expiry); a missing token is a
	// broken pod, reported as one.
	client := &apiClient{base: "http://unreachable", http: http.DefaultClient, credentials: t.TempDir()}
	if _, err := client.do(http.MethodGet, "/x", "", nil); err == nil {
		t.Error("a missing token must fail the request")
	}
}
