package main

// A fake API server for the requests that the operator sends. It serves
// them over the in-memory connections of apiservertest, so each test
// runs in a synctest bubble on the fake clock. A test acts as
// machine-operator and the kubelet: it publishes a ResourceSlice and
// sets a pod's final phase.

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
	operatorPodName = "firmware-operator-0"
	operatorNS      = "liken-system"
	slicesPath      = "/apis/resource.k8s.io/v1/resourceslices"
	machinesPath    = "/apis/liken.sh/v1alpha1/machines"
)

var (
	operatorPodsPath = podsPathIn(operatorNS)
	claimsPath       = claimsPathIn(operatorNS)
)

// kinds maps each collection path to the apiVersion and kind of its
// objects, for list responses and bookmark events.
var kinds = map[string][2]string{
	firmwaresPath:    {firmwareAPIVersion, firmwareKind},
	catalogsPath:     {firmwareAPIVersion, catalogKind},
	slicesPath:       {"resource.k8s.io/v1", "ResourceSlice"},
	machinesPath:     {"liken.sh/v1alpha1", "Machine"},
	operatorPodsPath: {"v1", "Pod"},
	claimsPath:       {"resource.k8s.io/v1", "ResourceClaim"},
	disruptionsPath:  {disruptionAPIVersion, disruptionKind},
}

type fakeEvent struct {
	collection string
	kind       string
	object     map[string]any
	version    int
}

type fakeAPI struct {
	server *apiservertest.Server

	recorded *eventstest.Events

	mu      sync.Mutex
	version int
	uids    int
	objects map[string]map[string]map[string]any // collection, name, object
	creates map[string]int                       // collection, successful creates
	events  []fakeEvent
	changed chan struct{}
	// logs maps a pod's name to its log. When logsForbidden is set,
	// every read of a log gets 403, as when RBAC grants the operator no
	// access to pods/log.
	logs          map[string]string
	logsForbidden bool

	// refusals is the number of next status writes that fail with 503, as
	// when an API server restarts.
	refusals int
}

func (a *fakeAPI) refuseStatusWrites(count int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refusals = count
}

func startFakeAPI(t *testing.T) *fakeAPI {
	api := &fakeAPI{
		objects:  map[string]map[string]map[string]any{},
		creates:  map[string]int{},
		changed:  make(chan struct{}),
		logs:     map[string]string{},
		recorded: &eventstest.Events{},
	}
	for collection := range kinds {
		api.objects[collection] = map[string]map[string]any{}
	}
	api.server = apiservertest.Start(t, api.recorded.Around(api))
	// After a failed list, client-go's reflector waits out its backoff,
	// and its context does not end that wait. The sleep keeps the bubble
	// alive until the wait ends (apiservertest).
	t.Cleanup(func() { time.Sleep(time.Minute) })
	return api
}

