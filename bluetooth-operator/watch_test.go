package main

// This file holds the scripted API server that the tests of the
// watches run client-go's real reflector against. The reflector's own
// loop is upstream's to test, and the shared informer package tests
// the copy it keeps. What the tests of this operator prove is that each
// change the API server sends reaches this operator's handlers.
//
// The server answers over the in-memory connections of apiservertest,
// so each test that runs a watch runs in a synctest bubble.
// synctest.Wait returns once the reflector and the handlers have done
// all they can do at the present moment, and a time.Sleep waits out a
// backoff or a TTL on the fake clock.

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
// the watcher ends the request. Any other script ends the stream after
// its last line, the way the API server does at timeoutSeconds.
const holdOpen = "hold"

// pause, as a line of a script, holds the stream open until the test
// calls release, and then plays the rest of the script.
const pause = "pause"

// watchServer is an API server for one collection. Each read of the
// collection answers the next of reads, and the last one again after
// that. Each watch connection plays the next script of events.
//
// The reflector reads a collection with a streaming list: a watch that
// asks for the initial events. The server answers it the way the API
// server does, with one ADDED event for each object and a bookmark
// that marks the end of the initial events, and then plays the
// connection's script on the same stream. It answers a plain list too,
// which the reflector sends when a streaming list fails.
type watchServer struct {
	collection string
	listKind   string
	reads      []string
	scripts    [][]string

	mu        sync.Mutex
	readCount int
	watches   int
	selectors []string
	holding   int
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

// read answers the objects of the next read, and the version it was
// made at.
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

// hold counts the streams the server holds open.
func (s *watchServer) hold(change int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.holding += change
}

// held answers how many streams the server holds open, and the label
// selector of each request in the order they came.
func (s *watchServer) held() (int, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.holding, append([]string{}, s.selectors...)
}

// opened answers how many watch connections the watcher has opened.
func (s *watchServer) opened() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.watches
}

// testWatcher serves a handler over in-memory connections, and points a
// dynamic client at it.
func testWatcher(t *testing.T, handler http.Handler) dynamic.Interface {
	t.Helper()
	client, err := dynamic.NewForConfig(apiservertest.Start(t, handler).Config())
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// asObject is an object the way the informer hands it to a handler.
func asObject[T any](t *testing.T, item T) *unstructured.Unstructured {
	t.Helper()
	fields, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&item)
	if err != nil {
		t.Fatal(err)
	}
	return &unstructured.Unstructured{Object: fields}
}

func encode(t *testing.T, item any) string {
	t.Helper()
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// woke answers whether the loop has a wake waiting, and takes it. A
// test calls it after synctest.Wait, when every wake that the present
// moment gives has arrived.
func woke(wakes <-chan struct{}) bool {
	select {
	case <-wakes:
		return true
	default:
		return false
	}
}
