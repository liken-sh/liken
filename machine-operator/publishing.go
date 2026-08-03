package main

// The publish policy: how one physical device becomes the devices a
// ResourceSlice offers.
//
// A device's sysfs subtree can deliver nodes of more than one kernel
// subsystem. i915 is the case that forced the question: beside its
// drm nodes, the driver registers an i2c bus for every display
// output, the wire that carries DDC and monitor control. A claim
// must not receive both, because a transcoder holds no business on a
// monitor's wire, and the sharing rule must not judge both, because
// the drm nodes share and the raw wires do not.
//
// So the policy publishes one device per subsystem the delivery
// holds, and the allocated device's own name states which nodes a
// claim receives. The mechanism is generic; the table below is
// deliberately not. Only a shape somebody examined splits. Everything
// else publishes whole and exclusive, so hardware liken has not met
// keeps the default that milestone 38 set: a device wrongly published
// as shareable hands the same hardware to two workloads and nothing
// can take that back, while a device wrongly published as exclusive
// costs a claim that waits, where a person can see it waiting.

import (
	"slices"
	"strings"

	"github.com/liken-sh/liken/hardware"
)

// publishedDevice is one slice device derived from one physical
// device: the nodes it delivers, the one kind they are, and the facts
// the slice publishes about them. The suffix joins the physical
// device's name to make the published name; the primary keeps the
// bare name, so an allocation that predates a split stays valid.
type publishedDevice struct {
	Suffix     string
	Subsystem  string
	Nodes      []string
	RenderNode bool
	Shareable  bool
}

// graphicsSubsystems are the kernel subsystems a graphics device
// delivers nodes through. drm is the modern interface, and graphics
// is the legacy framebuffer that the kernel's fbdev emulation creates
// for the same hardware. A GPU delivers both, so a rule that accepted
// drm alone would never fire on a real machine.
var graphicsSubsystems = map[string]bool{"drm": true, "graphics": true}

// companionSubsystems are the non-graphics kinds a graphics device is
// known to deliver, each published as its own exclusive device. i2c-dev
// is the monitor-control bus i915 registers per display output: raw
// wire access, one writer, no arbitration contract. drm_dp_aux_dev is
// the DisplayPort AUX channel, the wire that carries DPCD register
// access and EDID reads to a monitor over a DisplayPort output. The
// kernel registers it only while a display is connected, and like the
// i2c buses it is raw wire access with one writer, so it publishes as
// its own exclusive companion, never shared, and never delivered with
// the GPU's own claim.
var companionSubsystems = map[string]bool{"i2c-dev": true, "drm_dp_aux_dev": true}

// publishDevices applies the policy to one delivery. The primary
// device is always first, and the companions follow in sorted
// subsystem order, so the same hardware always publishes the same
// devices.
//
// The primary alone carries the delivery's bus node. A claim on the
// primary is a claim on the hardware, and a userspace driver needs
// that node to reach the device at all. A companion is one wire
// beside the primary. The bus node opens the whole device, so a
// companion must not carry it.
func publishDevices(delivery hardware.Delivery) []publishedDevice {
	published := splitBySubsystem(delivery)
	if len(published) > 0 && delivery.BusNode != "" {
		published[0].Nodes = append(published[0].Nodes, delivery.BusNode)
	}
	return published
}

// splitBySubsystem sorts one delivery's nodes into the devices the
// policy publishes for them.
func splitBySubsystem(delivery hardware.Delivery) []publishedDevice {
	if len(delivery.Nodes) == 0 {
		return nil
	}
	byKind := map[string][]string{}
	for _, node := range delivery.Nodes {
		byKind[node.Subsystem] = append(byKind[node.Subsystem], node.Path)
	}
	kinds := delivery.Subsystems()
	render := hasRenderNode(delivery)

	// A node the kernel did not categorize (sysfs's subsystem readlink
	// failed) is hardware nobody has examined. Route the whole delivery
	// to the unknown branch, which publishes it whole and exclusive,
	// applying the milestone 38 default to any hardware liken has not
	// met.
	if slices.ContainsFunc(delivery.Nodes, func(n hardware.DeliveredNode) bool {
		return n.Subsystem == ""
	}) {
		return []publishedDevice{{
			Nodes:      delivery.DevNodes(),
			RenderNode: render,
		}}
	}

	if len(kinds) == 1 {
		return []publishedDevice{{
			Subsystem:  kinds[0],
			Nodes:      delivery.DevNodes(),
			RenderNode: render,
			Shareable:  render && allGraphics(kinds),
		}}
	}

	known := render && slices.IndexFunc(kinds, func(kind string) bool {
		return !graphicsSubsystems[kind] && !companionSubsystems[kind]
	}) < 0
	if !known {
		return []publishedDevice{{
			Nodes:      delivery.DevNodes(),
			RenderNode: render,
		}}
	}

	published := []publishedDevice{{
		Subsystem:  "drm",
		Nodes:      byKind["drm"],
		RenderNode: true,
		Shareable:  true,
	}}
	for _, kind := range kinds {
		if !companionSubsystems[kind] {
			continue
		}
		published = append(published, publishedDevice{
			Suffix:    "-" + strings.ReplaceAll(kind, "_", "-"),
			Subsystem: kind,
			Nodes:     byKind[kind],
		})
	}
	return published
}

// hasRenderNode reports whether the device delivers a DRM render
// node. The kernel names these /dev/dri/renderD<n>, and they are the
// nodes that do GPU work without display authority: a container that
// holds one can encode, decode, and compute. The render node is also
// what makes sharing safe: the driver arbitrates concurrent clients
// on it, and the lab measured twelve encoders dividing one integrated
// GPU evenly.
func hasRenderNode(delivery hardware.Delivery) bool {
	return slices.ContainsFunc(delivery.DevNodes(), func(node string) bool {
		return strings.HasPrefix(node, "/dev/dri/renderD")
	})
}

// allGraphics reports whether every kind is part of the graphics
// stack.
func allGraphics(kinds []string) bool {
	for _, kind := range kinds {
		if !graphicsSubsystems[kind] {
			return false
		}
	}
	return true
}
