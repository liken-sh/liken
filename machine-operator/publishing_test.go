package main

// The publish policy: how one physical device becomes one or more
// published slice devices, each delivering exactly one kind of node.

import (
	"slices"
	"testing"

	"github.com/liken-sh/liken/hardware"
)

func TestPublishSplitsAGraphicsDeviceFromItsMonitorBuses(t *testing.T) {
	// i915 registers an i2c bus for each display output, under the
	// GPU's own sysfs directory. The GPU's drm nodes share; the raw
	// i2c wires do not. Each set publishes as its own device.
	published := publishDevices(hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/dri/card0", Subsystem: "drm"},
		{Path: "/dev/dri/renderD128", Subsystem: "drm"},
		{Path: "/dev/fb0", Subsystem: "graphics"},
		{Path: "/dev/i2c-0", Subsystem: "i2c-dev"},
		{Path: "/dev/i2c-1", Subsystem: "i2c-dev"},
	}})

	if len(published) != 2 {
		t.Fatalf("published = %+v, want the graphics device and its i2c companion", published)
	}
	gpu, i2c := published[0], published[1]
	if gpu.Suffix != "" || gpu.Subsystem != "drm" || !gpu.RenderNode || !gpu.Shareable {
		t.Errorf("gpu = %+v", gpu)
	}
	if !slices.Equal(gpu.Nodes, []string{"/dev/dri/card0", "/dev/dri/renderD128"}) {
		t.Errorf("gpu nodes = %v, want the dri nodes and nothing else", gpu.Nodes)
	}
	if i2c.Suffix != "-i2c-dev" || i2c.Subsystem != "i2c-dev" || i2c.RenderNode || i2c.Shareable {
		t.Errorf("i2c = %+v", i2c)
	}
	if !slices.Equal(i2c.Nodes, []string{"/dev/i2c-0", "/dev/i2c-1"}) {
		t.Errorf("i2c nodes = %v", i2c.Nodes)
	}
}

func TestPublishSplitsTheDPAuxChannelAsItsOwnCompanion(t *testing.T) {
	// An Alder Lake-N iGPU with a display on a DisplayPort output
	// registers a drm_dp_aux_dev node beside its i2c monitor buses. The
	// AUX channel carries DPCD register access and EDID reads, and like
	// an i2c bus it is raw wire access with one writer. It publishes as
	// its own exclusive companion, separate from the i2c-dev companion.
	published := publishDevices(hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/dri/card0", Subsystem: "drm"},
		{Path: "/dev/dri/renderD128", Subsystem: "drm"},
		{Path: "/dev/fb0", Subsystem: "graphics"},
		{Path: "/dev/i2c-0", Subsystem: "i2c-dev"},
		{Path: "/dev/i2c-9", Subsystem: "i2c-dev"},
		{Path: "/dev/drm_dp_aux0", Subsystem: "drm_dp_aux_dev"},
	}})

	if len(published) != 3 {
		t.Fatalf("published = %+v, want the graphics device and two companions", published)
	}
	gpu, aux, i2c := published[0], published[1], published[2]
	if gpu.Suffix != "" || gpu.Subsystem != "drm" || !gpu.RenderNode || !gpu.Shareable {
		t.Errorf("gpu = %+v", gpu)
	}
	if !slices.Equal(gpu.Nodes, []string{"/dev/dri/card0", "/dev/dri/renderD128"}) {
		t.Errorf("gpu nodes = %v, want the dri nodes and nothing else", gpu.Nodes)
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
	published := publishDevices(hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/dri/card0", Subsystem: "drm"},
		{Path: "/dev/dri/renderD128", Subsystem: "drm"},
		{Path: "/dev/fb0", Subsystem: "graphics"},
	}})

	if len(published) != 1 {
		t.Fatalf("published = %+v, want one device", published)
	}
	if slices.Contains(published[0].Nodes, "/dev/fb0") {
		t.Error("the framebuffer must not be delivered")
	}
	if !published[0].Shareable {
		t.Error("the lab guest's drm+graphics shape must stay shareable")
	}
}

func TestPublishKeepsASingleKindDeviceWhole(t *testing.T) {
	published := publishDevices(hardware.Delivery{Nodes: []hardware.DeliveredNode{
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
	published := publishDevices(hardware.Delivery{Nodes: []hardware.DeliveredNode{
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
	published := publishDevices(hardware.Delivery{
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
	published := publishDevices(hardware.Delivery{
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
	published := publishDevices(hardware.Delivery{
		Nodes: []hardware.DeliveredNode{
			{Path: "/dev/dri/card0", Subsystem: "drm"},
			{Path: "/dev/dri/renderD128", Subsystem: "drm"},
			{Path: "/dev/i2c-5", Subsystem: "i2c-dev"},
		},
		BusNode: "/dev/bus/usb/001/007",
	})

	if len(published) != 2 {
		t.Fatalf("published = %+v, want the graphics device and its companion", published)
	}
	if !slices.Contains(published[0].Nodes, "/dev/bus/usb/001/007") {
		t.Errorf("primary nodes = %v, want the usbfs node", published[0].Nodes)
	}
	if !slices.Equal(published[1].Nodes, []string{"/dev/i2c-5"}) {
		t.Errorf("companion nodes = %v, want the monitor bus alone", published[1].Nodes)
	}
}

func TestPublishReturnsNothingForAnEmptyDelivery(t *testing.T) {
	if published := publishDevices(hardware.Delivery{}); len(published) != 0 {
		t.Errorf("published = %+v, want none", published)
	}
}

func TestPublishOrdersThePrimaryFirst(t *testing.T) {
	// The primary carries the bare name, and resolveAllocated finds
	// it as the published entry whose Suffix is empty. A deterministic
	// order means the same hardware always publishes the same devices.
	published := publishDevices(hardware.Delivery{Nodes: []hardware.DeliveredNode{
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
	published := publishDevices(hardware.Delivery{Nodes: []hardware.DeliveredNode{
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
