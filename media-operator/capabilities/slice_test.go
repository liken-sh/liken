package main

import (
	"testing"
)

// gpuDevice is the `liken` device of a GPU's render node, as `liken`
// publishes it.
func gpuDevice(name, address string) SliceDevice {
	return SliceDevice{
		Name: name,
		Attributes: map[string]DeviceAttribute{
			"bus":        attrString("pci"),
			"address":    attrString(address),
			"driver":     attrString("i915"),
			"vendor":     attrString("8086"),
			"product":    attrString("7d55"),
			"name":       attrString("Meteor Lake-P [Intel Arc Graphics]"),
			"renderNode": attrBool(true),
			"subsystem":  attrString("drm"),
		},
	}
}

func TestTheDeviceOfAGPUStatesItsCapabilitiesAndItsPairing(t *testing.T) {
	device := mediaDevice(gpuDevice("pci-0000-00-02-0", "0000:00:02.0"), meteorLake())

	if device.Name != "pci-0000-00-02-0" {
		t.Errorf("name = %q, want the name of the render node's device", device.Name)
	}
	if device.AllowMultipleAllocations == nil || !*device.AllowMultipleAllocations {
		t.Error("a statement about a GPU can be allocated by any number of claims")
	}
	if got := device.stringAttribute(pciBusIDAttribute); got != "0000:00:02.0" {
		t.Errorf("%s = %q, want the GPU's PCI address", pciBusIDAttribute, got)
	}
	if got := device.Attributes["scale10bit"].Bool; got == nil || !*got {
		t.Errorf("scale10bit = %v, want true from the report", got)
	}
	for _, name := range []string{"address", "driver", "vendor", "product", "name"} {
		if device.stringAttribute(name) == "" {
			t.Errorf("the device does not repeat the GPU's %s", name)
		}
	}
	if _, ok := device.Attributes["renderNode"]; ok {
		t.Error("the device delivers no render node, so it must not state one")
	}
	if got := device.stringAttribute(vaDriverAttribute); got != meteorLake().Vendor {
		t.Errorf("%s = %q", vaDriverAttribute, got)
	}
}

func TestAFailedQueryStatesEveryCapabilityAsFalseAndNoDriver(t *testing.T) {
	device := mediaDevice(gpuDevice("pci-0000-00-02-0", "0000:00:02.0"), report{})
	if _, ok := device.Attributes[vaDriverAttribute]; ok {
		t.Error("a failed query names no driver")
	}
	if got := device.Attributes["decodeH264"].Bool; got == nil || *got {
		t.Errorf("decodeH264 = %v, want false", got)
	}
}

func TestALongDriverNameIsCutToTheAPIsLimit(t *testing.T) {
	facts := report{Vendor: "Mesa Gallium driver 26.0.0 for AMD Radeon Graphics (radeonsi, gfx1103_r1, LLVM 20.1.8, DRM 3.64, 7.0.0)"}
	if got := mediaDevice(gpuDevice("pci-0000-03-00-0", "0000:03:00.0"), facts).stringAttribute(vaDriverAttribute); len(got) != maxAttributeString {
		t.Errorf("%s has %d characters, want %d", vaDriverAttribute, len(got), maxAttributeString)
	}
}

func TestTheFirstPublishCreatesTheNodesSliceAndTheNextWritesNothing(t *testing.T) {
	api, client := startAPI(t)
	owner := OwnerReference{APIVersion: "v1", Kind: "Node", Name: "node-1", UID: "uid-1"}
	devices := []SliceDevice{mediaDevice(gpuDevice("pci-0000-00-02-0", "0000:00:02.0"), meteorLake())}

	for range 2 {
		if err := ensureResourceSlice(client, "node-1", owner, devices); err != nil {
			t.Fatal(err)
		}
	}

	slice, ok := get[ResourceSlice](t, api, resourceSlicesPath+"/node-1-media.liken.sh")
	if !ok {
		t.Fatal("no slice named node-1-media.liken.sh")
	}
	if slice.Spec.Driver != DriverName || slice.Spec.NodeName != "node-1" || len(slice.Spec.Devices) != 1 {
		t.Errorf("slice = %+v", slice.Spec)
	}
	if len(slice.Metadata.OwnerReferences) != 1 || slice.Metadata.OwnerReferences[0].UID != "uid-1" {
		t.Errorf("owners = %+v, want the Node", slice.Metadata.OwnerReferences)
	}
	if writes := api.writesTo("P"); writes != 1 {
		t.Errorf("%d writes, want one create and no write for a publish that changed nothing", writes)
	}
}

func TestAChangedDeviceRaisesTheGeneration(t *testing.T) {
	api, client := startAPI(t)
	gpu := gpuDevice("pci-0000-00-02-0", "0000:00:02.0")
	if err := ensureResourceSlice(client, "node-1", OwnerReference{}, []SliceDevice{mediaDevice(gpu, meteorLake())}); err != nil {
		t.Fatal(err)
	}
	if err := ensureResourceSlice(client, "node-1", OwnerReference{}, []SliceDevice{mediaDevice(gpu, report{})}); err != nil {
		t.Fatal(err)
	}
	slice, _ := get[ResourceSlice](t, api, resourceSlicesPath+"/node-1-media.liken.sh")
	if slice.Spec.Pool.Generation != 2 {
		t.Errorf("generation = %d, want 2", slice.Spec.Pool.Generation)
	}
}

func TestANodeWithNoRenderNodeHasNoSlice(t *testing.T) {
	api, client := startAPI(t)
	if err := ensureResourceSlice(client, "node-1", OwnerReference{}, []SliceDevice{mediaDevice(gpuDevice("pci-0000-00-02-0", "0000:00:02.0"), report{})}); err != nil {
		t.Fatal(err)
	}
	if err := ensureResourceSlice(client, "node-1", OwnerReference{}, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := get[ResourceSlice](t, api, resourceSlicesPath+"/node-1-media.liken.sh"); ok {
		t.Error("the slice is still there")
	}
}

func TestTheNodesOwnerReferenceCarriesItsUID(t *testing.T) {
	api, client := startAPI(t)
	node := nodeObject{}
	node.Metadata.Name, node.Metadata.UID = "node-1", "uid-1"
	api.put(t, "/api/v1/nodes/node-1", node)
	owner, err := nodeOwner(client, "node-1")
	if err != nil || owner.UID != "uid-1" || owner.Kind != "Node" {
		t.Errorf("owner = %+v, err = %v", owner, err)
	}
}

func TestAFailedReadOfTheSliceFailsThePublish(t *testing.T) {
	_, client := startAPIAnswering(t, 500)
	if err := ensureResourceSlice(client, "node-1", OwnerReference{}, nil); err == nil {
		t.Error("a 500 on the read did not fail the publish")
	}
	if _, err := nodeOwner(client, "node-1"); err == nil {
		t.Error("a 500 on the node did not fail the owner")
	}
}
