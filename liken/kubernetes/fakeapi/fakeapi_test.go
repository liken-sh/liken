package fakeapi

// These tests hold the fake to the answers the operators' tests rely
// on: a list, a read by name, a 404, a create and an update that each
// take a new resourceVersion, and a watch that receives each write.
// The operators' tests serve the fake through apiservertest, and so do
// these.

import (
	"bufio"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

func newTestServer(t *testing.T) (*Server, *http.Client) {
	t.Helper()
	fake := New(map[string]*Collection{
		"/api/v1/nodes": {APIVersion: "v1", Kind: "Node", Items: []map[string]any{Object("v1", "Node", "", "node-1", nil)}},
	})
	return fake, apiservertest.Start(t, fake).Client()
}

func request(t *testing.T, client *http.Client, method, path, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, apiservertest.Host+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestTheFakeAnswersReadsAndWrites(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{"a list", http.MethodGet, "/api/v1/nodes", "", http.StatusOK},
		{"a read by name", http.MethodGet, "/api/v1/nodes/node-1", "", http.StatusOK},
		{"an absent object", http.MethodGet, "/api/v1/nodes/node-9", "", http.StatusNotFound},
		{"an unknown collection", http.MethodGet, "/api/v1/pods/x", "", http.StatusNotFound},
		{"a create", http.MethodPost, "/api/v1/nodes", `{"metadata":{"name":"node-2"}}`, http.StatusCreated},
		{"an update", http.MethodPut, "/api/v1/nodes/node-1/status", `{"metadata":{"name":"node-1"}}`, http.StatusOK},
		{"a delete", http.MethodDelete, "/api/v1/nodes/node-1", "", http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake, client := newTestServer(t)
			if got := request(t, client, c.method, c.path, c.body).StatusCode; got != c.status {
				t.Errorf("status = %d, want %d", got, c.status)
			}
			if want := []string{c.method + " " + c.path}; !slices.Equal(fake.Requests(), want) {
				t.Errorf("requests = %q, want %q", fake.Requests(), want)
			}
		})
	}
}

// A write takes the next resourceVersion, and an open watch receives
// it after the initial events and the bookmark that ends them. A delete
// sends DELETED and removes the object. A held
// write sends nothing until the release.
func TestAWatchReceivesEachWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake, client := newTestServer(t)
		watch := request(t, client, http.MethodGet, "/api/v1/nodes?watch=true", "")
		lines := bufio.NewScanner(watch.Body)
		events := make(chan string, 8)
		go func() {
			for lines.Scan() {
				var event struct {
					Type string `json:"type"`
				}
				_ = json.Unmarshal(lines.Bytes(), &event)
				events <- event.Type
			}
		}()
		// sent waits until the watch has sent all it can, and answers the
		// types of the events it sent since the last call.
		sent := func() []string {
			synctest.Wait()
			var types []string
			for len(events) > 0 {
				types = append(types, <-events)
			}
			return types
		}
		if initial := sent(); !slices.Equal(initial, []string{"ADDED", "BOOKMARK"}) {
			t.Fatalf("initial events = %q; want ADDED, BOOKMARK", initial)
		}

		request(t, client, http.MethodPut, "/api/v1/nodes/node-1", `{"metadata":{"name":"node-1"}}`)

		if got := sent(); !slices.Equal(got, []string{"MODIFIED"}) {
			t.Errorf("events = %q, want MODIFIED", got)
		}
		if version := fake.ResourceVersion("/api/v1/nodes", "node-1"); version != "11" {
			t.Errorf("resourceVersion = %q, want the next version, 11", version)
		}
		fake.Forget()
		if len(fake.Requests()) != 0 {
			t.Error("Forget left requests behind")
		}

		request(t, client, http.MethodDelete, "/api/v1/nodes/node-1", "")
		if got := sent(); !slices.Equal(got, []string{"DELETED"}) {
			t.Errorf("events after the delete = %q, want DELETED", got)
		}
		if got := request(t, client, http.MethodGet, "/api/v1/nodes/node-1", "").StatusCode; got != http.StatusNotFound {
			t.Errorf("a read after the delete answered %d, want 404", got)
		}
		request(t, client, http.MethodPost, "/api/v1/nodes", `{"metadata":{"name":"node-1"}}`)
		sent()

		fake.Hold()
		request(t, client, http.MethodPut, "/api/v1/nodes/node-1", `{"metadata":{"name":"node-1"}}`)
		if held := sent(); len(held) != 0 {
			t.Fatalf("a held write sent %q", held)
		}
		fake.Release()
		if got := sent(); !slices.Equal(got, []string{"MODIFIED"}) {
			t.Errorf("events after the release = %q, want MODIFIED", got)
		}
	})
}

// With CheckVersions, an update from the stored version lands at the
// next version, an update from an older version gets 409, and an update
// that changes nothing keeps the stored version, as the API server
// answers each of them.
func TestCheckVersionsAnswersUpdatesAsTheAPIServerDoes(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		status  int
		version string
	}{
		{"a write from the stored version", `{"metadata":{"name":"node-1","resourceVersion":"5"},"status":{"phase":"Ready"}}`, http.StatusOK, "11"},
		{"a write from an older version", `{"metadata":{"name":"node-1","resourceVersion":"4"},"status":{"phase":"Ready"}}`, http.StatusConflict, "5"},
		{"a write that changes nothing", `{"apiVersion":"v1","kind":"Node","metadata":{"name":"node-1","resourceVersion":"5","uid":"uid-node-1"}}`, http.StatusOK, "5"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake, client := newTestServer(t)
			fake.CheckVersions()

			if got := request(t, client, http.MethodPut, "/api/v1/nodes/node-1/status", c.body).StatusCode; got != c.status {
				t.Errorf("status = %d, want %d", got, c.status)
			}
			if got := fake.ResourceVersion("/api/v1/nodes", "node-1"); got != c.version {
				t.Errorf("resourceVersion = %q, want %q", got, c.version)
			}
		})
	}
}
