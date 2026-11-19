package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
	regv1 "k8s.io/kubelet/pkg/apis/pluginregistration/v1"
)

// draFixture is a fake sysfs with a USB stick, a fake API server with
// one allocated claim, and a temporary CDI directory, with draSysfsRoot
// and cdiDir pointed at them for the test.
type draFixture struct {
	plugin    *draPlugin
	cdi       string
	allocated string

	// owner is the claim's ownerReferences. A claim made from a
	// ResourceClaimTemplate is owned by its pod.
	owner []map[string]any

	released chan struct{}
}

func newDRAFixture(t *testing.T) *draFixture {
	t.Helper()

	fixture := &draFixture{allocated: "usb-2-1-1-0"}

	sysfs := t.TempDir()
	stick := filepath.Join(sysfs, "bus", "usb", "devices", "2-1:1.0")
	sda := filepath.Join(stick, "host0", "target0:0:0", "0:0:0:0", "block", "sda")
	if err := os.MkdirAll(sda, 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
	files := map[string]string{
		filepath.Join(stick, "modalias"): "usb:v46F4p0001d0100dc00dsc00dp00ic08isc06ip50in00\n",
		filepath.Join(stick, "driver"):   "", // symlinked below
		filepath.Join(sda, "dev"):        "8:0\n",
		filepath.Join(sda, "uevent"):     "DEVNAME=sda\n",
	}
	for path, content := range files {
		if content == "" {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("writing the file: %v", err)
		}
	}
	driver := filepath.Join(sysfs, "bus", "usb", "drivers", "usb-storage")
	if err := os.MkdirAll(driver, 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
	if err := os.Symlink(driver, filepath.Join(stick, "driver")); err != nil {
		t.Fatalf("creating the symlink: %v", err)
	}
	block := filepath.Join(sysfs, "class", "block")
	if err := os.MkdirAll(block, 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
	if err := os.Symlink(block, filepath.Join(sda, "subsystem")); err != nil {
		t.Fatalf("creating the symlink: %v", err)
	}

	server := apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/resource.k8s.io/v1/namespaces/media/resourceclaims/stick" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"metadata": map[string]any{"name": "stick", "namespace": "media", "uid": "claim-1", "ownerReferences": fixture.owner},
			"status": map[string]any{
				"allocation": map[string]any{
					"devices": map[string]any{
						"results": []map[string]any{{
							"request": "disk",
							"driver":  "liken.sh",
							"pool":    "node-1",
							"device":  fixture.allocated,
						}},
					},
				},
			},
		})
	}))
	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "token"), []byte("t"), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}

	cdi := t.TempDir()
	origSysfs, origCDI := draSysfsRoot, cdiDir
	draSysfsRoot, cdiDir = sysfs, cdi
	t.Cleanup(func() { draSysfsRoot, cdiDir = origSysfs, origCDI })
	protecting(t, noRoles)

	fixture.released = make(chan struct{}, 1)
	fixture.plugin = &draPlugin{client: apiclient.New(apiservertest.Host, server.Client(), credentials), released: fixture.released}
	fixture.cdi = cdi
	return fixture
}

