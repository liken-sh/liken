package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
)

func testClient(t *testing.T, handler http.Handler) *apiclient.Client {
	t.Helper()
	server := apiservertest.Start(t, handler)
	// An empty credentials directory makes the client send no token.
	return apiclient.New(apiservertest.Host, server.Client(), "")
}

func testOwner() OwnerReference {
	return OwnerReference{APIVersion: "v1", Kind: "Node", Name: "liken-1", UID: "abc-123"}
}

// slicePublishFixture is an API server handler that serves at most one
// ResourceSlice and records each request.
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
			t.Errorf("the fixture received %s %s, want a GET, POST, or PUT", r.Method, r.URL.Path)
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
		t.Fatalf("publishing the slice: %v", err)
	}
	if api.created == nil {
		t.Fatal("the fixture received no create, want the slice created")
	}
	slice := api.created
	if slice.Metadata.Name != "liken-1-audio.liken.sh" {
		t.Errorf("the slice is named %q, want liken-1-audio.liken.sh", slice.Metadata.Name)
	}
	if slice.Spec.Driver != "audio.liken.sh" || slice.Spec.NodeName != "liken-1" {
		t.Errorf("spec = %+v, want the driver audio.liken.sh on the node liken-1", slice.Spec)
	}
	if slice.Spec.Pool.Name != "liken-1" || slice.Spec.Pool.Generation != 1 || slice.Spec.Pool.ResourceSliceCount != 1 {
		t.Errorf("pool = %+v, want the pool liken-1 at generation 1 with one slice", slice.Spec.Pool)
	}
	// The Node owns the slice, not the operator's pod, so the slice
	// remains when the pod is replaced and is deleted with the Node.
	if len(slice.Metadata.OwnerReferences) != 1 || slice.Metadata.OwnerReferences[0].UID != "abc-123" {
		t.Errorf("ownerReferences = %+v, want one reference to the Node with UID abc-123", slice.Metadata.OwnerReferences)
	}
}

func TestEnsureLeavesAnUnchangedSliceAlone(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	client := testClient(t, api.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), testDevices()); err != nil {
		t.Fatalf("publishing the slice: %v", err)
	}
	if api.created != nil || api.updated != nil {
		t.Errorf("an unchanged inventory sent %v, want no write", api.requests)
	}
}

func TestEnsureReplacesAChangedSliceAndBumpsTheGeneration(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	client := testClient(t, api.handler(t))

	changed := testDevices()
	changed[0].Taints = []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute"}}
	if err := EnsureResourceSlice(client, "liken-1", testOwner(), changed); err != nil {
		t.Fatalf("publishing the changed slice: %v", err)
	}
	if api.updated == nil {
		t.Fatal("a changed inventory sent no replace, want the slice written")
	}
	if got := api.updated.Spec.Pool.Generation; got != 4 {
		t.Errorf("generation = %d, want 4", got)
	}
	if len(api.updated.Spec.Devices[0].Taints) != 1 {
		t.Errorf("devices = %+v, want one taint on the device", api.updated.Spec.Devices)
	}
}

func TestEnsureRefusesToPublishNothing(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	client := testClient(t, api.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), nil); err != ErrNoDevices {
		t.Fatalf("error = %v, want %v", err, ErrNoDevices)
	}
	if len(api.requests) != 0 {
		t.Errorf("an empty inventory sent %v, want no request", api.requests)
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

func TestEnsureLogsTheSliceItCreated(t *testing.T) {
	capture := captureSliceLog(t)
	api := &slicePublishFixture{}
	client := testClient(t, api.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(),
		sliceDevices(named(alsaEndpoint{Card: 0, PCM: 3}), nil, pwGraph{})); err != nil {
		t.Fatalf("publishing the slice: %v", err)
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

	// The empty pwGraph is PipeWire after it lost the node. The device
	// count stays the same, so the line must name the taints that the
	// device gained.
	if err := EnsureResourceSlice(client, "liken-1", testOwner(), sliceDevices(outputs, nil, pwGraph{})); err != nil {
		t.Fatalf("publishing the slice without the node: %v", err)
	}
	want := "slice: wrote generation 4, 1 device, 1 tainted: " + testSinkName + " gained " +
		disconnectedTaint + ", " + noSinkTaint
	if got := capture.only(t); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

// The slice of a stopped operator has the same resourceVersion and pool
// generation as an unchanged slice, so only the log line shows that the
// operator still runs its passes.
func TestEnsureLogsAnUnchangedSlice(t *testing.T) {
	capture := captureSliceLog(t)
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	client := testClient(t, api.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), testDevices()); err != nil {
		t.Fatalf("publishing the slice: %v", err)
	}
	if api.updated != nil {
		t.Fatalf("an unchanged inventory sent %v, want no write", api.requests)
	}
	want := "slice: unchanged at generation 3, 1 device, 0 tainted (1 pass)"
	if got := capture.only(t); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

func TestSliceName(t *testing.T) {
	// The slice name ends with the driver name, so this operator's slice
	// and the liken.sh slice of the same node have different names.
	if got := sliceName("liken-1"); got != "liken-1-audio.liken.sh" {
		t.Fatalf("sliceName(liken-1) = %q, want liken-1-audio.liken.sh", got)
	}
}
