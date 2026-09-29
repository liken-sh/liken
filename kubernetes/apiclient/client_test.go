package apiclient

import (
	"context"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testClient points a client at a test server, with a credentials
// directory the test owns. One second of a 429's wait is one
// millisecond here.
func testClient(t *testing.T, handler http.Handler) (*Client, string) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	credentials := t.TempDir()
	writeToken(t, credentials, "first-token")
	client := New(server.URL, server.Client(), credentials)
	client.throttleUnit = time.Millisecond
	return client, credentials
}

func writeToken(t *testing.T, dir, token string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The client reads the token for each request, so a token the kubelet
// refreshed reaches the next request.
func TestEachRequestCarriesTheTokenOnDiskNow(t *testing.T) {
	var seen []string
	client, credentials := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{}`))
	}))

	if err := client.RequestJSON(http.MethodGet, "/api/v1/nodes/node-1", nil, nil); err != nil {
		t.Fatal(err)
	}
	writeToken(t, credentials, "second-token")
	if err := client.RequestJSON(http.MethodGet, "/api/v1/nodes/node-1", nil, nil); err != nil {
		t.Fatal(err)
	}

	if len(seen) != 2 || seen[0] != "Bearer first-token" || seen[1] != "Bearer second-token" {
		t.Errorf("Authorization headers = %q, want the first token and then the second", seen)
	}
}

// A client with no credentials directory sends no Authorization header,
// and a token it cannot read fails the request before it is sent.
func TestTheCredentialsDirectory(t *testing.T) {
	cases := []struct {
		name        string
		credentials func(t *testing.T) string
		wantHeader  string
		wantErr     string
	}{
		{"none", func(*testing.T) string { return "" }, "", ""},
		{"a token", func(t *testing.T) string { dir := t.TempDir(); writeToken(t, dir, "abc"); return dir }, "Bearer abc", ""},
		{"no token file", func(t *testing.T) string { return t.TempDir() }, "", "reading service account token"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var header string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				header = r.Header.Get("Authorization")
			}))
			t.Cleanup(server.Close)

			err := New(server.URL, server.Client(), c.credentials(t)).RequestJSON(http.MethodGet, "/", nil, nil)

			if c.wantErr == "" && (err != nil || header != c.wantHeader) {
				t.Errorf("err = %v, Authorization = %q; want no error and %q", err, header, c.wantHeader)
			}
			if c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
				t.Errorf("err = %v, want one that says %q", err, c.wantErr)
			}
		})
	}
}

// Each status maps to the answer its caller handles: an absent object,
// a conflict, and a failure that holds the server's own text on one
// line.
func TestEachStatusAnswersWhatItsCallerHandles(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantIs  error
		wantErr string
	}{
		{"a 404", http.StatusNotFound, `{"kind":"Status"}`, ErrNotFound, ""},
		{"a 409", http.StatusConflict, `{"kind":"Status"}`, ErrConflict, ""},
		{"a 403", http.StatusForbidden, "forbidden: no list\n", nil, "GET /things: 403 Forbidden: forbidden: no list"},
		{"a 500", http.StatusInternalServerError, "etcd is gone\n\n", nil, "500 Internal Server Error: etcd is gone"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))

			err := client.RequestJSON(http.MethodGet, "/things", nil, nil)

			if c.wantIs != nil && !errors.Is(err, c.wantIs) {
				t.Errorf("err = %v, want %v", err, c.wantIs)
			}
			if c.wantErr != "" && (err == nil || !strings.HasSuffix(err.Error(), c.wantErr)) {
				t.Errorf("err = %q, want one that ends %q", err, c.wantErr)
			}
		})
	}
}

// A request states the content type of its body, and a request with no
// body states none.
func TestARequestStatesTheContentTypeOfItsBody(t *testing.T) {
	cases := []struct {
		name     string
		send     func(c *Client) error
		wantType string
		wantBody string
	}{
		{"a JSON body", func(c *Client) error { return c.RequestJSON(http.MethodPost, "/", []byte(`{"a":1}`), nil) },
			"application/json", `{"a":1}`},
		{"a merge patch", func(c *Client) error {
			return c.Request(http.MethodPatch, "/", "application/merge-patch+json", []byte(`{"b":2}`), nil)
		}, "application/merge-patch+json", `{"b":2}`},
		{"no body", func(c *Client) error { return c.RequestJSON(http.MethodDelete, "/", nil, nil) }, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var contentType, body, accept string
			client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				contentType, accept = r.Header.Get("Content-Type"), r.Header.Get("Accept")
				read, _ := io.ReadAll(r.Body)
				body = string(read)
			}))

			if err := c.send(client); err != nil {
				t.Fatal(err)
			}
			if contentType != c.wantType || body != c.wantBody || accept != "application/json" {
				t.Errorf("Content-Type %q, body %q, Accept %q; want %q, %q, application/json",
					contentType, body, accept, c.wantType, c.wantBody)
			}
		})
	}
}

// throttling answers 429 to the first requests, with the given headers
// and body, and then answers the object. It counts the requests.
type throttling struct {
	refusals   int
	retryAfter string
	body       string
	requests   atomic.Int64
}

func (s *throttling) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.requests.Add(1) <= int64(s.refusals) {
		if s.retryAfter != "" {
			w.Header().Set("Retry-After", s.retryAfter)
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(s.body))
		return
	}
	_, _ = w.Write([]byte(`{"metadata":{"name":"studio"}}`))
}

// A 429 is sent again after the wait the API server asks for, while
// the total wait stays within maxThrottleWait. The API server states
// the wait in the Retry-After header, in the Status body, or not at
// all, which means one second.
func TestA429IsSentAgainAfterTheWaitItAsksFor(t *testing.T) {
	status := `{"kind":"Status","message":"storage is (re)initializing","details":{"retryAfterSeconds":2}}`
	cases := []struct {
		name         string
		server       *throttling
		wantErr      bool
		wantRequests int64
	}{
		{"the header", &throttling{refusals: 2, retryAfter: "3"}, false, 3},
		{"the Status body", &throttling{refusals: 2, body: status}, false, 3},
		{"no advice", &throttling{refusals: 1}, false, 2},
		{"longer than the limit", &throttling{refusals: 100, retryAfter: "4"}, true, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client, _ := testClient(t, c.server)
			var out struct {
				Metadata struct{ Name string } `json:"metadata"`
			}

			err := client.RequestJSON(http.MethodGet, "/things/studio", nil, &out)

			if c.wantErr != (err != nil) || (err == nil && out.Metadata.Name != "studio") {
				t.Errorf("err = %v, name %q; want an error: %v", err, out.Metadata.Name, c.wantErr)
			}
			if err != nil && (!strings.Contains(err.Error(), "429") || !errors.Is(err, ErrThrottled)) {
				t.Errorf("err = %v, want the 429 as ErrThrottled", err)
			}
			if got := c.server.requests.Load(); got != c.wantRequests {
				t.Errorf("the client sent %d requests, want %d", got, c.wantRequests)
			}
		})
	}
}

// A write that meets a 429 is sent again with its whole body, and with
// its content type.
func TestAWriteAfterA429SendsItsWholeBodyAgain(t *testing.T) {
	var bodies, types []string
	refused := false
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		read, _ := io.ReadAll(r.Body)
		bodies, types = append(bodies, string(read)), append(types, r.Header.Get("Content-Type"))
		if !refused {
			refused = true
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	body := `{"metadata":{"name":"studio","resourceVersion":"7"},"status":{"phase":"Ready"}}`

	if err := client.Request(http.MethodPut, "/things/studio/status", "application/json", []byte(body), nil); err != nil {
		t.Fatal(err)
	}

	if len(bodies) != 2 || bodies[0] != body || bodies[1] != body || types[1] != "application/json" {
		t.Errorf("the server received %q with types %q, want the whole body twice", bodies, types)
	}
}

// Get answers the object, and nothing with an error.
func TestGetAnswersTheObjectOrNothing(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/things/studio" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"name":"studio"}`))
	}))
	type named struct{ Name string }

	found, err := Get[named](client, "/things/studio")
	if err != nil || found.Name != "studio" {
		t.Errorf("Get(studio) = %+v, %v; want studio", found, err)
	}
	missing, err := Get[named](client, "/things/den")
	if missing != nil || !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(den) = %+v, %v; want nothing and ErrNotFound", missing, err)
	}
}