// enumerate writes the USB device that the stick's interface belongs
// to, with its busnum and devnum. The kernel gives a device a new
// devnum each time it enumerates, so the same stick plugged into the
// same port again gets a different one.
func (f *draFixture) enumerate(t *testing.T, devnum int) {
	t.Helper()
	device := filepath.Join(draSysfsRoot, "bus", "usb", "devices", "2-1")
	if err := os.MkdirAll(device, 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
	for name, value := range map[string]string{"busnum": "2", "devnum": fmt.Sprint(devnum)} {
		if err := os.WriteFile(filepath.Join(device, name), []byte(value+"\n"), 0o644); err != nil {
			t.Fatalf("writing the file: %v", err)
		}
	}
}

// addGPU adds an Intel integrated GPU bound to i915: a card node, a
// render node, and a monitor's i2c bus under one PCI device.
func (f *draFixture) addGPU(t *testing.T) {
	t.Helper()
	gpu := filepath.Join(draSysfsRoot, "bus", "pci", "devices", "0000:00:02.0")
	children := map[string][2]string{
		"drm/card0":           {"drm", "dri/card0"},
		"drm/renderD128":      {"drm", "dri/renderD128"},
		"i2c-0/i2c-dev/i2c-0": {"i2c-dev", "i2c-0"},
	}
	driver := filepath.Join(draSysfsRoot, "bus", "pci", "drivers", "i915")
	if err := os.MkdirAll(driver, 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
	if err := os.MkdirAll(gpu, 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gpu, "modalias"), []byte("pci:v00008086d000046D2sv00000301sd000002F3bc03sc00i00\n"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	if err := os.Symlink(driver, filepath.Join(gpu, "driver")); err != nil {
		t.Fatalf("creating the symlink: %v", err)
	}
	for rel, node := range children {
		dir := filepath.Join(gpu, rel)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("creating the directory: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "dev"), []byte("226:0\n"), 0o644); err != nil {
			t.Fatalf("writing the file: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "uevent"), []byte("DEVNAME="+node[1]+"\n"), 0o644); err != nil {
			t.Fatalf("writing the file: %v", err)
		}
		class := filepath.Join(draSysfsRoot, "class", node[0])
		if err := os.MkdirAll(class, 0o755); err != nil {
			t.Fatalf("creating the directory: %v", err)
		}
		if err := os.Symlink(class, filepath.Join(dir, "subsystem")); err != nil {
			t.Fatalf("creating the symlink: %v", err)
		}
	}
}

// addBluetooth adds a USB Bluetooth adapter bound to btusb, its hci0,
// and a connected game controller with an evdev node under hci0. As on
// real hardware, the adapter has no device node of its own except
// the usbfs node.
func (f *draFixture) addBluetooth(t *testing.T) {
	t.Helper()
	devices := filepath.Join(draSysfsRoot, "bus", "usb", "devices")
	adapter := filepath.Join(devices, "1-8:1.0")
	controller := filepath.Join(adapter, "bluetooth", "hci0", "hci0:11", "0005:054C:0CE6.0004")
	event := filepath.Join(controller, "input", "input25", "event20")
	if err := os.MkdirAll(event, 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(event, "dev"), []byte("13:84\n"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(event, "uevent"), []byte("DEVNAME=input/event20\n"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	for class, dir := range map[string]string{
		"bluetooth": filepath.Join(adapter, "bluetooth", "hci0"),
		"input":     event,
	} {
		target := filepath.Join(draSysfsRoot, "class", class)
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatalf("creating the directory: %v", err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "subsystem")); err != nil {
			t.Fatalf("creating the symlink: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(adapter, "modalias"), []byte("usb:v8087p0033d0001dcE0dsc01dp01icE0isc01ip01in00\n"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	driver := filepath.Join(draSysfsRoot, "bus", "usb", "drivers", "btusb")
	if err := os.MkdirAll(driver, 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
	if err := os.Symlink(driver, filepath.Join(adapter, "driver")); err != nil {
		t.Fatalf("creating the symlink: %v", err)
	}
	device := filepath.Join(devices, "1-8")
	if err := os.MkdirAll(device, 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
	for name, value := range map[string]string{"busnum": "1", "devnum": "8"} {
		if err := os.WriteFile(filepath.Join(device, name), []byte(value+"\n"), 0o644); err != nil {
			t.Fatalf("writing the file: %v", err)
		}
	}
}

func TestPrepareDeliversTheUsbfsNodeOfABluetoothAdapter(t *testing.T) {
	// The controller's event node is not delivered, because it
	// disappears when the controller turns off.
	fixture := newDRAFixture(t)
	fixture.addBluetooth(t)
	fixture.allocated = "usb-1-8-1-0"

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatalf("preparing the claim: %v", err)
	}
	if result := resp.Claims["claim-1"]; result == nil || result.Error != "" {
		t.Fatalf("the prepare result for claim-1 is %+v, want no error", result)
	}

	paths := specPaths(t, fixture, "claim-1")
	if !slices.Equal(paths, []string{"/dev/bus/usb/001/008"}) {
		t.Errorf("the paths are %v, want the adapter's usbfs node alone", paths)
	}
}

func TestPrepareDeliversUHIDWithABluetoothAdapter(t *testing.T) {
	// BlueZ implements HID over GATT in userspace and creates the
	// input device through /dev/uhid, so the HID profile fails in an
	// unprivileged container without that node.
	fixture := newDRAFixture(t)
	fixture.addBluetooth(t)
	miscDevice(t, draSysfsRoot, "uhid", 239)
	fixture.allocated = "usb-1-8-1-0"

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatalf("preparing the claim: %v", err)
	}
	if result := resp.Claims["claim-1"]; result == nil || result.Error != "" {
		t.Fatalf("the prepare result for claim-1 is %+v, want no error", result)
	}

	paths := specPaths(t, fixture, "claim-1")
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"/dev/bus/usb/001/008", "/dev/uhid"}) {
		t.Errorf("the paths are %v, want the usbfs node and the uhid node", paths)
	}
}

func TestPrepareDeliversUHIDToNoOtherDevice(t *testing.T) {
	fixture := newDRAFixture(t)
	fixture.enumerate(t, 4)
	miscDevice(t, draSysfsRoot, "uhid", 239)

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatalf("preparing the claim: %v", err)
	}
	if result := resp.Claims["claim-1"]; result == nil || result.Error != "" {
		t.Fatalf("the prepare result for claim-1 is %+v, want no error", result)
	}

	paths := specPaths(t, fixture, "claim-1")
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"/dev/bus/usb/002/004", "/dev/sda"}) {
		t.Errorf("the paths are %v, want the stick's own nodes", paths)
	}
}

func TestPrepareDeliversTheInputNodesWithABluetoothAdapter(t *testing.T) {
	// The Bluetooth stack reads a controller's evdev node and writes
	// a uinput device. The kernel creates an evdev node only while a
	// controller is connected, which can be after the container
	// starts, so the spec lists evdev minors 64 to 95 by number.
	fixture := newDRAFixture(t)
	fixture.addBluetooth(t)
	miscDevice(t, draSysfsRoot, "uhid", 239)
	miscDevice(t, draSysfsRoot, "uinput", 223)
	fixture.allocated = "usb-1-8-1-0"

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatalf("preparing the claim: %v", err)
	}
	if result := resp.Claims["claim-1"]; result == nil || result.Error != "" {
		t.Fatalf("the prepare result for claim-1 is %+v, want no error", result)
	}

	mode := 0o600
	want := []cdiDeviceNode{{Path: "/dev/uhid"}, {Path: "/dev/uinput"}}
	for i := range 32 {
		want = append(want, cdiDeviceNode{
			Path:     fmt.Sprintf("/dev/input/event%d", i),
			Type:     "c",
			Major:    13,
			Minor:    64 + i,
			FileMode: &mode,
		})
	}
	want = append(want, cdiDeviceNode{Path: "/dev/bus/usb/001/008"})

	if nodes := specNodes(t, fixture, "claim-1"); !reflect.DeepEqual(nodes, want) {
		t.Errorf("the nodes are %+v, want %+v", nodes, want)
	}
}

func TestPrepareDeliversOnlyTheRenderNodeOfAGPU(t *testing.T) {
	fixture := newDRAFixture(t)
	fixture.addGPU(t)
	fixture.allocated = "pci-0000-00-02-0"

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatalf("preparing the claim: %v", err)
	}
	if result := resp.Claims["claim-1"]; result == nil || result.Error != "" {
		t.Fatalf("the prepare result for claim-1 is %+v, want no error", result)
	}

	raw, err := os.ReadFile(filepath.Join(fixture.cdi, "liken.sh-claim-1.json"))
	if err != nil {
		t.Fatalf("reading the CDI spec: %v", err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parsing the CDI spec: %v", err)
	}
	var paths []string
	for _, n := range spec.Devices[0].ContainerEdits.DeviceNodes {
		paths = append(paths, n.Path)
	}
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"/dev/dri/renderD128"}) {
		t.Errorf("the paths are %v, want the render node, with no card node and no monitor bus", paths)
	}
}

func TestPrepareDeliversTheCardNodeForTheDisplayName(t *testing.T) {
	// The card node allows modesetting. A workload that sets modes
	// and renders requests both the display companion and the GPU.
	fixture := newDRAFixture(t)
	fixture.addGPU(t)
	fixture.allocated = "pci-0000-00-02-0-display"

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatalf("preparing the claim: %v", err)
	}
	if result := resp.Claims["claim-1"]; result == nil || result.Error != "" {
		t.Fatalf("the prepare result for claim-1 is %+v, want no error", result)
	}

	raw, err := os.ReadFile(filepath.Join(fixture.cdi, "liken.sh-claim-1.json"))
	if err != nil {
		t.Fatalf("reading the CDI spec: %v", err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parsing the CDI spec: %v", err)
	}
	var paths []string
	for _, n := range spec.Devices[0].ContainerEdits.DeviceNodes {
		paths = append(paths, n.Path)
	}
	if !slices.Equal(paths, []string{"/dev/dri/card0"}) {
		t.Errorf("the paths are %v, want the card node alone", paths)
	}
}

func TestPrepareDeliversTheMonitorBusesForTheCompanionName(t *testing.T) {
	fixture := newDRAFixture(t)
	fixture.addGPU(t)
	fixture.allocated = "pci-0000-00-02-0-i2c-dev"

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatalf("preparing the claim: %v", err)
	}
	if result := resp.Claims["claim-1"]; result == nil || result.Error != "" {
		t.Fatalf("the prepare result for claim-1 is %+v, want no error", result)
	}

	raw, err := os.ReadFile(filepath.Join(fixture.cdi, "liken.sh-claim-1.json"))
	if err != nil {
		t.Fatalf("reading the CDI spec: %v", err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parsing the CDI spec: %v", err)
	}
	nodes := spec.Devices[0].ContainerEdits.DeviceNodes
	if len(nodes) != 1 || nodes[0].Path != "/dev/i2c-0" {
		t.Errorf("the nodes are %+v, want the monitor bus alone", nodes)
	}
}

func TestPrepareFailsANameNoDevicePublishes(t *testing.T) {
	fixture := newDRAFixture(t)
	fixture.addGPU(t)
	fixture.allocated = "pci-0000-00-02-0-sound"

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatalf("preparing the claim: %v", err)
	}
	if resp.Claims["claim-1"].Error == "" {
		t.Error("the claim has no error, want one for a name that no device publishes")
	}
}

