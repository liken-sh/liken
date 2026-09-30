package watch

// The scripted API server the tests run the real reflector against.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"testing/synctest"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/informer"
)

const (
	testAPI  = "test.liken.sh/v1"
	testKind = "Thing"
)

var thingResource = schema.GroupVersionResource{Group: "test.liken.sh", Version: "v1", Resource: "things"}

// thingMeta is the metadata of the test kind.
type thingMeta struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace,omitempty"`
	UID               string            `json:"uid,omitempty"`
	ResourceVersion   string            `json:"resourceVersion,omitempty"`
	Generation        int64             `json:"generation,omitempty"`
	DeletionTimestamp string            `json:"deletionTimestamp,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	ManagedFields     []any             `json:"managedFields,omitempty"`
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

func (t *thing) GetObjectMeta() informer.Meta { return &t.Metadata }

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
	released  chan struct{}
}

func newWatchServer(reads [][]thing, scripts ...[]string) *watchServer {
	return &watchServer{
		reads:    reads,
		scripts:  scripts,
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

// event is one line of a watch stream.
func event(kind string, item thing) string {
	object, _ := json.Marshal(item)
	return fmt.Sprintf(`{"type":%q,"object":%s}`, kind, object)
}

// testWatcher points a dynamic client at a test server.
func testWatcher(t *testing.T, handler http.Handler) dynamic.Interface {
	t.Helper()
	client, err := dynamic.NewForConfig(apiservertest.Start(t, handler).Config())
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// woke waits until the watch has done all it can, and answers whether
// it woke the loop since the last call. The wake channel holds one
// wake, so the wakes of one moment count once.
func woke(wakes <-chan struct{}) bool {
	synctest.Wait()
	select {
	case <-wakes:
		return true
	default:
		return false
	}
}

// awaitReady waits until the watch has done all it can, and checks that
// the store holds the first read and the API server accepted a watch.
func awaitReady(t *testing.T, c *informer.Collection) {
	t.Helper()
	synctest.Wait()
	if !c.View().Ready() {
		t.Fatal("the copy never became ready")
	}
}
