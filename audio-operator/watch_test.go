package main

// These tests run the watch handlers through client-go's reflector
// against a scripted API server, to check that each change reaches a
// handler and that each watch sends its selector. They leave the
// reflector's loop to upstream's tests, and the local copy and the
// decoding to the informer package's tests.
//
// Each test that starts a watch runs in a synctest bubble.
// synctest.Wait returns when the reflector is idle at the current fake
// time, and time.Sleep moves the fake clock past the reflector's
// backoff.

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
// the client ends the request. A script without it closes the stream
// after its last line, as the API server does at timeoutSeconds.
const holdOpen = "hold"

// pause, as a line of a script, stops the stream until the test calls
// release.
const pause = "pause"

// watchServer fakes the API server for one collection. Each list
// returns the next entry of reads, and repeats the last entry after
// the end. Each watch connection plays the next script.
//
// The reflector lists with a streaming list, a watch with
// sendInitialEvents=true. For that request the server sends, as the
// API server does, an ADDED event for each object, then a BOOKMARK
// with the annotation k8s.io/initial-events-end, and then the script on
// the same stream. It also serves a plain list, which the reflector
// uses when a streaming list fails.
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

// read returns the objects of the next entry of reads, and a
// resourceVersion that grows with each read.
func (s *watchServer) read(t testing.TB) ([]json.RawMessage, string) {
	s.mu.Lock()
	index := min(s.readCount, len(s.reads)-1)
	s.readCount++
	version := fmt.Sprint(100 * s.readCount)
	s.mu.Unlock()
	var items []json.RawMessage
	if err := json.Unmarshal([]byte(s.reads[index]), &items); err != nil {
		t.Errorf("decoding the server's read %d as a JSON array: %v", index, err)
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

func (s *watchServer) release() { s.released <- struct{}{} }

// requests returns the raw query string of each request, in order.
func (s *watchServer) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.queries...)
}

// serveCollections routes each GET of a server's collection to that
// server, and every other request to rest, or to a 404 when rest is
// nil.
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

func testWatcher(t *testing.T, handler http.Handler) dynamic.Interface {
	t.Helper()
	return dynamicClient(t, apiservertest.Start(t, handler))
}

func dynamicClient(t *testing.T, server *apiservertest.Server) dynamic.Interface {
	t.Helper()
	client, err := dynamic.NewForConfig(server.Config())
	if err != nil {
		t.Fatalf("building the dynamic client: %v", err)
	}
	return client
}

// asObject converts item to the form that the informer passes to a
// handler.
func asObject[T any](t *testing.T, item T) *unstructured.Unstructured {
	t.Helper()
	fields, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&item)
	if err != nil {
		t.Fatalf("converting %T to an unstructured object: %v", item, err)
	}
	return &unstructured.Unstructured{Object: fields}
}

func encode(t *testing.T, item any) string {
	t.Helper()
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("encoding %T: %v", item, err)
	}
	return string(encoded)
}

// audio_watch_restarts_total counts each reconnection after the API
// server or a fault closed the stream. The first connection is not a
// restart.
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