func TestPrepareDeliversTheClaimedDevicesNodes(t *testing.T) {
	fixture := newDRAFixture(t)

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatalf("preparing the claim: %v", err)
	}
	result := resp.Claims["claim-1"]
	if result == nil || result.Error != "" {
		t.Fatalf("the prepare result for claim-1 is %+v, want no error", result)
	}
	if len(result.Devices) != 1 {
		t.Fatalf("the devices are %+v, want one", result.Devices)
	}
	d := result.Devices[0]
	if d.PoolName != "node-1" || d.DeviceName != "usb-2-1-1-0" {
		t.Errorf("the device is %+v, want pool node-1 and name usb-2-1-1-0", d)
	}
	if len(d.CdiDeviceIds) != 1 || d.CdiDeviceIds[0] != "liken.sh/device=claim-1-usb-2-1-1-0" {
		t.Errorf("the CDI IDs are %v, want [liken.sh/device=claim-1-usb-2-1-1-0]", d.CdiDeviceIds)
	}

	raw, err := os.ReadFile(filepath.Join(fixture.cdi, "liken.sh-claim-1.json"))
	if err != nil {
		t.Fatalf("reading the CDI spec: %v", err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parsing the CDI spec: %v", err)
	}
	if spec.Kind != "liken.sh/device" || len(spec.Devices) != 1 {
		t.Fatalf("the spec is %+v, want kind liken.sh/device and one device", spec)
	}
	nodes := spec.Devices[0].ContainerEdits.DeviceNodes
	if len(nodes) != 1 || nodes[0].Path != "/dev/sda" {
		t.Errorf("the device nodes are %+v, want /dev/sda alone", nodes)
	}
}

