package main

// These tests cover the publisher's four outcomes against a test API
// server: create the slice when it is absent, leave a current slice
// alone, replace a changed one with an increased pool generation, and
// delete the slice only when the paired set is authoritatively empty.

import (
	"encoding/json"
	"net/http"
	"testing"
)

// slicePublishFixture is a small API server that holds at most one
// ResourceSlice. It remembers the requests it received.
type slicePublishFixture struct {
	existing *ResourceSlice
	requests []string
	created  *ResourceSlice
	updated  *ResourceSlice
	deleted  bool
}

func (f *slicePublishFixture) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		switch r.Method {
		case http.MethodGet:
			if f.existing == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(f.existing)
		case http.MethodPost:
			f.created = &ResourceSlice{}
			_ = json.NewDecoder(r.Body).Decode(f.created)
			_ = json.NewEncoder(w).Encode(f.created)
		case http.MethodPut:
			f.updated = &ResourceSlice{}
			_ = json.NewDecoder(r.Body).Decode(f.updated)
			_ = json.NewEncoder(w).Encode(f.updated)
		case http.MethodDelete:
			f.deleted = true
			f.existing = nil
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
}

func testOwner() OwnerReference {
	return OwnerReference{APIVersion: "v1", Kind: "Node", Name: "liken-1", UID: "abc-123"}
}

// publishedDevice is one connected controller as the slice holds it.
func publishedDevice() SliceDevice {
	return SliceDevice{
		Name: "a0-ab-51-33-b7-12",
		Attributes: map[string]DeviceAttribute{
			"address":   AttrString("A0:AB:51:33:B7:12"),
			"connected": AttrBool(true),
		},
	}
}

func TestEnsureCreatesTheSliceOnFirstPublish(t *testing.T) {
	fixture := &slicePublishFixture{}
	client := testClient(t, fixture.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), []SliceDevice{publishedDevice()}); err != nil {
		t.Fatal(err)
	}
	slice := fixture.created
	if slice == nil {
		t.Fatal("no slice was created")
	}
	// The driver name is the suffix, so this slice and liken's own
	// slice for the same node cannot collide.
	if slice.Metadata.Name != "liken-1-bluetooth.liken.sh" {
		t.Errorf("name = %q", slice.Metadata.Name)
	}
	if slice.Spec.Driver != DriverName || slice.Spec.NodeName != "liken-1" {
		t.Errorf("spec = %+v", slice.Spec)
	}
	if slice.Spec.Pool.Name != "liken-1" || slice.Spec.Pool.Generation != 1 || slice.Spec.Pool.ResourceSliceCount != 1 {
		t.Errorf("pool = %+v", slice.Spec.Pool)
	}
	if len(slice.Metadata.OwnerReferences) != 1 || slice.Metadata.OwnerReferences[0].UID != "abc-123" {
		t.Errorf("ownerReferences = %+v", slice.Metadata.OwnerReferences)
	}
}

func TestEnsureWritesNothingWhenNothingMoved(t *testing.T) {
	fixture := &slicePublishFixture{existing: &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "liken-1-bluetooth.liken.sh", ResourceVersion: "7"},
		Spec: ResourceSliceSpec{
			Driver:   DriverName,
			NodeName: "liken-1",
			Pool:     ResourcePool{Name: "liken-1", Generation: 3, ResourceSliceCount: 1},
			Devices:  []SliceDevice{publishedDevice()},
		},
	}}
	client := testClient(t, fixture.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), []SliceDevice{publishedDevice()}); err != nil {
		t.Fatal(err)
	}
	if fixture.updated != nil || fixture.created != nil || fixture.deleted {
		t.Fatalf("a steady machine wrote to the API: %v", fixture.requests)
	}
}

func TestEnsureIgnoresTheServersTaintTimestamp(t *testing.T) {
	// The API server fills TimeAdded in on every taint it stores. A
	// publisher that read it would rewrite the slice on every pass,
	// and every write wakes every DRA-pending pod in the cluster.
	stored := publishedDevice()
	stored.Attributes["connected"] = AttrBool(false)
	stored.Taints = []DeviceTaint{
		{Key: disconnectedTaint, Effect: "NoExecute", TimeAdded: "2026-08-16T12:00:00Z"},
		{Key: noInputNodeTaint, Effect: "NoSchedule", TimeAdded: "2026-08-16T12:00:00Z"},
	}
	fixture := &slicePublishFixture{existing: &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "liken-1-bluetooth.liken.sh", ResourceVersion: "7"},
		Spec: ResourceSliceSpec{
			Driver:   DriverName,
			NodeName: "liken-1",
			Pool:     ResourcePool{Name: "liken-1", Generation: 3, ResourceSliceCount: 1},
			Devices:  []SliceDevice{stored},
		},
	}}
	client := testClient(t, fixture.handler(t))

	fresh := sliceDevices(
		map[string]controller{"a0:ab:51:33:b7:12": {Connected: false}},
		nil,
	)
	if err := EnsureResourceSlice(client, "liken-1", testOwner(), fresh); err != nil {
		t.Fatal(err)
	}
	if fixture.updated != nil {
		t.Fatalf("the stored timestamp caused a rewrite: %+v", fixture.updated.Spec.Devices)
	}
}