// statusObject is an object with a status that the API server stores
// only in part.
type statusObject struct {
	Metadata struct {
		ResourceVersion string `json:"resourceVersion"`
	} `json:"metadata"`
	Status struct {
		Phase   string `json:"phase,omitempty"`
		Pruned  string `json:"pruned,omitempty"`
		Battery int    `json:"battery,omitempty"`
	} `json:"status"`
}

// A status write goes to the status subresource, and the caller then
// holds the API server's copy: the new resourceVersion, and no field
// that the API server dropped. A refused write leaves the caller's
// object as it was.
func TestAStatusWriteLeavesTheCallerWithTheAPIServersCopy(t *testing.T) {
	cases := []struct {
		name        string
		answer      int
		wantErr     error
		wantVersion string
		wantPruned  string
	}{
		{"a write that lands", http.StatusOK, nil, "8", ""},
		{"a write from an older copy", http.StatusConflict, ErrConflict, "7", "kept"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var method, path string
			client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				method, path = r.Method, r.URL.Path
				w.WriteHeader(c.answer)
				_, _ = w.Write([]byte(`{"metadata":{"resourceVersion":"8"},"status":{"phase":"Ready"}}`))
			}))
			object := &statusObject{}
			object.Metadata.ResourceVersion = "7"
			object.Status.Phase, object.Status.Pruned = "Ready", "kept"

			err := ReplaceStatus(client, "/things/studio", object)

			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if method != http.MethodPut || path != "/things/studio/status" {
				t.Errorf("the write was %s %s, want PUT /things/studio/status", method, path)
			}
			if object.Metadata.ResourceVersion != c.wantVersion || object.Status.Pruned != c.wantPruned {
				t.Errorf("the caller holds version %q and pruned %q, want %q and %q",
					object.Metadata.ResourceVersion, object.Status.Pruned, c.wantVersion, c.wantPruned)
			}
		})
	}
}