func TestPrepareDeliversTheUsbfsNodeBesideTheDriversNodes(t *testing.T) {
	// A userspace driver that uses libusb needs the usbfs node.
	fixture := newDRAFixture(t)
	fixture.enumerate(t, 4)

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatalf("preparing the claim: %v", err)
	}
	if result := resp.Claims["claim-1"]; result == nil || result.Error != "" {
		t.Fatalf("the prepare result for claim-1 is %+v, want no error", result)
	}

	paths := specPaths(t, fixture, "claim-1")
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"/dev/bus/usb/002/004", "/dev/sda"}) {
		t.Errorf("the paths are %v, want the block node and the usbfs node", paths)
	}
}

func TestPrepareRefusesAClaimWhoseUIDChanged(t *testing.T) {
	fixture := newDRAFixture(t)

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "an-older-claim"}},
	})
	if err != nil {
		t.Fatalf("preparing the claim: %v", err)
	}
	if resp.Claims["an-older-claim"].Error == "" {
		t.Error("the claim has no error, want one because its UID changed")
	}
}

func TestPrepareReportsAMissingDevicePerClaim(t *testing.T) {
	fixture := newDRAFixture(t)
	// Unplug the stick.
	if err := os.RemoveAll(filepath.Join(draSysfsRoot, "bus", "usb", "devices", "2-1:1.0")); err != nil {
		t.Fatalf("removing the directory: %v", err)
	}

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatalf("preparing the claim: %v", err)
	}
	if resp.Claims["claim-1"].Error == "" {
		t.Error("the claim has no error, want one because its device is gone")
	}
}

