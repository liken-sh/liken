package informer

// The test kind, and the scripted API server that the tests run the
// real reflector against.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

const (
	testAPI  = "test.liken.sh/v1"
	testKind = "Thing"
)

var thingResource = schema.GroupVersionResource{Group: "test.liken.sh", Version: "v1", Resource: "things"}

type thingMeta struct {
	Name            string `json:"name"`
	Namespace       string `json:"namespace,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
	Generation      int64  `json:"generation,omitempty"`
	ManagedFields   []any  `json:"managedFields,omitempty"`
}

func (m *thingMeta) GetName() string            { return m.Name }
func (m *thingMeta) GetNamespace() string       { return m.Namespace }
func (m *thingMeta) GetResourceVersion() string { return m.ResourceVersion }

// thing is the operator's own struct for the test kind.
type thing struct {
	APIVersion string    `json:"apiVersion"`
	Kind       string    `json:"kind"`
	Metadata   thingMeta `json:"metadata"`
	Spec       struct {
		Size int `json:"size"`
	} `json:"spec"`
	Status struct {
		Phase string `json:"phase,omitempty"`
	} `json:"status"`
}

func (t *thing) GetObjectMeta() Meta { return &t.Metadata }

func newThing(name, version string, generation int64) thing {
	t := thing{APIVersion: testAPI, Kind: testKind}
	t.Metadata.Name = name
	t.Metadata.ResourceVersion = version
	t.Metadata.Generation = generation
	return t
}

// asObject is an object the way the informer hands it to a handler.
func asObject(t *testing.T, item thing) *unstructured.Unstructured {
	t.Helper()
	fields, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&item)
	if err != nil {
		t.Fatal(err)
	}
	return &unstructured.Unstructured{Object: fields}
}

// serverDown, as a failure, refuses each connection to the server, the
// way a client finds an API server that restarts.
const serverDown = -1

// holdOpen, as the last line of a script, keeps the stream open until
// the watcher ends the request. Any other script ends the stream after
// its last line, the way the API server does at timeoutSeconds.
const holdOpen = "hold"

// watchServer is an API server for one collection. Each read of the
// collection answers the next of reads, and the last one again after
// that. Each watch connection plays the next script of events. While
// refuse is set, the server answers every watch with 403.
//
// The reflector reads a collection with a streaming list: a watch that
// asks for the initial events. The server answers it the way the API
// server does, with one ADDED event for each object and a bookmark
// that marks the end of the initial events, and then plays the
// connection's script on the same stream. It answers a plain list too,
// which the reflector sends when a streaming list fails.
type watchServer struct {
	path    string
	reads   [][]thing
	scripts [][]string

	mu        sync.Mutex
	failure   int
	spared    int
	readCount int
	watches   int
	queries   []string
	opened    chan struct{}
	failed    chan struct{}

	// refused is a port that no process listens on.
	refused string
}

func newWatchServer(path string, reads [][]thing, scripts ...[]string) *watchServer {
	return &watchServer{path: path, reads: reads, scripts: scripts, opened: make(chan struct{}, len(scripts)+8), failed: make(chan struct{}, 1)}
}

// fails reports whether the server fails now with the failure.
func (s *watchServer) fails(failure int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failure == failure && s.watches >= s.spared
}

func (s *watchServer) read() ([]thing, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := min(s.readCount, len(s.reads)-1)
	s.readCount++
	return s.reads[index], fmt.Sprint(100 * s.readCount)
}

// failing sets how the server fails each watch after the first spared
// watches it accepted. A failure of 0 accepts every watch.
func (s *watchServer) failing(failure, spared int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failure, s.spared = failure, spared
}

func (s *watchServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.path {
		http.NotFound(w, r)
		return
	}
	query := r.URL.Query()
	s.mu.Lock()
	s.queries = append(s.queries, "labelSelector="+query.Get("labelSelector")+" fieldSelector="+query.Get("fieldSelector"))
	failure := s.failure
	fail := failure > 0 && s.watches >= s.spared
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
	if fail {
		s.fail(w, failure)
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
		if line == holdOpen {
			<-r.Context().Done()
			return
		}
		fmt.Fprintln(w, line)
		w.(http.Flusher).Flush()
	}
}

// fail answers one watch with the HTTP status, and counts it.
func (s *watchServer) fail(w http.ResponseWriter, status int) {
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":%q,"code":%d}`,
		strings.ReplaceAll(http.StatusText(status), " ", ""), status)
	s.countFailure()
}

func (s *watchServer) countFailure() {
	select {
	case s.failed <- struct{}{}:
	default:
	}
}

// dial connects a client to the server, or to the refused port while
// the server is down.
func (s *watchServer) dial(ctx context.Context, network, address string) (net.Conn, error) {
	if s.fails(serverDown) {
		address = s.refused
		defer s.countFailure()
	}
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

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

// awaitFailures waits until the server has failed count more watches.
func (s *watchServer) awaitFailures(t *testing.T, count int) {
	t.Helper()
	for range count {
		select {
		case <-s.failed:
		case <-time.After(10 * time.Second):
			t.Fatalf("the server failed fewer than %d watches", count)
		}
	}
}

// event is one line of a watch stream.
func event(kind string, item thing) string {
	object, _ := json.Marshal(item)
	return fmt.Sprintf(`{"type":%q,"object":%s}`, kind, object)
}

// testWatcher points a dynamic client at a test server.
func testWatcher(t *testing.T, script *watchServer) dynamic.Interface {
	t.Helper()
	server := httptest.NewUnstartedServer(script)
	server.Config.SetKeepAlivesEnabled(false)
	server.Start()
	t.Cleanup(server.Close)
	script.refused = refusedAddress(t)
	client, err := dynamic.NewForConfig(&rest.Config{Host: server.URL, Dial: script.dial})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// refusedAddress is a local port that no process listens on, so the
// kernel refuses each connection to it.
func refusedAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

// eventually waits until the condition holds.
func eventually(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within 5s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
