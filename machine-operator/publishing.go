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
// claim receives. One subsystem splits further: drm registers the
// card node and the render node for the same silicon, and the two
// carry different authority. Modesetting on the card node belongs to
// one process, and work on the render node divides between many, so
// each publishes as its own device with its own sharing rule.
//
// An audio controller goes the other way. Its jack nodes belong to
// the input subsystem, and they stay with the card, because a jack
// reports the state of an output the same claim plays through. The
// question each time is what a claim on this hardware is, and the
// answer is a device, not a subsystem.
//
// The mechanism is generic; the tables below are deliberately not.
// Only a shape somebody examined splits. Everything else publishes
// whole and exclusive, so hardware liken has not met keeps the
// default that milestone 38 set: a device wrongly published as
// shareable hands the same hardware to two workloads and nothing can
// take that back, while a device wrongly published as exclusive costs
// a claim that waits, where a person can see it waiting.

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
	Suffix      string
	Subsystem   string
	Nodes       []string
	RenderNode  bool
	DisplayNode bool
	Shareable   bool
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

// audioSubsystems are the kinds an audio controller delivers. sound
// is ALSA's own nodes: the control node, the hardware-dependent
// nodes, and one node for each PCM subdevice. input is jack
// detection: ALSA registers an input device for every jack it can
// sense, the kernel puts that device under the card, and each one
// carries an event node. An HDA controller with an HDMI output has
// one jack for each display pin, so a real machine delivers both
// kinds, and a rule that accepted sound alone would fire only on a
// USB audio interface.
var audioSubsystems = map[string]bool{"sound": true, "input": true}

// displaySuffix names the published device that carries a graphics
// device's card node.
const displaySuffix = "-display"

// publishDevices applies the policy to one delivery. The primary
// device is always first, the display companion follows it, and the
// wire companions follow in sorted subsystem order, so the same
// hardware always publishes the same devices.
//
// The primary alone carries the delivery's bus node. A claim on the
// primary is a claim on the hardware, and a userspace driver needs
// that node to reach the device at all. A companion is one wire
// beside the primary. The bus node opens the whole device, so a
// companion must not carry it.
//
// A delivery with a bus node and nothing else publishes the bus node
// alone. This is the state a userspace driver leaves an interface in
// while it runs: libusb detaches the kernel driver to claim the
// interface, and the kernel driver's nodes leave with it. The bus
// node is the one node such a program opens, and the one node it
// cannot destroy, so a spec refreshed to this shape survives a
// container restart under the same prepared claim. Publishing here
// does not add the device to any slice: the inventory gates on the
// driver and on the subtree's nodes before it applies this policy,
// so only resolution for a claim that already holds the hardware
// sees this shape.
func publishDevices(delivery hardware.Delivery) []publishedDevice {
	published := splitBySubsystem(delivery)
	if delivery.BusNode == "" {
		return published
	}
	if len(published) == 0 {
		return []publishedDevice{{Nodes: []string{delivery.BusNode}}}
	}
	published[0].Nodes = append(published[0].Nodes, delivery.BusNode)
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

	// The two examined shapes come before the single-kind branch. A
	// delivery of drm nodes alone, or of sound nodes alone, answers
	// that branch too, and the examined answer is the specific one: it
	// separates the card node from the render node, and it states what
	// the kernel arbitrates. The single-kind branch below is what is
	// left, and it says nothing about sharing.
	if render && graphicsDevice(kinds) {
		return publishGraphics(byKind, kinds)
	}

	if slices.Contains(kinds, "sound") && audioDevice(kinds) {
		return publishAudio(delivery)
	}

	if len(kinds) == 1 {
		return []publishedDevice{{
			Subsystem:  kinds[0],
			Nodes:      delivery.DevNodes(),
			RenderNode: render,
		}}
	}

	return []publishedDevice{{
		Nodes:      delivery.DevNodes(),
		RenderNode: render,
	}}
}

