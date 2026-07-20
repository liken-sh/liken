package kubernetes

// These tests cover the ResourceSlice publisher's decisions: create
// the slice when it is absent, leave the slice alone when it is
// current, replace the slice with an increased pool generation when
// the inventory changed, and delete the slice when the last device
// is gone.

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
			f.existing = nil
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
}

func testOwner() OwnerReference {
	return OwnerReference{APIVersion: "v1", Kind: "Node", Name: "node-1", UID: "abc-123"}
}

func testDevices() []SliceDevice {
	return []SliceDevice{{
		Name:       "usb-2-1-1-0",
		Attributes: map[string]DeviceAttribute{"driver": AttrString("uas")},
	}}
}

func TestEnsureCreatesTheSliceOnFirstPublish(t *testing.T) {
	fixture := &slicePublishFixture{}
	client := testClient(t, fixture.handler(t))

	if err := EnsureResourceSlice(client, "node-1", testOwner(), testDevices()); err != nil {
		t.Fatal(err)
	}
	if fixture.created == nil {
		t.Fatal("no slice was created")
	}
	slice := fixture.created
	if slice.Metadata.Name != "node-1-liken.sh" {
		t.Errorf("name = %q", slice.Metadata.Name)
	}
	if slice.Spec.Driver != "liken.sh" || slice.Spec.NodeName != "node-1" {
		t.Errorf("spec = %+v", slice.Spec)
	}
	if slice.Spec.Pool.Name != "node-1" || slice.Spec.Pool.Generation != 1 || slice.Spec.Pool.ResourceSliceCount != 1 {
		t.Errorf("pool = %+v", slice.Spec.Pool)
	}
	if len(slice.Metadata.OwnerReferences) != 1 || slice.Metadata.OwnerReferences[0].UID != "abc-123" {
		t.Errorf("ownerReferences = %+v", slice.Metadata.OwnerReferences)
	}
	if len(slice.Spec.Devices) != 1 || slice.Spec.Devices[0].Name != "usb-2-1-1-0" {
		t.Errorf("devices = %+v", slice.Spec.Devices)
	}
}

func TestEnsureLeavesAnUnchangedSliceAlone(t *testing.T) {
	fixture := &slicePublishFixture{existing: &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "node-1-liken.sh", ResourceVersion: "7"},
		Spec: ResourceSliceSpec{
			Driver:   "liken.sh",
			NodeName: "node-1",
			Pool:     ResourcePool{Name: "node-1", Generation: 3, ResourceSliceCount: 1},
			Devices:  testDevices(),
		},
	}}
	client := testClient(t, fixture.handler(t))

	if err := EnsureResourceSlice(client, "node-1", testOwner(), testDevices()); err != nil {
		t.Fatal(err)
	}
	if fixture.created != nil || fixture.updated != nil {
		t.Errorf("an unchanged inventory must not write: %v", fixture.requests)
	}
}

func TestEnsureReplacesAChangedSliceAndBumpsTheGeneration(t *testing.T) {
	fixture := &slicePublishFixture{existing: &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "node-1-liken.sh", ResourceVersion: "7"},
		Spec: ResourceSliceSpec{
			Driver:   "liken.sh",
			NodeName: "node-1",
			Pool:     ResourcePool{Name: "node-1", Generation: 3, ResourceSliceCount: 1},
			Devices:  []SliceDevice{{Name: "usb-2-2"}},
		},
	}}
	client := testClient(t, fixture.handler(t))

	if err := EnsureResourceSlice(client, "node-1", testOwner(), testDevices()); err != nil {
		t.Fatal(err)
	}
	if fixture.updated == nil {
		t.Fatal("a changed inventory must replace the slice")
	}
	if fixture.updated.Spec.Pool.Generation != 4 {
		t.Errorf("generation = %d, want the old one plus one", fixture.updated.Spec.Pool.Generation)
	}
	if fixture.updated.Metadata.ResourceVersion != "7" {
		t.Errorf("resourceVersion = %q, want the read copy's carried into the write", fixture.updated.Metadata.ResourceVersion)
	}
	if len(fixture.updated.Spec.Devices) != 1 || fixture.updated.Spec.Devices[0].Name != "usb-2-1-1-0" {
		t.Errorf("devices = %+v", fixture.updated.Spec.Devices)
	}
}

func TestEnsureDeletesTheSliceWhenNoDevicesRemain(t *testing.T) {
	fixture := &slicePublishFixture{existing: &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "node-1-liken.sh", ResourceVersion: "7"},
		Spec: ResourceSliceSpec{
			Driver: "liken.sh",
			Pool:   ResourcePool{Name: "node-1", Generation: 3, ResourceSliceCount: 1},
			Devices: []SliceDevice{
				{Name: "usb-2-2"},
			},
		},
	}}
	client := testClient(t, fixture.handler(t))

	if err := EnsureResourceSlice(client, "node-1", testOwner(), nil); err != nil {
		t.Fatal(err)
	}
	if fixture.existing != nil {
		t.Error("an empty inventory must delete the slice")
	}
}

func TestEnsureDoesNothingWhenAbsentAndEmpty(t *testing.T) {
	fixture := &slicePublishFixture{}
	client := testClient(t, fixture.handler(t))

	if err := EnsureResourceSlice(client, "node-1", testOwner(), nil); err != nil {
		t.Fatal(err)
	}
	if len(fixture.requests) != 1 {
		t.Errorf("requests = %v, want only the read", fixture.requests)
	}
}
