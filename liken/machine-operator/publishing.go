package main

// The policy that turns one physical device into the devices a
// ResourceSlice publishes.
//
// A device's sysfs subtree can contain nodes of several kernel
// subsystems. An i915 GPU registers an i2c bus for each display output
// beside its drm nodes, for DDC and monitor control. A transcoder that
// claims the GPU has no use for that bus, and the render node can be
// shared while the raw bus cannot. So the policy publishes a separate
// device for each subsystem, and the name of the allocated device tells
// which nodes the claim receives. The drm nodes split further, because
// one process at a time holds modesetting authority on the card node,
// and many processes can share the render node.
//
// An audio controller keeps its jack nodes, which belong to the input
// subsystem, because a jack reports the state of an output that the
// same claim plays through. A Bluetooth adapter has no device node of
// its own, so the policy recognizes it by its driver
// (publishBluetooth). The serial line of a USB-CEC adapter needs the
// Machine's spec, and publishingserio.go has the policy for it.
//
// Any other hardware publishes as one exclusive device with all its
// nodes. A device wrongly published as shareable gives the same
// hardware to two workloads, and nothing can undo that. A device
// wrongly published as exclusive only makes a second claim wait in the
// scheduler, where a person can see it.

import (
	"slices"
	"strings"

	"github.com/liken-sh/liken/liken/hardware"
)

// publishedDevice is one slice device derived from one physical
// device. Suffix is appended to the physical device's name to make
// the published name. The primary has no suffix, so an allocation made
// before a split still resolves.
type publishedDevice struct {
	Suffix      string
	Subsystem   string
	Nodes       []string
	RenderNode  bool
	DisplayNode bool
	Shareable   bool
}

// graphicsSubsystems are the subsystems of a GPU's own nodes: drm, and
// graphics for the /dev/fb node that the kernel's fbdev emulation
// creates for the same GPU.
var graphicsSubsystems = map[string]bool{"drm": true, "graphics": true}

// companionSubsystems are the other subsystems of a GPU's nodes, and
// each one publishes as its own exclusive device. i2c-dev is the
// monitor-control bus that i915 registers for each display output.
// drm_dp_aux_dev is the DisplayPort AUX channel, for DPCD registers and
// EDID reads, and the kernel registers it only while a display is
// connected. Both give raw access to a wire that one writer must own.
var companionSubsystems = map[string]bool{"i2c-dev": true, "drm_dp_aux_dev": true}

// audioSubsystems are the subsystems of an audio controller's nodes:
// sound for ALSA's nodes, and input for each jack that ALSA can sense,
// because the kernel registers jack input devices under the card. An
// HDA controller with HDMI outputs has a jack for each display pin.
var audioSubsystems = map[string]bool{"sound": true, "input": true}

const displaySuffix = "-display"

// claimDelivery returns the nodes a claim on d receives: the nodes in
// d's sysfs subtree, except the nodes that the machine keeps for itself
// (reserved.go). A Bluetooth adapter's claim also receives nodes from
// outside its subtree, for the Bluetooth stack in the claiming
// container:
//
//   - /dev/uhid, which BlueZ writes to present a BLE HID-over-GATT
//     peripheral as an input device. It exists only when the machine
//     loads the uhid module.
//   - /dev/uinput and the legacy evdev nodes /dev/input/event0 through
//     event31. The stack reads a controller's events from its evdev
//     node and writes them to a uinput virtual device. The kernel
//     registers a controller's evdev node only while the controller is
//     connected, and the container runtime adds a node only when the
//     container starts, so the claim lists every evdev node by number
//     in advance.
func claimDelivery(sysRoot string, d hardware.Device) hardware.Delivery {
	delivery := withoutReserved(hardware.InspectDelivery(sysRoot, d), reservedNodes(sysRoot))
	if !bluetoothAdapter(d) {
		return delivery
	}
	if node := hardware.MiscNode(sysRoot, "uhid"); node != "" {
		delivery.Nodes = append(delivery.Nodes, hardware.DeliveredNode{Path: node, Subsystem: "misc"})
	}
	if node := hardware.MiscNode(sysRoot, "uinput"); node != "" {
		delivery.Nodes = append(delivery.Nodes, hardware.DeliveredNode{Path: node, Subsystem: "misc"})
		delivery.Nodes = append(delivery.Nodes, hardware.EvdevNodes()...)
	}
	return delivery
}

