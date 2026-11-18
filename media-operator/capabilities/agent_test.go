package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// fakeQueries returns each render node's report from a table and
// records the paths it was queried for.
type fakeQueries struct {
	answers map[string]report
	failing map[string]error
	queried []string
}

func (f *fakeQueries) query(ctx context.Context, path string) (report, error) {
	f.queried = append(f.queried, path)
	if err := f.failing[path]; err != nil {
		return report{}, err
	}
	return f.answers[path], nil
}

// renderNodeOf maps the PCI address of each test GPU to its render
// node.
func renderNodeOf(address string) (string, error) {
	nodes := map[string]string{"0000:00:02.0": "/dev/dri/renderD128", "0000:03:00.0": "/dev/dri/renderD129"}
	if node, ok := nodes[address]; ok {
		return node, nil
	}
	return "", errors.New("no render node")
}

// likenSlice returns the `liken.sh` slice of node-1. It adds a display
// device that has no render node, which the agent must ignore.
func likenSlice(gpus ...SliceDevice) *ResourceSlice {
	display := SliceDevice{Name: "pci-0000-00-02-0-display", Attributes: map[string]DeviceAttribute{
		"address": attrString("0000:00:02.0"), "displayNode": attrBool(true),
	}}
	return &ResourceSlice{Spec: ResourceSliceSpec{Driver: likenDriver, NodeName: "node-1", Devices: append(gpus, display)}}
}

// testAgent builds an agent on node-1 against the fake API server,
// with held as the devices that its claim allocated.
func testAgent(t *testing.T, queries *fakeQueries, held ...string) (*agent, *fakeAPI) {
	t.Helper()
	api, client := startAPI(t)
	heldSet := map[string]bool{}
	for _, name := range held {
		heldSet[name] = true
	}
	return &agent{
		client: client, node: "node-1", pod: "media-capabilities-x", namespace: "liken-system",
		held: heldSet, reports: map[string]report{},
		query: queries.query, renderNode: renderNodeOf,
	}, api
}

const mediaSlicePath = resourceSlicesPath + "/node-1-media.liken.sh"

func TestThePassPublishesOneDeviceForEachRenderNode(t *testing.T) {
	queries := &fakeQueries{answers: map[string]report{
		"/dev/dri/renderD128": meteorLake(),
		"/dev/dri/renderD129": {Vendor: "Mesa Gallium driver"},
	}}
	a, api := testAgent(t, queries, "pci-0000-00-02-0", "pci-0000-03-00-0")
	gpus := likenSlice(gpuDevice("pci-0000-03-00-0", "0000:03:00.0"), gpuDevice("pci-0000-00-02-0", "0000:00:02.0"))

	if err := a.pass(context.Background(), gpus); err != nil {
		t.Fatalf("running the pass: %v", err)
	}

	slice, _ := get[ResourceSlice](t, api, mediaSlicePath)
	var names []string
	for _, device := range slice.Spec.Devices {
		names = append(names, device.Name)
	}
	if want := []string{"pci-0000-00-02-0", "pci-0000-03-00-0"}; !slices.Equal(names, want) {
		t.Errorf("devices = %v, want %v", names, want)
	}
	if got := *slice.Spec.Devices[0].Attributes["scale10bit"].Bool; !got {
		t.Error("scale10bit of the first GPU is false, want true because its report has it")
	}
	if got := *slice.Spec.Devices[1].Attributes["scale10bit"].Bool; got {
		t.Error("scale10bit of the second GPU is true, want false because its report is empty")
	}
}

