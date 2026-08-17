package main

// The fixtures the three pairing controllers are driven with: an API
// server that holds the custom resources the way Kubernetes holds
// them, and a radio that records what the operator asked it to do.
//
// The API fixture keeps the two behaviors these controllers depend on.
// A delete against an object that carries a finalizer stamps a
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

	"github.com/liken-sh/bluetooth-operator/bonds"
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
// more path than the objects themselves live under.
var collections = map[string]string{
	pairingBase + "/adapters":        adapterKind,
	pairingBase + "/pairings":        pairingKind,
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
	// The API server assigns these two, and both matter here: a UID is
	// what an owner reference names, and a resourceVersion is what makes
	// the next write conditional.
	metadata["uid"] = "uid-" + name
	f.version++
	metadata["resourceVersion"] = strconv.Itoa(f.version)
	// A resource with a status subresource drops any status a create
	// carried.
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
		// Optimistic concurrency, which every write here carries: a
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

// serveDelete stamps a deletionTimestamp on an object that carries a
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

// currentVersion reports whether a write carries the version the
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

// fakeRadio returns a snapshot the test supplies, and records
// every call the controllers make into bluetoothd.
type fakeRadio struct {
	snapshot radioSnapshot
	err      error
	calls    []string

	// pairErr makes Pair fail, the way bluetoothd does for a controller
	// that stopped answering mid-pairing.
	pairErr error
}

func (r *fakeRadio) record(format string, args ...any) {
	r.calls = append(r.calls, fmt.Sprintf(format, args...))
}

// called reports whether this call was recorded, matched by prefix,
// so a test names the call and not its arguments when the arguments
// do not matter.
func (r *fakeRadio) called(prefix string) bool {
	for _, call := range r.calls {
		if strings.HasPrefix(call, prefix) {
			return true
		}
	}
	return false
}

func (r *fakeRadio) Snapshot() (radioSnapshot, error) {
	if r.err != nil {
		return radioSnapshot{}, r.err
	}
	return r.snapshot, nil
}

func (r *fakeRadio) SetAdapterAlias(alias string) error {
	r.record("SetAdapterAlias %s", alias)
	r.snapshot.Adapter.Alias = alias
	return nil
}

func (r *fakeRadio) SetDeviceAlias(device bonds.Address, alias string) error {
	r.record("SetDeviceAlias %s %s", device.Key(), alias)
	r.update(device, func(state *deviceState) { state.Alias = alias })
	return nil
}

func (r *fakeRadio) SetDeviceTrusted(device bonds.Address, trusted bool) error {
	r.record("SetDeviceTrusted %s %t", device.Key(), trusted)
	r.update(device, func(state *deviceState) { state.Trusted = trusted })
	return nil
}

func (r *fakeRadio) OpenWindow(window time.Duration) error {
	r.record("OpenWindow %s", window)
	r.snapshot.Adapter.Discoverable = true
	r.snapshot.Adapter.Pairable = true
	r.snapshot.Adapter.Discovering = true
	return nil
}

func (r *fakeRadio) CloseWindow() error {
	r.record("CloseWindow")
	r.snapshot.Adapter.Discoverable = false
	r.snapshot.Adapter.Pairable = false
	r.snapshot.Adapter.Discovering = false
	return nil
}

func (r *fakeRadio) Pair(device bonds.Address) error {
	r.record("Pair %s", device.Key())
	if r.pairErr != nil {
		return r.pairErr
	}
	r.update(device, func(state *deviceState) { state.Paired = true })
	return nil
}

func (r *fakeRadio) Disconnect(device bonds.Address) error {
	r.record("Disconnect %s", device.Key())
	r.update(device, func(state *deviceState) { state.Connected = false })
	return nil
}

func (r *fakeRadio) Remove(device bonds.Address) error {
	r.record("Remove %s", device.Key())
	kept := make([]deviceState, 0, len(r.snapshot.Devices))
	for _, state := range r.snapshot.Devices {
		if state.Address != device {
			kept = append(kept, state)
		}
	}
	r.snapshot.Devices = kept
	return nil
}

func (r *fakeRadio) update(device bonds.Address, change func(*deviceState)) {
	for index := range r.snapshot.Devices {
		if r.snapshot.Devices[index].Address == device {
			change(&r.snapshot.Devices[index])
		}
	}
}

// testRadio is one adapter with the devices a test names.
func testRadio(t *testing.T, devices ...deviceState) *fakeRadio {
	t.Helper()
	return &fakeRadio{snapshot: radioSnapshot{
		Adapter: adapterState{Address: testAdapterAddress(t), Powered: true, Alias: "liken-1"},
		Devices: devices,
	}}
}

// pairedDevice is a controller with a bond, as bluetoothd reports it.
func pairedDevice(t *testing.T, address string) deviceState {
	t.Helper()
	return deviceState{
		Address:   testAddress(t, address),
		Name:      "DualSense Wireless Controller",
		Alias:     "DualSense Wireless Controller",
		Paired:    true,
		Connected: true,
		Trusted:   true,
	}
}

// seenDevice is a controller the radio has observed and holds no bond
// with, which is the kind of device a pairing window reports.
func seenDevice(t *testing.T, address, name string) deviceState {
	t.Helper()
	return deviceState{Address: testAddress(t, address), Name: name, Alias: name}
}

// testInventory wires an inventory to the fixtures, with the CDI
// directory pointed at one the test owns so that no claim holds
// anything unless the test writes one.
func testInventory(t *testing.T, fixture *apiFixture, radio *fakeRadio) *inventory {
	t.Helper()
	cdiTempDir(t)
	i := newInventory(testClient(t, fixture.handler(t)), radio, "liken-1", "liken-system")
	i.now = func() time.Time { return testNow }
	return i
}

// testAdapterName is the Adapter object's name, which is the radio's
// address in the form a Kubernetes name accepts.
const testAdapterName = "14-b4-57-91-2f-c8"

func testAdapterObjectPath() string { return adapterPath(testAdapterName) }