// publishDevices applies the policy to one device and its delivery.
// The primary device comes first, then the display companion, then the
// other companions in subsystem order, so the same hardware always
// publishes the same devices.
//
// Only the primary receives the delivery's usbfs bus node. A userspace
// driver needs that node to open the device, and the node opens every
// interface of the device, so a companion with it would reach more
// than its own nodes.
//
// A delivery with only a bus node publishes that node alone. This
// happens when a libusb program detaches the kernel driver to claim the
// interface, which removes the driver's nodes. The bus node stays, so a
// CDI spec written in that state still has a node to inject when the
// container restarts. The inventory skips a device with no nodes in its
// subtree, so for a device with a driver only resolveAllocated, for a
// claim that is already allocated, reaches this case.
func publishDevices(d hardware.Device, delivery hardware.Delivery) []publishedDevice {
	if bluetoothAdapter(d) {
		return publishBluetooth(delivery)
	}
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

	// A node has no subsystem when its sysfs subsystem link could not
	// be read. The policy cannot recognize that hardware.
	if slices.ContainsFunc(delivery.Nodes, func(n hardware.DeliveredNode) bool {
		return n.Subsystem == ""
	}) {
		return []publishedDevice{{
			Nodes:      delivery.DevNodes(),
			RenderNode: render,
		}}
	}

	// A delivery of only drm nodes or only sound nodes has one
	// subsystem too, so the graphics and audio checks come first to
	// give it the split and the sharing rule of its kind.
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

// publishGraphics divides a GPU's delivery into the render node, the
// card node, and one device for each companion subsystem.
//
// The render node is the primary and keeps the bare name. It is
// shareable, because the driver arbitrates between concurrent clients
// on it.
//
// The card node is an exclusive companion, because the kernel gives DRM
// master to one open card node at a time. A second display program on
// the same card fails at startup, after the scheduler has placed its
// pod, and an exclusive device makes its claim wait in the scheduler
// instead. A workload that both modesets and renders requests both
// devices in one claim.
//
// The framebuffer node goes to no device. It is the kernel's legacy
// console interface, a process that writes to it can take over the
// display, and no workload needs it.
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

// publishAudio publishes one audio controller as one exclusive device
// with its whole subtree.
//
// A sound server such as PipeWire opens every PCM of the card and
// mixes all streams through them. ALSA gives each PCM subdevice to one
// opener and returns EBUSY to the next, so a second claim on the card
// would fail at play time in whichever pod opened second. An exclusive
// device makes that claim wait in the scheduler instead.
//
// The jack nodes stay with the card, because a jack's state tells a
// player whether a display is connected to an output it plays through.
// The device's subsystem is sound, because a workload selects an audio
// controller by that subsystem.
func publishAudio(delivery hardware.Delivery) []publishedDevice {
	return []publishedDevice{{
		Subsystem: "sound",
		Nodes:     delivery.DevNodes(),
	}}
}

// publishBluetooth publishes a Bluetooth adapter as one exclusive
// device with its usbfs node, and publishes nothing for an adapter
// without one.
//
// An adapter has no device node of its own, because a Bluetooth stack
// binds an AF_BLUETOOTH socket to the adapter by index. The nodes under
// the adapter in sysfs belong to connected peripherals, and the walk
// leaves them out. So the inventory, which skips a device with no
// nodes in its subtree, makes an exception for an adapter. The claim
// still matters: it records which workload owns the radio and
// schedules that workload on the radio's machine. Any node left in the
// subtree is the adapter's own, and the device has it beside the usbfs
// node. The nodes that claimDelivery adds reach the claim through this
// device.
//
// The device is exclusive, because the kernel does not arbitrate
// between two Bluetooth stacks on one radio. Both would scan and pair,
// and each would read state that the other wrote.
func publishBluetooth(delivery hardware.Delivery) []publishedDevice {
	if delivery.BusNode == "" {
		return nil
	}
	return []publishedDevice{{Nodes: append(delivery.DevNodes(), delivery.BusNode)}}
}

// hasRenderNode reports whether the delivery has a DRM render node,
// /dev/dri/renderD<n>, which lets a container encode, decode, and
// compute on the GPU without display authority.
func hasRenderNode(delivery hardware.Delivery) bool {
	return slices.ContainsFunc(delivery.DevNodes(), isRenderNode)
}

// isRenderNode reports whether a drm node is a render node. Any other
// drm node of a GPU is a card node.
func isRenderNode(node string) bool {
	return strings.HasPrefix(node, "/dev/dri/renderD")
}

// graphicsDevice reports whether every subsystem is a graphics or a
// companion subsystem. A delivery with any other subsystem is hardware
// that the policy does not recognize.
func graphicsDevice(kinds []string) bool {
	for _, kind := range kinds {
		if !graphicsSubsystems[kind] && !companionSubsystems[kind] {
			return false
		}
	}
	return true
}

// audioDevice reports whether every subsystem is an audio subsystem.
func audioDevice(kinds []string) bool {
	for _, kind := range kinds {
		if !audioSubsystems[kind] {
			return false
		}
	}
	return true
}

// bluetoothAdapter reports whether d is a Bluetooth host controller on
// the USB bus. btusb drives every adapter that follows the standard USB
// Bluetooth interface, both dongles and radios built into a board.
func bluetoothAdapter(d hardware.Device) bool {
	return d.Bus == "usb" && d.Driver == "btusb"
}
