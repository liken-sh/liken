package main

// The fixture here is the API server that holds the custom resources
// the way Kubernetes holds them. The fake radio the controllers are
// driven through is in radio_test.go, beside the snapshot it answers
// with.
//
// The API fixture keeps the two behaviors these controllers depend on.
// A delete against an object that has a finalizer stamps a
// deletionTimestamp and keeps the object, and the object goes when the
// last finalizer is patched off. A write to the status subresource
// changes status and nothing else.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testNow is the clock every one of these tests runs on, so that a
// test checks a window's deadline with arithmetic instead of waiting
// for it.
var testNow = time.Date(2026, 8, 17, 17, 30, 0, 0, time.UTC)

// apiFixture is a small API server that holds objects by the path they
// are read from.
type apiFixture struct {
	objects  map[string]map[string]any
	requests []string
	version  int

	// failWrites makes every write return this status, the way an
	// API server does when RBAC denies one.
	failWrites int
}

func newAPIFixture() *apiFixture {
	return &apiFixture{objects: map[string]map[string]any{}}
}

func (f *apiFixture) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		if r.Method != http.MethodGet && f.failWrites != 0 {
			w.WriteHeader(f.failWrites)
			return
		}
		switch r.Method {
		case http.MethodGet:
			f.serveGet(t, w, r)
		case http.MethodPost:
			f.serveCreate(t, w, r)
		case http.MethodPut:
			f.serveReplace(t, w, r)
		case http.MethodPatch:
			f.servePatch(t, w, r)
		case http.MethodDelete:
			f.serveDelete(w, r)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
}

// collections names each list path and the kind it returns. A
// namespaced resource is listed across every namespace, which is one
// more path than the objects themselves are under.
var collections = map[string]string{
	pairingBase + "/adapters":        adapterKind,
	pairingBase + "/peripherals":     peripheralKind,
	pairingBase + "/pairingrequests": pairingRequestKind,
}

