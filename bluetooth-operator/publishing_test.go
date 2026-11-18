package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

// slicePublishFixture is a fake API server that stores at most one
// ResourceSlice and records the requests it receives.
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
			t.Errorf("the fake API server got %s %s, which it does not serve", r.Method, r.URL.Path)
		}
	})
}

func testOwner() OwnerReference {
	return OwnerReference{APIVersion: "v1", Kind: "Node", Name: "liken-1", UID: "abc-123"}
}

// publishedDevice is a connected controller as it appears in the
// slice.
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
		t.Fatalf("publishing the ResourceSlice: %v", err)
	}
	slice := fixture.created
	if slice == nil {
		t.Fatal("the publisher did not create a ResourceSlice on the first publish")
	}
	// The slice name ends with the driver name, so it differs from the
	// slice that liken itself publishes for the same node.
	if slice.Metadata.Name != "liken-1-bluetooth.liken.sh" {
		t.Errorf("the ResourceSlice name is %q, want liken-1-bluetooth.liken.sh", slice.Metadata.Name)
	}
	if slice.Spec.Driver != DriverName || slice.Spec.NodeName != "liken-1" {
		t.Errorf("the ResourceSlice has driver %q and node %q, want %q and liken-1", slice.Spec.Driver, slice.Spec.NodeName, DriverName)
	}
	if slice.Spec.Pool.Name != "liken-1" || slice.Spec.Pool.Generation != 1 || slice.Spec.Pool.ResourceSliceCount != 1 {
		t.Errorf("the pool is %+v, want name liken-1, generation 1, and 1 slice", slice.Spec.Pool)
	}
	if len(slice.Metadata.OwnerReferences) != 1 || slice.Metadata.OwnerReferences[0].UID != "abc-123" {
		t.Errorf("the ownerReferences are %+v, want only the Node with UID abc-123", slice.Metadata.OwnerReferences)
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
		t.Fatalf("publishing the ResourceSlice: %v", err)
	}
	if fixture.updated != nil || fixture.created != nil || fixture.deleted {
		t.Fatalf("the publisher wrote to the API with no change to publish: %v", fixture.requests)
	}
}

func TestEnsureIgnoresTheServersTaintTimestamp(t *testing.T) {
	// The API server sets TimeAdded on every taint it stores. If the
	// publisher compared it, the stored slice would never equal the
	// new one, and every pass would rewrite it. Each slice write makes
	// the scheduler retry every pod that waits on a claim.
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
		nil,
		nil,
	)
	if err := EnsureResourceSlice(client, "liken-1", testOwner(), fresh); err != nil {
		t.Fatalf("publishing the ResourceSlice: %v", err)
	}
	if fixture.updated != nil {
		t.Fatalf("the publisher rewrote the slice because of the stored taint timestamp: %+v", fixture.updated.Spec.Devices)
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

	// The controller is not connected, so it stays in the slice with
	// both taints.
	tainted := sliceDevices(map[string]controller{"a0:ab:51:33:b7:12": {}}, nil, nil, nil)
	if err := EnsureResourceSlice(client, "liken-1", testOwner(), tainted); err != nil {
		t.Fatalf("publishing the ResourceSlice: %v", err)
	}
	if fixture.updated == nil {
		t.Fatal("the publisher did not write the changed ResourceSlice")
	}
	if fixture.updated.Spec.Pool.Generation != 4 {
		t.Errorf("the pool generation is %d, want 4", fixture.updated.Spec.Pool.Generation)
	}
	// The write carries the resourceVersion from the read. A
	// concurrent writer then gets a 409 and does not lose its change.
	if fixture.updated.Metadata.ResourceVersion != "7" {
		t.Errorf("the write has resourceVersion %q, want 7, the version it read", fixture.updated.Metadata.ResourceVersion)
	}
	if len(fixture.updated.Spec.Devices) != 1 {
		t.Fatalf("the slice has devices %+v, want 1 device", fixture.updated.Spec.Devices)
	}
	if len(fixture.updated.Spec.Devices[0].Taints) != 2 {
		t.Errorf("the device has taints %+v, want 2", fixture.updated.Spec.Devices[0].Taints)
	}
}

func TestEnsureDeletesTheSliceWhenTheLastControllerIsUnpaired(t *testing.T) {
	fixture := &slicePublishFixture{existing: &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "liken-1-bluetooth.liken.sh"},
		Spec:     ResourceSliceSpec{Devices: []SliceDevice{publishedDevice()}},
	}}
	client := testClient(t, fixture.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), nil); err != nil {
		t.Fatalf("publishing the ResourceSlice: %v", err)
	}
	if !fixture.deleted {
		t.Fatal("the publisher did not delete the ResourceSlice for an empty paired set")
	}
}

// The next four tests check the log line that the publisher prints for
// each outcome. A publisher that finds nothing to change and one that
// has stopped leave the same slice, with the same resourceVersion and
// pool generation, so only the log line tells them apart.

func TestEnsureLogsTheSliceItCreated(t *testing.T) {
	capture := captureSliceLog(t)
	fixture := &slicePublishFixture{}
	client := testClient(t, fixture.handler(t))

	// The controller is paired and not connected, so it is published
	// with both taints.
	if err := EnsureResourceSlice(client, "liken-1", testOwner(),
		sliceDevices(map[string]controller{"a0:ab:51:33:b7:12": {}}, nil, nil, nil)); err != nil {
		t.Fatalf("publishing the ResourceSlice: %v", err)
	}
	want := "slice: created generation 1, 1 device, 1 tainted: a0-ab-51-33-b7-12 has " +
		disconnectedTaint + ", " + noInputNodeTaint
	if got := capture.only(t); got != want {
		t.Errorf("the log line is %q, want %q", got, want)
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

	// The controller is not connected, so the only change is its two
	// new taints.
	tainted := sliceDevices(map[string]controller{"a0:ab:51:33:b7:12": {}}, nil, nil, nil)
	if err := EnsureResourceSlice(client, "liken-1", testOwner(), tainted); err != nil {
		t.Fatalf("publishing the ResourceSlice: %v", err)
	}
	want := "slice: wrote generation 4, 1 device, 1 tainted: a0-ab-51-33-b7-12 gained " +
		disconnectedTaint + ", " + noInputNodeTaint
	if got := capture.only(t); got != want {
		t.Errorf("the log line is %q, want %q", got, want)
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
		t.Fatalf("publishing the ResourceSlice: %v", err)
	}
	if fixture.updated != nil {
		t.Fatalf("the publisher wrote to the API with no change to publish: %v", fixture.requests)
	}
	want := "slice: unchanged at generation 3, 1 device, 0 tainted (1 pass)"
	if got := capture.only(t); got != want {
		t.Errorf("the log line is %q, want %q", got, want)
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
		t.Fatalf("publishing the ResourceSlice: %v", err)
	}
	want := "slice: deleted the ResourceSlice, because the node has no device to publish"
	if got := capture.only(t); got != want {
		t.Errorf("the log line is %q, want %q", got, want)
	}
}

// An object tree with no adapter must not look like an empty paired
// set. bluetoothd has no device objects just after it starts, and it
// removes them all when the adapter goes away. If the publisher took
// either case as an empty paired set, it would delete a slice that a
// claim still uses.
func TestControllersFromATreeWithNoAdapterReturnsErrNoAdapter(t *testing.T) {
	if _, err := controllersFrom(nil); err != ErrNoAdapter {
		t.Fatalf("controllersFrom(nil) returned %v, want ErrNoAdapter", err)
	}
}
