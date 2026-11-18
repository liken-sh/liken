package main

// The watch tests run client-go's reflector against a scripted API
// server, to check which changes the server sends wake the loop.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// holdOpen, as the last line of a script, keeps the stream open until
// the watcher ends the request. A script without it ends the stream
// after its last line, as the API server does at timeoutSeconds.
const holdOpen = "hold"

// pause, as a line of a script, stops the stream until the test calls
// release.
const pause = "pause"

// watchServer is a fake API server for one collection. Each read of the
// collection returns the next entry of reads, and the last entry again
// after that. Each watch connection plays the next script of events.
//
// The reflector lists with a watch that sets sendInitialEvents. The
// server responds as the API server does: one ADDED event for each
// object, a bookmark that ends the initial events, and then the
// connection's script. It also serves a plain list, which the reflector
// falls back to when a streaming list fails.
type watchServer struct {
	path       string
	apiVersion string
	kind       string
	reads      []string
	scripts    [][]string

	mu        sync.Mutex
	readCount int
	watches   int
	opened    chan struct{}
	released  chan struct{}
}

func newWatchServer(path, apiVersion, kind string, reads []string, scripts ...[]string) *watchServer {
	return &watchServer{
		path:       path,
		apiVersion: apiVersion,
		kind:       kind,
		reads:      reads,
		scripts:    scripts,
		opened:     make(chan struct{}, len(scripts)+64),
		released:   make(chan struct{}, 1),
	}
}

func (s *watchServer) read(t testing.TB) ([]json.RawMessage, string) {
	s.mu.Lock()
	index := min(s.readCount, len(s.reads)-1)
	s.readCount++
	version := fmt.Sprint(100 * s.readCount)
	s.mu.Unlock()
	var items []json.RawMessage
	if err := json.Unmarshal([]byte(s.reads[index]), &items); err != nil {
		t.Errorf("the server's read %d is not a JSON array: %v", index, err)
	}
	return items, version
}

func (s *watchServer) handler(t testing.TB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != s.path {
			http.NotFound(w, r)
			return
		}
		query := r.URL.Query()
		w.Header().Set("Content-Type", "application/json")

		if query.Get("watch") != "true" {
			items, version := s.read(t)
			list, _ := json.Marshal(map[string]any{
				"apiVersion": s.apiVersion,
				"kind":       s.kind + "List",
				"metadata":   map[string]string{"resourceVersion": version},
				"items":      items,
			})
			_, _ = w.Write(list)
			return
		}

		s.mu.Lock()
		connection := s.watches
		s.watches++
		s.mu.Unlock()
		if query.Get("sendInitialEvents") == "true" {
			items, version := s.read(t)
			for _, item := range items {
				fmt.Fprintf(w, `{"type":"ADDED","object":%s}`+"\n", item)
			}
			fmt.Fprintln(w, initialEventsEnd(s.apiVersion, s.kind, version))
		}
		w.(http.Flusher).Flush()
		s.opened <- struct{}{}

		script := []string{holdOpen}
		if connection < len(s.scripts) {
			script = s.scripts[connection]
		}
		for _, line := range script {
			switch line {
			case holdOpen:
				<-r.Context().Done()
				return
			case pause:
				select {
				case <-s.released:
				case <-r.Context().Done():
					return
				}
			default:
				fmt.Fprintln(w, line)
				w.(http.Flusher).Flush()
			}
		}
	})
}

func initialEventsEnd(apiVersion, kind, version string) string {
	return fmt.Sprintf(`{"type":"BOOKMARK","object":{"apiVersion":%q,"kind":%q,"metadata":{"resourceVersion":%q,"annotations":{"k8s.io/initial-events-end":"true"}}}}`,
		apiVersion, kind, version)
}

func (s *watchServer) release() { s.released <- struct{}{} }

// awaitWatches waits until the watcher has opened count more watches.
func (s *watchServer) awaitWatches(t *testing.T, count int) {
	t.Helper()
	for range count {
		select {
		case <-s.opened:
		case <-time.After(5 * time.Second):
			t.Fatalf("the watcher opened fewer than %d watches", count)
		}
	}
}

func startWatchServer(t *testing.T, s *watchServer) *Client {
	t.Helper()
	return testAPIClient(t, s.handler(t))
}

// watchFunc is the shape of each watch this operator runs.
type watchFunc func(ctx context.Context, client *Client, wake chan<- struct{}, restarted func(), held *watchStore)

// runWatch runs watch until the test ends, and waits for it to return
// before the test server closes.
func runWatch(t *testing.T, client *Client, watch watchFunc, restarted func()) <-chan struct{} {
	t.Helper()
	return runHeldWatch(t, client, watch, restarted, nil)
}

// runHeldWatch is runWatch for a watch that fills a watchStore.
func runHeldWatch(t *testing.T, client *Client, watch watchFunc, restarted func(), held *watchStore) <-chan struct{} {
	t.Helper()
	wake := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		watch(ctx, client, wake, restarted, held)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(testTimeout):
			t.Error("the watch did not stop")
		}
	})
	return wake
}

// asObject converts an item to the unstructured object that an
// informer passes to a handler.
func asObject[T any](t *testing.T, item T) *unstructured.Unstructured {
	t.Helper()
	fields, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&item)
	if err != nil {
		t.Fatalf("converting the item to an unstructured object: %v", err)
	}
	return &unstructured.Unstructured{Object: fields}
}

func encodeAll(t *testing.T, items ...any) string {
	t.Helper()
	encoded := make([]string, 0, len(items))
	for _, item := range items {
		one, err := json.Marshal(item)
		if err != nil {
			t.Fatalf("encoding an item: %v", err)
		}
		encoded = append(encoded, string(one))
	}
	return "[" + strings.Join(encoded, ",") + "]"
}

func event(t *testing.T, kind string, object any) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"type": kind, "object": object})
	if err != nil {
		t.Fatalf("encoding the %s event: %v", kind, err)
	}
	return string(encoded)
}

// watchQuiet is how long a test waits before it concludes that nothing
// woke the loop.
const watchQuiet = 300 * time.Millisecond

func awaitWake(t *testing.T, wakes <-chan struct{}, within time.Duration) {
	t.Helper()
	select {
	case <-wakes:
	case <-time.After(within):
		t.Fatalf("the loop had no wake within %s", within)
	}
}

// settleWakes reads wakes until none arrives for quiet. A watch wakes
// the loop when it starts, so a test drains those wakes before it
// checks the ones its script causes.
func settleWakes(wakes <-chan struct{}, quiet time.Duration) {
	for {
		select {
		case <-wakes:
		case <-time.After(quiet):
			return
		}
	}
}

func wokeWithin(wakes <-chan struct{}, within time.Duration) bool {
	select {
	case <-wakes:
		return true
	case <-time.After(within):
		return false
	}
}
