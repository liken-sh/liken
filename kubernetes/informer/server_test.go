package informer

// The tests run client-go's reflector against a scripted fake API
// server. The fake serves through apiservertest's in-memory
// connections, so a test in a synctest bubble waits out the reflector's
// backoff on the fake clock.

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

// asObject converts item to the unstructured object that the informer
// passes to a handler.
func asObject(t *testing.T, item thing) *unstructured.Unstructured {
	t.Helper()
	fields, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&item)
	if err != nil {
		t.Fatalf("creating the dynamic client: %v", err)
	}
	return &unstructured.Unstructured{Object: fields}
}

// serverDown, passed to watchServer.failing, makes the server close
// each connection and refuse each new one, like an API server that is
// restarting.
const serverDown = -1

// holdOpen, as the last line of a script, keeps the stream open until
// the client ends the request. Without it, the stream ends after the
// last line, as the API server ends a watch at timeoutSeconds.
const holdOpen = "hold"

// awaitGate, as a line of a script, pauses the stream until the test
// closes the server's gate channel, so a test can send an event only
// after a step such as the end of the first read.
const awaitGate = "gate"

// watchServer is a fake API server for one collection. Each read of the
// collection returns the next element of reads, and the last element
// again after they run out. Each watch connection plays the next script
// of events. While a failure is set, every watch fails with it.
//
// The reflector reads a collection with a streaming list, which is a
// watch with sendInitialEvents set. The server responds like the API
// server does: one ADDED event for each object, a bookmark that ends
// the initial events, and then the connection's script on the same
// stream. It also serves a plain list, which the reflector sends when a
// streaming list fails.
type watchServer struct {
	path    string
	reads   [][]thing
	scripts [][]string
	gate    chan struct{}

	// testWatcher sets server.
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
// HTTP status, or by going down (serverDown). A failure of 0 accepts
// every watch and brings a server that was down back up.
func (s *watchServer) failing(failure int) {
	s.mu.Lock()
	s.failure = failure
	s.mu.Unlock()
	s.server.SetDown(failure == serverDown)
}

// failed returns how many watches and connections the server refused.
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

// fail responds to one watch with the HTTP status, and counts it.
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

// testWatcher serves the collection and returns a dynamic client for
// it. A test that calls it must run in a synctest bubble, because its
// cleanup sleeps for a minute.
//
// After a refused streaming list, client-go's reflector waits out its
// backoff without checking its context. The backoff is at most 30
// seconds with full jitter, so after the minute the reflector has
// returned and the bubble can end.
func testWatcher(t *testing.T, fake *watchServer) dynamic.Interface {
	t.Helper()
	fake.server = apiservertest.Start(t, fake)
	t.Cleanup(func() { time.Sleep(time.Minute) })
	config := fake.server.Config()
	config.Transport = countRefusals{fake}
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		t.Fatalf("converting the item to an unstructured object: %v", err)
	}
	return client
}

// countRefusals counts each request that fails to connect, while the
// server is down, as a failure of the server.
type countRefusals struct{ fake *watchServer }

func (c countRefusals) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := c.fake.server.RoundTrip(req)
	if err != nil {
		c.fake.countFailure()
	}
	return resp, err
}
