package main

// These tests run the handlers of the three watches through client-go's
// real reflector, against a scripted API server. The reflector's own
// loop is upstream's to test; what these tests prove is that each
// change the API server sends reaches this operator's handlers, and
// that an object that does not convert is reported.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
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
	opened    chan struct{}
	released  chan struct{}
}

func newWatchServer(collection, listKind string, reads []string, scripts ...[]string) *watchServer {
	return &watchServer{
		collection: collection,
		listKind:   listKind,
		reads:      reads,
		scripts:    scripts,
		opened:     make(chan struct{}, len(scripts)+8),
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
		s.opened <- struct{}{}

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

// awaitWatches waits until the watcher has opened count more watch
// connections.
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

// testWatcher points a dynamic client at a test server.
func testWatcher(t *testing.T, handler http.Handler) dynamic.Interface {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
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

func awaitWake(t *testing.T, wakes <-chan struct{}, within time.Duration) {
	t.Helper()
	select {
	case <-wakes:
	case <-time.After(within):
		t.Fatalf("the loop had no wake within %s", within)
	}
}

// settleWakes reads wakes until none arrives for quiet. A watch wakes
// the loop at its start, and the test reads those wakes before the
// events it scripts.
func settleWakes(wakes <-chan struct{}, quiet time.Duration) {
	for {
		select {
		case <-wakes:
		case <-time.After(quiet):
			return
		}
	}
}

// wokeWithin answers whether a wake arrives within the given time.
func wokeWithin(wakes <-chan struct{}, within time.Duration) bool {
	select {
	case <-wakes:
		return true
	case <-time.After(within):
		return false
	}
}

// An object that does not convert to the operator's struct is an
// error that names the object, and a handler logs it. A tombstone, which
// the informer hands a handler for an object deleted while the watch
// was down, converts as the object it holds.
func TestAnObjectThatDoesNotConvertIsAnErrorThatNamesIt(t *testing.T) {
	good := Peripheral{APIVersion: pairingAPI, Kind: peripheralKind, Metadata: ObjectMeta{Name: "a0-ab-51-33-b7-12", Generation: 2}}
	mistyped := asObject(t, good)
	if err := unstructured.SetNestedField(mistyped.Object, "two", "metadata", "generation"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		object  any
		wantErr string
	}{
		{name: "an object", object: asObject(t, good)},
		{name: "a tombstone", object: cache.DeletedFinalStateUnknown{Key: good.Metadata.Name, Obj: asObject(t, good)}},
		{name: "a field of the wrong type", object: mistyped, wantErr: "Peripheral a0-ab-51-33-b7-12 does not convert"},
		{name: "something that is not an object", object: "a0-ab-51-33-b7-12", wantErr: "not an object"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := convert[Peripheral](c.object)
			if c.wantErr == "" && (err != nil || got.Metadata.Generation != 2) {
				t.Fatalf("convert = %+v, %v; want generation 2 and no error", got.Metadata, err)
			}
			if c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
				t.Fatalf("convert error = %v, want one that says %q", err, c.wantErr)
			}
		})
	}
}
