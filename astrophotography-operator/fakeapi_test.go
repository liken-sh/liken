package main

// A fake API server that holds every collection the operator watches,
// and answers a list and a streaming watch of each, with a label or a
// field selector of the form key=value. It records each request, so a
// test reads what the operator asked. It answers over the in-memory
// connections of apiservertest, so each test runs in a synctest bubble
// on the fake clock.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
)

const (
	operatorPodName = "astrophotography-operator-0"
	operatorNS      = "liken-system"
)

// collection is one collection's URL and the apiVersion and kind of its
// objects, which a list and a bookmark carry.
type collection struct {
	path       string
	apiVersion string
	kind       string
}

// collectionOf answers the cluster-wide collection of a watch.
func collectionOf(w watched) collection {
	r := w.source.Resource
	if r.Group == "" {
		return collection{path: "/api/" + r.Version + "/" + r.Resource, apiVersion: r.Version, kind: w.kind}
	}
	return collection{path: "/apis/" + r.Group + "/" + r.Version + "/" + r.Resource, apiVersion: r.Group + "/" + r.Version, kind: w.kind}
}

type fakeEvent struct {
	collection string
	kind       string
	object     map[string]any
	version    int
}

type fakeAPI struct {
	server *apiservertest.Server

	mu          sync.Mutex
	version     int
	collections map[string]collection
	objects     map[string]map[string]map[string]any // collection, key, object
	events      []fakeEvent
	changed     chan struct{}
	// requests holds each request's method and URL, in order.
	requests []string
	// absent holds the collections whose kind the cluster does not
	// serve, which answer 404 the way an uninstalled CRD does.
	absent map[string]bool
	// recorded holds the Events that the operator writes.
	recorded eventstest.Events
	// refusing makes every write to a collection answer 500, the way an
	// API server that cannot reach etcd does (fakeobjects_test.go).
	refusing bool
	// objectHooks script the status writes (fakewrites_test.go).
	objectHooks
}

func startFakeAPI(t *testing.T) *fakeAPI {
	api := &fakeAPI{
		collections: map[string]collection{},
		objects:     map[string]map[string]map[string]any{},
		changed:     make(chan struct{}),
		absent:      map[string]bool{},
	}
	for _, w := range watches() {
		c := collectionOf(w)
		api.collections[c.path] = c
		api.objects[c.path] = map[string]map[string]any{}
	}
	api.server = apiservertest.Start(t, api.recorded.Around(api))
	// The reflector waits out its backoff after a refused list without
	// reading its context, so the bubble waits past it (apiservertest).
	t.Cleanup(func() { time.Sleep(time.Minute) })
	return api
}

// watcher is a client of the watches, as informer.InCluster builds one
// in a pod.
func (a *fakeAPI) watcher(t *testing.T) dynamic.Interface {
	t.Helper()
	client, err := dynamic.NewForConfig(a.server.Config())
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// drop makes a collection one whose kind the cluster does not serve.
func (a *fakeAPI) drop(path string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.absent[path] = true
}

// put stores an object in a collection, created or replaced, and tells
// each watch.
func (a *fakeAPI) put(path string, object map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	metadata := object["metadata"].(map[string]any)
	key := fmt.Sprint(metadata["namespace"], "/", metadata["name"])
	kind := "ADDED"
	if _, held := a.objects[path][key]; held {
		kind = "MODIFIED"
	}
	c := a.collections[path]
	object["apiVersion"], object["kind"] = c.apiVersion, c.kind
	a.store(path, key, object, kind)
}

// remove deletes an object from a collection, and tells each watch.
func (a *fakeAPI) remove(path, namespace, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := namespace + "/" + name
	a.store(path, key, a.objects[path][key], "DELETED")
}

// store records an object at the next version and tells each watch.
// Callers hold mu.
func (a *fakeAPI) store(path, key string, object map[string]any, kind string) {
	a.version++
	object = clone(object)
	object["metadata"].(map[string]any)["resourceVersion"] = strconv.Itoa(a.version)
	if kind == "DELETED" {
		delete(a.objects[path], key)
	} else {
		a.objects[path][key] = object
	}
	a.events = append(a.events, fakeEvent{collection: path, kind: kind, object: clone(object), version: a.version})
	close(a.changed)
	a.changed = make(chan struct{})
}

func clone(object map[string]any) map[string]any {
	var out map[string]any
	body, _ := json.Marshal(object)
	_ = json.Unmarshal(body, &out)
	return out
}

// requested answers the URL of each request to a collection, in order.
func (a *fakeAPI) requested(path string) []url.URL {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []url.URL
	for _, request := range a.requests {
		u, _ := url.Parse(strings.TrimPrefix(request, "GET "))
		if u.Path == path {
			out = append(out, *u)
		}
	}
	return out
}

// requestCount answers how many requests the server received.
func (a *fakeAPI) requestCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.requests)
}

