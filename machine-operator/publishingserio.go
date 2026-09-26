package main

// The fourth examined shape of the publish policy: a serial line that
// init holds a serio attachment for (publishing.go describes the
// policy, and init/serio.go the attachment).

import (
	"slices"
	"strings"

	"github.com/liken-sh/liken/hardware"
	"github.com/liken-sh/liken/machine"
)

// publishFor is the policy's entry point. A device that carries a
// serial line a spec.serio entry matches takes the serio shape, and
// every other device takes publishDevices. serio is the list the
// machine holds attachments for (serioInEffect, dra.go).
//
// Every other interface of a matched adapter keeps its own devices,
// but loses the usbfs node. That node opens the whole USB device, so
// a claimant of the Pulse-Eight's HID interface could reset the
// adapter through it with USBDEVFS_RESET, and the reset ends the
// attachment under every CEC claim.
func publishFor(d hardware.Device, delivery hardware.Delivery, serio []machine.SerioAttachment) []publishedDevice {
	if !serioAdapter(d, serio) {
		return publishDevices(d, delivery)
	}
	if slices.Contains(delivery.Subsystems(), "tty") {
		return publishSerio(delivery)
	}
	delivery.BusNode = ""
	return publishDevices(d, delivery)
}

// serioAdapter reports whether a device is an interface of a USB
// device that a spec.serio entry matches. An interface carries its
// USB device's identity, so every interface of the adapter matches:
// the one with the serial line, and the others beside it.
func serioAdapter(d hardware.Device, serio []machine.SerioAttachment) bool {
	if d.Bus != "usb" {
		return false
	}
	return slices.ContainsFunc(serio, func(a machine.SerioAttachment) bool {
		return a.Matches(d.Vendor, d.Product, d.Serial)
	})
}

// The suffixes of the two devices an attached CEC adapter publishes.
// Neither device keeps the interface's bare name. Before spec.serio
// declared the adapter, the bare name was the tty's device, and a
// claim allocated to it then must resolve to nothing now, not to the
// CEC node.
const (
	serioCECSuffix   = "-cec"
	serioInputSuffix = "-input"
)

// serioAbsentNode is the node a claim's spec names while its serio
// device is absent: unplugged, not yet attached, or unbound. No such
// node exists, so the runtime fails to create the container, and the
// kubelet retries it under its restart backoff. The old nodes would be
// worse. An event node carries a fixed device number, and in the
// meantime another input device can take that number, so a restarted
// container would open that device instead.
const serioAbsentNode = "/dev/liken.sh/serio-device-absent"

// serioDeviceName reports whether an allocated device name is one of
// the two a CEC adapter publishes.
func serioDeviceName(name string) bool {
	return strings.HasSuffix(name, serioCECSuffix) || strings.HasSuffix(name, serioInputSuffix)
}

// publishSerio publishes one attached serial line as the devices its
// driver created.
//
// The tty is never published, before or after the attachment. A pod
// that received it could set another line discipline or write to the
// line, and either one takes the port down under every other claim.
// This is the same rule the inventory applies to a disk that holds a
// storage role. So before the attachment, the line publishes nothing.
//
// The CEC node is one device, with the -cec suffix. It is the bus: a
// claimant sends and receives CEC messages through it. The CEC
// core lets several processes open one adapter, but only one can be
// the exclusive initiator or follower, and two programs that configure
// logical addresses on one adapter undo each other, so it is
// exclusive.
//
// The input node is a second device with the -input suffix. It is the
// TV remote's key presses, which one reader must own, so it is
// exclusive too. It is a device of its own, not part of the CEC
// device, because it answers a different claim: a remote control claims it
// the way it claims a Bluetooth remote. This is the opposite of the
// audio shape, where a jack reports on the output the same claim
// plays through. Both devices carry the adapter's attributes, so a
// claim can pair them with matchAttribute on address.
//
// The usbfs node is delivered nowhere. Through it, a claimant could
// detach cdc_acm from the interface, which hangs up the tty and ends
// the port under the other claim. Any other kind of node under the
// line is hardware nobody has examined, and it is withheld with the
// tty. The CEC core registers no LIRC node for the input device, so
// the delivery holds none to withhold.
func publishSerio(delivery hardware.Delivery) []publishedDevice {
	var cec, input []string
	for _, node := range delivery.Nodes {
		switch node.Subsystem {
		case "cec":
			cec = append(cec, node.Path)
		case "input":
			input = append(input, node.Path)
		}
	}
	if len(cec) == 0 {
		return nil
	}
	published := []publishedDevice{{Suffix: serioCECSuffix, Subsystem: "cec", Nodes: cec}}
	if len(input) > 0 {
		published = append(published, publishedDevice{Suffix: serioInputSuffix, Subsystem: "input", Nodes: input})
	}
	return published
}
