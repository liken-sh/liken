package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/liken-sh/liken/liken/hardware"
)

func TestPublishSplitsAGraphicsDeviceFromItsMonitorBuses(t *testing.T) {
	// i915 registers an i2c bus for each display output under the
	// GPU's sysfs directory. The render node can be shared and the i2c
	// buses cannot, so each publishes as its own device.
	published := publishDevices(hardware.Device{}, hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/dri/card0", Subsystem: "drm"},
		{Path: "/dev/dri/renderD128", Subsystem: "drm"},
		{Path: "/dev/fb0", Subsystem: "graphics"},
		{Path: "/dev/i2c-0", Subsystem: "i2c-dev"},
		{Path: "/dev/i2c-1", Subsystem: "i2c-dev"},
	}})

	if len(published) != 3 {
		t.Fatalf("published = %+v, want the render device, the display companion, and the i2c companion", published)
	}
	gpu, display, i2c := published[0], published[1], published[2]
	if gpu.Suffix != "" || gpu.Subsystem != "drm" || !gpu.RenderNode || !gpu.Shareable {
		t.Errorf("gpu = %+v, want no suffix, the drm subsystem, a render node, and shareable", gpu)
	}
	if !slices.Equal(gpu.Nodes, []string{"/dev/dri/renderD128"}) {
		t.Errorf("gpu nodes = %v, want the render node and nothing else", gpu.Nodes)
	}
	if display.Suffix != "-display" || display.Subsystem != "drm" || display.RenderNode || display.Shareable {
		t.Errorf("display = %+v, want the -display suffix, the drm subsystem, no render node, and not shareable", display)
	}
	if i2c.Suffix != "-i2c-dev" || i2c.Subsystem != "i2c-dev" || i2c.RenderNode || i2c.Shareable {
		t.Errorf("i2c = %+v, want the -i2c-dev suffix, the i2c-dev subsystem, no render node, and not shareable", i2c)
	}
	if !slices.Equal(i2c.Nodes, []string{"/dev/i2c-0", "/dev/i2c-1"}) {
		t.Errorf("i2c nodes = %v, want /dev/i2c-0 and /dev/i2c-1", i2c.Nodes)
	}
}

func TestPublishSplitsTheCardNodeFromTheRenderNode(t *testing.T) {
	// The kernel gives DRM master to one open card node at a time, so
	// the card node publishes as an exclusive companion of the
	// shareable render node.
	published := publishDevices(hardware.Device{}, hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/dri/card0", Subsystem: "drm"},
		{Path: "/dev/dri/renderD128", Subsystem: "drm"},
	}})

	if len(published) != 2 {
		t.Fatalf("published = %+v, want the render device and its display companion", published)
	}
	render, display := published[0], published[1]
	if !slices.Equal(render.Nodes, []string{"/dev/dri/renderD128"}) {
		t.Errorf("render nodes = %v, want the render node alone", render.Nodes)
	}
	if render.DisplayNode {
		t.Error("the render device has DisplayNode set, want it unset")
	}
	if display.Suffix != "-display" || display.Subsystem != "drm" {
		t.Errorf("display = %+v, want the -display suffix and the drm subsystem", display)
	}
	if !slices.Equal(display.Nodes, []string{"/dev/dri/card0"}) {
		t.Errorf("display nodes = %v, want the card node alone", display.Nodes)
	}
	if !display.DisplayNode || display.RenderNode {
		t.Errorf("display = %+v, want DisplayNode set and RenderNode unset", display)
	}
	if display.Shareable {
		t.Error("the display companion is shareable, want exclusive because two modesetting clients cannot share one card")
	}
}

func TestPublishOmitsTheDisplayCompanionWithoutACardNode(t *testing.T) {
	// A device that delivers a render node and no card node has no
	// display half to publish.
	published := publishDevices(hardware.Device{}, hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/dri/renderD128", Subsystem: "drm"},
	}})

	if len(published) != 1 {
		t.Fatalf("published = %+v, want the render device alone", published)
	}
	if !published[0].Shareable || !published[0].RenderNode {
		t.Errorf("published = %+v, want a shareable render device", published[0])
	}
}