// publishGraphics splits one graphics device into the devices the
// policy publishes for it: the render node, the card node, and each
// companion wire.
//
// The render node is the primary. It keeps the bare name, and it is
// the half that shares, because the driver arbitrates concurrent
// clients on it.
//
// The card node publishes as an exclusive companion, because DRM
// master is one per card. The kernel gives modesetting authority to
// one open card node, and a second display program on the same card
// fails when it starts, after the scheduler has already placed its
// pod. An exclusive companion moves that refusal to the scheduler,
// where the second claim waits and a person can see it waiting.
//
// The companion delivers the card node alone. Every device this
// policy publishes carries a disjoint part of the delivery, so the
// allocated name states exactly which nodes the claim receives, and
// the render node keeps one sharing rule rather than one rule per
// device that carries it. A workload that modesets and renders asks
// for both devices, in two requests of one claim.
//
// The framebuffer node is delivered nowhere. It is the kernel's
// legacy console interface, holding it grants display takeover, and
// no workload claims a bare framebuffer.
func publishGraphics(byKind map[string][]string, kinds []string) []publishedDevice {
	var render, cards []string
	for _, node := range byKind["drm"] {
		if isRenderNode(node) {
			render = append(render, node)
			continue
		}
		cards = append(cards, node)
	}

	published := []publishedDevice{{
		Subsystem:  "drm",
		Nodes:      render,
		RenderNode: true,
		Shareable:  true,
	}}
	if len(cards) > 0 {
		published = append(published, publishedDevice{
			Suffix:      displaySuffix,
			Subsystem:   "drm",
			Nodes:       cards,
			DisplayNode: true,
		})
	}
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

// publishAudio publishes one audio controller as one shareable
// device that delivers its whole subtree.
//
// ALSA multiplexes the card. A card holds several PCM subdevices, the
// core gives each one to the process that opened it and refuses a
// second open of the same subdevice with EBUSY, and the control node
// answers every opener. So two claims on one controller play through
// different outputs at the same time, which is what the HDMI outputs
// of a display controller are. Over-sharing here fails the way a
// person can see: the second open returns EBUSY, and no stream is
// damaged by the attempt.
//
// The jack nodes are delivered with the card, not split off. An i2c
// bus is a wire to separate hardware, and the split withholds it. A
// jack belongs to the outputs the same claim already plays through,
// and reading its state tells a player whether a display is
// connected. An event node serves many readers, so it shares the same
// way the card does.
//
// The device names sound as its subsystem, because that is the kind a
// deployment selects an audio controller by. The jack nodes ride
// along under that name.
func publishAudio(delivery hardware.Delivery) []publishedDevice {
	return []publishedDevice{{
		Subsystem: "sound",
		Nodes:     delivery.DevNodes(),
		Shareable: true,
	}}
}

// hasRenderNode reports whether the device delivers a DRM render
// node. The kernel names these /dev/dri/renderD<n>, and they are the
// nodes that do GPU work without display authority: a container that
// holds one can encode, decode, and compute. The render node is also
// what makes sharing safe: the driver arbitrates concurrent clients
// on it, and the lab measured twelve encoders dividing one integrated
// GPU evenly.
func hasRenderNode(delivery hardware.Delivery) bool {
	return slices.ContainsFunc(delivery.DevNodes(), isRenderNode)
}

// isRenderNode reports whether one drm node is a render node. Every
// other node the drm subsystem registers for a card carries display
// authority with it, so the inverse of this test is the card half.
func isRenderNode(node string) bool {
	return strings.HasPrefix(node, "/dev/dri/renderD")
}

// graphicsDevice reports whether every kind belongs to the graphics
// stack or to the companions a graphics device is known to deliver. A
// kind outside both is hardware nobody has examined, and such a
// delivery keeps the whole and exclusive default.
func graphicsDevice(kinds []string) bool {
	for _, kind := range kinds {
		if !graphicsSubsystems[kind] && !companionSubsystems[kind] {
			return false
		}
	}
	return true
}

// audioDevice reports whether every kind belongs to an audio
// controller. A kind outside the table is something else beside the
// card, and such a delivery keeps the whole and exclusive default
// until somebody examines it.
func audioDevice(kinds []string) bool {
	for _, kind := range kinds {
		if !audioSubsystems[kind] {
			return false
		}
	}
	return true
}
