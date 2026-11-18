package main

// watchServer is a scripted API server that the watch tests run
// client-go's reflector against. client-go tests the reflector, and the
// shared informer package tests its cache, so these tests check only
// that each change the server sends reaches this operator's handlers.
//
// The server responds over the in-memory connections of apiservertest,
// so a test that runs a watch can run in a synctest bubble.
// synctest.Wait returns when the reflector and the handlers are all
// blocked, and a time.Sleep waits out a backoff or a TTL on the fake
// clock.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

// holdOpen, as the last line of a script, keeps the stream open until
// the client cancels the request. A script without it ends the stream
// after its last line, as the API server does at timeoutSeconds.
const holdOpen = "hold"

// pause, as a line of a script, keeps the stream open until the test
// calls release, and then plays the rest of the script.
const pause = "pause"

// watchServer serves one collection. Each read of the collection
// returns the next entry of reads, and the last entry again after that.
// Each watch connection plays the next script.
//
// The reflector reads a collection with a streaming list: a watch with
// sendInitialEvents=true. The server responds as the API server does,
// with one ADDED event for each object and a bookmark that marks the end
// of the initial events, and then plays the connection's script on the
// same stream. It also serves a plain list, which the reflector requests
// when a streaming list fails.
type watchServer struct {
	collection string
	listKind   string
	reads      []string
	scripts    [][]string

	mu        sync.Mutex
	readCount int
	watches   int
	selectors []string
	open      int
	released  chan struct{}
}

func newWatchServer(collection, listKind string, reads []string, scripts ...[]string) *watchServer {
	return &watchServer{
		collection: collection,
		listKind:   listKind,
		reads:      reads,
		scripts:    scripts,
		released:   make(chan struct{}, 1),
	}
}

// read returns the objects of the next read, and its resourceVersion.
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
		if r.URL.Path != s.collection {
			http.NotFound(w, r)
			return
		}
		query := r.URL.Query()
		s.mu.Lock()
		s.selectors = append(s.selectors, query.Get("labelSelector"))
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")

		if query.Get("watch") != "true" {
			items, version := s.read(t)
			list, _ := json.Marshal(map[string]any{
				"apiVersion": pairingAPI,
				"kind":       s.listKind,
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
			fmt.Fprintln(w, initialEventsEnd(strings.TrimSuffix(s.listKind, "List"), version))
		}
		w.(http.Flusher).Flush()

		script := []string{holdOpen}
		if connection < len(s.scripts) {
			script = s.scripts[connection]
		}
		for _, line := range script {
			switch line {
			case holdOpen:
				s.hold(1)
				<-r.Context().Done()
				s.hold(-1)
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

// initialEventsEnd is the bookmark that closes the initial events of a
// streaming list.
func initialEventsEnd(kind, version string) string {
	return fmt.Sprintf(`{"type":"BOOKMARK","object":{"apiVersion":%q,"kind":%q,"metadata":{"resourceVersion":%q,"annotations":{"k8s.io/initial-events-end":"true"}}}}`,
		pairingAPI, kind, version)
}

// release lets a paused stream play the rest of its script.
func (s *watchServer) release() { s.released <- struct{}{} }

// hold adds change to the count of streams that holdOpen keeps open.
func (s *watchServer) hold(change int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.open += change
}

// openWatches returns how many streams holdOpen keeps open, and the label
// selector of each request in the order the requests arrived.
func (s *watchServer) openWatches() (int, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open, append([]string{}, s.selectors...)
}

// opened returns how many watch connections the watcher has opened.
func (s *watchServer) opened() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.watches
}

// testWatcher serves handler over in-memory connections and returns a
// dynamic client for it.
func testWatcher(t *testing.T, handler http.Handler) dynamic.Interface {
	t.Helper()
	client, err := dynamic.NewForConfig(apiservertest.Start(t, handler).Config())
	if err != nil {
		t.Fatalf("creating the dynamic client: %v", err)
	}
	return client
}

// asObject converts item to the *unstructured.Unstructured that the
// informer passes to a handler.
func asObject[T any](t *testing.T, item T) *unstructured.Unstructured {
	t.Helper()
	fields, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&item)
	if err != nil {
		t.Fatalf("converting the item to unstructured: %v", err)
	}
	return &unstructured.Unstructured{Object: fields}
}

func encode(t *testing.T, item any) string {
	t.Helper()
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("encoding the item as JSON: %v", err)
	}
	return string(encoded)
}

// woke reports whether a wake is waiting, and receives it. A test calls
// it after synctest.Wait, so every wake due at the current fake time
// has arrived.
func woke(wakes <-chan struct{}) bool {
	select {
	case <-wakes:
		return true
	default:
		return false
	}
}
