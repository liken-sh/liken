package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

// objectStore is a fake API server for one collection. It handles get,
// list, watch, create, and update, which are all the requests that the
// watches and the certificate and anchor loops make. The tests run
// client-go's reflector against it, so its watch does what the API
// server's does: a streaming list sends each object as an ADDED event
// and then the bookmark that ends the initial events, and a watch from
// a resourceVersion first sends the changes after that version.
//
// The store filters only by a field selector on metadata.name, and it
// records the query of each list and watch, so a test can check which
// selectors a watch sent. It serves over the in-memory connections of
// apiservertest, so a watch against it can run in a synctest bubble.
type objectStore struct {
	server     *apiservertest.Server
	t          *testing.T
	collection string
	apiVersion string
	kind       string

	mu       sync.Mutex
	version  int
	objects  map[string]map[string]any
	watchers []storeWatcher
	// history is every change in order, so a watch from an older
	// resourceVersion first receives the changes after it, as the API
	// server's watch cache replays them.
	history []storeChange
	// queries holds the query of each list and watch, in order.
	queries []url.Values
}

type storeChange struct {
	name    string
	version int
	line    string
}

// storeWatcher is one open watch. name is the metadata.name of its
// field selector, or empty for the whole collection. An empty line on
// events ends the watch.
type storeWatcher struct {
	name   string
	events chan string
}

func newObjectStore(t *testing.T, collection, apiVersion, kind string) *objectStore {
	t.Helper()
	store := &objectStore{
		t: t, collection: collection, apiVersion: apiVersion, kind: kind,
		objects: map[string]map[string]any{},
	}
	store.server = apiservertest.Start(t, http.HandlerFunc(store.serve))
	return store
}

// newConfigMapStore and newSecretStore return a store for the
// ConfigMaps or the Secrets of one namespace.
func newConfigMapStore(t *testing.T, namespace string) *objectStore {
	return newObjectStore(t, configMapsPath(namespace), "v1", "ConfigMap")
}

func newSecretStore(t *testing.T, namespace string) *objectStore {
	return newObjectStore(t, secretsPath(namespace), "v1", "Secret")
}

func (o *objectStore) client() *apiclient.Client {
	return apiclient.New(apiservertest.Host, o.server.Client(), "")
}

func (o *objectStore) watcher() dynamic.Interface {
	o.t.Helper()
	return dynamicClient(o.t, o.server)
}

// put creates or updates an object and sends the change to the
// watches.
func (o *objectStore) put(object any) {
	o.t.Helper()
	raw, err := json.Marshal(object)
	if err != nil {
		o.t.Fatalf("encoding the object: %v", err)
	}
	var stored map[string]any
	if err := json.Unmarshal(raw, &stored); err != nil {
		o.t.Fatalf("decoding the object: %v", err)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.apply(stored)
}

// remove deletes an object and sends the change to the watches.
func (o *objectStore) remove(name string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	stored := o.objects[name]
	delete(o.objects, name)
	o.version++
	stored["metadata"].(map[string]any)["resourceVersion"] = strconv.Itoa(o.version)
	o.notify("DELETED", stored)
}

// hangUp ends every open watch without an error, as the API server
// does when a watch reaches its timeout.
func (o *objectStore) hangUp() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, watcher := range o.watchers {
		watcher.events <- ""
	}
}

// asked returns the query of each list and watch received so far.
func (o *objectStore) asked() []url.Values {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]url.Values(nil), o.queries...)
}

func (o *objectStore) watching() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.watchers)
}

// apply stores an object with the apiVersion, kind, and new
// resourceVersion that the API server would set, because a watch
// cannot decode an object with no kind. Then it sends the change to the
// watches. The caller holds mu.
func (o *objectStore) apply(stored map[string]any) {
	name := stored["metadata"].(map[string]any)["name"].(string)
	kind := "MODIFIED"
	if _, there := o.objects[name]; !there {
		kind = "ADDED"
	}
	stored["apiVersion"] = o.apiVersion
	stored["kind"] = o.kind
	o.version++
	stored["metadata"].(map[string]any)["resourceVersion"] = strconv.Itoa(o.version)
	o.objects[name] = stored
	o.notify(kind, stored)
}

// notify records a change in history and sends it to each watch whose
// selector matches. The caller holds mu.
func (o *objectStore) notify(kind string, stored map[string]any) {
	line, _ := json.Marshal(map[string]any{"type": kind, "object": stored})
	name := stored["metadata"].(map[string]any)["name"].(string)
	o.history = append(o.history, storeChange{name: name, version: o.version, line: string(line)})
	for _, watcher := range o.watchers {
		if watcher.name == "" || watcher.name == name {
			watcher.events <- string(line)
		}
	}
}

