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

// objectStore is an API server that holds one collection. It answers a
// get, a list, a watch, a create, and an update, which is every request
// the watches and the certificate and anchor loops make. The tests run
// client-go's real reflector against it, so the watch answers the way
// the API server does: a streaming list sends each object as an ADDED
// event and then a bookmark that ends the initial events, and a watch
// from a version first sends the changes after that version.
//
// A field selector on metadata.name narrows a list and a watch to that
// object. The store keeps every other selector a request carries, so a
// test reads what the watch asked for, and it filters by none of them.
//
// The store answers over the in-memory connections of apiservertest,
// so a test that runs a watch against it can run in a synctest bubble.
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
	// Every change, in order, as the watch line it makes and the
	// version it made, so a watch that starts at an older version
	// first gets the changes it missed, the way the API server's watch
	// cache replays them.
	history []storeChange
	// The query of each list and watch, in order.
	queries []url.Values
}

type storeChange struct {
	name    string
	version int
	line    string
}

// One open watch: the name its field selector names, or none for the
// whole collection, and the lines still to be written to it.
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

// A store of one namespace's ConfigMaps, and one of its Secrets.
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

// put stores an object under its name, the way a writer's create or
// update lands, and tells every open watch.
func (o *objectStore) put(object any) {
	o.t.Helper()
	raw, err := json.Marshal(object)
	if err != nil {
		o.t.Fatal(err)
	}
	var held map[string]any
	if err := json.Unmarshal(raw, &held); err != nil {
		o.t.Fatal(err)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.land(held)
}

// remove deletes an object and tells every open watch.
func (o *objectStore) remove(name string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	held := o.objects[name]
	delete(o.objects, name)
	o.version++
	held["metadata"].(map[string]any)["resourceVersion"] = strconv.Itoa(o.version)
	o.announce("DELETED", held)
}

// hangUp ends every open watch with no error, the way the API server
// ends a watch at its timeout.
func (o *objectStore) hangUp() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, watcher := range o.watchers {
		watcher.events <- ""
	}
}

// asked answers the query of each list and watch so far.
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

// Held under mu. The kind and the version are set on every object, as
// the API server sets them, because a watch cannot decode an object
// that names no kind.
func (o *objectStore) land(held map[string]any) {
	name := held["metadata"].(map[string]any)["name"].(string)
	kind := "MODIFIED"
	if _, there := o.objects[name]; !there {
		kind = "ADDED"
	}
	held["apiVersion"] = o.apiVersion
	held["kind"] = o.kind
	o.version++
	held["metadata"].(map[string]any)["resourceVersion"] = strconv.Itoa(o.version)
	o.objects[name] = held
	o.announce(kind, held)
}

// Held under mu.
func (o *objectStore) announce(kind string, held map[string]any) {
	line, _ := json.Marshal(map[string]any{"type": kind, "object": held})
	name := held["metadata"].(map[string]any)["name"].(string)
	o.history = append(o.history, storeChange{name: name, version: o.version, line: string(line)})
	for _, watcher := range o.watchers {
		if watcher.name == "" || watcher.name == name {
			watcher.events <- string(line)
		}
	}
}

// Held under mu. The objects a request's field selector names.
func (o *objectStore) matching(name string) []map[string]any {
	var items []map[string]any
	for held, object := range o.objects {
		if name == "" || held == name {
			items = append(items, object)
		}
	}
	return items
}

// selectedName is the name a request's field selector names, and
// empty for a request that names none.
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
		// Each answer marshals under the lock, because remove and land
		// change a stored object in place, and an encode after the
		// unlock would read it while they write.
		o.mu.Lock()
		held, there := o.objects[name]
		raw, _ := json.Marshal(held)
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
		var held map[string]any
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &held); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		o.mu.Lock()
		o.land(held)
		raw, _ := json.Marshal(held)
		o.mu.Unlock()
		_, _ = w.Write(raw)
	}
}

// stream answers one watch. A streaming list sends the objects as they
// stand and the bookmark that ends them. A watch from a version first
// sends each change after it.
func (o *objectStore) stream(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	name := selectedName(query)
	events := make(chan string, 64)
	var initial []string
	o.mu.Lock()
	o.queries = append(o.queries, query)
	if query.Get("sendInitialEvents") == "true" {
		for _, held := range o.matching(name) {
			line, _ := json.Marshal(map[string]any{"type": "ADDED", "object": held})
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

// testWatcher serves the handler over in-memory connections, and
// points a dynamic client at it.
func testWatcher(t *testing.T, handler http.Handler) dynamic.Interface {
	t.Helper()
	return dynamicClient(t, apiservertest.Start(t, handler))
}

func dynamicClient(t *testing.T, server *apiservertest.Server) dynamic.Interface {
	t.Helper()
	client, err := dynamic.NewForConfig(server.Config())
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// idleWatcher is a dynamic client whose API server answers nothing
// until the request ends. A test that starts a watch it does not read
// gets no error from it and no log line.
func idleWatcher(t *testing.T) dynamic.Interface {
	t.Helper()
	return testWatcher(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
}

// eventually waits for a condition a loop reaches on a goroutine of
// its own, and fails the test when five seconds pass first.
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
