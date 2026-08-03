package kubernetes

// These tests run the API client against a real HTTP server
// (net/http/httptest) instead of mocks. The client's whole job is to
// send HTTP requests, so the tests use real requests and real
// responses.

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
)

// testClient connects a Client to a test server. It creates a
// credentials directory that holds a token, the same way kubelet
// mounts one.
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
			Metadata: api.ObjectMeta{Name: "node-1"},
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

// countingClient connects a Client to a test server that counts the
// TCP connections it accepts. Go hands a connection back to its pool
// only when the response body reaches EOF, so the count is how many
// bodies the client left unfinished, plus one.
func countingClient(t *testing.T, handler http.Handler) (*Client, *atomic.Int64) {
	t.Helper()
	connections := &atomic.Int64{}
	server := httptest.NewUnstartedServer(handler)
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	t.Cleanup(server.Close)

	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "token"), []byte("test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	return NewClient(server.URL, server.Client(), credentials), connections
}

func TestEveryRequestLeavesTheConnectionReusable(t *testing.T) {
	// Every Kubernetes response carries a body, including the ones
	// that report a failure: a 404 and a 409 each answer with a
	// Status object. A caller that returns before reading one costs
	// the connection and reaches the API server as a hang-up. The
	// connection count is that cost, made visible.
	filler := strings.Repeat("x", 8192)
	cases := []struct {
		name   string
		status int
		send   func(c *Client) error
	}{
		{"a write whose answer the caller does not want", http.StatusOK, func(c *Client) error {
			return c.RequestJSON(http.MethodPut, "/x", []byte(`{}`), nil)
		}},
		{"a read the caller decodes", http.StatusOK, func(c *Client) error {
			out := map[string]any{}
			return c.RequestJSON(http.MethodGet, "/x", nil, &out)
		}},
		{"an absent object", http.StatusNotFound, func(c *Client) error {
			return c.RequestJSON(http.MethodGet, "/x", nil, nil)
		}},
		{"a lost write race", http.StatusConflict, func(c *Client) error {
			return c.RequestJSON(http.MethodGet, "/x", nil, nil)
		}},
		{"a refusal whose message runs past the excerpt", http.StatusForbidden, func(c *Client) error {
			return c.RequestJSON(http.MethodGet, "/x", nil, nil)
		}},
		{"a merge patch", http.StatusOK, func(c *Client) error {
			return c.PatchJSON("/x", []byte(`{}`))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client, connections := countingClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(c.status)
				_ = json.NewEncoder(w).Encode(map[string]string{"filler": filler})
			}))
			for range 5 {
				_ = c.send(client)
			}
			if got := connections.Load(); got != 1 {
				t.Errorf("5 requests opened %d connections, want 1", got)
			}
		})
	}
}

func TestListClustersReadsTheCollection(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kind": "ClusterList",
			"items": []cluster.Cluster{
				{Kind: "Cluster", Metadata: api.ObjectMeta{Name: "lab"}},
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

func TestInClusterClientNeedsTheEnvironment(t *testing.T) {
	// Outside a pod, the injected variables are absent. That absence
	// is the entire diagnosis.
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	if _, err := InClusterClient(); err == nil {
		t.Error("no environment means no client")
	}
}

// testCA is a self-signed certificate used in place of the cluster's
// server CA. The client only parses it into a trust pool, so its
// subject and validity dates do not matter for these tests.
const testCA = `-----BEGIN CERTIFICATE-----
MIIBhTCCASugAwIBAgIUOGbmxgO5IBnZ+AdPZ+KxpFp/WSowCgYIKoZIzj0EAwIw
GDEWMBQGA1UEAwwNbGlrZW4tdGVzdC1jYTAeFw0yNjA3MTAxNDM2NTVaFw0zNjA3
MDcxNDM2NTVaMBgxFjAUBgNVBAMMDWxpa2VuLXRlc3QtY2EwWTATBgcqhkjOPQIB
BggqhkjOPQMBBwNCAASaQglZfYXr1EOnBa5GCmRcHF9l09EqXuGMZcXWWI6FKi31
InZx5N3F4T8uDCyIyXP9s99z5nJpEyGkmer45bmGo1MwUTAdBgNVHQ4EFgQUnxeb
k5I/ZsgFlDQvQgv02Wa/nwswHwYDVR0jBBgwFoAUnxebk5I/ZsgFlDQvQgv02Wa/
nwswDwYDVR0TAQH/BAUwAwEB/zAKBggqhkjOPQQDAgNIADBFAiEA5TQTNngoyPu6
j58aLfXyXoNxNnxkIFvzXX2zMT55O5gCIESzr4d5khIPY9y/CAS0nry2rvAP5Y5S
FHJFfRsR8TLD
-----END CERTIFICATE-----
`

// serviceAccount points the serviceAccountDir seam at a directory
// that holds the given files, the same way kubelet would mount them.
// It restores the real path when the test ends.
func serviceAccount(t *testing.T, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	previous := serviceAccountDir
	serviceAccountDir = dir
	t.Cleanup(func() { serviceAccountDir = previous })
}

func TestInClusterClientBuildsFromTheEnvironment(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.43.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")
	serviceAccount(t, map[string]string{"token": "test-token", "ca.crt": testCA})
	client, err := InClusterClient()
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://10.43.0.1:443"; client.base != want {
		t.Errorf("the environment names the endpoint: got %s", client.base)
	}
}

func TestInClusterClientAtPrefersTheGivenEndpoint(t *testing.T) {
	// A hostNetwork pod on a server machine reaches the API through
	// its own loopback address instead of the service VIP. The
	// credentials stay the same in both cases.
	serviceAccount(t, map[string]string{"token": "test-token", "ca.crt": testCA})
	client, err := InClusterClientAt("https://127.0.0.1:6443")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://127.0.0.1:6443"; client.base != want {
		t.Errorf("got %s", client.base)
	}
}

func TestInClusterClientAtNeedsTheMountedCA(t *testing.T) {
	serviceAccount(t, map[string]string{"token": "test-token"})
	if _, err := InClusterClientAt("https://127.0.0.1:6443"); err == nil {
		t.Error("a pod without its mounted CA cannot verify the server")
	}
}

func TestInClusterClientAtRejectsAnEmptyCA(t *testing.T) {
	serviceAccount(t, map[string]string{"token": "test-token", "ca.crt": "not a certificate"})
	if _, err := InClusterClientAt("https://127.0.0.1:6443"); err == nil {
		t.Error("a CA file holding no certificates can trust nothing")
	}
}

func TestDoNeedsAServiceAccountToken(t *testing.T) {
	// The client reads the token from disk again on every request,
	// because kubelet refreshes it as it nears expiry. A missing
	// token means a broken pod, and the client reports it as an
	// error.
	client := NewClient("http://unreachable", http.DefaultClient, t.TempDir())
	if _, err := client.Do(http.MethodGet, "/x", "", nil); err == nil {
		t.Error("a missing token must fail the request")
	}
	if err := client.PatchJSON("/x", []byte(`{}`)); err == nil {
		t.Error("a patch cannot go out unauthenticated either")
	}
}

func TestGetReportsAnAbsentObject(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	if _, err := GetMachine(client, "node-9"); err != ErrNotFound {
		t.Errorf("an absent object is ErrNotFound: %v", err)
	}
}

func TestListCarriesTheServersRefusal(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "clusters.liken.sh is forbidden", http.StatusForbidden)
	}))
	if _, err := ListClusters(client); err == nil {
		t.Error("a refused list is an error")
	}
	if _, err := ListHeartbeats(client); err == nil {
		t.Error("and so is a refused heartbeat sweep")
	}
}