func (f *apiFixture) serveGet(t *testing.T, w http.ResponseWriter, r *http.Request) {
	if kind, listed := collections[r.URL.Path]; listed {
		selector := r.URL.Query().Get("labelSelector")
		items := []map[string]any{}
		for _, path := range sortedPaths(f.objects) {
			object := f.objects[path]
			if object["kind"] != kind || !matchesSelector(object, selector) {
				continue
			}
			items = append(items, object)
		}
		writeJSON(t, w, map[string]any{"items": items})
		return
	}
	object, found := f.objects[r.URL.Path]
	if !found {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	writeJSON(t, w, object)
}

func (f *apiFixture) serveCreate(t *testing.T, w http.ResponseWriter, r *http.Request) {
	object := decodeBody(t, r)
	metadata := meta(object)
	name, _ := metadata["name"].(string)
	path := r.URL.Path + "/" + name
	if _, exists := f.objects[path]; exists {
		w.WriteHeader(http.StatusConflict)
		return
	}
	// The API server assigns these two, and both matter here: an owner
	// reference names a UID, and a resourceVersion makes the next write
	// conditional.
	metadata["uid"] = "uid-" + name
	f.version++
	metadata["resourceVersion"] = strconv.Itoa(f.version)
	// A resource with a status subresource drops any status a create
	// stated.
	delete(object, "status")
	f.objects[path] = object
	writeJSON(t, w, object)
}

func (f *apiFixture) serveReplace(t *testing.T, w http.ResponseWriter, r *http.Request) {
	path, status := strings.CutSuffix(r.URL.Path, "/status")
	stored, found := f.objects[path]
	if !found {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	object := decodeBody(t, r)
	if !currentVersion(stored, object) {
		// Optimistic concurrency, which every write here states: a
		// version from before somebody else's write is refused.
		w.WriteHeader(http.StatusConflict)
		return
	}
	f.version++
	if status {
		// The status subresource changes status and nothing else.
		stored["status"] = object["status"]
		meta(stored)["resourceVersion"] = strconv.Itoa(f.version)
		writeJSON(t, w, stored)
		return
	}
	meta(object)["resourceVersion"] = strconv.Itoa(f.version)
	object["status"] = stored["status"]
	f.objects[path] = object
	writeJSON(t, w, object)
}

// servePatch applies a merge patch to an object's metadata, which is
// the only patch this operator sends. An object that is deleting and
// has lost its last finalizer is removed, the way the API server
// removes one.
func (f *apiFixture) servePatch(t *testing.T, w http.ResponseWriter, r *http.Request) {
	stored, found := f.objects[r.URL.Path]
	if !found {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	patch := decodeBody(t, r)
	if !currentVersion(stored, patch) {
		w.WriteHeader(http.StatusConflict)
		return
	}
	for key, value := range meta(patch) {
		meta(stored)[key] = value
	}
	f.version++
	meta(stored)["resourceVersion"] = strconv.Itoa(f.version)
	if finalizers(stored) == 0 && meta(stored)["deletionTimestamp"] != nil {
		delete(f.objects, r.URL.Path)
	}
	writeJSON(t, w, stored)
}

// serveDelete stamps a deletionTimestamp on an object that has a
// finalizer, and removes one that does not.
func (f *apiFixture) serveDelete(w http.ResponseWriter, r *http.Request) {
	stored, found := f.objects[r.URL.Path]
	if !found {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if finalizers(stored) > 0 {
		meta(stored)["deletionTimestamp"] = timestamp(testNow)
		w.WriteHeader(http.StatusOK)
		return
	}
	delete(f.objects, r.URL.Path)
	w.WriteHeader(http.StatusOK)
}

// put stores an object the test wrote, at the path the operator reads
// it from.
func (f *apiFixture) put(t *testing.T, path string, object any) {
	t.Helper()
	raw, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	stored := map[string]any{}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if meta(stored)["uid"] == nil {
		meta(stored)["uid"] = "uid-" + fmt.Sprint(meta(stored)["name"])
	}
	f.version++
	meta(stored)["resourceVersion"] = strconv.Itoa(f.version)
	f.objects[path] = stored
}

// read decodes one stored object, and fails the test when the object
// is not there.
func read[T any](t *testing.T, fixture *apiFixture, path string) *T {
	t.Helper()
	stored, found := fixture.objects[path]
	if !found {
		t.Fatalf("no object at %s; the fixture holds %v", path, sortedPaths(fixture.objects))
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	object := new(T)
	if err := json.Unmarshal(raw, object); err != nil {
		t.Fatal(err)
	}
	return object
}

// currentVersion reports whether a write states the version the
// object holds now. A write with no version at all is accepted, which
// is what the API server does.
func currentVersion(stored, write map[string]any) bool {
	version, stated := meta(write)["resourceVersion"].(string)
	if !stated || version == "" {
		return true
	}
	return version == meta(stored)["resourceVersion"]
}

func meta(object map[string]any) map[string]any {
	metadata, ok := object["metadata"].(map[string]any)
	if !ok {
		metadata = map[string]any{}
		object["metadata"] = metadata
	}
	return metadata
}

func finalizers(object map[string]any) int {
	held, _ := meta(object)["finalizers"].([]any)
	return len(held)
}

func matchesSelector(object map[string]any, selector string) bool {
	if selector == "" {
		return true
	}
	key, value, _ := strings.Cut(selector, "=")
	labels, _ := meta(object)["labels"].(map[string]any)
	return labels[key] == value
}

func sortedPaths(objects map[string]map[string]any) []string {
	paths := make([]string, 0, len(objects))
	for path := range objects {
		paths = append(paths, path)
	}
	// A list comes back in a stable order, so a test that reads the
	// first item reads the same item every run.
	for i := 1; i < len(paths); i++ {
		for j := i; j > 0 && paths[j] < paths[j-1]; j-- {
			paths[j], paths[j-1] = paths[j-1], paths[j]
		}
	}
	return paths
}

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	object := map[string]any{}
	if err := json.NewDecoder(r.Body).Decode(&object); err != nil {
		t.Fatalf("decoding a %s to %s: %v", r.Method, r.URL.Path, err)
	}
	return object
}

func writeJSON(t *testing.T, w http.ResponseWriter, object any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(object); err != nil {
		t.Fatal(err)
	}
}

// testInventory wires an inventory to the fixtures, with the CDI
// directory pointed at one the test owns so that no claim holds
// anything unless the test writes one.
func testInventory(t *testing.T, fixture *apiFixture, radio *fakeRadio) *inventory {
	t.Helper()
	cdiTempDir(t)
	i := newInventory(testClient(t, fixture.handler(t)), radio, relaysFor(t), "liken-1", "liken-system")
	i.now = func() time.Time { return testNow }
	return i
}

// testAdapterName is the Adapter object's name, which is the radio's
// address in the form a Kubernetes name accepts.
const testAdapterName = "14-b4-57-91-2f-c8"

func testAdapterObjectPath() string { return adapterPath(testAdapterName) }
