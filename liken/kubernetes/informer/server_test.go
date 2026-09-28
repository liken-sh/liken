package informer

// The scripted API server the tests run the real reflector against.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

const (
	testAPI  = "test.liken.sh/v1"
	testKind = "Thing"
)

var thingResource = schema.GroupVersionResource{Group: "test.liken.sh", Version: "v1", Resource: "things"}

// thing is the operator's own struct for the test kind.
type thing struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name              string            `json:"name"`
		UID               string            `json:"uid,omitempty"`
		ResourceVersion   string            `json:"resourceVersion,omitempty"`
		Generation        int64             `json:"generation,omitempty"`
		DeletionTimestamp string            `json:"deletionTimestamp,omitempty"`
		Labels            map[string]string `json:"labels,omitempty"`
		ManagedFields     []any             `json:"managedFields,omitempty"`
	} `json:"metadata"`
	Spec struct {
		Size int `json:"size"`
	} `json:"spec"`
	Status struct {
		Phase string `json:"phase,omitempty"`
	} `json:"status"`
}

func newThing(name, version string, generation int64) thing {
	t := thing{APIVersion: testAPI, Kind: testKind}
	t.Metadata.Name = name
	t.Metadata.UID = "uid-" + name
	t.Metadata.ResourceVersion = version
	t.Metadata.Generation = generation
	return t
}

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
	reads   [][]thing
	scripts [][]string

	mu        sync.Mutex
	readCount int
	watches   int
	queries   []string
	opened    chan struct{}
	released  chan struct{}
}

func newWatchServer(reads [][]thing, scripts ...[]string) *watchServer {
	return &watchServer{
		reads:    reads,
		scripts:  scripts,
		opened:   make(chan struct{}, len(scripts)+8),
		released: make(chan struct{}, 1),
	}
}

func (s *watchServer) read() ([]thing, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := min(s.readCount, len(s.reads)-1)
	s.readCount++
	return s.reads[index], fmt.Sprint(100 * s.readCount)
}

func (s *watchServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/apis/test.liken.sh/v1/things" {
		http.NotFound(w, r)
		return
	}
	query := r.URL.Query()
	s.mu.Lock()
	s.queries = append(s.queries, "labelSelector="+query.Get("labelSelector")+" fieldSelector="+query.Get("fieldSelector"))
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")

	if query.Get("watch") != "true" {
		items, version := s.read()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"apiVersion": testAPI, "kind": testKind + "List",
			"metadata": map[string]string{"resourceVersion": version},
			"items":    items,
		})
		return
	}

	s.mu.Lock()
	connection := s.watches
	s.watches++
	s.mu.Unlock()
	if query.Get("sendInitialEvents") == "true" {
		items, version := s.read()
		for _, item := range items {
			fmt.Fprintln(w, event("ADDED", item))
		}
		fmt.Fprintf(w, `{"type":"BOOKMARK","object":{"apiVersion":%q,"kind":%q,"metadata":{"resourceVersion":%q,"annotations":{"k8s.io/initial-events-end":"true"}}}}`+"\n",
			testAPI, testKind, version)
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
}

// release lets a paused stream play the rest of its script.
func (s *watchServer) release() { s.released <- struct{}{} }

func (s *watchServer) sent() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.queries...)
}

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

// event is one line of a watch stream.
func event(kind string, item thing) string {
	object, _ := json.Marshal(item)
	return fmt.Sprintf(`{"type":%q,"object":%s}`, kind, object)
}

// testClient points a dynamic client at a test server.
func testClient(t *testing.T, handler http.Handler) dynamic.Interface {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// settleWakes reads wakes until none arrives for a while. A watch wakes
// the loop at its start, and a test reads those wakes before the events
// it scripts.
func settleWakes(wakes <-chan struct{}) {
	for {
		select {
		case <-wakes:
		case <-time.After(200 * time.Millisecond):
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

// awaitSynced waits until the copy holds the first read.
func awaitSynced(t *testing.T, c *Collection) {
	t.Helper()
	select {
	case <-c.controller.HasSyncedChecker().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the copy never synced")
	}
}
