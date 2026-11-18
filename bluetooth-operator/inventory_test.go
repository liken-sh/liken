package main

// This file has the fake API server that the operator's tests share,
// and the tests of the pass's metrics. The fake radio is in
// radio_test.go.
//
// The fake API server copies two behaviors of the real one that the
// operator depends on. A delete of an object that has a finalizer sets
// deletionTimestamp and keeps the object, and the object is removed
// when the last finalizer is patched off. A write to the status
// subresource changes status and nothing else.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testNow is the fixed time that tests give the inventory as its
// clock, so a test can compute a window's deadline without waiting.
var testNow = time.Date(2026, 8, 17, 17, 30, 0, 0, time.UTC)

// apiFixture is an in-memory API server that stores objects by their
// request path.
type apiFixture struct {
	objects  map[string]map[string]any
	requests []string
	version  int

	// failWrites is an HTTP status that every non-GET request returns,
	// such as 403 for a write that RBAC denies. Zero means writes
	// succeed.
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
			t.Errorf("the fake API server got %s %s, which it does not serve", r.Method, r.URL.Path)
		}
	})
}

// collections maps each list path to the kind it returns. The paths
// list a namespaced kind across all namespaces, so they are not the
// paths the objects are stored under.
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
	// The API server assigns both of these. Owner references point at
	// the uid, and writes are conditional on the resourceVersion.
	metadata["uid"] = "uid-" + name
	f.version++
	metadata["resourceVersion"] = strconv.Itoa(f.version)
	// A create ignores status on a resource that has a status
	// subresource.
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
		// A write that carries a resourceVersion older than the stored
		// one fails with 409, as in the real API server.
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
// the only kind of patch this operator sends. It removes an object
// that has a deletionTimestamp when the patch takes off the last
// finalizer.
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

// serveDelete sets deletionTimestamp on an object that has a
// finalizer, and removes an object that has none.
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

// put stores an object at the path the operator reads it from.
func (f *apiFixture) put(t *testing.T, path string, object any) {
	t.Helper()
	raw, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("encoding the object for %s: %v", path, err)
	}
	stored := map[string]any{}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("decoding the object for %s: %v", path, err)
	}
	if meta(stored)["uid"] == nil {
		meta(stored)["uid"] = "uid-" + fmt.Sprint(meta(stored)["name"])
	}
	f.version++
	meta(stored)["resourceVersion"] = strconv.Itoa(f.version)
	f.objects[path] = stored
}

// read returns the stored object at path as a T, and fails the test
// when there is none.
func read[T any](t *testing.T, fixture *apiFixture, path string) *T {
	t.Helper()
	stored, found := fixture.objects[path]
	if !found {
		t.Fatalf("the fake API server has no object at %s, only %v", path, sortedPaths(fixture.objects))
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("encoding the object at %s: %v", path, err)
	}
	object := new(T)
	if err := json.Unmarshal(raw, object); err != nil {
		t.Fatalf("decoding the object at %s: %v", path, err)
	}
	return object
}

// currentVersion reports whether a write's resourceVersion matches the
// stored object's. A write with no resourceVersion matches, as it does
// in the API server.
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
	finalizers, _ := meta(object)["finalizers"].([]any)
	return len(finalizers)
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
	// Sorting makes a list return its items in the same order on every
	// run.
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
		t.Fatalf("decoding the body of %s %s: %v", r.Method, r.URL.Path, err)
	}
	return object
}

func writeJSON(t *testing.T, w http.ResponseWriter, object any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(object); err != nil {
		t.Fatalf("writing the response: %v", err)
	}
}

// testInventory builds an inventory on the fake API server and the
// given radio. The CDI directory is a temporary one, so no claim holds
// a device unless the test writes a spec.
func testInventory(t *testing.T, fixture *apiFixture, radio *fakeRadio) *inventory {
	t.Helper()
	cdiTempDir(t)
	sysfsFor(t)
	i := newInventory(testClient(t, fixture.handler(t)), radio, relaysFor(t), "liken-1", "liken-system", newMetrics())
	i.now = func() time.Time { return testNow }
	return i
}

// testAdapterName is the name of the Adapter for the test radio: its
// address in the form that a Kubernetes name allows.
const testAdapterName = "14-b4-57-91-2f-c8"

func testAdapterObjectPath() string { return adapterPath(testAdapterName) }

// A pass that finishes records one run in the duration histogram for
// each of the three kinds, and no error for any of them.
func TestReconcileTimesEachResourceKind(t *testing.T) {
	fixture := newAPIFixture()
	inventory := testInventory(t, fixture, testRadio(t, pairedDevice(t, testDevice)))

	inventory.reconcile()

	for _, kind := range []string{adapterKind, peripheralKind, pairingRequestKind} {
		if runs, found := metricValue(t, inventory.metrics.registry,
			"bluetooth_reconcile_duration_seconds", map[string]string{"kind": kind}); !found || runs != 1 {
			t.Errorf("bluetooth_reconcile_duration_seconds{kind=%q} ran %v times (found: %v), want 1", kind, runs, found)
		}
		if errs, found := metricValue(t, inventory.metrics.registry,
			"bluetooth_reconcile_errors_total", map[string]string{"kind": kind}); found && errs != 0 {
			t.Errorf("bluetooth_reconcile_errors_total{kind=%q} = %v, want none on a pass that finished", kind, errs)
		}
	}
}

