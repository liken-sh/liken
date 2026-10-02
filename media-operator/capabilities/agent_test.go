package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// fakeQueries answers each render node's query from a table, and
// counts the queries it ran.
type fakeQueries struct {
	answers map[string]report
	failing map[string]error
	asked   []string
}

func (f *fakeQueries) query(ctx context.Context, path string) (report, error) {
	f.asked = append(f.asked, path)
	if err := f.failing[path]; err != nil {
		return report{}, err
	}
	return f.answers[path], nil
}

// renderNodeOf maps the two test GPUs to their nodes.
func renderNodeOf(address string) (string, error) {
	nodes := map[string]string{"0000:00:02.0": "/dev/dri/renderD128", "0000:03:00.0": "/dev/dri/renderD129"}
	if node, ok := nodes[address]; ok {
		return node, nil
	}
	return "", errors.New("no render node")
}

// likenSlice is the `liken.sh` slice of node-1, with a card node beside
// the render nodes, which the agent must leave out.
func likenSlice(gpus ...SliceDevice) *ResourceSlice {
	display := SliceDevice{Name: "pci-0000-00-02-0-display", Attributes: map[string]DeviceAttribute{
		"address": attrString("0000:00:02.0"), "displayNode": attrBool(true),
	}}
	return &ResourceSlice{Spec: ResourceSliceSpec{Driver: likenDriver, NodeName: "node-1", Devices: append(gpus, display)}}
}

// testAgent builds an agent on node-1 against the fake API, holding the
// named devices.
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
		t.Fatal(err)
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
		t.Error("the first GPU's report states scale10bit")
	}
	if got := *slice.Spec.Devices[1].Attributes["scale10bit"].Bool; got {
		t.Error("the second GPU's report states nothing")
	}
}

func TestAPassQueriesEachRenderNodeOnceForTheLifeOfThePod(t *testing.T) {
	queries := &fakeQueries{answers: map[string]report{"/dev/dri/renderD128": meteorLake()}}
	a, api := testAgent(t, queries, "pci-0000-00-02-0")
	gpus := likenSlice(gpuDevice("pci-0000-00-02-0", "0000:00:02.0"))

	for range 3 {
		if err := a.pass(context.Background(), gpus); err != nil {
			t.Fatal(err)
		}
	}
	if len(queries.asked) != 1 {
		t.Errorf("queries = %v, want one", queries.asked)
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
		t.Fatal(err)
	}
	slice, _ := get[ResourceSlice](t, api, mediaSlicePath)
	if len(slice.Spec.Devices) != 1 || *slice.Spec.Devices[0].Attributes["decodeH264"].Bool {
		t.Errorf("devices = %+v, want the GPU with decodeH264 false", slice.Spec.Devices)
	}
}

func TestARenderNodeThatLeavesTakesItsDeviceWithIt(t *testing.T) {
	queries := &fakeQueries{}
	a, api := testAgent(t, queries, "pci-0000-00-02-0", "pci-0000-03-00-0")
	both := likenSlice(gpuDevice("pci-0000-00-02-0", "0000:00:02.0"), gpuDevice("pci-0000-03-00-0", "0000:03:00.0"))
	if err := a.pass(context.Background(), both); err != nil {
		t.Fatal(err)
	}
	if err := a.pass(context.Background(), likenSlice(gpuDevice("pci-0000-00-02-0", "0000:00:02.0"))); err != nil {
		t.Fatal(err)
	}
	slice, _ := get[ResourceSlice](t, api, mediaSlicePath)
	if len(slice.Spec.Devices) != 1 || slice.Spec.Devices[0].Name != "pci-0000-00-02-0" {
		t.Errorf("devices = %+v, want the GPU that is left", slice.Spec.Devices)
	}
}

func TestANodeWhoseLikenSliceIsGonePublishesNothing(t *testing.T) {
	a, api := testAgent(t, &fakeQueries{}, "pci-0000-00-02-0")
	if err := a.pass(context.Background(), likenSlice(gpuDevice("pci-0000-00-02-0", "0000:00:02.0"))); err != nil {
		t.Fatal(err)
	}
	if err := a.pass(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := get[ResourceSlice](t, api, mediaSlicePath); ok {
		t.Error("the slice outlived every render node")
	}
}

func TestARenderNodeTheClaimDoesNotHoldReplacesThePodOnce(t *testing.T) {
	queries := &fakeQueries{}
	a, api := testAgent(t, queries, "pci-0000-00-02-0")
	both := likenSlice(gpuDevice("pci-0000-00-02-0", "0000:00:02.0"), gpuDevice("pci-0000-03-00-0", "0000:03:00.0"))

	for range 2 {
		if err := a.pass(context.Background(), both); err != nil {
			t.Fatal(err)
		}
	}
	if deletes := api.writesTo("DELETE /api/v1/namespaces/liken-system/pods/media-capabilities-x"); deletes != 1 {
		t.Errorf("%d deletes of the agent's pod, want one", deletes)
	}
	if len(queries.asked) != 0 || api.writesTo("P") != 0 {
		t.Error("a pod on its way out queries and publishes nothing")
	}
}

func TestStatedListsEveryCapabilityInOrder(t *testing.T) {
	got := stated(report{})
	if !strings.HasPrefix(got, "decodeH264=false decodeHEVCMain=false") || !strings.HasSuffix(got, "scale10bit=false") {
		t.Errorf("stated = %q", got)
	}
}

func TestTheAgentReadsTheDevicesItsClaimHolds(t *testing.T) {
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
		t.Fatal(err)
	}
	if !held["pci-0000-00-02-0"] || !held["pci-0000-03-00-0"] || len(held) != 2 {
		t.Errorf("held = %v", held)
	}
}

func TestAPodWithNoClaimStatusFails(t *testing.T) {
	api, client := startAPI(t)
	api.put(t, "/api/v1/namespaces/liken-system/pods/media-capabilities-x", map[string]any{})
	_, err := heldDevices(client, "liken-system", "media-capabilities-x")
	if err == nil || !strings.Contains(err.Error(), "states no claim") {
		t.Errorf("err = %v", err)
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
	if err == nil || !strings.Contains(err.Error(), "has no allocation") {
		t.Errorf("err = %v", err)
	}
}

func TestAFailedDeleteOfThePodFailsThePassAndRunsAgain(t *testing.T) {
	_, client := startAPIAnswering(t, 500)
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
		t.Fatal(err)
	}
	slice, _ := get[ResourceSlice](t, api, mediaSlicePath)
	if len(queries.asked) != 0 || len(slice.Spec.Devices) != 1 || *slice.Spec.Devices[0].Attributes["scale8bit"].Bool {
		t.Errorf("queries = %v, devices = %+v", queries.asked, slice.Spec.Devices)
	}
}
