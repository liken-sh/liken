package apiclient

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

// testClient returns a client for a fake API server and the credentials
// directory that holds its token. A test that gets a 429 runs in a
// synctest bubble, so the wait after the 429 runs on the fake clock.
func testClient(t *testing.T, handler http.Handler) (*Client, string) {
	t.Helper()
	credentials := t.TempDir()
	writeToken(t, credentials, "first-token")
	return New(apiservertest.Host, apiservertest.Start(t, handler).Client(), credentials), credentials
}

func writeToken(t *testing.T, dir, token string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte(token), 0o600); err != nil {
		t.Fatalf("writing the token file: %v", err)
	}
}

// The client reads the token file for each request, so a token that the
// kubelet rewrites is used by the next request.
func TestEachRequestReadsTheTokenFileAgain(t *testing.T) {
	var seen []string
	client, credentials := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{}`))
	}))

	if err := client.RequestJSON(http.MethodGet, "/api/v1/nodes/node-1", nil, nil); err != nil {
		t.Fatalf("the first request: %v", err)
	}
	writeToken(t, credentials, "second-token")
	if err := client.RequestJSON(http.MethodGet, "/api/v1/nodes/node-1", nil, nil); err != nil {
		t.Fatalf("the second request: %v", err)
	}

	if len(seen) != 2 || seen[0] != "Bearer first-token" || seen[1] != "Bearer second-token" {
		t.Errorf("Authorization headers = %q, want the first token and then the second", seen)
	}
}

// A client with no credentials directory sends no Authorization header.
// When the token file cannot be read, the request fails before it is
// sent.
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
				t.Errorf("err = %v, want one that contains %q", err, c.wantErr)
			}
		})
	}
}

// namespaceTerminating is the Status body of the 403 for a create in a
// namespace that is being deleted.
const namespaceTerminating = `{"kind":"Status","apiVersion":"v1","status":"Failure","message":"events \"east.\" is forbidden: unable to create new content in namespace observatory because it is being terminated","reason":"Forbidden","details":{"name":"east.","kind":"events","causes":[{"reason":"NamespaceTerminating","message":"namespace observatory is being terminated","field":"metadata.namespace"}]},"code":403}`

// Each error status returns the error its caller checks for. A 500
// returns an error that includes the server's message on one line.
func TestEachErrorStatusReturnsItsError(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantIs  error
		wantErr string
	}{
		{"a 404", http.StatusNotFound, `{"kind":"Status"}`, ErrNotFound, ""},
		{"a 409", http.StatusConflict, `{"kind":"Status"}`, ErrConflict, ""},
		{"a 403", http.StatusForbidden, "forbidden: no list\n", ErrForbidden, "GET /things: 403 Forbidden: forbidden: no list"},
		{"a 403 in a namespace being deleted", http.StatusForbidden, namespaceTerminating, ErrNamespaceTerminating, ""},
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

// A request sends the content type of its body, and a request with no
// body sends none.
func TestARequestSendsTheContentTypeOfItsBody(t *testing.T) {
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
				t.Fatalf("sending the request: %v", err)
			}
			if contentType != c.wantType || body != c.wantBody || accept != "application/json" {
				t.Errorf("Content-Type %q, body %q, Accept %q; want %q, %q, application/json",
					contentType, body, accept, c.wantType, c.wantBody)
			}
		})
	}
}

// Get returns the object, or nil with an error.
func TestGetReturnsTheObjectOrNil(t *testing.T) {
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

// A caller that passes a *[]byte gets a response body that is not JSON,
// such as a pod's log, as it is.
func TestABodyThatIsNotJSONIsReturnedRawToABytesTarget(t *testing.T) {
	log := "fwupdtool: loading\n{\"result\":\"Scanned\"}\n"
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(log))
	}))

	var body []byte
	if err := client.RequestJSON(http.MethodGet, "/api/v1/namespaces/a/pods/b/log", nil, &body); err != nil || string(body) != log {
		t.Errorf("the body is %q with err %v, want %q", body, err, log)
	}
}

// statusObject has a status field, Pruned, that the fake API server
// leaves out of its response, as a CRD schema prunes an unknown field.
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

// ReplaceStatus sends a PUT to the status subresource. When the write
// succeeds, the object is replaced with the API server's copy, which has
// the new resourceVersion and lacks the field the server dropped. When
// the write fails, the object is unchanged.
func TestReplaceStatusReplacesTheObjectWithTheAPIServersCopy(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		wantErr     error
		wantVersion string
		wantPruned  string
	}{
		{"a write that succeeds", http.StatusOK, nil, "8", ""},
		{"a write from an older copy", http.StatusConflict, ErrConflict, "7", "kept"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var method, path string
			client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				method, path = r.Method, r.URL.Path
				w.WriteHeader(c.status)
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

func TestStaleIsTrueForConflictAndNotFound(t *testing.T) {
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

// The client reads a response body that no caller wants to its end, so
// the next request reuses the connection.
func TestAResponseNoCallerReadsStillReusesTheConnection(t *testing.T) {
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

// A write guard refuses each write and no read, and a refused write
// never reaches the API server.
func TestAWriteGuardRefusesWritesAndNotReads(t *testing.T) {
	var sent []string
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = append(sent, r.Method)
		_, _ = w.Write([]byte(`{}`))
	}))
	refusal := errors.New("the lease is overdue")
	guarded := client.WithWriteGuard(func() error { return refusal })

	if err := guarded.RequestJSON(http.MethodGet, "/api/v1/nodes/node-1", nil, nil); err != nil {
		t.Errorf("a read under the guard returned %v, want no error", err)
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if err := guarded.Request(method, "/api/v1/nodes/node-1", "application/json", []byte(`{}`), nil); !errors.Is(err, refusal) {
			t.Errorf("%s under the guard = %v, want the guard's refusal", method, err)
		}
	}
	if err := client.RequestJSON(http.MethodPut, "/api/v1/nodes/node-1", []byte(`{}`), nil); err != nil {
		t.Errorf("a write from the client with no guard returned %v, want no error", err)
	}
	if want := []string{http.MethodGet, http.MethodPut}; !slices.Equal(sent, want) {
		t.Errorf("the API server received %q, want %q", sent, want)
	}
}
