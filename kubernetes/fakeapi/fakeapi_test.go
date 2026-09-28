package fakeapi

// These tests hold the fake to the answers the operators' tests rely
// on: a list, a read by name, a 404, a create and an update that each
// take a new resourceVersion, and a watch that receives each write.

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	fake := New(map[string]*Collection{
		"/api/v1/nodes": {APIVersion: "v1", Kind: "Node", Items: []map[string]any{Object("v1", "Node", "", "node-1", nil)}},
	})
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return fake, server
}

func request(t *testing.T, method, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
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
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake, server := newTestServer(t)
			if got := request(t, c.method, server.URL+c.path, c.body).StatusCode; got != c.status {
				t.Errorf("status = %d, want %d", got, c.status)
			}
			if want := []string{c.method + " " + c.path}; !slices.Equal(fake.Requests(), want) {
				t.Errorf("requests = %q, want %q", fake.Requests(), want)
			}
		})
	}
}

// A write takes the next resourceVersion, and an open watch receives
// it after the initial events and the bookmark that ends them.
func TestAWatchReceivesEachWrite(t *testing.T) {
	fake, server := newTestServer(t)
	watch := request(t, http.MethodGet, server.URL+"/api/v1/nodes?watch=true", "")
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
	next := func() string {
		select {
		case e := <-events:
			return e
		case <-time.After(5 * time.Second):
			t.Fatal("the watch sent no event")
			return ""
		}
	}
	if first, second := next(), next(); first != "ADDED" || second != "BOOKMARK" {
		t.Fatalf("initial events = %s, %s; want ADDED, BOOKMARK", first, second)
	}

	request(t, http.MethodPut, server.URL+"/api/v1/nodes/node-1", `{"metadata":{"name":"node-1"}}`)

	if got := next(); got != "MODIFIED" {
		t.Errorf("event = %s, want MODIFIED", got)
	}
	if version := fake.ResourceVersion("/api/v1/nodes", "node-1"); version != "11" {
		t.Errorf("resourceVersion = %q, want the next version, 11", version)
	}
	fake.Forget()
	if len(fake.Requests()) != 0 {
		t.Error("Forget left requests behind")
	}

	fake.Hold()
	request(t, http.MethodPut, server.URL+"/api/v1/nodes/node-1", `{"metadata":{"name":"node-1"}}`)
	select {
	case e := <-events:
		t.Fatalf("a held write sent %s", e)
	case <-time.After(100 * time.Millisecond):
	}
	fake.Release()
	if got := next(); got != "MODIFIED" {
		t.Errorf("event after the release = %s, want MODIFIED", got)
	}
}
