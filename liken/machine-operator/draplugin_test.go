package main

// The kubelet plugin's behavior: what prepare delivers, what it
// refuses, and that both gRPC services actually answer on their
// sockets the way the kubelet will call them.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	healthv1alpha1 "k8s.io/kubelet/pkg/apis/dra-health/v1alpha1"
	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
	regv1 "k8s.io/kubelet/pkg/apis/pluginregistration/v1"

	"github.com/liken-sh/liken/kubernetes"
)

// draFixture builds everything one prepare call touches: a fake
// sysfs with the stick plugged in, a fake API server holding one
// allocated claim, and a CDI directory to write into. The package
// seams (draSysfsRoot, cdiDir) point at the fixture, and the test
// restores them afterward.
type draFixture struct {
	plugin    *draPlugin
	cdi       string
	allocated string
}

func newDRAFixture(t *testing.T) *draFixture {
	t.Helper()

	fixture := &draFixture{allocated: "usb-2-1-1-0"}

	sysfs := t.TempDir()
	stick := filepath.Join(sysfs, "bus", "usb", "devices", "2-1:1.0")
	sda := filepath.Join(stick, "host0", "target0:0:0", "0:0:0:0", "block", "sda")
	if err := os.MkdirAll(sda, 0o755); err != nil {
		t.Fatal(err)
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
			t.Fatal(err)
		}
	}
	driver := filepath.Join(sysfs, "bus", "usb", "drivers", "usb-storage")
	if err := os.MkdirAll(driver, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(driver, filepath.Join(stick, "driver")); err != nil {
		t.Fatal(err)
	}
	block := filepath.Join(sysfs, "class", "block")
	if err := os.MkdirAll(block, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(block, filepath.Join(sda, "subsystem")); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/resource.k8s.io/v1/namespaces/media/resourceclaims/stick" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"metadata": map[string]any{"name": "stick", "namespace": "media", "uid": "claim-1"},
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
	t.Cleanup(server.Close)
	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "token"), []byte("t"), 0o600); err != nil {
		t.Fatal(err)
	}

	cdi := t.TempDir()
	origSysfs, origCDI := draSysfsRoot, cdiDir
	draSysfsRoot, cdiDir = sysfs, cdi
	t.Cleanup(func() { draSysfsRoot, cdiDir = origSysfs, origCDI })

	fixture.plugin = &draPlugin{client: kubernetes.NewClient(server.URL, server.Client(), credentials)}
	fixture.cdi = cdi
	return fixture
}

