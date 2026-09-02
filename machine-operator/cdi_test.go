package main

// The kubelet prepares a claim once, and the reconcile pass keeps
// that claim's spec file current while the hardware under it
// enumerates again.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
)

// specPaths reads back every device node in one claim's spec file.
func specPaths(t *testing.T, fixture *draFixture, claimUID string) []string {
	t.Helper()
	var paths []string
	for _, node := range specNodes(t, fixture, claimUID) {
		paths = append(paths, node.Path)
	}
	return paths
}

// specNodes reads back every device node one claim's spec grants,
// with the fields the runtime creates the node from.
func specNodes(t *testing.T, fixture *draFixture, claimUID string) []cdiDeviceNode {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixture.cdi, "liken.sh-"+claimUID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	var nodes []cdiDeviceNode
	for _, device := range spec.Devices {
		nodes = append(nodes, device.ContainerEdits.DeviceNodes...)
	}
	return nodes
}

// prepared runs one prepare call, the way the kubelet does before it
// starts the first pod that holds the claim.
func prepared(t *testing.T, fixture *draFixture) {
	t.Helper()
	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if answer := resp.Claims["claim-1"]; answer == nil || answer.Error != "" {
		t.Fatalf("answer = %+v", answer)
	}
}

func TestRefreshFollowsADeviceThatEnumeratedAgain(t *testing.T) {
	// The kubelet does not prepare a claim twice, so a device that is
	// unplugged and plugged back in leaves the file naming a usbfs node
	// that no longer exists. The next pod that holds this claim would
	// get that node.
	fixture := newDRAFixture(t)
	fixture.enumerate(t, 4)
	prepared(t, fixture)

	fixture.enumerate(t, 9)
	refreshCDISpecs(draSysfsRoot)

	paths := specPaths(t, fixture, "claim-1")
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"/dev/bus/usb/002/009", "/dev/sda"}) {
		t.Errorf("paths = %v, want the node this enumeration assigned", paths)
	}
}

func TestRefreshLeavesASpecThatStillMatchesAlone(t *testing.T) {
	// Hardware that stays put must not produce a write on every pass.
	// containerd reads these files whenever it creates a container.
	fixture := newDRAFixture(t)
	fixture.enumerate(t, 4)
	prepared(t, fixture)

	path := filepath.Join(fixture.cdi, "liken.sh-claim-1.json")
	written := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}
	refreshCDISpecs(draSysfsRoot)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(written) {
		t.Errorf("the spec was rewritten at %v, want no write at all", info.ModTime())
	}
}

func TestRefreshKeepsTheNodesOfHardwareThatLeft(t *testing.T) {
	// The claim names hardware that is gone. Unprepare ends the claim
	// when its pods do. An empty edit list would start the next pod
	// with no device in it and no error.
	fixture := newDRAFixture(t)
	fixture.enumerate(t, 4)
	prepared(t, fixture)

	if err := os.RemoveAll(filepath.Join(draSysfsRoot, "bus", "usb", "devices", "2-1:1.0")); err != nil {
		t.Fatal(err)
	}
	refreshCDISpecs(draSysfsRoot)

	paths := specPaths(t, fixture, "claim-1")
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"/dev/bus/usb/002/004", "/dev/sda"}) {
		t.Errorf("paths = %v, want the nodes the claim was prepared with", paths)
	}
}

func TestRefreshDeliversTheBusNodeAloneAfterADriverDetach(t *testing.T) {
	// A libusb program in the claim's pod detaches the kernel driver
	// from the interface, and the driver's nodes leave with it. The
	// interface stays on the bus. The refresh rewrites the spec to
	// the bus node, the one node the program uses, so a container
	// restart under the same prepared claim injects a node that
	// exists. Before this rewrite the spec kept the driver's deleted
	// node, and every restart failed on it.
	fixture := newDRAFixture(t)
	fixture.enumerate(t, 4)
	prepared(t, fixture)

	stick := filepath.Join(draSysfsRoot, "bus", "usb", "devices", "2-1:1.0")
	if err := os.Remove(filepath.Join(stick, "driver")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(stick, "host0")); err != nil {
		t.Fatal(err)
	}
	refreshCDISpecs(draSysfsRoot)

	paths := specPaths(t, fixture, "claim-1")
	if !slices.Equal(paths, []string{"/dev/bus/usb/002/004"}) {
		t.Errorf("paths = %v, want the bus node alone", paths)
	}
}

func TestRefreshDeliversUHIDAfterTheModuleLoads(t *testing.T) {
	// The module loop loads a declared module with no reboot, so
	// /dev/uhid can appear after the kubelet prepared the adapter's
	// claim, and the pass that follows writes it into the spec the
	// next pod receives.
	fixture := newDRAFixture(t)
	fixture.addBluetooth(t)
	fixture.allocated = "usb-1-8-1-0"
	prepared(t, fixture)

	miscDevice(t, draSysfsRoot, "uhid", 239)
	refreshCDISpecs(draSysfsRoot)

	paths := specPaths(t, fixture, "claim-1")
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"/dev/bus/usb/001/008", "/dev/uhid"}) {
		t.Errorf("paths = %v, want the uhid node beside the usbfs node", paths)
	}
}

func TestRefreshIgnoresFilesItDidNotWrite(t *testing.T) {
	// containerd reads every spec in this directory, and another
	// driver's file can sit beside liken's.
	fixture := newDRAFixture(t)
	fixture.enumerate(t, 4)
	prepared(t, fixture)

	foreign := filepath.Join(fixture.cdi, "nvidia.com-gpu.json")
	if err := os.WriteFile(foreign, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	refreshCDISpecs(draSysfsRoot)

	raw, err := os.ReadFile(foreign)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{}" {
		t.Errorf("the other driver's spec = %s, want it untouched", raw)
	}
}