// When the API server refuses the write that creates the Adapter,
// reconcile counts one Adapter error and returns before the Peripheral
// and PairingRequest parts run, so they record no duration.
func TestReconcileCountsAnAdapterErrorAndStops(t *testing.T) {
	fixture := newAPIFixture()
	fixture.failWrites = http.StatusForbidden
	inventory := testInventory(t, fixture, testRadio(t, pairedDevice(t, testDevice)))

	if pass := inventory.reconcile(); pass.ok {
		t.Fatal("reconcile finished the pass although the API server refused the write")
	}

	if errs, found := metricValue(t, inventory.metrics.registry,
		"bluetooth_reconcile_errors_total", map[string]string{"kind": adapterKind}); !found || errs != 1 {
		t.Errorf("bluetooth_reconcile_errors_total{kind=Adapter} = %v (found: %v), want 1", errs, found)
	}
	for _, kind := range []string{peripheralKind, pairingRequestKind} {
		if _, found := metricValue(t, inventory.metrics.registry,
			"bluetooth_reconcile_duration_seconds", map[string]string{"kind": kind}); found {
			t.Errorf("bluetooth_reconcile_duration_seconds{kind=%q} has a sample, but the Adapter part of the pass never returned", kind)
		}
	}
}

// bluetooth_adapter_present is 1 while bluetoothd reports an adapter
// and 0 once it reports none.
func TestAdapterPresentGaugeFollowsTheRadio(t *testing.T) {
	fixture := newAPIFixture()
	radio := testRadio(t, pairedDevice(t, testDevice))
	inventory := testInventory(t, fixture, radio)

	inventory.reconcile()
	if present, found := metricValue(t, inventory.metrics.registry, "bluetooth_adapter_present", nil); !found || present != 1 {
		t.Errorf("bluetooth_adapter_present = %v (found: %v), want 1", present, found)
	}

	radio.err = ErrNoAdapter
	inventory.reconcile()
	if present, found := metricValue(t, inventory.metrics.registry, "bluetooth_adapter_present", nil); !found || present != 0 {
		t.Errorf("bluetooth_adapter_present = %v (found: %v), want 0 after bluetoothd stopped reporting the radio", present, found)
	}
}

// A read that fails with any error other than ErrNoAdapter does not
// show whether the adapter is there. The gauge keeps the value from
// the last valid read, and bluetooth_observation_valid drops to 0.
func TestAdapterPresentGaugeKeepsItsValueWhenTheReadFails(t *testing.T) {
	fixture := newAPIFixture()
	radio := testRadio(t, pairedDevice(t, testDevice))
	inventory := testInventory(t, fixture, radio)
	inventory.reconcile()

	radio.err = fmt.Errorf("the bus went away")
	inventory.reconcile()

	if present, found := metricValue(t, inventory.metrics.registry, "bluetooth_adapter_present", nil); !found || present != 1 {
		t.Errorf("bluetooth_adapter_present = %v (found: %v), want the last valid reading of 1", present, found)
	}
	if valid, found := metricValue(t, inventory.metrics.registry,
		"bluetooth_observation_valid", map[string]string{"source": sourceBlueZ}); !found || valid != 0 {
		t.Errorf("bluetooth_observation_valid{source=bluez} = %v (found: %v), want 0", valid, found)
	}
}

// A successful read counts as a valid observation even when it reports
// no adapter. The success timestamp comes from the inventory's clock,
// so it is testNow here.
func TestObservationIsValidWhenTheRadioReportsNoAdapter(t *testing.T) {
	fixture := newAPIFixture()
	radio := testRadio(t, pairedDevice(t, testDevice))
	radio.err = ErrNoAdapter
	inventory := testInventory(t, fixture, radio)

	inventory.reconcile()

	if valid, found := metricValue(t, inventory.metrics.registry,
		"bluetooth_observation_valid", map[string]string{"source": sourceBlueZ}); !found || valid != 1 {
		t.Errorf("bluetooth_observation_valid{source=bluez} = %v (found: %v), want 1 because bluetoothd reported no adapter, which is a valid reading", valid, found)
	}
	success, found := metricValue(t, inventory.metrics.registry,
		"bluetooth_observation_last_success_timestamp_seconds", map[string]string{"source": sourceBlueZ})
	if !found || success != float64(testNow.Unix()) {
		t.Errorf("bluetooth_observation_last_success_timestamp_seconds{source=bluez} = %v (found: %v), want %d",
			success, found, testNow.Unix())
	}
}