func TestPublishKeepsAnAudioControllerExclusive(t *testing.T) {
	// An HDA controller delivers ALSA's nodes and one input node for
	// each HDMI jack that the codec can sense. It publishes as one
	// exclusive device with all of them.
	published := publishDevices(hardware.Device{}, hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/snd/controlC0", Subsystem: "sound"},
		{Path: "/dev/snd/hwC0D2", Subsystem: "sound"},
		{Path: "/dev/snd/pcmC0D3p", Subsystem: "sound"},
		{Path: "/dev/snd/pcmC0D7p", Subsystem: "sound"},
		{Path: "/dev/input/event16", Subsystem: "input"},
		{Path: "/dev/input/event17", Subsystem: "input"},
	}})

	if len(published) != 1 {
		t.Fatalf("published = %+v, want one device", published)
	}
	p := published[0]
	if p.Suffix != "" || p.Subsystem != "sound" || p.RenderNode || p.DisplayNode {
		t.Errorf("p = %+v, want no suffix, the sound subsystem, and no render or display node", p)
	}
	if p.Shareable {
		t.Error("the audio controller is shareable, want exclusive")
	}
	if len(p.Nodes) != 6 {
		t.Errorf("nodes = %v, want the whole delivery, jack nodes with the card", p.Nodes)
	}
}

func TestPublishKeepsAUSBAudioInterfaceExclusive(t *testing.T) {
	// A USB audio interface delivers only ALSA's nodes and its usbfs
	// node. It still publishes as an exclusive sound device.
	published := publishDevices(hardware.Device{}, hardware.Delivery{
		Nodes: []hardware.DeliveredNode{
			{Path: "/dev/snd/controlC1", Subsystem: "sound"},
			{Path: "/dev/snd/pcmC1D0p", Subsystem: "sound"},
		},
		BusNode: "/dev/bus/usb/001/006",
	})

	if len(published) != 1 {
		t.Fatalf("published = %+v, want one device", published)
	}
	p := published[0]
	if p.Subsystem != "sound" || p.Shareable {
		t.Errorf("p = %+v, want an exclusive sound device", p)
	}
	if !slices.Contains(p.Nodes, "/dev/bus/usb/001/006") {
		t.Errorf("nodes = %v, want the usbfs node with the card", p.Nodes)
	}
}

func TestPublishKeepsSoundExclusiveBesideAnUnrecognizedKind(t *testing.T) {
	// A sound node beside a subsystem that audio controllers do not
	// have is hardware that the policy does not recognize. It publishes
	// as one exclusive device with no subsystem.
	published := publishDevices(hardware.Device{}, hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/snd/controlC0", Subsystem: "sound"},
		{Path: "/dev/ttyS4", Subsystem: "tty"},
	}})

	if len(published) != 1 {
		t.Fatalf("published = %+v, want one whole device", published)
	}
	if published[0].Subsystem != "" || published[0].Shareable {
		t.Errorf("p = %+v, want no sole subsystem and no sharing", published[0])
	}
}

