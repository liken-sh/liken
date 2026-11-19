package main

// A fake API server for every collection the operator reads and
// writes. It serves list, watch, create, get, replace, status update,
// merge patch of metadata, and delete, and a delete waits for
// finalizers as the real API server does. It listens on the in-memory
// connections of apiservertest, so each test runs in a synctest bubble
// on the fake clock, and Events go to the fake in kubernetes/events.
// The fake also acts as the kubelet: a pod it creates is Ready at
// once, unless a test holds it Pending.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/events/eventstest"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

const testNamespace = "observatory"

type fakeEvent struct {
	collection string
	kind       string
	object     map[string]any
	version    int
}

type fakeAPI struct {
	server *apiservertest.Server
	// recorded is the Events the operator writes.
	recorded *eventstest.Events

	mu      sync.Mutex
	version int
	uids    int
	// objects maps each collection to its objects by name. A collection
	// is the path of its list, such as
	// /api/v1/namespaces/observatory/pods.
	objects map[string]map[string]map[string]any
	events  []fakeEvent
	changed chan struct{}

	// pending is the set of pods that stay Pending, by name.
	pending map[string]bool
	// rejected is the set of pods that the kubelet rejects at admission
	// as soon as the operator creates them, by name.
	rejected map[string]bool
	// writes counts the status writes to each object, keyed by its
	// path, and the creates in each collection, keyed by "POST " and
	// the collection's path.
	writes map[string]int
	// refusals is how many of the next creates and deletes of pods,
	// Services, and ResourceClaimTemplates the server refuses, as an
	// API server does while it restarts.
	refusals int
	// patchRefusals is how many of the next merge patches the server
	// refuses.
	patchRefusals int
	// lateDeletes is how many of the patches that remove a deleted
	// object's last finalizer get a 404 after the object is gone. The
	// real server does the same when another request deletes the
	// object first.
	lateDeletes int
	// statusRefusals is how many of the next status writes the server
	// refuses.
	statusRefusals int
	// jobs acts as the Job controller (fakejobs_test.go).
	jobs fakeJobController
	// onRequest, when a test sets it with onEachRequest, receives each request
	// before the server answers it, including requests for Events.
	onRequest func(*http.Request)
}

func (a *fakeAPI) onEachRequest(onRequest func(*http.Request)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onRequest = onRequest
}

func (a *fakeAPI) refuse(count int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refusals = count
}

// refused writes a 503 and returns true while refusals remain and the
// collection is pods, Services, or ResourceClaimTemplates. The caller
// holds a.mu.
func (a *fakeAPI) refused(w http.ResponseWriter, collection string) bool {
	if a.refusals == 0 || !slices.Contains([]string{"pods", "services", "resourceclaimtemplates"}, plural(collection)) {
		return false
	}
	a.refusals--
	w.WriteHeader(http.StatusServiceUnavailable)
	return true
}

// fakeKinds maps the last part of a collection's path to the
// apiVersion and kind of its objects.
var fakeKinds = func() map[string][2]string {
	out := map[string][2]string{
		"pods":                   {"v1", "Pod"},
		"services":               {"v1", "Service"},
		"resourceclaimtemplates": {"resource.k8s.io/v1", "ResourceClaimTemplate"},
		"jobs":                   {"batch/v1", "Job"},
	}
	for _, kind := range observatory.Kinds {
		out[kind.Plural] = [2]string{observatory.APIVersion, kind.Name}
	}
	return out
}()

func startFakeAPI(t *testing.T) *fakeAPI {
	api := &fakeAPI{
		objects:  map[string]map[string]map[string]any{},
		changed:  make(chan struct{}),
		pending:  map[string]bool{},
		rejected: map[string]bool{},
		writes:   map[string]int{},

		recorded: &eventstest.Events{},
	}
	answer := api.recorded.Around(api)
	api.server = apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.mu.Lock()
		onRequest := api.onRequest
		api.mu.Unlock()
		if onRequest != nil {
			onRequest(r)
		}
		answer.ServeHTTP(w, r)
	}))
	// After a refused list, the reflector sleeps through its backoff
	// without watching its context. The bubble cannot end until that
	// sleep does, so wait it out (apiservertest).
	t.Cleanup(func() { time.Sleep(time.Minute) })
	return api
}

