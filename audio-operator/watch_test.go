package main

// These tests run the handlers of the watches through client-go's real
// reflector, against a scripted API server. The reflector's own loop
// is upstream's to test, and the shared informer package tests the
// copy it keeps and the decode of each object. What these tests prove
// is that each change the API server sends reaches this program's
// handlers, and that each watch carries its selector.
//
// Each test that runs a watch runs in a synctest bubble, against a
// server from apiservertest. synctest.Wait returns once the reflector
// has done all it can do at the present moment, and a time.Sleep
// waits out its backoff on the fake clock.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
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
	apiVersion string
	kind       string
	reads      []string
	scripts    [][]string

	mu        sync.Mutex
	readCount int
	watches   int
	queries   []string
	released  chan struct{}
}

func newWatchServer(collection, apiVersion, kind string, reads []string, scripts ...[]string) *watchServer {
	return &watchServer{
		collection: collection,
		apiVersion: apiVersion,
		kind:       kind,
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

func (s *watchServer) ServeHTTP(t testing.TB, w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	s.mu.Lock()
	s.queries = append(s.queries, r.URL.RawQuery)
	s.mu.Unlock()
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
		fmt.Fprintf(w, `{"type":"BOOKMARK","object":{"apiVersion":%q,"kind":%q,"metadata":{"resourceVersion":%q,"annotations":{"k8s.io/initial-events-end":"true"}}}}`+"\n",
			s.apiVersion, s.kind, version)
	}
	w.(http.Flusher).Flush()

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
}

// release lets a paused stream play the rest of its script.
func (s *watchServer) release() { s.released <- struct{}{} }

// requests answers the query string of each request, in the order they
// came.
func (s *watchServer) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.queries...)
}

// serveCollections answers each watchServer's collection from that
// server, and every other request from rest, or with a 404 when rest
// is nil.
func serveCollections(t testing.TB, rest http.Handler, servers ...*watchServer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, server := range servers {
			if r.URL.Path == server.collection && r.Method == http.MethodGet {
				server.ServeHTTP(t, w, r)
				return
			}
		}
		if rest == nil {
			http.NotFound(w, r)
			return
		}
		rest.ServeHTTP(w, r)
	})
}

// testWatcher serves the handler, and points a dynamic client at it.
func testWatcher(t *testing.T, handler http.Handler) dynamic.Interface {
	t.Helper()
	return dynamicClient(t, apiservertest.Start(t, handler))
}

// dynamicClient points a dynamic client at a server that a test also
// reaches with other clients.
func dynamicClient(t *testing.T, server *apiservertest.Server) dynamic.Interface {
	t.Helper()
	client, err := dynamic.NewForConfig(server.Config())
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

// audio_watch_restarts_total counts a watch reopening, and not the
// watch's first open: the first connection is the start of watching,
// and only a connection the API server or a fault closed is a restart.
func TestAWatchThatReopensCountsOneRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sinks := newWatchServer(SinksPath, EndpointAPIVersion, SinkKind, []string{`[]`},
			[]string{sinkEvent(t, "ADDED", sinkAt("1", 1)), sinkEvent(t, "MODIFIED", sinkAt("1", 2))})
		sources := newWatchServer(SourcesPath, EndpointAPIVersion, SourceKind, []string{`[]`})
		readings := newMetrics("test")
		watchEndpoints(t.Context(), testWatcher(t, serveCollections(t, nil, sinks, sources)),
			"liken-1", func() {}, func() {}, readings)
		time.Sleep(time.Minute)
		synctest.Wait()

		if got := testutil.ToFloat64(readings.watchRestarts.WithLabelValues(SinkKind)); got != 1 {
			t.Errorf("audio_watch_restarts_total{kind=Sink} = %v, want 1", got)
		}
		if got := testutil.ToFloat64(readings.watchRestarts.WithLabelValues(SourceKind)); got != 0 {
			t.Errorf("audio_watch_restarts_total{kind=Source} = %v, want 0", got)
		}
	})
}
