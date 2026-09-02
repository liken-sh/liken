package main

// The publish policy: how one physical device becomes one or more
// published slice devices, each delivering exactly one kind of node.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/liken-sh/liken/hardware"
)

func TestPublishSplitsAGraphicsDeviceFromItsMonitorBuses(t *testing.T) {
	// i915 registers an i2c bus for each display output, under the
	// GPU's own sysfs directory. The GPU's render node shares; the raw
	// i2c wires do not. Each set publishes as its own device.
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
		t.Errorf("gpu = %+v", gpu)
	}
	if !slices.Equal(gpu.Nodes, []string{"/dev/dri/renderD128"}) {
		t.Errorf("gpu nodes = %v, want the render node and nothing else", gpu.Nodes)
	}
	if display.Suffix != "-display" || display.Subsystem != "drm" || display.RenderNode || display.Shareable {
		t.Errorf("display = %+v", display)
	}
	if i2c.Suffix != "-i2c-dev" || i2c.Subsystem != "i2c-dev" || i2c.RenderNode || i2c.Shareable {
		t.Errorf("i2c = %+v", i2c)
	}
	if !slices.Equal(i2c.Nodes, []string{"/dev/i2c-0", "/dev/i2c-1"}) {
		t.Errorf("i2c nodes = %v", i2c.Nodes)
	}
}

func TestPublishSplitsTheCardNodeFromTheRenderNode(t *testing.T) {
	// DRM master is one per card: the kernel gives modesetting
	// authority to one open card node. The render node has no such
	// limit. The card node publishes as an exclusive companion, so a
	// second display workload waits in the scheduler instead of
	// starting and failing.
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
		t.Error("the render node carries no display authority")
	}
	if display.Suffix != "-display" || display.Subsystem != "drm" {
		t.Errorf("display = %+v, want the -display suffix and the drm subsystem", display)
	}
	if !slices.Equal(display.Nodes, []string{"/dev/dri/card0"}) {
		t.Errorf("display nodes = %v, want the card node alone", display.Nodes)
	}
	if !display.DisplayNode || display.RenderNode {
		t.Errorf("display = %+v, want the display fact and not the render fact", display)
	}
	if display.Shareable {
		t.Error("two modesetting clients on one card have no arbitration contract")
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
	// The HDA controller on the testbed, with the shape sysfs gives
	// it: ALSA's own nodes under the card, and one input device for
	// each HDMI jack the codec can sense. Both kinds belong to the
	// card, so the controller publishes as one exclusive device that
	// delivers the whole subtree. One sound server owns every PCM on
	// the card, so a second claimant waits in the scheduler instead
	// of meeting EBUSY at play time.
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
		t.Errorf("p = %+v", p)
	}
	if p.Shareable {
		t.Error("an audio controller belongs to one claim")
	}
	if len(p.Nodes) != 6 {
		t.Errorf("nodes = %v, want the whole delivery, jack nodes with the card", p.Nodes)
	}
}

func TestPublishKeepsAUSBAudioInterfaceExclusive(t *testing.T) {
	// A USB audio interface delivers ALSA's nodes and nothing else,
	// because its control endpoints are a separate USB interface, and
	// the walk stops at a nested bus device. One kind is still a card.
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

func TestPublishKeepsSoundExclusiveBesideAnUnexaminedKind(t *testing.T) {
	// A card beside a kind that no examined audio controller delivers
	// is hardware nobody has looked at. It publishes whole and
	// exclusive, and names no subsystem, which is the milestone 38
	// default.
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
	// AUX channel carries DPCD register access and EDID reads, and like
	// an i2c bus it is raw wire access with one writer. It publishes as
	// its own exclusive companion, separate from the i2c-dev companion.
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
		t.Errorf("gpu = %+v", gpu)
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
		t.Errorf("i2c = %+v", i2c)
	}
	if !slices.Equal(i2c.Nodes, []string{"/dev/i2c-0", "/dev/i2c-9"}) {
		t.Errorf("i2c nodes = %v", i2c.Nodes)
	}
}

func TestPublishDropsTheFramebufferFromAGraphicsDevice(t *testing.T) {
	// The fbdev node is the kernel's legacy console interface. Holding
	// it grants display takeover, and no workload claims a bare
	// framebuffer, so a graphics device does not deliver it.
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
			t.Errorf("%+v: the framebuffer must not be delivered", p)
		}
	}
	if !published[0].Shareable {
		t.Error("the lab guest's drm+graphics shape must stay shareable")
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
		t.Errorf("p = %+v", p)
	}
	if !slices.Equal(p.Nodes, []string{"/dev/ttyUSB0"}) {
		t.Errorf("nodes = %v", p.Nodes)
	}
}

func TestPublishKeepsAnUnknownMixWholeAndExclusive(t *testing.T) {
	// A render node beside a node from outside the graphics stack and
	// its known companions is hardware nobody has examined. It
	// publishes whole, names no one subsystem, and stays exclusive,
	// which is the milestone 38 default.
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
		t.Error("the render node is still a fact about the hardware")
	}
	if len(p.Nodes) != 2 {
		t.Errorf("nodes = %v, want the whole delivery", p.Nodes)
	}
}

func TestPublishGivesTheUsbfsNodeToThePrimary(t *testing.T) {
	// A USB HID device delivers what usbhid registers for it. A libusb
	// program opens neither of those nodes, so the claim also carries
	// the usbfs node of the device the interface belongs to.
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
	// The usbfs node does not count as a kind of its own. A device that
	// delivers one kind still names it, so a selector on subsystem
	// keeps working.
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
		t.Errorf("nodes = %v", published[0].Nodes)
	}
}