// collectionOf splits a request's path into the collection, the object
// name, and the subresource.
func collectionOf(path string) (collection, name, sub string) {
	var prefix, rest string
	switch {
	case strings.HasPrefix(path, "/api/v1/namespaces/"):
		prefix, rest = "/api/v1/namespaces/", strings.TrimPrefix(path, "/api/v1/namespaces/")
	case strings.HasPrefix(path, "/api/v1/") && strings.Count(path, "/") == 3:
		// A list or a watch of every namespace.
		return "/api/v1/namespaces/" + anyNamespace + "/" + strings.TrimPrefix(path, "/api/v1/"), "", ""
	case strings.HasPrefix(path, "/apis/"):
		parts := strings.SplitN(strings.TrimPrefix(path, "/apis/"), "/", 4)
		if len(parts) == 3 {
			return "/apis/" + parts[0] + "/" + parts[1] + "/namespaces/" + anyNamespace + "/" + parts[2], "", ""
		}
		if len(parts) < 4 || parts[2] != "namespaces" {
			return "", "", ""
		}
		prefix, rest = "/apis/"+parts[0]+"/"+parts[1]+"/namespaces/", parts[3]
	default:
		return "", "", ""
	}
	parts := strings.Split(rest, "/")
	collection = prefix + parts[0] + "/" + parts[1]
	if len(parts) > 2 {
		name = parts[2]
	}
	if len(parts) > 3 {
		sub = parts[3]
	}
	return collection, name, sub
}

// anyNamespace replaces the namespace in the collection path of a list
// or a watch across all namespaces.
const anyNamespace = "*"

// covers reports whether a list or watch of watched includes
// collection: the same path, or one namespace of a watch across all
// namespaces.
func covers(watched, collection string) bool {
	if watched == collection {
		return true
	}
	before, after, found := strings.Cut(watched, "/"+anyNamespace+"/")
	if !found || !strings.HasPrefix(collection, before+"/") || !strings.HasSuffix(collection, "/"+after) {
		return false
	}
	namespace := strings.TrimSuffix(strings.TrimPrefix(collection, before+"/"), "/"+after)
	return namespace != "" && !strings.Contains(namespace, "/")
}

func namespaceOf(collection string) string {
	_, rest, _ := strings.Cut(collection, "/namespaces/")
	namespace, _, _ := strings.Cut(rest, "/")
	return namespace
}

func plural(collection string) string {
	return collection[strings.LastIndex(collection, "/")+1:]
}

func (a *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	collection, name, sub := collectionOf(r.URL.Path)
	switch {
	case collection == "":
		http.NotFound(w, r)
	case name == "" && r.Method == http.MethodGet && r.URL.Query().Get("watch") == "true":
		a.watch(w, r, collection)
	case name == "" && r.Method == http.MethodGet:
		a.list(w, r, collection)
	case name == "" && r.Method == http.MethodPost:
		a.create(w, r, collection)
	case sub == "status" && r.Method == http.MethodPut:
		a.writeStatus(w, r, collection, name)
	case sub == "" && r.Method == http.MethodPut:
		a.replace(w, r, collection, name)
	case r.Method == http.MethodGet:
		a.get(w, collection, name)
	case r.Method == http.MethodPatch:
		a.patch(w, r, collection, name)
	case r.Method == http.MethodDelete:
		a.remove(w, collection, name)
	default:
		http.NotFound(w, r)
	}
}

// store saves an object at the next resourceVersion and wakes each
// watch. The caller holds a.mu. Nothing changes a stored object in
// place, so watch events can share it.
func (a *fakeAPI) store(collection, name string, object map[string]any, kind string) map[string]any {
	a.version++

	object = normalize(object)
	metadata := object["metadata"].(map[string]any)
	metadata["resourceVersion"] = strconv.Itoa(a.version)
	if a.objects[collection] == nil {
		a.objects[collection] = map[string]map[string]any{}
	}
	if kind == "DELETED" {
		delete(a.objects[collection], name)
	} else {
		a.objects[collection][name] = object
	}
	a.events = append(a.events, fakeEvent{collection: collection, kind: kind, object: object, version: a.version})
	close(a.changed)
	a.changed = make(chan struct{})
	return object
}

// normalize returns a copy of an object as JSON decoding produces it,
// so every number is a float64, as in a response from the API
// server.
func normalize(object map[string]any) map[string]any {
	var out map[string]any
	body, _ := json.Marshal(object)
	_ = json.Unmarshal(body, &out)
	return out
}