func TestAPassQueriesEachRenderNodeOnceForTheLifeOfThePod(t *testing.T) {
	queries := &fakeQueries{answers: map[string]report{"/dev/dri/renderD128": meteorLake()}}
	a, api := testAgent(t, queries, "pci-0000-00-02-0")
	gpus := likenSlice(gpuDevice("pci-0000-00-02-0", "0000:00:02.0"))

	for range 3 {
		if err := a.pass(context.Background(), gpus); err != nil {
			t.Fatalf("running the pass: %v", err)
		}
	}
	if len(queries.queried) != 1 {
		t.Errorf("queries = %v, want one", queries.queried)
	}
	if writes := api.writesTo("P"); writes != 1 {
		t.Errorf("%d slice writes, want one", writes)
	}
}

func TestAFailedQueryPublishesTheGPUWithEveryCapabilityFalse(t *testing.T) {
	queries := &fakeQueries{failing: map[string]error{
		"/dev/dri/renderD128": errors.New("vaInitialize on /dev/dri/renderD128: unknown libva error"),
	}}
	a, api := testAgent(t, queries, "pci-0000-00-02-0")
	if err := a.pass(context.Background(), likenSlice(gpuDevice("pci-0000-00-02-0", "0000:00:02.0"))); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	slice, _ := get[ResourceSlice](t, api, mediaSlicePath)
	if len(slice.Spec.Devices) != 1 || *slice.Spec.Devices[0].Attributes["decodeH264"].Bool {
		t.Errorf("devices = %+v, want the GPU with decodeH264 false", slice.Spec.Devices)
	}
}

func TestARemovedRenderNodeRemovesItsDevice(t *testing.T) {
	queries := &fakeQueries{}
	a, api := testAgent(t, queries, "pci-0000-00-02-0", "pci-0000-03-00-0")
	both := likenSlice(gpuDevice("pci-0000-00-02-0", "0000:00:02.0"), gpuDevice("pci-0000-03-00-0", "0000:03:00.0"))
	if err := a.pass(context.Background(), both); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	if err := a.pass(context.Background(), likenSlice(gpuDevice("pci-0000-00-02-0", "0000:00:02.0"))); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	slice, _ := get[ResourceSlice](t, api, mediaSlicePath)
	if len(slice.Spec.Devices) != 1 || slice.Spec.Devices[0].Name != "pci-0000-00-02-0" {
		t.Errorf("devices = %+v, want the GPU that is left", slice.Spec.Devices)
	}
}