// store saves an object at the next resourceVersion and wakes each
// watch. The caller holds a.mu.
func (a *fakeAPI) store(collection string, object map[string]any, kind string) map[string]any {
	a.version++
	object = clone(object)
	metadata := object["metadata"].(map[string]any)
	metadata["resourceVersion"] = strconv.Itoa(a.version)
	name := metadata["name"].(string)
	if kind == "DELETED" {
		delete(a.objects[collection], name)
	} else {
		a.objects[collection][name] = object
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

func toMap(object any) map[string]any {
	var out map[string]any
	_ = json.Unmarshal(mustJSON(object), &out)
	return out
}

func (a *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path
	if _, collection := kinds[path]; collection {
		switch {
		case r.Method == http.MethodPost:
			a.create(w, r, path)
		case r.URL.Query().Get("watch") == "true":
			a.watch(w, r, path)
		default:
			a.list(w, r, path)
		}
		return
	}
	for collection := range kinds {
		rest, found := strings.CutPrefix(path, collection+"/")
		if !found {
			continue
		}
		name, status := strings.CutSuffix(rest, "/status")
		if pod, log := strings.CutSuffix(rest, "/log"); log && collection == operatorPodsPath {
			a.log(w, r, pod)
			return
		}
		switch {
		case status && r.Method == http.MethodPut:
			a.writeStatus(w, r, collection, name)
		case r.Method == http.MethodDelete:
			a.delete(w, collection, name)
		case r.Method == http.MethodPatch:
			a.patch(w, r, collection, name)
		default:
			a.get(w, collection, name)
		}
		return
	}
	http.NotFound(w, r)
}

// selects reports whether an object matches the request's label
// selector and field selector. Each selector supports only one
// key=value term, and a field selector's key is a dotted path.
func selects(r *http.Request, object map[string]any) bool {
	if key, value, found := strings.Cut(r.URL.Query().Get("labelSelector"), "="); found {
		labels, _ := object["metadata"].(map[string]any)["labels"].(map[string]any)
		if labels[key] != value {
			return false
		}
	}
	if path, value, found := strings.Cut(r.URL.Query().Get("fieldSelector"), "="); found {
		var field any = object
		for _, part := range strings.Split(path, ".") {
			field, _ = field.(map[string]any)[part]
		}
		if field != value {
			return false
		}
	}
	return true
}

func (a *fakeAPI) list(w http.ResponseWriter, r *http.Request, collection string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	items := []any{}
	for _, object := range a.objects[collection] {
		if selects(r, object) {
			items = append(items, object)
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"apiVersion": kinds[collection][0], "kind": kinds[collection][1] + "List",
		"metadata": map[string]any{"resourceVersion": strconv.Itoa(a.version)},
		"items":    items,
	})
}

// watch sends each change after the requested resourceVersion. With
// sendInitialEvents, it first sends an ADDED event for each object and
// the bookmark that ends the initial events.
func (a *fakeAPI) watch(w http.ResponseWriter, r *http.Request, collection string) {
	a.mu.Lock()
	from, _ := strconv.Atoi(r.URL.Query().Get("resourceVersion"))
	if r.URL.Query().Get("sendInitialEvents") == "true" {
		for _, object := range a.objects[collection] {
			if selects(r, object) {
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
				if selects(r, event.object) {
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
	object, found := a.objects[collection][name]
	if !found {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(object)
}

// create responds 409 when the name exists.
func (a *fakeAPI) create(w http.ResponseWriter, r *http.Request, collection string) {
	var created map[string]any
	_ = json.NewDecoder(r.Body).Decode(&created)
	a.mu.Lock()
	defer a.mu.Unlock()
	metadata := created["metadata"].(map[string]any)
	if _, exists := a.objects[collection][metadata["name"].(string)]; exists {
		w.WriteHeader(http.StatusConflict)
		return
	}
	a.uids++
	metadata["uid"] = fmt.Sprintf("uid-%d", a.uids)
	metadata["creationTimestamp"] = time.Now().UTC().Format(time.RFC3339)
	if collection == operatorPodsPath {
		created["status"] = map[string]any{"phase": podPending}
	}
	if collection == firmwaresPath {
		metadata["generation"] = 1
	}
	a.creates[collection]++
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(a.store(collection, created, "ADDED"))
}

// delete also deletes each claim that the object owns, as the garbage
// collector does.
func (a *fakeAPI) delete(w http.ResponseWriter, collection, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	stored, exists := a.objects[collection][name]
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	// An object with a deletionTimestamp stays until its kubelet or its
	// finalizers finish, so a second delete changes nothing.
	if started, _ := stored["metadata"].(map[string]any)["deletionTimestamp"].(string); started != "" {
		_, _ = fmt.Fprint(w, `{"kind":"Status","status":"Success"}`)
		return
	}
	a.store(collection, stored, "DELETED")
	uid := stored["metadata"].(map[string]any)["uid"]
	for _, claim := range a.objects[claimsPath] {
		owners, _ := claim["metadata"].(map[string]any)["ownerReferences"].([]any)
		for _, owner := range owners {
			if owner.(map[string]any)["uid"] == uid {
				a.store(claimsPath, claim, "DELETED")
			}
		}
	}
	_, _ = fmt.Fprint(w, `{"kind":"Status","status":"Success"}`)
}

// writeStatus responds 409 to a write whose resourceVersion differs
// from the stored object's, as the API server does.
func (a *fakeAPI) writeStatus(w http.ResponseWriter, r *http.Request, collection, name string) {
	var written map[string]any
	_ = json.NewDecoder(r.Body).Decode(&written)
	a.mu.Lock()
	defer a.mu.Unlock()
	stored, exists := a.objects[collection][name]
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if a.refusals > 0 {
		a.refusals--
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if written["metadata"].(map[string]any)["resourceVersion"] != stored["metadata"].(map[string]any)["resourceVersion"] {
		w.WriteHeader(http.StatusConflict)
		return
	}
	next := clone(stored)
	next["status"] = written["status"]
	_ = json.NewEncoder(w).Encode(a.store(collection, next, "MODIFIED"))
}

// log responds with the last tailLines lines of a pod's log, as the
// kubelet does.
func (a *fakeAPI) log(w http.ResponseWriter, r *http.Request, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.logsForbidden {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	log, found := a.logs[name]
	if _, exists := a.objects[operatorPodsPath][name]; !exists || !found {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	lines := strings.SplitAfter(log, "\n")
	if tail, err := strconv.Atoi(r.URL.Query().Get("tailLines")); err == nil && tail < len(lines) {
		lines = lines[len(lines)-tail:]
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = fmt.Fprint(w, strings.Join(lines, ""))
}