// clone returns a deep copy of a normalized object. It copies the maps
// and slices directly, because a JSON round trip on every read would
// cost more than the rest of the fake.
func clone(object map[string]any) map[string]any {
	return copyValue(object).(map[string]any)
}

func copyValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = copyValue(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = copyValue(item)
		}
		return out
	default:
		return v
	}
}

// selects reports whether an object has the label that a key=value
// selector names. An empty selector selects every object.
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
	for held, objects := range a.objects {
		for _, object := range objects {
			if covers(collection, held) && selects(r.URL.Query().Get("labelSelector"), object) {
				items = append(items, object)
			}
		}
	}
	// The API server lists the objects of a namespace in the order of
	// their names.
	slices.SortFunc(items, func(a, b any) int {
		return strings.Compare(a.(map[string]any)["metadata"].(map[string]any)["name"].(string), b.(map[string]any)["metadata"].(map[string]any)["name"].(string))
	})
	kind := fakeKinds[plural(collection)]
	_ = json.NewEncoder(w).Encode(map[string]any{
		"apiVersion": kind[0], "kind": kind[1] + "List",
		"metadata": map[string]any{"resourceVersion": strconv.Itoa(a.version)},
		"items":    items,
	})
}

// watch streams the changes after the request's resourceVersion. When
// the request sets sendInitialEvents, it first sends an ADDED event for
// each existing object and a bookmark that ends them, as the API
// server does.
func (a *fakeAPI) watch(w http.ResponseWriter, r *http.Request, collection string) {
	selector := r.URL.Query().Get("labelSelector")
	kind := fakeKinds[plural(collection)]
	a.mu.Lock()
	from, _ := strconv.Atoi(r.URL.Query().Get("resourceVersion"))
	if r.URL.Query().Get("sendInitialEvents") == "true" {
		for held, objects := range a.objects {
			for _, object := range objects {
				if covers(collection, held) && selects(selector, object) {
					writeEvent(w, "ADDED", object)
				}
			}
		}
		from = a.version
		writeEvent(w, "BOOKMARK", map[string]any{"apiVersion": kind[0], "kind": kind[1], "metadata": map[string]any{
			"resourceVersion": strconv.Itoa(from),
			"annotations":     map[string]any{"k8s.io/initial-events-end": "true"},
		}})
	}
	// store appends events in resourceVersion order. The watch starts
	// at the first event after the version it opened from, and after
	// each change it reads only the events it has not sent.
	next, _ := slices.BinarySearchFunc(a.events, from+1, func(event fakeEvent, version int) int {
		return event.version - version
	})
	a.mu.Unlock()
	w.(http.Flusher).Flush()
	for {
		a.mu.Lock()
		changed := a.changed
		for _, event := range a.events[next:] {
			if covers(collection, event.collection) && selects(selector, event.object) {
				writeEvent(w, event.kind, event.object)
			}
		}
		next = len(a.events)
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

func (a *fakeAPI) get(w http.ResponseWriter, collection, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	object, held := a.objects[collection][name]
	if !held {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(object)
}

// create adds an object. When the object has no name, it builds one
// from generateName, as the API server does.
func (a *fakeAPI) create(w http.ResponseWriter, r *http.Request, collection string) {
	var object map[string]any
	_ = json.NewDecoder(r.Body).Decode(&object)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.writes["POST "+collection]++
	if a.refused(w, collection) {
		return
	}
	added := a.add(collection, object)
	if added == nil {
		w.WriteHeader(http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(added)
}

// add stores a new object, and returns nil when an object of that
// name exists. The caller holds a.mu.
func (a *fakeAPI) add(collection string, object map[string]any) map[string]any {
	metadata := object["metadata"].(map[string]any)
	name, _ := metadata["name"].(string)
	if name == "" {
		a.uids++
		name = metadata["generateName"].(string) + strconv.Itoa(a.uids)
		metadata["name"] = name
	}
	if _, exists := a.objects[collection][name]; exists {
		return nil
	}
	a.uids++
	metadata["uid"] = "uid-" + strconv.Itoa(a.uids)
	metadata["namespace"] = namespaceOf(collection)
	metadata["generation"] = 1
	metadata["creationTimestamp"] = time.Now().UTC().Format(time.RFC3339)
	if plural(collection) == "jobs" {
		object["status"] = a.jobs.started()
		defer a.failedPod(object)
	}
	if plural(collection) == "pods" {
		object["status"] = map[string]any{"phase": "Pending"}
		if !a.pending[name] {
			object["status"] = readyStatus()
			object["spec"].(map[string]any)["nodeName"] = "node-1"
		}
		if a.rejected[name] {
			object["status"] = map[string]any{"phase": "Failed", "reason": "OutOfcpu", "message": "Pod was rejected: Node didn't have enough resource: cpu"}
		}
	}
	return a.store(collection, name, object, "ADDED")
}

func readyStatus() map[string]any {
	return map[string]any{
		"phase": "Running", "podIP": "10.0.0.1",
		"conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
	}
}

// writeStatus replaces an object's status. It answers 409 when the
// request's resourceVersion differs from the stored one, as the API
// server does.
func (a *fakeAPI) writeStatus(w http.ResponseWriter, r *http.Request, collection, name string) {
	var written map[string]any
	_ = json.NewDecoder(r.Body).Decode(&written)
	a.mu.Lock()
	defer a.mu.Unlock()
	held, exists := a.objects[collection][name]
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if a.statusRefusals > 0 {
		a.statusRefusals--
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if written["metadata"].(map[string]any)["resourceVersion"] != held["metadata"].(map[string]any)["resourceVersion"] {
		w.WriteHeader(http.StatusConflict)
		return
	}
	a.writes[collection+"/"+name]++
	next := clone(held)
	next["status"] = written["status"]
	// The API server stores nothing for a write that changes nothing.
	// The resourceVersion stays the same and no watch event follows.
	// Both statuses came from JSON, so their numbers are already
	// float64 and DeepEqual compares them directly.
	if reflect.DeepEqual(next["status"], held["status"]) {
		_ = json.NewEncoder(w).Encode(held)
		return
	}
	_ = json.NewEncoder(w).Encode(a.store(collection, name, next, "MODIFIED"))
}

// replace stores a whole object in place of the one it names, such as
// a ConfigMap with new data. It answers 409 when the request's
// resourceVersion differs from the stored one.
func (a *fakeAPI) replace(w http.ResponseWriter, r *http.Request, collection, name string) {
	var written map[string]any
	_ = json.NewDecoder(r.Body).Decode(&written)
	a.mu.Lock()
	defer a.mu.Unlock()
	held, exists := a.objects[collection][name]
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if written["metadata"].(map[string]any)["resourceVersion"] != held["metadata"].(map[string]any)["resourceVersion"] {
		w.WriteHeader(http.StatusConflict)
		return
	}
	next := clone(written)
	next["metadata"].(map[string]any)["uid"] = held["metadata"].(map[string]any)["uid"]
	_ = json.NewEncoder(w).Encode(a.store(collection, name, next, "MODIFIED"))
}

// patch applies a merge patch to the metadata. It answers 409 when the
// patch sets a resourceVersion that differs from the stored one. An
// object with a deletionTimestamp is removed when the patch removes
// its last finalizer.
func (a *fakeAPI) patch(w http.ResponseWriter, r *http.Request, collection, name string) {
	var patch struct {
		Metadata map[string]any `json:"metadata"`
	}
	_ = json.NewDecoder(r.Body).Decode(&patch)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.patchRefusals > 0 {
		a.patchRefusals--
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	held, exists := a.objects[collection][name]
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	metadata := held["metadata"].(map[string]any)
	if version, stated := patch.Metadata["resourceVersion"]; stated && version != metadata["resourceVersion"] {
		w.WriteHeader(http.StatusConflict)
		return
	}
	next := clone(held)
	nextMeta := next["metadata"].(map[string]any)
	for key, value := range patch.Metadata {
		switch {
		case key == "resourceVersion":
		case key == "annotations":
			annotations, _ := nextMeta["annotations"].(map[string]any)
			if annotations == nil {
				annotations = map[string]any{}
			}
			for k, v := range value.(map[string]any) {
				if v == nil {
					delete(annotations, k)
				} else {
					annotations[k] = v
				}
			}
			nextMeta["annotations"] = annotations
		case value == nil:
			delete(nextMeta, key)
		default:
			nextMeta[key] = value
		}
	}
	finalizers, _ := nextMeta["finalizers"].([]any)
	if nextMeta["deletionTimestamp"] != nil && len(finalizers) == 0 {
		deleted := a.store(collection, name, next, "DELETED")
		if a.lateDeletes > 0 {
			a.lateDeletes--
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(deleted)
		return
	}
	_ = json.NewEncoder(w).Encode(a.store(collection, name, next, "MODIFIED"))
}

// remove handles a DELETE request. It deletes the object, or only
// sets its deletionTimestamp while it still has a finalizer.
func (a *fakeAPI) remove(w http.ResponseWriter, collection, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.refused(w, collection) {
		return
	}
	if !a.delete(collection, name) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_, _ = fmt.Fprint(w, `{"kind":"Status","status":"Success"}`)
}

// delete deletes an object as remove does, and returns false when the
// object does not exist. The caller holds a.mu.
func (a *fakeAPI) delete(collection, name string) bool {
	held, exists := a.objects[collection][name]
	if !exists {
		return false
	}
	metadata := held["metadata"].(map[string]any)
	finalizers, _ := metadata["finalizers"].([]any)
	if len(finalizers) > 0 {
		if metadata["deletionTimestamp"] == nil {
			next := clone(held)
			next["metadata"].(map[string]any)["deletionTimestamp"] = time.Now().UTC().Format(time.RFC3339)
			a.store(collection, name, next, "MODIFIED")
		}
		return true
	}
	a.store(collection, name, clone(held), "DELETED")
	return true
}

func kindCollection(kind observatory.Kind) string { return kind.Path(testNamespace) }

const (
	podsCollection       = "/api/v1/namespaces/" + testNamespace + "/pods"
	servicesCollection   = "/api/v1/namespaces/" + testNamespace + "/services"
	configMapsCollection = "/api/v1/namespaces/" + testNamespace + "/configmaps"
)

// put creates an object, or replaces the spec of an existing one, as
// kubectl apply does. A change to the spec increments the
// generation.
func (a *fakeAPI) put(collection string, object map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	object = normalize(object)
	name := object["metadata"].(map[string]any)["name"].(string)
	held, exists := a.objects[collection][name]
	if !exists {
		delete(object, "status")
		a.add(collection, object)
		return
	}
	next := clone(held)
	next["spec"] = object["spec"]
	if fmt.Sprint(held["spec"]) != fmt.Sprint(object["spec"]) {
		next["metadata"].(map[string]any)["generation"] = held["metadata"].(map[string]any)["generation"].(float64) + 1
	}
	a.store(collection, name, next, "MODIFIED")
}

func (a *fakeAPI) deleteNamed(collection, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.delete(collection, name)
}

// object returns a copy, so a test cannot change the stored object.
func (a *fakeAPI) object(collection, name string) (map[string]any, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	object, ok := a.objects[collection][name]
	if !ok {
		return nil, false
	}
	return clone(object), true
}

func decode[T any](t *testing.T, a *fakeAPI, collection, name string) (T, bool) {
	t.Helper()
	var out T
	object, ok := a.object(collection, name)
	if !ok {
		return out, false
	}
	body, _ := json.Marshal(object)
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decoding %s %s: %v", collection, name, err)
	}
	return out, true
}

func (a *fakeAPI) names(collection string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for name := range a.objects[collection] {
		out = append(out, name)
	}
	return out
}

// setPodReady makes a pod that a test held Pending Ready, as the
// kubelet would.
func (a *fakeAPI) setPodReady(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.pending, name)
	held, ok := a.objects[podsCollection][name]
	if !ok {
		return
	}
	next := clone(held)
	next["status"] = readyStatus()
	next["spec"].(map[string]any)["nodeName"] = "node-1"
	a.store(podsCollection, name, next, "MODIFIED")
}

// holdPending keeps the named pod Pending when the operator creates
// it, until setPodReady.
func (a *fakeAPI) holdPending(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending[name] = true
}

func (a *fakeAPI) eventReasons() []string {
	var out []string
	for _, e := range a.recorded.List() {
		out = append(out, e.Reason)
	}
	return out
}

func (a *fakeAPI) statusWrites(collection, name string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.writes[collection+"/"+name]
}

// creates returns how many creates one collection received, including
// the refused ones.
func (a *fakeAPI) creates(collection string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.writes["POST "+collection]
}