func TestUnprepareRemovesTheSpecAndIsIdempotent(t *testing.T) {
	fixture := newDRAFixture(t)
	if err := writeCDISpec("claim-1", []cdiDevice{{Name: "x"}}); err != nil {
		t.Fatalf("writing the CDI spec: %v", err)
	}

	req := &drav1.NodeUnprepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	}
	resp, err := fixture.plugin.NodeUnprepareResources(t.Context(), req)
	if err != nil {
		t.Fatalf("unpreparing the claim: %v", err)
	}
	if resp.Claims["claim-1"].Error != "" {
		t.Fatalf("the unprepare result for claim-1 is %+v, want no error", resp.Claims["claim-1"])
	}
	if _, err := os.Stat(filepath.Join(fixture.cdi, "liken.sh-claim-1.json")); !os.IsNotExist(err) {
		t.Error("the spec file still exists, want it removed")
	}

	resp, err = fixture.plugin.NodeUnprepareResources(t.Context(), req)
	if err != nil {
		t.Fatalf("unpreparing the claim: %v", err)
	}
	if resp.Claims["claim-1"].Error != "" {
		t.Errorf("the second unprepare returned %+v, want no error", resp.Claims["claim-1"])
	}
}

func TestServeRespondsOnBothSockets(t *testing.T) {
	fixture := newDRAFixture(t)

	sockets := t.TempDir()
	origRegistry, origPlugin := draRegistryDir, draPluginDir
	draRegistryDir = sockets
	draPluginDir = filepath.Join(sockets, "liken.sh")
	t.Cleanup(func() { draRegistryDir, draPluginDir = origRegistry, origPlugin })

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- serveDRAPlugin(ctx, fixture.plugin.client, nil) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serving the DRA plugin: %v", err)
		}
	})

	// Call the sockets in the order that the kubelet does.
	registration := filepath.Join(sockets, "liken.sh-reg.sock")
	waitForSocket(t, registration)
	regConn, err := grpc.NewClient("unix://"+registration, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("creating the gRPC client: %v", err)
	}
	defer regConn.Close()
	info, err := regv1.NewRegistrationClient(regConn).GetInfo(t.Context(), &regv1.InfoRequest{})
	if err != nil {
		t.Fatalf("getting the plugin info: %v", err)
	}
	if info.Type != regv1.DRAPlugin || info.Name != "liken.sh" {
		t.Errorf("the plugin info is %+v, want type DRAPlugin and name liken.sh", info)
	}
	if len(info.SupportedVersions) != 1 || info.SupportedVersions[0] != "v1.DRAPlugin" {
		t.Errorf("the supported versions are %v, want [v1.DRAPlugin]", info.SupportedVersions)
	}

	draConn, err := grpc.NewClient("unix://"+info.Endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("creating the gRPC client: %v", err)
	}
	defer draConn.Close()
	resp, err := drav1.NewDRAPluginClient(draConn).NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatalf("preparing the claim: %v", err)
	}
	if result := resp.Claims["claim-1"]; result == nil || result.Error != "" {
		t.Errorf("the prepare result for claim-1 is %+v, want no error", result)
	}
}

// waitForSocket waits for serveDRAPlugin, which runs in another
// goroutine, to create a socket file.
func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the socket %s does not exist after 5 seconds", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
