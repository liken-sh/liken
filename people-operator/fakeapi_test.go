package main

// A fake API server that holds the people and the pods, and serves the
// requests the operator sends: a list and a watch of each collection,
// a status write, and a pod's create, read, log, and delete. It answers
// over the in-memory connections of apiservertest, so each test runs in
// a synctest bubble on the fake clock. A test plays the kubelet: it
// finishes a baker pod and writes the pod's log.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
)

const (
	podsPath        = "/api/v1/pods"
	namespacesPath  = "/api/v1/namespaces/"
	operatorPodName = "people-operator-0"
	operatorNS      = "liken-system"
	operatorImage   = "ghcr.io/liken-sh/people-operator:test"
)

// kinds names the apiVersion and kind of each collection's objects,
// which a list and a bookmark carry.
var kinds = map[string][2]string{
	peoplePath: {personAPIVersion, personKind},
	podsPath:   {"v1", "Pod"},
}

type fakeEvent struct {
	collection string
	kind       string
	object     map[string]any
	version    int
}

type fakeAPI struct {
	server *apiservertest.Server

	// recorded holds the Events the operator posts.
	recorded *eventstest.Events

	mu      sync.Mutex
	version int
	objects map[string]map[string]map[string]any // collection, key, object
	logs    map[string]string                    // pod key, log
	events  []fakeEvent
	changed chan struct{}

	// refusals is how many status writes the server refuses next, the
	// way an API server that restarts does.
	refusals int

	// absent holds the namespaces that do not exist, where a pod
	// create answers 404 as a real API server does.
	absent map[string]bool
}

// dropNamespace makes a namespace one that does not exist.
func (a *fakeAPI) dropNamespace(namespace string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.absent[namespace] = true
}

// refuseStatusWrites makes the server refuse the next status writes.
func (a *fakeAPI) refuseStatusWrites(count int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refusals = count
}

func startFakeAPI(t *testing.T) *fakeAPI {
	api := &fakeAPI{
		objects: map[string]map[string]map[string]any{peoplePath: {}, podsPath: {}},
		logs:    map[string]string{},
		changed: make(chan struct{}),
		absent:  map[string]bool{},

		recorded: &eventstest.Events{},
	}
	api.server = apiservertest.Start(t, api.recorded.Around(api))
	// The reflector waits out its backoff after a refused list without
	// reading its context, so the bubble waits past it (apiservertest).
	t.Cleanup(func() { time.Sleep(time.Minute) })
	// The operator's own pod, which names the image a baker pod runs.
	api.store(podsPath, operatorNS+"/"+operatorPodName, map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": operatorPodName, "namespace": operatorNS},
		"spec":     map[string]any{"containers": []any{map[string]any{"name": "operator", "image": operatorImage}}},
	}, "ADDED")
	return api
}

// store records an object at the next version and tells each watch.
func (a *fakeAPI) store(collection, key string, object map[string]any, kind string) map[string]any {
	a.version++
	object = clone(object)
	metadata := object["metadata"].(map[string]any)
	metadata["resourceVersion"] = strconv.Itoa(a.version)
	if kind == "DELETED" {
		delete(a.objects[collection], key)
	} else {
		a.objects[collection][key] = object
	}
	a.events = append(a.events, fakeEvent{collection: collection, kind: kind, object: clone(object), version: a.version})
	close(a.changed)
	a.changed = make(chan struct{})
	return object
}

func clone(object map[string]any) map[string]any {
	var out map[string]any
	body, _ := json.Marshal(object)
	_ = json.Unmarshal(body, &out)
	return out
}

func (a *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path
	switch {
	case path == peoplePath || path == podsPath:
		if r.URL.Query().Get("watch") == "true" {
			a.watch(w, r, path)
			return
		}
		a.list(w, r, path)
	case strings.HasPrefix(path, peoplePath+"/"):
		name, status := strings.CutSuffix(strings.TrimPrefix(path, peoplePath+"/"), "/status")
		if status && r.Method == http.MethodPut {
			a.writeStatus(w, r, name)
			return
		}
		a.get(w, peoplePath, name)
	case strings.HasPrefix(path, namespacesPath):
		a.servePod(w, r, strings.Split(strings.TrimPrefix(path, namespacesPath), "/"))
	default:
		http.NotFound(w, r)
	}
}

