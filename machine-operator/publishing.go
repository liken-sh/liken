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
// for the same hardware.
var graphicsSubsystems = map[string]bool{"drm": true, "graphics": true}

// companionSubsystems are the non-graphics kinds a graphics device is
// known to deliver, each published as its own exclusive device. i2c-dev
// is the monitor-control bus i915 registers per display output: raw
// wire access, one writer, no arbitration contract.
var companionSubsystems = map[string]bool{"i2c-dev": true}

// publishDevices applies the policy to one delivery. The primary
// device is always first, and the companions follow in sorted
// subsystem order, so the same hardware always publishes the same
// devices.
func publishDevices(delivery hardware.Delivery) []publishedDevice {
	if len(delivery.Nodes) == 0 {
		return nil
	}
	byKind := map[string][]string{}
	for _, node := range delivery.Nodes {
		byKind[node.Subsystem] = append(byKind[node.Subsystem], node.Path)
	}
	kinds := delivery.Subsystems()
	render := hasRenderNode(delivery)

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
