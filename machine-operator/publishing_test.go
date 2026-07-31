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

func TestPublishReturnsNothingForAnEmptyDelivery(t *testing.T) {
	if published := publishDevices(hardware.Delivery{}); len(published) != 0 {
		t.Errorf("published = %+v, want none", published)
	}
}

func TestPublishOrdersThePrimaryFirst(t *testing.T) {
	// The primary carries the bare name, and Task 4 resolves an
	// allocated name by taking the first published entry whose full
	// name matches, so the order must be deterministic.
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