// servePod answers namespaces/<ns>/pods, namespaces/<ns>/pods/<name>,
// and namespaces/<ns>/pods/<name>/log.
func (a *fakeAPI) servePod(w http.ResponseWriter, r *http.Request, parts []string) {
	switch {
	case len(parts) == 2 && r.Method == http.MethodPost:
		a.createPod(w, r, parts[0])
	case len(parts) == 3 && r.Method == http.MethodDelete:
		a.deletePod(w, parts[0]+"/"+parts[2])
	case len(parts) == 3:
		a.get(w, podsPath, parts[0]+"/"+parts[2])
	case len(parts) == 4 && parts[3] == "log":
		a.mu.Lock()
		log, held := a.logs[parts[0]+"/"+parts[2]]
		_, exists := a.objects[podsPath][parts[0]+"/"+parts[2]]
		a.mu.Unlock()
		if !held || !exists {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = fmt.Fprint(w, log)
	default:
		http.NotFound(w, r)
	}
}

// selects answers whether an object carries the label a selector of
// the form key=value names. An empty selector selects every object.
func selects(selector string, object map[string]any) bool {
	key, value, found := strings.Cut(selector, "=")
	if !found {
		return true
	}
	labels, _ := object["metadata"].(map[string]any)["labels"].(map[string]any)
	return labels[key] == value
}

func (a *fakeAPI) list(w http.ResponseWriter, r *http.Request, collection string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	items := []any{}
	for _, object := range a.objects[collection] {
		if selects(r.URL.Query().Get("labelSelector"), object) {
			items = append(items, object)
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"apiVersion": kinds[collection][0], "kind": kinds[collection][1] + "List",
		"metadata": map[string]any{"resourceVersion": strconv.Itoa(a.version)},
		"items":    items,
	})
}

// watch answers a streaming list the way the API server does: an ADDED
// event for each object, a bookmark that ends the initial events, and
// then every later change. A watch from a version sends each change
// after that version.
func (a *fakeAPI) watch(w http.ResponseWriter, r *http.Request, collection string) {
	selector := r.URL.Query().Get("labelSelector")
	a.mu.Lock()
	from, _ := strconv.Atoi(r.URL.Query().Get("resourceVersion"))
	if r.URL.Query().Get("sendInitialEvents") == "true" {
		for _, object := range a.objects[collection] {
			if selects(selector, object) {
				writeEvent(w, "ADDED", object)
			}
		}
		from = a.version
		writeEvent(w, "BOOKMARK", map[string]any{"apiVersion": kinds[collection][0], "kind": kinds[collection][1], "metadata": map[string]any{
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
			if event.collection == collection && event.version > from {
				if selects(selector, event.object) {
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

func (a *fakeAPI) get(w http.ResponseWriter, collection, key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	object, held := a.objects[collection][key]
	if !held {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(object)
}

// writeStatus replaces a Person's status, and refuses a write from a
// copy older than the stored one, as the API server does.
func (a *fakeAPI) writeStatus(w http.ResponseWriter, r *http.Request, name string) {
	var written map[string]any
	_ = json.NewDecoder(r.Body).Decode(&written)
	a.mu.Lock()
	defer a.mu.Unlock()
	held, exists := a.objects[peoplePath][name]
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if a.refusals > 0 {
		a.refusals--
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if written["metadata"].(map[string]any)["resourceVersion"] != held["metadata"].(map[string]any)["resourceVersion"] {
		w.WriteHeader(http.StatusConflict)
		return
	}
	next := clone(held)
	next["status"] = written["status"]
	_ = json.NewEncoder(w).Encode(a.store(peoplePath, name, next, "MODIFIED"))
}

func (a *fakeAPI) createPod(w http.ResponseWriter, r *http.Request, namespace string) {
	var created map[string]any
	_ = json.NewDecoder(r.Body).Decode(&created)
	a.mu.Lock()
	defer a.mu.Unlock()
	metadata := created["metadata"].(map[string]any)
	key := namespace + "/" + metadata["name"].(string)
	if a.absent[namespace] {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if _, exists := a.objects[podsPath][key]; exists {
		w.WriteHeader(http.StatusConflict)
		return
	}
	metadata["namespace"] = namespace
	metadata["creationTimestamp"] = time.Now().UTC().Format(time.RFC3339)
	created["status"] = map[string]any{"phase": "Pending"}
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(a.store(podsPath, key, created, "ADDED"))
}

func (a *fakeAPI) deletePod(w http.ResponseWriter, key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	held, exists := a.objects[podsPath][key]
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	a.store(podsPath, key, clone(held), "DELETED")
	delete(a.logs, key)
	_, _ = fmt.Fprint(w, `{"kind":"Status","status":"Success"}`)
}

// putPerson creates a Person, or replaces its spec and metadata the way
// kubectl apply does. A change of the spec moves the generation.
func (a *fakeAPI) putPerson(p person) {
	a.mu.Lock()
	defer a.mu.Unlock()
	object := map[string]any{}
	body, _ := json.Marshal(p)
	_ = json.Unmarshal(body, &object)
	object["apiVersion"], object["kind"] = personAPIVersion, personKind
	metadata := object["metadata"].(map[string]any)
	kind := "ADDED"
	if held, exists := a.objects[peoplePath][p.Metadata.Name]; exists {
		kind = "MODIFIED"
		heldMeta := held["metadata"].(map[string]any)
		metadata["uid"] = heldMeta["uid"]
		metadata["generation"] = heldMeta["generation"]
		object["status"] = held["status"]
		if fmt.Sprint(held["spec"]) != fmt.Sprint(object["spec"]) {
			metadata["generation"] = heldMeta["generation"].(float64) + 1
		}
	} else {
		metadata["uid"] = "uid-" + p.Metadata.Name
		metadata["generation"] = 1
		delete(object, "status")
	}
	a.store(peoplePath, p.Metadata.Name, object, kind)
}

// person answers the stored Person.
func (a *fakeAPI) person(t *testing.T, name string) person {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	var out person
	body, _ := json.Marshal(a.objects[peoplePath][name])
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// bakerPod answers the stored baker pod of a Person, and false when
// there is none.
func (a *fakeAPI) bakerPod(t *testing.T, namespace, personName string) (pod, bool) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	object, held := a.objects[podsPath][namespace+"/"+bakerPodName(personName)]
	if !held {
		return pod{}, false
	}
	var out pod
	body, _ := json.Marshal(object)
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out, true
}

// putPod stores a pod the way another process made it, such as a baker
// pod an earlier copy of the operator started.
func (a *fakeAPI) putPod(item pod) {
	a.mu.Lock()
	defer a.mu.Unlock()
	object := map[string]any{}
	_ = json.Unmarshal(mustJSON(item), &object)
	a.store(podsPath, item.Metadata.Namespace+"/"+item.Metadata.Name, object, "ADDED")
}

// finishPod plays the kubelet: the pod's container wrote the log and
// ended in the phase.
func (a *fakeAPI) finishPod(namespace, name, phase, log string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := namespace + "/" + name
	next := clone(a.objects[podsPath][key])
	next["status"] = map[string]any{"phase": phase}
	a.logs[key] = log
	a.store(podsPath, key, next, "MODIFIED")
}
