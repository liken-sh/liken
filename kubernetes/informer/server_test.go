package informer

// The test kind, and the scripted API server that the tests run the
// real reflector against. The server answers over the in-memory
// connections of apiservertest, so each test runs in a synctest bubble
// and waits out the reflector's backoff on the fake clock.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
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

// serverDown, as a failure, takes the server down, so it closes each
// connection and refuses each new one, the way an API server that
// restarts does.
const serverDown = -1

// holdOpen, as the last line of a script, keeps the stream open until
// the watcher ends the request. Any other script ends the stream after
// its last line, the way the API server does at timeoutSeconds.
const holdOpen = "hold"

// awaitGate, as a line of a script, holds the stream until the test
// closes the server's gate, so an event reaches the watcher only after
// a step of the test, such as the end of the first read.
const awaitGate = "gate"

// watchServer is an API server for one collection. Each read of the
// collection answers the next of reads, and the last one again after
// that. Each watch connection plays the next script of events. While a
// failure is set, the server fails every watch with it.
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
	gate    chan struct{}

	// server serves the collection to the test's client (testWatcher).
	server *apiservertest.Server

	mu        sync.Mutex
	failure   int
	failures  int
	readCount int
	watches   int
	queries   []string
}

func newWatchServer(path string, reads [][]thing, scripts ...[]string) *watchServer {
	return &watchServer{path: path, reads: reads, scripts: scripts, gate: make(chan struct{})}
}

func (s *watchServer) read() ([]thing, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := min(s.readCount, len(s.reads)-1)
	s.readCount++
	return s.reads[index], fmt.Sprint(100 * s.readCount)
}

// failing sets how the server fails each watch from now on: with an
// HTTP status, or by going down. A failure of 0 accepts every watch,
// and brings a server that was down up again.
func (s *watchServer) failing(failure int) {
	s.mu.Lock()
	s.failure = failure
	s.mu.Unlock()
	s.server.SetDown(failure == serverDown)
}

// failed answers how many watches and connections the server refused.
func (s *watchServer) failed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failures
}

func (s *watchServer) countFailure() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures++
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
	if failure > 0 {
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

	script := []string{holdOpen}
	if connection < len(s.scripts) {
		script = s.scripts[connection]
	}
	for _, line := range script {
		if line == holdOpen {
			<-r.Context().Done()
			return
		}
		if line == awaitGate {
			select {
			case <-s.gate:
			case <-r.Context().Done():
				return
			}
			continue
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

func (s *watchServer) sent() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.queries...)
}

// event is one line of a watch stream.
func event(kind string, item thing) string {
	object, _ := json.Marshal(item)
	return fmt.Sprintf(`{"type":%q,"object":%s}`, kind, object)
}

// testWatcher serves the collection, and points a dynamic client at it.
// The server counts each connection it refuses while it is down as a
// failure. A test that calls it runs in a synctest bubble, because its
// cleanup sleeps for a minute.
//
// client-go's reflector waits out its backoff after a refused streaming
// list without reading its context. The backoff is at most 30 seconds
// with full jitter, so the wait lasts less than a minute. The cleanup
// sleeps past it, so the reflector returns before the bubble ends.
func testWatcher(t *testing.T, script *watchServer) dynamic.Interface {
	t.Helper()
	script.server = apiservertest.Start(t, script)
	t.Cleanup(func() { time.Sleep(time.Minute) })
	config := script.server.Config()
	config.Transport = countRefusals{script}
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// countRefusals sends each request to the script's server, and counts
// each connection the server refuses while it is down as a failure.
type countRefusals struct{ script *watchServer }

func (c countRefusals) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := c.script.server.RoundTrip(req)
	if err != nil {
		c.script.countFailure()
	}
	return resp, err
}