func TestPublishKeepsTheUsbfsNodeOffACompanion(t *testing.T) {
	// A USB display adapter delivers drm nodes and a monitor bus. The
	// graphics device is the claim on the hardware, so it carries the
	// usbfs node. The monitor bus must not: the usbfs node reaches
	// every endpoint of the device, which is what the split withholds.
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
	// it claims, and the driver's nodes leave with it. Network UPS
	// Tools leaves a UPS in this state for as long as it runs. The
	// delivery still carries the bus node, and the claim receives it,
	// so a spec refreshed while the program runs names a node that a
	// container restart can inject.
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
	// A Bluetooth adapter is reached through a socket, so the kernel
	// registers no node for the radio, and the walk stops at the
	// bluetooth subtree that holds the connected peripherals' nodes.
	// The adapter publishes on the strength of its driver, with the
	// one node it has.
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
		t.Error("one Bluetooth stack drives one radio")
	}
	if !slices.Equal(adapter.Nodes, []string{"/dev/bus/usb/001/008"}) {
		t.Errorf("nodes = %v, want the usbfs node", adapter.Nodes)
	}
}

func TestPublishDeliversAnAdaptersOwnNodeBesideItsUsbfsNode(t *testing.T) {
	// The walk removes the connected peripherals' nodes, so anything
	// left under the interface is the adapter's own. Such a node is
	// delivered beside the usbfs node, and never in place of it.
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
	// The usbfs node is the whole delivery of an adapter. Without it
	// the claim would grant nothing, so nothing publishes.
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
	// The primary carries the bare name, and resolveAllocated finds
	// it as the published entry whose Suffix is empty. A deterministic
	// order means the same hardware always publishes the same devices.
	published := publishDevices(hardware.Device{}, hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/i2c-0", Subsystem: "i2c-dev"},
		{Path: "/dev/dri/renderD128", Subsystem: "drm"},
	}})

	if len(published) != 2 || published[0].Suffix != "" || published[1].Suffix != "-i2c-dev" {
		t.Errorf("published = %+v, want the primary first", published)
	}
}

func TestPublishRoutesAnUnknownNodeToTheUnknownBranch(t *testing.T) {
	// A node the kernel did not categorize (sysfs's subsystem readlink
	// failed) is hardware nobody has examined. Even if it is a render
	// node beside a real subsystem, the delivery publishes whole,
	// exclusive, with no sole subsystem, applying the milestone 38
	// default to any hardware liken has not met.
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
		t.Error("the render node is still a fact about the hardware")
	}
	if len(p.Nodes) != 2 {
		t.Errorf("nodes = %v, want both nodes", p.Nodes)
	}
	if !slices.Contains(p.Nodes, "/dev/dri/renderD128") || !slices.Contains(p.Nodes, "/dev/i2c-0") {
		t.Errorf("nodes = %v, want both /dev/dri/renderD128 and /dev/i2c-0", p.Nodes)
	}
}

// miscDevice registers a misc class entry under a fake sysfs root, the
// shape the kernel gives a driver that owns one character node and no
// bus device of its own. MiscNode reads the uevent file's DEVNAME, and
// the dev file carries the node's numbers the way the kernel writes
// them.
func miscDevice(t *testing.T, sysRoot, name string, minor int) {
	t.Helper()
	dir := filepath.Join(sysRoot, "class", "misc", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dev"), fmt.Appendf(nil, "10:%d\n", minor), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "uevent"), []byte("DEVNAME="+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// bluetoothAdapterDevice is the shape claimDelivery names by driver:
// a USB interface bound to btusb.
var bluetoothAdapterDevice = hardware.Device{Bus: "usb", Address: "1-8:1.0", Driver: "btusb"}

func TestClaimDeliveryAddsTheInputNodesToABluetoothAdapter(t *testing.T) {
	// The bluetooth-operator relays a controller's events from the real
	// evdev node, which appears and vanishes with the radio link, into a
	// uinput virtual device with a stable minor. Its container is
	// unprivileged, so it needs the uinput outlet and one node for every
	// evdev minor the kernel may register later.
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
	// Without the uinput outlet the inlet nodes serve nothing, so a
	// machine that has no /dev/uinput delivers uhid alone.
	sysRoot := t.TempDir()
	miscDevice(t, sysRoot, "uhid", 239)

	delivery := claimDelivery(sysRoot, bluetoothAdapterDevice)

	if !slices.Equal(delivery.DevNodes(), []string{"/dev/uhid"}) {
		t.Errorf("nodes = %v, want the uhid node alone", delivery.DevNodes())
	}
}

func TestClaimDeliveryAddsTheEvdevNodesWithoutUHID(t *testing.T) {
	// The two misc nodes are separate declarations, so uinput delivers
	// its own range whether or not uhid is loaded.
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
	// These nodes belong to the adapter's claim alone, because a
	// Bluetooth stack is what they exist for.
	sysRoot := t.TempDir()
	miscDevice(t, sysRoot, "uhid", 239)
	miscDevice(t, sysRoot, "uinput", 223)

	delivery := claimDelivery(sysRoot, hardware.Device{Bus: "usb", Address: "2-1:1.0", Driver: "usb-storage"})

	if len(delivery.Nodes) != 0 {
		t.Errorf("nodes = %v, want none", delivery.DevNodes())
	}
}