// Stale separates a write from an older copy, or from a copy of an
// object that is gone, from any other failure.
func TestStaleNamesAWriteFromACopyThatIsNotTheAPIServers(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{ErrConflict, true},
		{ErrNotFound, true},
		{errors.New("connection refused"), false},
		{nil, false},
	}
	for _, c := range cases {
		if got := Stale(c.err); got != c.want {
			t.Errorf("Stale(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

// An answer that no caller reads is read to its end, so the next
// request uses the same connection.
func TestAnAnswerNobodyReadsKeepsTheConnection(t *testing.T) {
	var connections atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(strings.Repeat(`{"kind":"Status"}`, 1000)))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	t.Cleanup(server.Close)
	client := New(server.URL, server.Client(), "")

	for range 3 {
		if err := client.RequestJSON(http.MethodGet, "/things/den", nil, nil); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	}

	if got := connections.Load(); got != 1 {
		t.Errorf("three requests opened %d connections, want 1", got)
	}
}

// tlsCluster is an API server over TLS, and a ServiceAccount directory
// that holds its CA and a token, the way the kubelet mounts one.
func tlsCluster(t *testing.T) (dir string, server *httptest.Server) {
	t.Helper()
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name":"` + r.Header.Get("Authorization") + `"}`))
	}))
	t.Cleanup(server.Close)
	dir = t.TempDir()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), ca, 0o600); err != nil {
		t.Fatal(err)
	}
	writeToken(t, dir, "pod-token")
	return dir, server
}

