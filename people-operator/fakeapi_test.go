package main

// A fake API server that stores Persons and pods and serves the
// requests the operator sends. It serves over the in-memory connections
// of apiservertest, so each test runs in a synctest bubble on the fake
// clock. A test acts as the kubelet with finishPod.

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

// kinds maps each collection to the apiVersion and kind of its objects,
// for list responses and watch bookmarks.
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

	// recorded collects the Events the operator posts.
	recorded *eventstest.Events

	mu      sync.Mutex
	version int
	objects map[string]map[string]map[string]any // collection, key, object
	logs    map[string]string                    // pod key, log
	events  []fakeEvent
	changed chan struct{}

	// refusals is the number of next status writes to reject with a
	// 503, as an API server does while it restarts.
	refusals int

	// absent holds the namespaces that do not exist. A pod create in one
	// of them returns 404.
	absent map[string]bool

	// statusDelay and eventDelay imitate a loaded API server.
	// afterStatusWrite runs once a status write is stored.
	// afterEventWrite runs after the delay, just before the Event is
	// stored.
	statusDelay      time.Duration
	afterStatusWrite func()
	eventDelay       time.Duration
	afterEventWrite  func()
}

// slowEventWrites delays each Event write by delay and calls after
// just before the Event is stored.
func (a *fakeAPI) slowEventWrites(delay time.Duration, after func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.eventDelay, a.afterEventWrite = delay, after
}

// slowStatusWrites delays each status write by delay and calls after
// when the write is stored.
func (a *fakeAPI) slowStatusWrites(delay time.Duration, after func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.statusDelay, a.afterStatusWrite = delay, after
}

// dropNamespace makes a namespace not exist.
func (a *fakeAPI) dropNamespace(namespace string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.absent[namespace] = true
}

// refuseStatusWrites makes the server reject the next count status
// writes.
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
	handler := api.recorded.Around(api)
	api.server = apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.mu.Lock()
		delay, after := api.eventDelay, api.afterEventWrite
		api.mu.Unlock()
		if after != nil && strings.Contains(r.URL.Path, "/events") {
			time.Sleep(delay)
			after()
		}
		handler.ServeHTTP(w, r)
	}))
	// After a rejected list, the reflector sleeps through its backoff
	// without watching its context. The bubble cannot end until that
	// sleep ends, so the cleanup moves the fake clock past it.
	t.Cleanup(func() { time.Sleep(time.Minute) })
	// The operator's own pod, which ownImage reads.
	api.store(podsPath, operatorNS+"/"+operatorPodName, map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": operatorPodName, "namespace": operatorNS},
		"spec":     map[string]any{"containers": []any{map[string]any{"name": "operator", "image": operatorImage}}},
	}, "ADDED")
	return api
}

// store saves an object at the next resource version and wakes every
// watch. A kind of DELETED removes it.
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

// servePod serves namespaces/<ns>/pods, namespaces/<ns>/pods/<name>,
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
		log, found := a.logs[parts[0]+"/"+parts[2]]
		_, exists := a.objects[podsPath][parts[0]+"/"+parts[2]]
		a.mu.Unlock()
		if !found || !exists {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = fmt.Fprint(w, log)
	default:
		http.NotFound(w, r)
	}
}

// selects reports whether an object has the label that a key=value
// selector requires. An empty selector matches every object.
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

// watch serves a watch as the API server does. With sendInitialEvents
// it sends an ADDED event for each object, then the bookmark that ends
// the initial events, and then every later change. Without it, the
// watch sends each change after resourceVersion.
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
	object, found := a.objects[collection][key]
	if !found {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(object)
}

// writeStatus replaces a Person's status. As the API server does, it
// returns 409 when the request's resourceVersion is not the stored one.
func (a *fakeAPI) writeStatus(w http.ResponseWriter, r *http.Request, name string) {
	var written map[string]any
	_ = json.NewDecoder(r.Body).Decode(&written)
	a.mu.Lock()
	delay := a.statusDelay
	a.mu.Unlock()
	time.Sleep(delay)
	a.mu.Lock()
	defer a.mu.Unlock()
	current, exists := a.objects[peoplePath][name]
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if a.refusals > 0 {
		a.refusals--
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if written["metadata"].(map[string]any)["resourceVersion"] != current["metadata"].(map[string]any)["resourceVersion"] {
		w.WriteHeader(http.StatusConflict)
		return
	}
	next := clone(current)
	next["status"] = written["status"]
	stored := a.store(peoplePath, name, next, "MODIFIED")
	if a.afterStatusWrite != nil {
		a.afterStatusWrite()
	}
	_ = json.NewEncoder(w).Encode(stored)
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
	stored, exists := a.objects[podsPath][key]
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	a.store(podsPath, key, clone(stored), "DELETED")
	delete(a.logs, key)
	_, _ = fmt.Fprint(w, `{"kind":"Status","status":"Success"}`)
}

// putPerson creates a Person, or replaces its spec and metadata as
// kubectl apply does and keeps its status. A change to the spec
// increments metadata.generation.
func (a *fakeAPI) putPerson(p person) {
	a.mu.Lock()
	defer a.mu.Unlock()
	object := map[string]any{}
	body, _ := json.Marshal(p)
	_ = json.Unmarshal(body, &object)
	object["apiVersion"], object["kind"] = personAPIVersion, personKind
	metadata := object["metadata"].(map[string]any)
	kind := "ADDED"
	if stored, exists := a.objects[peoplePath][p.Metadata.Name]; exists {
		kind = "MODIFIED"
		storedMeta := stored["metadata"].(map[string]any)
		metadata["uid"] = storedMeta["uid"]
		metadata["generation"] = storedMeta["generation"]
		object["status"] = stored["status"]
		if fmt.Sprint(stored["spec"]) != fmt.Sprint(object["spec"]) {
			metadata["generation"] = storedMeta["generation"].(float64) + 1
		}
	} else {
		metadata["uid"] = "uid-" + p.Metadata.Name
		metadata["generation"] = 1
		delete(object, "status")
	}
	a.store(peoplePath, p.Metadata.Name, object, kind)
}

// person returns the stored Person.
func (a *fakeAPI) person(t *testing.T, name string) person {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	var out person
	body, _ := json.Marshal(a.objects[peoplePath][name])
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decoding the stored object: %v", err)
	}
	return out
}

// bakerPod returns the stored baker pod of a Person, and false when
// there is none.
func (a *fakeAPI) bakerPod(t *testing.T, namespace, personName string) (pod, bool) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	object, found := a.objects[podsPath][namespace+"/"+bakerPodName(personName)]
	if !found {
		return pod{}, false
	}
	var out pod
	body, _ := json.Marshal(object)
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decoding the stored object: %v", err)
	}
	return out, true
}

// putPod stores a pod that the operator did not create in this test,
// such as a baker pod from an earlier copy of the operator.
func (a *fakeAPI) putPod(item pod) {
	a.mu.Lock()
	defer a.mu.Unlock()
	object := map[string]any{}
	_ = json.Unmarshal(mustJSON(item), &object)
	a.store(podsPath, item.Metadata.Namespace+"/"+item.Metadata.Name, object, "ADDED")
}

// finishPod acts as the kubelet: it sets the pod's log and moves the
// pod to the given phase.
func (a *fakeAPI) finishPod(namespace, name, phase, log string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := namespace + "/" + name
	next := clone(a.objects[podsPath][key])
	next["status"] = map[string]any{"phase": phase}
	a.logs[key] = log
	a.store(podsPath, key, next, "MODIFIED")
}
