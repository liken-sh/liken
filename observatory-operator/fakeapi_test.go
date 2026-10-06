package main

// A fake API server that holds every collection the operator reads and
// writes: the 20 kinds of the group, the pods, the Services, the
// claims, and the Events. It serves a list and a watch of each
// collection, a create, a read, a status write, a merge patch of the
// metadata, and a delete that a finalizer holds, the way the API
// server does. It answers over the in-memory connections of
// apiservertest, so each test runs in a synctest bubble on the fake
// clock. The fake also plays the kubelet: a pod it creates is Ready at
// once, unless a test holds it Pending.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
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

	mu      sync.Mutex
	version int
	uids    int
	// objects holds each collection's objects by name. A collection is
	// the path of its list, such as /api/v1/namespaces/observatory/pods.
	objects map[string]map[string]map[string]any
	events  []fakeEvent
	changed chan struct{}

	// pending holds the pods that the kubelet keeps Pending.
	pending map[string]bool
	// writes counts the status writes to each object, by its path, and
	// the creates in each collection, by "POST " and its path.
	writes map[string]int
	// refusals is how many creates and deletes the server refuses next,
	// the way an API server that restarts does.
	refusals int
	// patchRefusals is how many merge patches the server refuses next.
	patchRefusals int
	// statusRefusals and eventRefusals are how many status writes and
	// Event creates the server refuses next.
	statusRefusals, eventRefusals int
}

// refuse makes the server refuse the next creates and deletes.
func (a *fakeAPI) refuse(count int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refusals = count
}

// refused answers whether the server refuses this write to a pod, a
// Service, or a ResourceClaim. The caller holds a.mu.
func (a *fakeAPI) refused(w http.ResponseWriter, collection string) bool {
	if a.refusals == 0 || !slices.Contains([]string{"pods", "services", "resourceclaims"}, plural(collection)) {
		return false
	}
	a.refusals--
	w.WriteHeader(http.StatusServiceUnavailable)
	return true
}

// kinds names the kind of each collection's objects, by the last part
// of its path.
var fakeKinds = func() map[string][2]string {
	out := map[string][2]string{
		"pods":           {"v1", "Pod"},
		"services":       {"v1", "Service"},
		"events":         {"v1", "Event"},
		"resourceclaims": {"resource.k8s.io/v1", "ResourceClaim"},
	}
	for _, kind := range observatory.Kinds {
		out[kind.Plural] = [2]string{observatory.APIVersion, kind.Name}
	}
	return out
}()

func startFakeAPI(t *testing.T) *fakeAPI {
	api := &fakeAPI{
		objects: map[string]map[string]map[string]any{},
		changed: make(chan struct{}),
		pending: map[string]bool{},
		writes:  map[string]int{},
	}
	api.server = apiservertest.Start(t, api)
	// The reflector waits out its backoff after a refused list without
	// reading its context, so the bubble waits past it (apiservertest).
	t.Cleanup(func() { time.Sleep(time.Minute) })
	return api
}

