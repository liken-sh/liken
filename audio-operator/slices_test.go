package main

// These tests cover two decisions: what one output publishes as a
// device, and when the operator writes the slice at all. The second
// set runs against a small API server that holds one ResourceSlice
// and records the requests it received.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	// The credentials are empty, so the client sends no bearer token
	// and reads no file from disk.
	return NewClient(server.URL, server.Client(), "")
}

func testOwner() OwnerReference {
	return OwnerReference{APIVersion: "v1", Kind: "Node", Name: "liken-1", UID: "abc-123"}
}

// slicePublishFixture is a small API server that holds at most one
// ResourceSlice. It records the requests it received.
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
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
}

func testDevices() []SliceDevice {
	return []SliceDevice{{
		Name:       testSinkName,
		Attributes: map[string]DeviceAttribute{"connectionType": AttrString("hdmi")},
	}}
}

func TestEnsureCreatesTheSliceOnFirstPublish(t *testing.T) {
	api := &slicePublishFixture{}
	client := testClient(t, api.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), testDevices()); err != nil {
		t.Fatal(err)
	}
	if api.created == nil {
		t.Fatal("no slice was created")
	}
	slice := api.created
	if slice.Metadata.Name != "liken-1-audio.liken.sh" {
		t.Errorf("name = %q", slice.Metadata.Name)
	}
	if slice.Spec.Driver != "audio.liken.sh" || slice.Spec.NodeName != "liken-1" {
		t.Errorf("spec = %+v", slice.Spec)
	}
	if slice.Spec.Pool.Name != "liken-1" || slice.Spec.Pool.Generation != 1 || slice.Spec.Pool.ResourceSliceCount != 1 {
		t.Errorf("pool = %+v", slice.Spec.Pool)
	}
	// The Node owns the slice, not the pod, so the slice outlives an
	// operator restart and leaves with the machine.
	if len(slice.Metadata.OwnerReferences) != 1 || slice.Metadata.OwnerReferences[0].UID != "abc-123" {
		t.Errorf("ownerReferences = %+v", slice.Metadata.OwnerReferences)
	}
}

func TestEnsureLeavesAnUnchangedSliceAlone(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	client := testClient(t, api.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), testDevices()); err != nil {
		t.Fatal(err)
	}
	if api.created != nil || api.updated != nil {
		t.Errorf("an unchanged inventory must not write: %v", api.requests)
	}
}

func TestEnsureReplacesAChangedSliceAndBumpsTheGeneration(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	client := testClient(t, api.handler(t))

	changed := testDevices()
	changed[0].Taints = []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute"}}
	if err := EnsureResourceSlice(client, "liken-1", testOwner(), changed); err != nil {
		t.Fatal(err)
	}
	if api.updated == nil {
		t.Fatal("a changed inventory did not write")
	}
	if got := api.updated.Spec.Pool.Generation; got != 4 {
		t.Errorf("generation = %d, want 4", got)
	}
	if len(api.updated.Spec.Devices[0].Taints) != 1 {
		t.Errorf("devices = %+v", api.updated.Spec.Devices)
	}
}

// An empty list is never a real state of a machine this operator runs
// on, because the pod holds an exclusive claim on a card and a card
// has playback PCM devices. Publishing one would retract devices that
// prepared claims still name.
func TestEnsureRefusesToPublishNothing(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	client := testClient(t, api.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), nil); err != ErrNoDevices {
		t.Fatalf("error = %v, want %v", err, ErrNoDevices)
	}
	if len(api.requests) != 0 {
		t.Errorf("an empty inventory reached the API server: %v", api.requests)
	}
}

func publishedSlice(devices []SliceDevice, generation int64) *ResourceSlice {
	return &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "liken-1-audio.liken.sh", ResourceVersion: "7"},
		Spec: ResourceSliceSpec{
			Driver:   DriverName,
			NodeName: "liken-1",
			Pool:     ResourcePool{Name: "liken-1", Generation: generation, ResourceSliceCount: 1},
			Devices:  devices,
		},
	}
}

// The next three tests read the line the publisher prints for each
// outcome. A slice that nobody rewrites and a slice that a stopped
// operator left behind hold the same resourceVersion and the same pool
// generation, so the log is the only place the two come apart.

func TestEnsureLogsTheSliceItCreated(t *testing.T) {
	capture := captureSliceLog(t)
	api := &slicePublishFixture{}
	client := testClient(t, api.handler(t))

	// PipeWire holds no node for the output, so the device publishes
	// tainted.
	if err := EnsureResourceSlice(client, "liken-1", testOwner(),
		sliceDevices(named(alsaEndpoint{Card: 0, PCM: 3}), nil, pwGraph{})); err != nil {
		t.Fatal(err)
	}
	want := "slice: created generation 1, 1 device, 1 tainted: " + testSinkName + " has " +
		disconnectedTaint + ", " + noSinkTaint
	if got := capture.only(t); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

func TestEnsureLogsTheSliceItWrote(t *testing.T) {
	capture := captureSliceLog(t)
	outputs := named(alsaEndpoint{Card: 0, PCM: 3})
	playing := sliceDevices(outputs, nil, outputGraph(map[pcmAddress]string{{Card: 0, PCM: 3}: "alsa_output.test"}))
	api := &slicePublishFixture{existing: publishedSlice(playing, 3)}
	client := testClient(t, api.handler(t))

	// PipeWire lost the node. The device count does not move, so the
	// taints are the whole event, and they evict the pod that held the
	// claim.
	if err := EnsureResourceSlice(client, "liken-1", testOwner(), sliceDevices(outputs, nil, pwGraph{})); err != nil {
		t.Fatal(err)
	}
	want := "slice: wrote generation 4, 1 device, 1 tainted: " + testSinkName + " gained " +
		disconnectedTaint + ", " + noSinkTaint
	if got := capture.only(t); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

func TestEnsureLogsThatNothingMoved(t *testing.T) {
	capture := captureSliceLog(t)
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	client := testClient(t, api.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), testDevices()); err != nil {
		t.Fatal(err)
	}
	if api.updated != nil {
		t.Fatalf("an unchanged inventory wrote to the API: %v", api.requests)
	}
	want := "slice: unchanged at generation 3, 1 device, 0 tainted (1 pass)"
	if got := capture.only(t); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

func TestSliceName(t *testing.T) {
	// The driver name is the suffix, so liken's slice and this
	// operator's slice can both exist for one node.
	if got := sliceName("liken-1"); got != "liken-1-audio.liken.sh" {
		t.Fatalf("sliceName = %q", got)
	}
}
