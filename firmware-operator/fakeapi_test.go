package main

// A fake API server that holds the Firmware and FirmwareCatalog objects, the
// slices, the Machines, the upload and scan pods, the upload pods'
// claims, and the Disruptions, and serves the requests the operator
// sends: a list and a watch of each collection, a status write, an
// object's create, read, merge patch, and delete, and a pod's log. It answers
// over the in-memory connections of apiservertest, so each test runs in
// a synctest bubble on the fake clock. A test plays machine-operator
// and the kubelet: it publishes a slice and finishes an upload pod.

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
	uploadPodsPath = podsPathIn(operatorNS)
	claimsPath     = claimsPathIn(operatorNS)
)

// kinds names the apiVersion and kind of each collection's objects,
// which a list and a bookmark carry.
var kinds = map[string][2]string{
	firmwaresPath:   {firmwareAPIVersion, firmwareKind},
	catalogsPath:    {firmwareAPIVersion, catalogKind},
	slicesPath:      {"resource.k8s.io/v1", "ResourceSlice"},
	machinesPath:    {"liken.sh/v1alpha1", "Machine"},
	uploadPodsPath:  {"v1", "Pod"},
	claimsPath:      {"resource.k8s.io/v1", "ResourceClaim"},
	disruptionsPath: {disruptionAPIVersion, disruptionKind},
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
	uids    int
	objects map[string]map[string]map[string]any // collection, name, object
	creates map[string]int                       // collection, creates that landed
	events  []fakeEvent
	changed chan struct{}
	// logs holds each pod's log, by the pod's name. logsForbidden
	// refuses every read of a log, the way RBAC refuses an operator with
	// no grant on pods/log.
	logs          map[string]string
	logsForbidden bool

	// refusals is how many status writes the server refuses next, the
	// way an API server that restarts does.
	refusals int
}

// refuseStatusWrites makes the server refuse the next status writes.
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
	// The reflector waits out its backoff after a refused list without
	// reading its context, so the bubble waits past it (apiservertest).
	t.Cleanup(func() { time.Sleep(time.Minute) })
	return api
}

// store records an object at the next version and tells each watch.
// The caller holds the lock.
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
		if pod, log := strings.CutSuffix(rest, "/log"); log && collection == uploadPodsPath {
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

// selects answers whether an object matches a label selector of the
// form key=value and a field selector of the form path.to.field=value.
// An empty selector selects every object.
func selects(r *http.Request, object map[string]any) bool {
	if key, value, found := strings.Cut(r.URL.Query().Get("labelSelector"), "="); found {
		labels, _ := object["metadata"].(map[string]any)["labels"].(map[string]any)
		if labels[key] != value {
			return false
		}
	}
	if path, value, found := strings.Cut(r.URL.Query().Get("fieldSelector"), "="); found {
		var held any = object
		for _, part := range strings.Split(path, ".") {
			held, _ = held.(map[string]any)[part]
		}
		if held != value {
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

// watch answers a streaming list the way the API server does: an ADDED
// event for each object, a bookmark that ends the initial events, and
// then every later change. A watch from a version sends each change
// after that version.
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
	object, held := a.objects[collection][name]
	if !held {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(object)
}

// create stores a new pod or claim, with a UID and a creation time,
// and answers 409 for a name that exists.
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
	if collection == uploadPodsPath {
		created["status"] = map[string]any{"phase": podPending}
	}
	if collection == firmwaresPath {
		metadata["generation"] = 1
	}
	a.creates[collection]++
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(a.store(collection, created, "ADDED"))
}

// delete removes an object. A pod takes its claims with it, the way the
// garbage collector follows their owner references.
func (a *fakeAPI) delete(w http.ResponseWriter, collection, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	held, exists := a.objects[collection][name]
	if !exists {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	a.store(collection, held, "DELETED")
	uid := held["metadata"].(map[string]any)["uid"]
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

// writeStatus replaces a Firmware's status, and refuses a write from a
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
	_ = json.NewEncoder(w).Encode(a.store(collection, next, "MODIFIED"))
}

// log answers the end of a pod's log as text, the way the kubelet does
// for tailLines.
func (a *fakeAPI) log(w http.ResponseWriter, r *http.Request, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.logsForbidden {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	log, held := a.logs[name]
	if _, exists := a.objects[uploadPodsPath][name]; !exists || !held {
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
