package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

// fakeAPI holds objects by path and answers the four verbs the agent
// sends. A POST to a collection stores the object under its name. It
// records each request that is not a GET, so a test can count writes.
type fakeAPI struct {
	mu      sync.Mutex
	objects map[string][]byte
	writes  []string
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method != http.MethodGet {
		f.writes = append(f.writes, r.Method+" "+r.URL.Path)
	}
	switch r.Method {
	case http.MethodGet:
		body, ok := f.objects[r.URL.Path]
		if !ok {
			http.Error(w, `{"kind":"Status","code":404}`, http.StatusNotFound)
			return
		}
		_, _ = w.Write(body)
	case http.MethodPost, http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		path := r.URL.Path
		if r.Method == http.MethodPost {
			var named struct {
				Metadata struct{ Name string } `json:"metadata"`
			}
			_ = json.Unmarshal(body, &named)
			path += "/" + named.Metadata.Name
		}
		f.objects[path] = body
		_, _ = w.Write(body)
	case http.MethodDelete:
		delete(f.objects, r.URL.Path)
		_, _ = w.Write([]byte(`{}`))
	}
}

// put stores an object at a path, as JSON.
func (f *fakeAPI) put(t *testing.T, path string, object any) {
	t.Helper()
	body, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[path] = body
}

// get decodes the object at a path, and reports whether it exists.
func get[T any](t *testing.T, f *fakeAPI, path string) (T, bool) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var out T
	body, ok := f.objects[path]
	if ok {
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatal(err)
		}
	}
	return out, ok
}

// writesTo counts the writes whose method and path start with prefix.
func (f *fakeAPI) writesTo(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, write := range f.writes {
		if strings.HasPrefix(write, prefix) {
			count++
		}
	}
	return count
}

// startAPI serves a fake API server and returns a client of it.
func startAPI(t *testing.T) (*fakeAPI, *apiclient.Client) {
	t.Helper()
	api := &fakeAPI{objects: map[string][]byte{}}
	server := apiservertest.Start(t, api)
	return api, apiclient.New(apiservertest.Host, server.Client(), "")
}

// startAPIAnswering serves an API server that answers every request
// with one status code.
func startAPIAnswering(t *testing.T, code int) (*apiservertest.Server, *apiclient.Client) {
	t.Helper()
	server := apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"kind":"Status","message":"etcdserver: request timed out"}`, code)
	}))
	return server, apiclient.New(apiservertest.Host, server.Client(), "")
}