// enumerate writes the usb_device that the stick's interface belongs
// to, with the numbers the bus assigned it. The bus number names the
// controller and does not change. The bus assigns the device number
// in order at each enumeration, so a replug of the same hardware in
// the same port produces a different one.
func (f *draFixture) enumerate(t *testing.T, devnum int) {
	t.Helper()
	device := filepath.Join(draSysfsRoot, "bus", "usb", "devices", "2-1")
	if err := os.MkdirAll(device, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"busnum": "2", "devnum": fmt.Sprint(devnum)} {
		if err := os.WriteFile(filepath.Join(device, name), []byte(value+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// addGPU plants an integrated GPU beside the stick: two drm nodes and
// one i2c monitor bus under one PCI device, the shape i915 gives a
// real machine.
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
		t.Fatal(err)
	}
	if err := os.MkdirAll(gpu, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gpu, "modalias"), []byte("pci:v00008086d000046D2sv00000301sd000002F3bc03sc00i00\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(driver, filepath.Join(gpu, "driver")); err != nil {
		t.Fatal(err)
	}
	for rel, node := range children {
		dir := filepath.Join(gpu, rel)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "dev"), []byte("226:0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "uevent"), []byte("DEVNAME="+node[1]+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		class := filepath.Join(draSysfsRoot, "class", node[0])
		if err := os.MkdirAll(class, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(class, filepath.Join(dir, "subsystem")); err != nil {
			t.Fatal(err)
		}
	}
}

// addBluetooth plants a Bluetooth adapter beside the stick: a USB
// interface bound to btusb, with the hci object the kernel registers
// under it and a game controller connected to that object. The
// adapter's own subtree carries no device node, which is the shape a
// working radio has.
func (f *draFixture) addBluetooth(t *testing.T) {
	t.Helper()
	devices := filepath.Join(draSysfsRoot, "bus", "usb", "devices")
	adapter := filepath.Join(devices, "1-8:1.0")
	controller := filepath.Join(adapter, "bluetooth", "hci0", "hci0:11", "0005:054C:0CE6.0004")
	event := filepath.Join(controller, "input", "input25", "event20")
	if err := os.MkdirAll(event, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(event, "dev"), []byte("13:84\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(event, "uevent"), []byte("DEVNAME=input/event20\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for class, dir := range map[string]string{
		"bluetooth": filepath.Join(adapter, "bluetooth", "hci0"),
		"input":     event,
	} {
		target := filepath.Join(draSysfsRoot, "class", class)
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "subsystem")); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(adapter, "modalias"), []byte("usb:v8087p0033d0001dcE0dsc01dp01icE0isc01ip01in00\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := filepath.Join(draSysfsRoot, "bus", "usb", "drivers", "btusb")
	if err := os.MkdirAll(driver, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(driver, filepath.Join(adapter, "driver")); err != nil {
		t.Fatal(err)
	}
	device := filepath.Join(devices, "1-8")
	if err := os.MkdirAll(device, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"busnum": "1", "devnum": "8"} {
		if err := os.WriteFile(filepath.Join(device, name), []byte(value+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPrepareDeliversTheUsbfsNodeOfABluetoothAdapter(t *testing.T) {
	// A claim on an adapter receives the usbfs node, which is the only
	// node the adapter has. The connected controller's event node stays
	// out: it belongs to the controller, and it leaves when somebody
	// turns the controller off.
	fixture := newDRAFixture(t)
	fixture.addBluetooth(t)
	fixture.allocated = "usb-1-8-1-0"

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if answer := resp.Claims["claim-1"]; answer == nil || answer.Error != "" {
		t.Fatalf("answer = %+v", answer)
	}

	paths := specPaths(t, fixture, "claim-1")
	if !slices.Equal(paths, []string{"/dev/bus/usb/001/008"}) {
		t.Errorf("paths = %v, want the adapter's usbfs node alone", paths)
	}
}

func TestPrepareDeliversUHIDWithABluetoothAdapter(t *testing.T) {
	// BlueZ carries HID over GATT in userspace and presents the
	// peripheral as an input device by writing /dev/uhid, so a stack
	// in an unprivileged container needs that node beside the
	// adapter, and the HID profile fails without it.
	fixture := newDRAFixture(t)
	fixture.addBluetooth(t)
	miscDevice(t, draSysfsRoot, "uhid", 239)
	fixture.allocated = "usb-1-8-1-0"

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if answer := resp.Claims["claim-1"]; answer == nil || answer.Error != "" {
		t.Fatalf("answer = %+v", answer)
	}

	paths := specPaths(t, fixture, "claim-1")
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"/dev/bus/usb/001/008", "/dev/uhid"}) {
		t.Errorf("paths = %v, want the usbfs node and the uhid node", paths)
	}
}

func TestPrepareDeliversUHIDToNoOtherDevice(t *testing.T) {
	// /dev/uhid belongs to the adapter's claim alone, because a
	// Bluetooth stack is what the node exists for.
	fixture := newDRAFixture(t)
	fixture.enumerate(t, 4)
	miscDevice(t, draSysfsRoot, "uhid", 239)

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if answer := resp.Claims["claim-1"]; answer == nil || answer.Error != "" {
		t.Fatalf("answer = %+v", answer)
	}

	paths := specPaths(t, fixture, "claim-1")
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"/dev/bus/usb/002/004", "/dev/sda"}) {
		t.Errorf("paths = %v, want the stick's own nodes", paths)
	}
}

func TestPrepareDeliversTheInputNodesWithABluetoothAdapter(t *testing.T) {
	// A Bluetooth stack in an unprivileged container relays a controller's
	// events from the evdev node the radio link creates into a uinput
	// virtual device with a stable minor. The spec states the evdev range
	// by number, because the kernel registers each of those nodes only
	// while a controller is connected, and the container must hold a node
	// for a minor that appears later.
	fixture := newDRAFixture(t)
	fixture.addBluetooth(t)
	miscDevice(t, draSysfsRoot, "uhid", 239)
	miscDevice(t, draSysfsRoot, "uinput", 223)
	fixture.allocated = "usb-1-8-1-0"

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if answer := resp.Claims["claim-1"]; answer == nil || answer.Error != "" {
		t.Fatalf("answer = %+v", answer)
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
		t.Errorf("nodes = %+v, want %+v", nodes, want)
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
		t.Fatal(err)
	}
	if answer := resp.Claims["claim-1"]; answer == nil || answer.Error != "" {
		t.Fatalf("answer = %+v", answer)
	}

	raw, err := os.ReadFile(filepath.Join(fixture.cdi, "liken.sh-claim-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, n := range spec.Devices[0].ContainerEdits.DeviceNodes {
		paths = append(paths, n.Path)
	}
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"/dev/dri/renderD128"}) {
		t.Errorf("paths = %v, want the render node, with no card node and no monitor bus", paths)
	}
}

func TestPrepareDeliversTheCardNodeForTheDisplayName(t *testing.T) {
	// A claim on the display companion receives the card node, which
	// carries modesetting authority, and nothing else. A workload that
	// modesets and renders holds both devices, one request each.
	fixture := newDRAFixture(t)
	fixture.addGPU(t)
	fixture.allocated = "pci-0000-00-02-0-display"

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if answer := resp.Claims["claim-1"]; answer == nil || answer.Error != "" {
		t.Fatalf("answer = %+v", answer)
	}

	raw, err := os.ReadFile(filepath.Join(fixture.cdi, "liken.sh-claim-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, n := range spec.Devices[0].ContainerEdits.DeviceNodes {
		paths = append(paths, n.Path)
	}
	if !slices.Equal(paths, []string{"/dev/dri/card0"}) {
		t.Errorf("paths = %v, want the card node alone", paths)
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
		t.Fatal(err)
	}
	if answer := resp.Claims["claim-1"]; answer == nil || answer.Error != "" {
		t.Fatalf("answer = %+v", answer)
	}

	raw, err := os.ReadFile(filepath.Join(fixture.cdi, "liken.sh-claim-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	nodes := spec.Devices[0].ContainerEdits.DeviceNodes
	if len(nodes) != 1 || nodes[0].Path != "/dev/i2c-0" {
		t.Errorf("nodes = %+v, want the monitor bus alone", nodes)
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
		t.Fatal(err)
	}
	if resp.Claims["claim-1"].Error == "" {
		t.Error("a name the hardware no longer publishes must fail the claim")
	}
}

func TestPrepareDeliversTheClaimedDevicesNodes(t *testing.T) {
	fixture := newDRAFixture(t)

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	answer := resp.Claims["claim-1"]
	if answer == nil || answer.Error != "" {
		t.Fatalf("answer = %+v", answer)
	}
	if len(answer.Devices) != 1 {
		t.Fatalf("devices = %+v", answer.Devices)
	}
	d := answer.Devices[0]
	if d.PoolName != "node-1" || d.DeviceName != "usb-2-1-1-0" {
		t.Errorf("device = %+v", d)
	}
	if len(d.CdiDeviceIds) != 1 || d.CdiDeviceIds[0] != "liken.sh/device=claim-1-usb-2-1-1-0" {
		t.Errorf("cdi ids = %v", d.CdiDeviceIds)
	}

	raw, err := os.ReadFile(filepath.Join(fixture.cdi, "liken.sh-claim-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.Kind != "liken.sh/device" || len(spec.Devices) != 1 {
		t.Fatalf("spec = %+v", spec)
	}
	nodes := spec.Devices[0].ContainerEdits.DeviceNodes
	if len(nodes) != 1 || nodes[0].Path != "/dev/sda" {
		t.Errorf("device nodes = %+v", nodes)
	}
}

func TestPrepareDeliversTheUsbfsNodeBesideTheDriversNodes(t *testing.T) {
	// The stick's driver delivers a block node. A userspace driver that
	// speaks to the same hardware over libusb needs the usbfs node
	// instead, and the pod cannot open a node the spec does not name.
	fixture := newDRAFixture(t)
	fixture.enumerate(t, 4)

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if answer := resp.Claims["claim-1"]; answer == nil || answer.Error != "" {
		t.Fatalf("answer = %+v", answer)
	}

	paths := specPaths(t, fixture, "claim-1")
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"/dev/bus/usb/002/004", "/dev/sda"}) {
		t.Errorf("paths = %v, want the block node and the usbfs node", paths)
	}
}

func TestPrepareRefusesAClaimWhoseUIDChanged(t *testing.T) {
	fixture := newDRAFixture(t)

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "an-older-claim"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Claims["an-older-claim"].Error == "" {
		t.Error("a recreated claim is a different grant and must be refused")
	}
}

func TestPrepareReportsAMissingDevicePerClaim(t *testing.T) {
	fixture := newDRAFixture(t)
	// Unplug the stick: the walk no longer finds the allocated name.
	if err := os.RemoveAll(filepath.Join(draSysfsRoot, "bus", "usb", "devices", "2-1:1.0")); err != nil {
		t.Fatal(err)
	}

	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Claims["claim-1"].Error == "" {
		t.Error("hardware that left must fail the claim, in-band, for the kubelet to retry")
	}
}

func TestUnprepareRemovesTheSpecAndIsIdempotent(t *testing.T) {
	fixture := newDRAFixture(t)
	if err := writeCDISpec("claim-1", []cdiDevice{{Name: "x"}}); err != nil {
		t.Fatal(err)
	}

	req := &drav1.NodeUnprepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	}
	resp, err := fixture.plugin.NodeUnprepareResources(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Claims["claim-1"].Error != "" {
		t.Fatalf("answer = %+v", resp.Claims["claim-1"])
	}
	if _, err := os.Stat(filepath.Join(fixture.cdi, "liken.sh-claim-1.json")); !os.IsNotExist(err) {
		t.Error("the spec file must be gone")
	}

	// A second unprepare of the same claim still succeeds.
	resp, err = fixture.plugin.NodeUnprepareResources(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Claims["claim-1"].Error != "" {
		t.Errorf("unprepare must be idempotent: %+v", resp.Claims["claim-1"])
	}
}

func TestServeAnswersOnBothSockets(t *testing.T) {
	fixture := newDRAFixture(t)

	sockets := t.TempDir()
	origRegistry, origPlugin := draRegistryDir, draPluginDir
	draRegistryDir = sockets
	draPluginDir = filepath.Join(sockets, "liken.sh")
	t.Cleanup(func() { draRegistryDir, draPluginDir = origRegistry, origPlugin })

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- serveDRAPlugin(ctx, fixture.plugin.client) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})

	// The registration handshake, exactly as the kubelet's plugin
	// watcher performs it.
	registration := filepath.Join(sockets, "liken.sh-reg.sock")
	waitForSocket(t, registration)
	regConn, err := grpc.NewClient("unix://"+registration, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer regConn.Close()
	info, err := regv1.NewRegistrationClient(regConn).GetInfo(t.Context(), &regv1.InfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if info.Type != regv1.DRAPlugin || info.Name != "liken.sh" {
		t.Errorf("info = %+v", info)
	}
	if len(info.SupportedVersions) != 1 || info.SupportedVersions[0] != "v1.DRAPlugin" {
		t.Errorf("versions = %v", info.SupportedVersions)
	}

	// Then dial the endpoint the registration announced and prepare a
	// claim over the wire.
	draConn, err := grpc.NewClient("unix://"+info.Endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer draConn.Close()
	resp, err := drav1.NewDRAPluginClient(draConn).NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if answer := resp.Claims["claim-1"]; answer == nil || answer.Error != "" {
		t.Errorf("answer = %+v", answer)
	}

	// The health stream must open and stay open, not answer
	// Unimplemented. The kubelet retries a missing service every
	// few seconds, forever, and that noise is exactly what
	// registering the silent stream prevents.
	streamCtx, streamCancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer streamCancel()
	watch, err := healthv1alpha1.NewDRAResourceHealthClient(draConn).NodeWatchResources(streamCtx, &healthv1alpha1.NodeWatchResourcesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = watch.Recv()
	if status.Code(err) == codes.Unimplemented {
		t.Error("the health service must accept the stream, not answer Unimplemented")
	}
}

// waitForSocket polls for a unix socket to exist. The server sets
// its sockets up in a goroutine, and the test can run ahead of it.
func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("socket %s never appeared", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