// matching returns the objects that a field selector on name selects,
// or all objects when name is empty. The caller holds mu.
func (o *objectStore) matching(name string) []map[string]any {
	var items []map[string]any
	for key, object := range o.objects {
		if name == "" || key == name {
			items = append(items, object)
		}
	}
	return items
}

// selectedName returns the metadata.name in the request's field
// selector, or empty when the selector does not select by name.
func selectedName(query url.Values) string {
	name, found := strings.CutPrefix(query.Get("fieldSelector"), "metadata.name=")
	if !found {
		return ""
	}
	return name
}

func (o *objectStore) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", jsonMediaType)
	query := r.URL.Query()
	name, named := strings.CutPrefix(r.URL.Path, o.collection+"/")
	switch {
	case r.URL.Path != o.collection && !named:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"kind":"Status","code":404}`)
	case r.Method == http.MethodGet && query.Get("watch") == "true":
		o.stream(w, r)
	case r.Method == http.MethodGet && named:
		// Marshal under the lock, because remove and apply modify a
		// stored object in place.
		o.mu.Lock()
		stored, there := o.objects[name]
		raw, _ := json.Marshal(stored)
		o.mu.Unlock()
		if !there {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"kind":"Status","code":404}`)
			return
		}
		_, _ = w.Write(raw)
	case r.Method == http.MethodGet:
		o.mu.Lock()
		o.queries = append(o.queries, query)
		list := map[string]any{
			"apiVersion": o.apiVersion,
			"kind":       o.kind + "List",
			"metadata":   map[string]any{"resourceVersion": strconv.Itoa(o.version)},
			"items":      o.matching(selectedName(query)),
		}
		raw, _ := json.Marshal(list)
		o.mu.Unlock()
		_, _ = w.Write(raw)
	default:
		var stored map[string]any
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &stored); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		o.mu.Lock()
		o.apply(stored)
		raw, _ := json.Marshal(stored)
		o.mu.Unlock()
		_, _ = w.Write(raw)
	}
}

// stream serves one watch. A streaming list sends the current objects
// and then the bookmark that ends the initial events. A watch from a
// resourceVersion first sends each change after that version.
func (o *objectStore) stream(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	name := selectedName(query)
	events := make(chan string, 64)
	var initial []string
	o.mu.Lock()
	o.queries = append(o.queries, query)
	if query.Get("sendInitialEvents") == "true" {
		for _, stored := range o.matching(name) {
			line, _ := json.Marshal(map[string]any{"type": "ADDED", "object": stored})
			initial = append(initial, string(line))
		}
		initial = append(initial, fmt.Sprintf(
			`{"type":"BOOKMARK","object":{"apiVersion":%q,"kind":%q,"metadata":{"resourceVersion":"%d","annotations":{"k8s.io/initial-events-end":"true"}}}}`,
			o.apiVersion, o.kind, o.version))
	} else {
		from, _ := strconv.Atoi(query.Get("resourceVersion"))
		for _, change := range o.history {
			if (name == "" || change.name == name) && change.version > from {
				initial = append(initial, change.line)
			}
		}
	}
	o.watchers = append(o.watchers, storeWatcher{name: name, events: events})
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		defer o.mu.Unlock()
		for i, watcher := range o.watchers {
			if watcher.events == events {
				o.watchers = append(o.watchers[:i], o.watchers[i+1:]...)
				break
			}
		}
	}()
	w.WriteHeader(http.StatusOK)
	for _, line := range initial {
		fmt.Fprintln(w, line)
	}
	w.(http.Flusher).Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case line := <-events:
			if line == "" {
				return
			}
			fmt.Fprintln(w, line)
			w.(http.Flusher).Flush()
		}
	}
}

// testWatcher returns a dynamic client connected to handler over
// in-memory connections.
func testWatcher(t *testing.T, handler http.Handler) dynamic.Interface {
	t.Helper()
	return dynamicClient(t, apiservertest.Start(t, handler))
}

func dynamicClient(t *testing.T, server *apiservertest.Server) dynamic.Interface {
	t.Helper()
	client, err := dynamic.NewForConfig(server.Config())
	if err != nil {
		t.Fatalf("creating the dynamic client: %v", err)
	}
	return client
}

// idleWatcher returns a dynamic client whose API server sends nothing
// until the request ends, so a test can start a watch that it does not
// read without an error or a log line.
func idleWatcher(t *testing.T) dynamic.Interface {
	t.Helper()
	return testWatcher(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
}

// eventually polls reached until it returns true, and fails the test
// after five seconds.
func eventually(t *testing.T, what string, reached func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !reached() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within five seconds", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