func TestPublishSplitsTheDPAuxChannelAsItsOwnCompanion(t *testing.T) {
	// An Alder Lake-N iGPU with a display on a DisplayPort output
	// registers a drm_dp_aux_dev node beside its i2c monitor buses. The
	// AUX channel publishes as its own exclusive companion, separate
	// from the i2c-dev companion.
	published := publishDevices(hardware.Device{}, hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/dri/card0", Subsystem: "drm"},
		{Path: "/dev/dri/renderD128", Subsystem: "drm"},
		{Path: "/dev/fb0", Subsystem: "graphics"},
		{Path: "/dev/i2c-0", Subsystem: "i2c-dev"},
		{Path: "/dev/i2c-9", Subsystem: "i2c-dev"},
		{Path: "/dev/drm_dp_aux0", Subsystem: "drm_dp_aux_dev"},
	}})

	if len(published) != 4 {
		t.Fatalf("published = %+v, want the graphics device and three companions", published)
	}
	gpu, display, aux, i2c := published[0], published[1], published[2], published[3]
	if gpu.Suffix != "" || gpu.Subsystem != "drm" || !gpu.RenderNode || !gpu.Shareable {
		t.Errorf("gpu = %+v, want no suffix, the drm subsystem, a render node, and shareable", gpu)
	}
	if !slices.Equal(gpu.Nodes, []string{"/dev/dri/renderD128"}) {
		t.Errorf("gpu nodes = %v, want the render node and nothing else", gpu.Nodes)
	}
	if display.Suffix != "-display" || !slices.Equal(display.Nodes, []string{"/dev/dri/card0"}) {
		t.Errorf("display = %+v, want the card node alone", display)
	}
	if aux.Suffix != "-drm-dp-aux-dev" || aux.Subsystem != "drm_dp_aux_dev" || aux.RenderNode || aux.Shareable {
		t.Errorf("aux = %+v", aux)
	}
	if !slices.Equal(aux.Nodes, []string{"/dev/drm_dp_aux0"}) {
		t.Errorf("aux nodes = %v, want only the AUX node", aux.Nodes)
	}
	if i2c.Suffix != "-i2c-dev" || i2c.Subsystem != "i2c-dev" || i2c.RenderNode || i2c.Shareable {
		t.Errorf("i2c = %+v, want the -i2c-dev suffix, the i2c-dev subsystem, no render node, and not shareable", i2c)
	}
	if !slices.Equal(i2c.Nodes, []string{"/dev/i2c-0", "/dev/i2c-9"}) {
		t.Errorf("i2c nodes = %v, want /dev/i2c-0 and /dev/i2c-1", i2c.Nodes)
	}
}

func TestPublishDropsTheFramebufferFromAGraphicsDevice(t *testing.T) {
	// The fbdev node is the kernel's legacy console interface, and a
	// process that writes to it can take over the display. No workload
	// needs it, so no device of a GPU has it.
	published := publishDevices(hardware.Device{}, hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/dri/card0", Subsystem: "drm"},
		{Path: "/dev/dri/renderD128", Subsystem: "drm"},
		{Path: "/dev/fb0", Subsystem: "graphics"},
	}})

	if len(published) != 2 {
		t.Fatalf("published = %+v, want the render device and its display companion", published)
	}
	for _, p := range published {
		if slices.Contains(p.Nodes, "/dev/fb0") {
			t.Errorf("%+v holds /dev/fb0, want no framebuffer node", p)
		}
	}
	if !published[0].Shareable {
		t.Error("the render device is exclusive, want shareable")
	}
}

func TestPublishKeepsASingleKindDeviceWhole(t *testing.T) {
	published := publishDevices(hardware.Device{}, hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/ttyUSB0", Subsystem: "tty"},
	}})

	if len(published) != 1 {
		t.Fatalf("published = %+v, want one device", published)
	}
	p := published[0]
	if p.Suffix != "" || p.Subsystem != "tty" || p.Shareable || p.RenderNode {
		t.Errorf("p = %+v, want no suffix, the tty subsystem, exclusive, and no render node", p)
	}
	if !slices.Equal(p.Nodes, []string{"/dev/ttyUSB0"}) {
		t.Errorf("nodes = %v, want /dev/ttyUSB0", p.Nodes)
	}
}

func TestPublishKeepsAnUnknownMixWholeAndExclusive(t *testing.T) {
	// A render node beside a subsystem that GPUs do not have is
	// hardware that the policy does not recognize. It publishes as one
	// exclusive device with no subsystem.
	published := publishDevices(hardware.Device{}, hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/dri/renderD128", Subsystem: "drm"},
		{Path: "/dev/ttyS4", Subsystem: "tty"},
	}})

	if len(published) != 1 {
		t.Fatalf("published = %+v, want one whole device", published)
	}
	p := published[0]
	if p.Subsystem != "" || p.Shareable {
		t.Errorf("p = %+v, want no sole subsystem and no sharing", p)
	}
	if !p.RenderNode {
		t.Error("RenderNode is false, want true because the device delivers a render node")
	}
	if len(p.Nodes) != 2 {
		t.Errorf("nodes = %v, want the whole delivery", p.Nodes)
	}
}