func (a *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.requests = append(a.requests, r.Method+" "+r.URL.String())
	_, known := a.collections[r.URL.Path]
	absent := a.absent[r.URL.Path]
	a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if a.serveObject(w, r) {
		return
	}
	switch {
	case !known || absent || r.Method != http.MethodGet:
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`)
	case r.URL.Query().Get("watch") == "true":
		a.watch(w, r)
	default:
		a.list(w, r)
	}
}

// selects answers whether an object matches the request's label and
// field selectors, each of the form key=value. A field selector's key
// is a path of the object, such as spec.holder.
func selects(query url.Values, object map[string]any) bool {
	if key, value, found := strings.Cut(query.Get("labelSelector"), "="); found {
		labels, _ := object["metadata"].(map[string]any)["labels"].(map[string]any)
		if labels[key] != value {
			return false
		}
	}
	if key, value, found := strings.Cut(query.Get("fieldSelector"), "="); found {
		var held any = object
		for step := range strings.SplitSeq(key, ".") {
			fields, _ := held.(map[string]any)
			held = fields[step]
		}
		if held != value {
			return false
		}
	}
	return true
}

func (a *fakeAPI) list(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.collections[r.URL.Path]
	items := []any{}
	for _, object := range a.objects[c.path] {
		if selects(r.URL.Query(), object) {
			items = append(items, object)
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"apiVersion": c.apiVersion, "kind": c.kind + "List",
		"metadata": map[string]any{"resourceVersion": strconv.Itoa(a.version)},
		"items":    items,
	})
}

// watch answers a streaming list the way the API server does: an ADDED
// event for each object, a bookmark that ends the initial events, and
// then every later change. A watch from a version sends each change
// after that version.
func (a *fakeAPI) watch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	a.mu.Lock()
	c := a.collections[r.URL.Path]
	from, _ := strconv.Atoi(query.Get("resourceVersion"))
	if query.Get("sendInitialEvents") == "true" {
		for _, object := range a.objects[c.path] {
			if selects(query, object) {
				writeEvent(w, "ADDED", object)
			}
		}
		from = a.version
		writeEvent(w, "BOOKMARK", map[string]any{"apiVersion": c.apiVersion, "kind": c.kind, "metadata": map[string]any{
			"resourceVersion": strconv.Itoa(from),
			"annotations":     map[string]any{"k8s.io/initial-events-end": "true"},
		}})
	}
	a.mu.Unlock()
	w.(http.Flusher).Flush()
	for {
		a.mu.Lock()
		changed := a.changed
		for _, event := range a.events {
			if event.collection == c.path && event.version > from {
				if selects(query, event.object) {
					writeEvent(w, event.kind, event.object)
				}
				from = event.version
			}
		}
		a.mu.Unlock()
		w.(http.Flusher).Flush()
		select {
		case <-changed:
		case <-r.Context().Done():
			return
		}
	}
}

func writeEvent(w http.ResponseWriter, kind string, object map[string]any) {
	body, _ := json.Marshal(map[string]any{"type": kind, "object": object})
	_, _ = fmt.Fprintf(w, "%s\n", body)
}

// object builds an object's metadata and spec.
func object(namespace, name string, labels map[string]any, spec map[string]any) map[string]any {
	return map[string]any{
		"metadata": map[string]any{"name": name, "namespace": namespace, "uid": path.Join(namespace, name), "labels": labels},
		"spec":     spec,
	}
}