// collectionOf splits a request's path into its collection, the name of
// the object, and the subresource.
func collectionOf(path string) (collection, name, sub string) {
	var prefix, rest string
	switch {
	case strings.HasPrefix(path, "/api/v1/namespaces/"):
		prefix, rest = "/api/v1/namespaces/", strings.TrimPrefix(path, "/api/v1/namespaces/")
	case strings.HasPrefix(path, "/apis/"):
		parts := strings.SplitN(strings.TrimPrefix(path, "/apis/"), "/", 4)
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

// store records an object at the next version and tells each watch.
// The caller holds a.mu. The stored object is never changed after this:
// a change stores a new copy, so the events share it.
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

// normalize answers a copy of an object as JSON decodes it, so every
// number is a float64, the way the API server's answer reads.
func normalize(object map[string]any) map[string]any {
	var out map[string]any
	body, _ := json.Marshal(object)
	_ = json.Unmarshal(body, &out)
	return out
}

// clone answers a deep copy of a normalized object. It copies the
// maps and slices directly, because a JSON round trip on every read
// costs more than the rest of the fake.
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

// selects answers whether an object carries the label a selector of the
// form key=value names. An empty selector selects every object.
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
	kind := fakeKinds[plural(collection)]
	_ = json.NewEncoder(w).Encode(map[string]any{
		"apiVersion": kind[0], "kind": kind[1] + "List",
		"metadata": map[string]any{"resourceVersion": strconv.Itoa(a.version)},
		"items":    items,
	})
}

// watch answers a streaming list the way the API server does: an ADDED
// event for each object, a bookmark that ends the initial events, and
// then every later change.
func (a *fakeAPI) watch(w http.ResponseWriter, r *http.Request, collection string) {
	selector := r.URL.Query().Get("labelSelector")
	kind := fakeKinds[plural(collection)]
	a.mu.Lock()
	from, _ := strconv.Atoi(r.URL.Query().Get("resourceVersion"))
	if r.URL.Query().Get("sendInitialEvents") == "true" {
		for _, object := range a.objects[collection] {
			if selects(selector, object) {
				writeEvent(w, "ADDED", object)
			}
		}
		from = a.version
		writeEvent(w, "BOOKMARK", map[string]any{"apiVersion": kind[0], "kind": kind[1], "metadata": map[string]any{
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

// create adds an object, and names it from generateName when it has no
// name, as the API server does for an Event.
func (a *fakeAPI) create(w http.ResponseWriter, r *http.Request, collection string) {
	var object map[string]any
	_ = json.NewDecoder(r.Body).Decode(&object)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.writes["POST "+collection]++
	if a.refused(w, collection) {
		return
	}
	if plural(collection) == "events" && a.eventRefusals > 0 {
		a.eventRefusals--
		w.WriteHeader(http.StatusServiceUnavailable)
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

// add creates an object, and answers nil when one of its name exists.
// The caller holds a.mu.
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
	metadata["namespace"] = testNamespace
	metadata["generation"] = 1
	metadata["creationTimestamp"] = time.Now().UTC().Format(time.RFC3339)
	if plural(collection) == "pods" {
		object["status"] = map[string]any{"phase": "Pending"}
		if !a.pending[name] {
			object["status"] = readyStatus()
			object["spec"].(map[string]any)["nodeName"] = "node-1"
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

// writeStatus replaces an object's status, and refuses a write from a
// copy older than the stored one, as the API server does.
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
	_ = json.NewEncoder(w).Encode(a.store(collection, name, next, "MODIFIED"))
}

// patch applies a merge patch to the metadata, and refuses one that
// states an older resourceVersion. An object that a person deleted
// goes away when its last finalizer goes.
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
		_ = json.NewEncoder(w).Encode(a.store(collection, name, next, "DELETED"))
		return
	}
	_ = json.NewEncoder(w).Encode(a.store(collection, name, next, "MODIFIED"))
}

// remove deletes an object, or marks it deleted while a finalizer holds
// it.
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

// delete deletes an object, and answers false when it does not exist.
// The caller holds a.mu.
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
	podsCollection     = "/api/v1/namespaces/" + testNamespace + "/pods"
	servicesCollection = "/api/v1/namespaces/" + testNamespace + "/services"
	eventsCollection   = "/api/v1/namespaces/" + testNamespace + "/events"
)

// put creates an object, or replaces its spec the way kubectl apply
// does. A change of the spec moves the generation.
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

// deleteNamed deletes an object as a person does with kubectl delete.
func (a *fakeAPI) deleteNamed(collection, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.delete(collection, name)
}

// object answers a copy of a stored object, and false when it is gone.
func (a *fakeAPI) object(collection, name string) (map[string]any, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	object, ok := a.objects[collection][name]
	if !ok {
		return nil, false
	}
	return clone(object), true
}

// decode answers a stored object as a typed value.
func decode[T any](t *testing.T, a *fakeAPI, collection, name string) (T, bool) {
	t.Helper()
	var out T
	object, ok := a.object(collection, name)
	if !ok {
		return out, false
	}
	body, _ := json.Marshal(object)
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out, true
}

// names answers the names of a collection's objects.
func (a *fakeAPI) names(collection string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for name := range a.objects[collection] {
		out = append(out, name)
	}
	return out
}

// setPodReady plays the kubelet for a pod that a test held Pending.
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

// holdPending keeps the pod of a name Pending when the operator creates
// it.
func (a *fakeAPI) holdPending(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending[name] = true
}

// eventReasons answers the reason of each Event, in the order of their
// creation.
func (a *fakeAPI) eventReasons() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, e := range a.events {
		if e.collection == eventsCollection && e.kind == "ADDED" {
			out = append(out, e.object["reason"].(string))
		}
	}
	return out
}

// statusWrites answers how many status writes one object received.
func (a *fakeAPI) statusWrites(collection, name string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.writes[collection+"/"+name]
}

// creates answers how many creates one collection received, the ones
// it refused included.
func (a *fakeAPI) creates(collection string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.writes["POST "+collection]
}