func TestPublishGivesTheUsbfsNodeToThePrimary(t *testing.T) {
	// A libusb program opens neither of the nodes that usbhid registers,
	// so the claim also receives the usbfs node of the USB device.
	published := publishDevices(hardware.Device{}, hardware.Delivery{
		Nodes: []hardware.DeliveredNode{
			{Path: "/dev/hidraw0", Subsystem: "hidraw"},
			{Path: "/dev/usb/hiddev0", Subsystem: "usbmisc"},
		},
		BusNode: "/dev/bus/usb/003/004",
	})

	if len(published) != 1 {
		t.Fatalf("published = %+v, want one device", published)
	}
	want := []string{"/dev/hidraw0", "/dev/usb/hiddev0", "/dev/bus/usb/003/004"}
	if !slices.Equal(published[0].Nodes, want) {
		t.Errorf("nodes = %v, want the driver's nodes and the usbfs node", published[0].Nodes)
	}
}

func TestPublishNamesTheSubsystemBesideTheUsbfsNode(t *testing.T) {
	// The usbfs node does not count as a subsystem, so a device with
	// one subsystem and a usbfs node still states its subsystem for a
	// selector to match.
	published := publishDevices(hardware.Device{}, hardware.Delivery{
		Nodes:   []hardware.DeliveredNode{{Path: "/dev/ttyUSB0", Subsystem: "tty"}},
		BusNode: "/dev/bus/usb/001/007",
	})

	if len(published) != 1 {
		t.Fatalf("published = %+v, want one device", published)
	}
	if published[0].Subsystem != "tty" {
		t.Errorf("subsystem = %q, want tty", published[0].Subsystem)
	}
	if !slices.Equal(published[0].Nodes, []string{"/dev/ttyUSB0", "/dev/bus/usb/001/007"}) {
		t.Errorf("nodes = %v, want /dev/ttyUSB0 and /dev/bus/usb/001/007", published[0].Nodes)
	}
}

func TestPublishKeepsTheUsbfsNodeOffACompanion(t *testing.T) {
	// The usbfs node opens every interface of the USB device, so only
	// the primary receives it. A companion that had it could reach the
	// nodes that the split keeps from it.
	published := publishDevices(hardware.Device{}, hardware.Delivery{
		Nodes: []hardware.DeliveredNode{
			{Path: "/dev/dri/card0", Subsystem: "drm"},
			{Path: "/dev/dri/renderD128", Subsystem: "drm"},
			{Path: "/dev/i2c-5", Subsystem: "i2c-dev"},
		},
		BusNode: "/dev/bus/usb/001/007",
	})

	if len(published) != 3 {
		t.Fatalf("published = %+v, want the graphics device and its two companions", published)
	}
	if !slices.Contains(published[0].Nodes, "/dev/bus/usb/001/007") {
		t.Errorf("primary nodes = %v, want the usbfs node", published[0].Nodes)
	}
	if !slices.Equal(published[1].Nodes, []string{"/dev/dri/card0"}) {
		t.Errorf("display nodes = %v, want the card node alone", published[1].Nodes)
	}
	if !slices.Equal(published[2].Nodes, []string{"/dev/i2c-5"}) {
		t.Errorf("companion nodes = %v, want the monitor bus alone", published[2].Nodes)
	}
}

func TestPublishDeliversTheBusNodeAloneWhenTheSubtreeIsEmpty(t *testing.T) {
	// A libusb program detaches the kernel driver from the interface
	// it claims, which removes the driver's nodes. Network UPS Tools
	// leaves a UPS in this state for as long as it runs. The claim
	// still receives the bus node, so a CDI spec written while the
	// program runs has a node to inject when the container restarts.
	published := publishDevices(hardware.Device{}, hardware.Delivery{BusNode: "/dev/bus/usb/003/002"})

	if len(published) != 1 {
		t.Fatalf("published = %+v, want one device", published)
	}
	only := published[0]
	if only.Suffix != "" || only.Subsystem != "" || only.RenderNode || only.Shareable {
		t.Errorf("device = %+v, want a bare exclusive primary", only)
	}
	if !slices.Equal(only.Nodes, []string{"/dev/bus/usb/003/002"}) {
		t.Errorf("nodes = %v, want the bus node alone", only.Nodes)
	}
}