func TestEnsureReplacesAChangedSliceAndBumpsTheGeneration(t *testing.T) {
	fixture := &slicePublishFixture{existing: &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "liken-1-bluetooth.liken.sh", ResourceVersion: "7"},
		Spec: ResourceSliceSpec{
			Driver:   DriverName,
			NodeName: "liken-1",
			Pool:     ResourcePool{Name: "liken-1", Generation: 3, ResourceSliceCount: 1},
			Devices:  []SliceDevice{publishedDevice()},
		},
	}}
	client := testClient(t, fixture.handler(t))

	// The controller went off the air, so it takes both taints and
	// stays in the slice.
	tainted := sliceDevices(map[string]controller{"a0:ab:51:33:b7:12": {}}, nil)
	if err := EnsureResourceSlice(client, "liken-1", testOwner(), tainted); err != nil {
		t.Fatal(err)
	}
	if fixture.updated == nil {
		t.Fatal("the changed slice was not written")
	}
	if fixture.updated.Spec.Pool.Generation != 4 {
		t.Errorf("generation = %d, want 4", fixture.updated.Spec.Pool.Generation)
	}
	// The write includes the resourceVersion from the read, so a
	// conflicting writer gets a 409 instead of losing its change.
	if fixture.updated.Metadata.ResourceVersion != "7" {
		t.Errorf("resourceVersion = %q", fixture.updated.Metadata.ResourceVersion)
	}
	if len(fixture.updated.Spec.Devices) != 1 {
		t.Fatalf("devices = %+v", fixture.updated.Spec.Devices)
	}
	if len(fixture.updated.Spec.Devices[0].Taints) != 2 {
		t.Errorf("taints = %+v", fixture.updated.Spec.Devices[0].Taints)
	}
}

func TestEnsureDeletesTheSliceWhenTheLastControllerIsUnpaired(t *testing.T) {
	fixture := &slicePublishFixture{existing: &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "liken-1-bluetooth.liken.sh"},
		Spec:     ResourceSliceSpec{Devices: []SliceDevice{publishedDevice()}},
	}}
	client := testClient(t, fixture.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), nil); err != nil {
		t.Fatal(err)
	}
	if !fixture.deleted {
		t.Fatal("an empty paired set did not delete the slice")
	}
}

// The next three tests read the line the publisher prints for each
// outcome. A slice that nobody rewrites and a slice that an operator
// died and left behind hold the same resourceVersion and the same pool
// generation, so the log is the only place the two come apart.

func TestEnsureLogsTheSliceItCreated(t *testing.T) {
	capture := captureSliceLog(t)
	fixture := &slicePublishFixture{}
	client := testClient(t, fixture.handler(t))

	// The controller is paired and switched off, so it publishes with
	// both taints on it.
	if err := EnsureResourceSlice(client, "liken-1", testOwner(),
		sliceDevices(map[string]controller{"a0:ab:51:33:b7:12": {}}, nil)); err != nil {
		t.Fatal(err)
	}
	want := "slice: created generation 1, 1 device, 1 tainted: a0-ab-51-33-b7-12 has " +
		disconnectedTaint + ", " + noInputNodeTaint
	if got := capture.only(t); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

func TestEnsureLogsTheSliceItWrote(t *testing.T) {
	capture := captureSliceLog(t)
	fixture := &slicePublishFixture{existing: &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "liken-1-bluetooth.liken.sh", ResourceVersion: "7"},
		Spec: ResourceSliceSpec{
			Driver:   DriverName,
			NodeName: "liken-1",
			Pool:     ResourcePool{Name: "liken-1", Generation: 3, ResourceSliceCount: 1},
			Devices:  []SliceDevice{publishedDevice()},
		},
	}}
	client := testClient(t, fixture.handler(t))

	// The controller went off the air. The device count does not move,
	// so the taints are the whole event, and they evict the pod that
	// held the claim.
	tainted := sliceDevices(map[string]controller{"a0:ab:51:33:b7:12": {}}, nil)
	if err := EnsureResourceSlice(client, "liken-1", testOwner(), tainted); err != nil {
		t.Fatal(err)
	}
	want := "slice: wrote generation 4, 1 device, 1 tainted: a0-ab-51-33-b7-12 gained " +
		disconnectedTaint + ", " + noInputNodeTaint
	if got := capture.only(t); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

func TestEnsureLogsThatNothingMoved(t *testing.T) {
	capture := captureSliceLog(t)
	fixture := &slicePublishFixture{existing: &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "liken-1-bluetooth.liken.sh", ResourceVersion: "7"},
		Spec: ResourceSliceSpec{
			Driver:   DriverName,
			NodeName: "liken-1",
			Pool:     ResourcePool{Name: "liken-1", Generation: 3, ResourceSliceCount: 1},
			Devices:  []SliceDevice{publishedDevice()},
		},
	}}
	client := testClient(t, fixture.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), []SliceDevice{publishedDevice()}); err != nil {
		t.Fatal(err)
	}
	if fixture.updated != nil {
		t.Fatalf("a steady machine wrote to the API: %v", fixture.requests)
	}
	want := "slice: unchanged at generation 3, 1 device, 0 tainted (1 pass)"
	if got := capture.only(t); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

func TestEnsureLogsTheSliceItDeleted(t *testing.T) {
	capture := captureSliceLog(t)
	fixture := &slicePublishFixture{existing: &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "liken-1-bluetooth.liken.sh"},
		Spec:     ResourceSliceSpec{Devices: []SliceDevice{publishedDevice()}},
	}}
	client := testClient(t, fixture.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), nil); err != nil {
		t.Fatal(err)
	}
	want := "slice: deleted, no adapter answered and nothing is paired"
	if got := capture.only(t); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

// TestReconcileNeverPublishesWithoutAnAdapter is the regression test
// for the empty answer that is not an empty paired set. bluetoothd
// publishes no device objects in the moments after it starts, and it
// removes every device object when the adapter goes away. Either
// answer reaching the publisher would delete a slice that a claim
// still held.
func TestReconcileNeverPublishesWithoutAnAdapter(t *testing.T) {
	if _, err := controllersFrom(nil); err != ErrNoAdapter {
		t.Fatalf("an empty object tree gave %v, want ErrNoAdapter", err)
	}
}