func TestANodeWithNoLikenSlicePublishesNothing(t *testing.T) {
	a, api := testAgent(t, &fakeQueries{}, "pci-0000-00-02-0")
	if err := a.pass(context.Background(), likenSlice(gpuDevice("pci-0000-00-02-0", "0000:00:02.0"))); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	if err := a.pass(context.Background(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	if _, ok := get[ResourceSlice](t, api, mediaSlicePath); ok {
		t.Error("the slice still exists, want it deleted after the last render node left")
	}
}

func TestARenderNodeOutsideTheClaimDeletesThePodOnce(t *testing.T) {
	queries := &fakeQueries{}
	a, api := testAgent(t, queries, "pci-0000-00-02-0")
	both := likenSlice(gpuDevice("pci-0000-00-02-0", "0000:00:02.0"), gpuDevice("pci-0000-03-00-0", "0000:03:00.0"))

	for range 2 {
		if err := a.pass(context.Background(), both); err != nil {
			t.Fatalf("running the pass: %v", err)
		}
	}
	if deletes := api.writesTo("DELETE /api/v1/namespaces/liken-system/pods/media-capabilities-x"); deletes != 1 {
		t.Errorf("%d deletes of the agent's pod, want one", deletes)
	}
	if len(queries.queried) != 0 || api.writesTo("P") != 0 {
		t.Error("the pod queried a render node or wrote the slice, want neither while it is being replaced")
	}
}

func TestCapabilityListHasEveryCapabilityInOrder(t *testing.T) {
	got := capabilityList(report{})
	if !strings.HasPrefix(got, "decodeH264=false decodeHEVCMain=false") || !strings.HasSuffix(got, "scale10bit=false") {
		t.Errorf("capabilityList = %q, want every capability in order of capabilityRules, from decodeH264 to scale10bit", got)
	}
}

func TestTheAgentReadsTheDevicesItsClaimAllocated(t *testing.T) {
	api, client := startAPI(t)
	api.put(t, "/api/v1/namespaces/liken-system/pods/media-capabilities-x", map[string]any{
		"status": map[string]any{"resourceClaimStatuses": []any{
			map[string]any{"name": claimName, "resourceClaimName": "media-capabilities-x-render-nodes-abcde"},
		}},
	})
	api.put(t, "/apis/resource.k8s.io/v1/namespaces/liken-system/resourceclaims/media-capabilities-x-render-nodes-abcde", map[string]any{
		"status": map[string]any{"allocation": map[string]any{"devices": map[string]any{"results": []any{
			map[string]any{"driver": "liken.sh", "device": "pci-0000-00-02-0"},
			map[string]any{"driver": "liken.sh", "device": "pci-0000-03-00-0"},
		}}}},
	})
	held, err := heldDevices(client, "liken-system", "media-capabilities-x")
	if err != nil {
		t.Fatalf("reading the held devices: %v", err)
	}
	if !held["pci-0000-00-02-0"] || !held["pci-0000-03-00-0"] || len(held) != 2 {
		t.Errorf("held = %v, want pci-0000-00-02-0 and pci-0000-03-00-0", held)
	}
}

func TestAPodWithNoClaimStatusFails(t *testing.T) {
	api, client := startAPI(t)
	api.put(t, "/api/v1/namespaces/liken-system/pods/media-capabilities-x", map[string]any{})
	_, err := heldDevices(client, "liken-system", "media-capabilities-x")
	if err == nil || !strings.Contains(err.Error(), "status.resourceClaimStatuses") {
		t.Errorf("err = %v, want one that names status.resourceClaimStatuses", err)
	}
}

func TestAClaimWithNoAllocationFails(t *testing.T) {
	api, client := startAPI(t)
	api.put(t, "/api/v1/namespaces/liken-system/pods/media-capabilities-x", map[string]any{
		"status": map[string]any{"resourceClaimStatuses": []any{
			map[string]any{"name": claimName, "resourceClaimName": "claim-1"},
		}},
	})
	api.put(t, "/apis/resource.k8s.io/v1/namespaces/liken-system/resourceclaims/claim-1", map[string]any{})
	_, err := heldDevices(client, "liken-system", "media-capabilities-x")
	if err == nil || !strings.Contains(err.Error(), "status.allocation") {
		t.Errorf("err = %v, want one that names status.allocation", err)
	}
}

func TestAFailedPodDeleteFailsThePassAndIsRetried(t *testing.T) {
	_, client := startAPIFailing(t, 500)
	a := &agent{client: client, node: "node-1", pod: "media-capabilities-x", namespace: "liken-system",
		held: map[string]bool{}, reports: map[string]report{}}
	gpus := likenSlice(gpuDevice("pci-0000-00-02-0", "0000:00:02.0"))
	if err := a.pass(context.Background(), gpus); err == nil || a.replacing {
		t.Errorf("err = %v, replacing = %t, want a failure that the next pass retries", err, a.replacing)
	}
}

func TestAGPUWithNoRenderNodeInSysfsPublishesEveryCapabilityFalse(t *testing.T) {
	queries := &fakeQueries{}
	a, api := testAgent(t, queries, "pci-0000-05-00-0")
	if err := a.pass(context.Background(), likenSlice(gpuDevice("pci-0000-05-00-0", "0000:05:00.0"))); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	slice, _ := get[ResourceSlice](t, api, mediaSlicePath)
	if len(queries.queried) != 0 || len(slice.Spec.Devices) != 1 || *slice.Spec.Devices[0].Attributes["scale8bit"].Bool {
		t.Errorf("queries = %v, devices = %+v, want no queries and one GPU with scale8bit false", queries.queried, slice.Spec.Devices)
	}
}