func TestPublishGivesABluetoothAdapterItsUsbfsNode(t *testing.T) {
	// A Bluetooth stack reaches the radio through a socket, so the
	// adapter has no node in its subtree. The policy recognizes it by
	// its btusb driver and publishes its usbfs node.
	published := publishDevices(
		hardware.Device{Bus: "usb", Address: "1-8:1.0", Driver: "btusb"},
		hardware.Delivery{BusNode: "/dev/bus/usb/001/008"})

	if len(published) != 1 {
		t.Fatalf("published = %+v, want the adapter", published)
	}
	adapter := published[0]
	if adapter.Suffix != "" || adapter.Subsystem != "" || adapter.RenderNode || adapter.DisplayNode {
		t.Errorf("adapter = %+v, want a bare primary", adapter)
	}
	if adapter.Shareable {
		t.Error("the adapter is shareable, want exclusive")
	}
	if !slices.Equal(adapter.Nodes, []string{"/dev/bus/usb/001/008"}) {
		t.Errorf("nodes = %v, want the usbfs node", adapter.Nodes)
	}
}

func TestPublishDeliversAnAdaptersOwnNodeBesideItsUsbfsNode(t *testing.T) {
	// The walk leaves out the connected peripherals' nodes, so a node
	// left under the interface is the adapter's own, and the device
	// has it beside the usbfs node.
	published := publishDevices(
		hardware.Device{Bus: "usb", Address: "1-8:1.0", Driver: "btusb"},
		hardware.Delivery{
			Nodes:   []hardware.DeliveredNode{{Path: "/dev/rfkill", Subsystem: "misc"}},
			BusNode: "/dev/bus/usb/001/008",
		})

	if len(published) != 1 {
		t.Fatalf("published = %+v, want one device", published)
	}
	want := []string{"/dev/rfkill", "/dev/bus/usb/001/008"}
	if !slices.Equal(published[0].Nodes, want) {
		t.Errorf("nodes = %v, want %v", published[0].Nodes, want)
	}
}

func TestPublishSkipsABluetoothAdapterWithNoUsbfsNode(t *testing.T) {
	published := publishDevices(
		hardware.Device{Bus: "usb", Address: "1-8:1.0", Driver: "btusb"},
		hardware.Delivery{})

	if len(published) != 0 {
		t.Errorf("published = %+v, want none", published)
	}
}

func TestPublishReturnsNothingForAnEmptyDelivery(t *testing.T) {
	if published := publishDevices(hardware.Device{}, hardware.Delivery{}); len(published) != 0 {
		t.Errorf("published = %+v, want none", published)
	}
}

func TestPublishOrdersThePrimaryFirst(t *testing.T) {
	// resolveAllocated finds the primary as the entry with an empty
	// Suffix, and a fixed order publishes the same devices for the same
	// hardware.
	published := publishDevices(hardware.Device{}, hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/i2c-0", Subsystem: "i2c-dev"},
		{Path: "/dev/dri/renderD128", Subsystem: "drm"},
	}})

	if len(published) != 2 || published[0].Suffix != "" || published[1].Suffix != "-i2c-dev" {
		t.Errorf("published = %+v, want the primary first", published)
	}
}

func TestPublishKeepsADeliveryWholeWhenANodeHasNoSubsystem(t *testing.T) {
	// A node has no subsystem when its sysfs subsystem link could not
	// be read. The delivery then publishes as one exclusive device with
	// no subsystem, even when it has a render node.
	published := publishDevices(hardware.Device{}, hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/dri/renderD128", Subsystem: ""},
		{Path: "/dev/i2c-0", Subsystem: "i2c-dev"},
	}})

	if len(published) != 1 {
		t.Fatalf("published = %+v, want one device", published)
	}
	p := published[0]
	if p.Suffix != "" || p.Subsystem != "" || p.Shareable {
		t.Errorf("p = %+v, want empty suffix and subsystem, exclusive", p)
	}
	if !p.RenderNode {
		t.Error("RenderNode is false, want true because the device delivers a render node")
	}
	if len(p.Nodes) != 2 {
		t.Errorf("nodes = %v, want both nodes", p.Nodes)
	}
	if !slices.Contains(p.Nodes, "/dev/dri/renderD128") || !slices.Contains(p.Nodes, "/dev/i2c-0") {
		t.Errorf("nodes = %v, want both /dev/dri/renderD128 and /dev/i2c-0", p.Nodes)
	}
}