// An in-cluster client reaches the API server that the environment
// names, trusts the mounted CA, and sends the mounted token.
func TestAnInClusterClientReachesTheAPIServer(t *testing.T) {
	dir, server := tlsCluster(t)
	host, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBERNETES_SERVICE_HOST", host)
	t.Setenv("KUBERNETES_SERVICE_PORT", port)

	client, err := InCluster(InClusterOptions{ServiceAccountDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	answer, err := Get[struct{ Name string }](client, "/api/v1/nodes/node-1")
	if err != nil || answer.Name != "Bearer pod-token" {
		t.Errorf("Get = %+v, %v; want the answer to the pod's token", answer, err)
	}
}

// An in-cluster client refuses to start without the environment's
// address or a CA it can read.
func TestAnInClusterClientNeedsItsEnvironment(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		ca      []byte
		wantErr string
	}{
		{"no address", "", []byte("unused"), "not running in a cluster"},
		{"no CA", "10.43.0.1", nil, "reading service account CA"},
		{"a CA with no certificate", "10.43.0.1", []byte("not a certificate"), "contains no certificates"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("KUBERNETES_SERVICE_HOST", c.host)
			t.Setenv("KUBERNETES_SERVICE_PORT", "443")
			dir := t.TempDir()
			if c.ca != nil {
				if err := os.WriteFile(filepath.Join(dir, "ca.crt"), c.ca, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			_, err := InCluster(InClusterOptions{ServiceAccountDir: dir})

			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("err = %v, want one that says %q", err, c.wantErr)
			}
		})
	}
}

// A client with a context sends each request with it, so a request
// whose context ended is never sent.
func TestAClientWithAnEndedContextSendsNothing(t *testing.T) {
	server := &throttling{}
	client, _ := testClient(t, server)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := client.WithContext(ctx).RequestJSON(http.MethodGet, "/things/studio", nil, nil)

	if !errors.Is(err, context.Canceled) || server.requests.Load() != 0 {
		t.Errorf("err = %v after %d requests, want the context's end and none", err, server.requests.Load())
	}
}

// The wait after a 429 ends when the client's context ends, and the
// request answers the 429.
func TestTheWaitAfterA429EndsWithTheContext(t *testing.T) {
	server := &throttling{refusals: 100, retryAfter: "4"}
	client, _ := testClient(t, server)
	client.throttleUnit = time.Hour
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(20*time.Millisecond, cancel)
	began := time.Now()

	err := client.WithContext(ctx).RequestJSON(http.MethodGet, "/things/studio", nil, nil)

	if err == nil || !strings.Contains(err.Error(), "429") || time.Since(began) > 5*time.Second {
		t.Errorf("err = %v after %s, want the 429 at once", err, time.Since(began))
	}
	if server.requests.Load() != 1 {
		t.Errorf("the client sent %d requests, want 1", server.requests.Load())
	}
}

// A client with a wait context ends only its wait after a 429 when the
// context ends. A request already sent runs to its end, so a writer
// that must know whether its write landed, such as an operator that
// releases a Lease after its last write, still learns the answer.
func TestAWaitContextEndsTheWaitAndNotTheRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	release := make(chan struct{})
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel()
		<-release
		_, _ = w.Write([]byte(`{}`))
	}))
	time.AfterFunc(20*time.Millisecond, func() { close(release) })

	if err := client.WithWaitContext(ctx).RequestJSON(http.MethodGet, "/things/studio", nil, nil); err != nil {
		t.Errorf("err = %v, want the answer of the request that was sent", err)
	}

	throttled := &throttling{refusals: 100, retryAfter: "4"}
	client, _ = testClient(t, throttled)
	client.throttleUnit = time.Hour
	began := time.Now()
	err := client.WithWaitContext(ctx).RequestJSON(http.MethodGet, "/things/studio", nil, nil)
	if !errors.Is(err, ErrThrottled) || time.Since(began) > 5*time.Second || throttled.requests.Load() != 1 {
		t.Errorf("err = %v after %s and %d requests, want the 429 at once", err, time.Since(began), throttled.requests.Load())
	}
}