// miscDevice writes a misc class entry under a fake sysfs root, as
// the kernel writes one for a driver with one character node and no
// bus device. MiscNode reads DEVNAME from the uevent file.
func miscDevice(t *testing.T, sysRoot, name string, minor int) {
	t.Helper()
	dir := filepath.Join(sysRoot, "class", "misc", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("writing the misc class entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dev"), fmt.Appendf(nil, "10:%d\n", minor), 0o644); err != nil {
		t.Fatalf("writing the misc class entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "uevent"), []byte("DEVNAME="+name+"\n"), 0o644); err != nil {
		t.Fatalf("writing the misc class entry: %v", err)
	}
}

var bluetoothAdapterDevice = hardware.Device{Bus: "usb", Address: "1-8:1.0", Driver: "btusb"}

func TestClaimDeliveryAddsTheInputNodesToABluetoothAdapter(t *testing.T) {
	// bluetooth-operator copies a controller's events from its evdev
	// node, which exists only while the controller is connected, into a
	// uinput virtual device. Its container is unprivileged, so the claim
	// gives it /dev/uinput and a node for every evdev minor that the
	// kernel can register later.
	sysRoot := t.TempDir()
	miscDevice(t, sysRoot, "uhid", 239)
	miscDevice(t, sysRoot, "uinput", 223)

	delivery := claimDelivery(sysRoot, bluetoothAdapterDevice)

	want := []string{"/dev/uhid", "/dev/uinput"}
	for i := range 32 {
		want = append(want, fmt.Sprintf("/dev/input/event%d", i))
	}
	if !slices.Equal(delivery.DevNodes(), want) {
		t.Errorf("nodes = %v, want %v", delivery.DevNodes(), want)
	}
}

func TestClaimDeliveryAddsNoEvdevNodesWithoutUinput(t *testing.T) {
	// The evdev range comes with /dev/uinput, so without uinput the
	// claim receives /dev/uhid alone.
	sysRoot := t.TempDir()
	miscDevice(t, sysRoot, "uhid", 239)

	delivery := claimDelivery(sysRoot, bluetoothAdapterDevice)

	if !slices.Equal(delivery.DevNodes(), []string{"/dev/uhid"}) {
		t.Errorf("nodes = %v, want the uhid node alone", delivery.DevNodes())
	}
}

func TestClaimDeliveryAddsTheEvdevNodesWithoutUHID(t *testing.T) {
	// /dev/uinput and its evdev range do not depend on /dev/uhid.
	sysRoot := t.TempDir()
	miscDevice(t, sysRoot, "uinput", 223)

	delivery := claimDelivery(sysRoot, bluetoothAdapterDevice)

	if len(delivery.Nodes) != 33 {
		t.Fatalf("nodes = %v, want uinput and the 32 event nodes", delivery.DevNodes())
	}
	if delivery.Nodes[0].Path != "/dev/uinput" {
		t.Errorf("first node = %q, want /dev/uinput", delivery.Nodes[0].Path)
	}
}

func TestClaimDeliveryAddsTheInputNodesToNoOtherDevice(t *testing.T) {
	// Only a Bluetooth adapter's claim receives the misc and evdev
	// nodes.
	sysRoot := t.TempDir()
	miscDevice(t, sysRoot, "uhid", 239)
	miscDevice(t, sysRoot, "uinput", 223)

	delivery := claimDelivery(sysRoot, hardware.Device{Bus: "usb", Address: "2-1:1.0", Driver: "usb-storage"})

	if len(delivery.Nodes) != 0 {
		t.Errorf("nodes = %v, want none", delivery.DevNodes())
	}
}
