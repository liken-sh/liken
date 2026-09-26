package main

// The fourth examined shape of the publish policy: a serial line that
// init holds a serio attachment for (publishing.go describes the
// policy, and init/serio.go the attachment).

import (
	"slices"

	"github.com/liken-sh/liken/hardware"
	"github.com/liken-sh/liken/machine"
)

// publishFor is the policy's entry point. A device that carries a
// serial line a spec.serio entry matches takes the serio shape, and
// every other device takes publishDevices. serio is the list the
// machine holds attachments for (serioInEffect, dra.go).
func publishFor(d hardware.Device, delivery hardware.Delivery, serio []machine.SerioAttachment) []publishedDevice {
	if serioLine(d, delivery, serio) {
		return publishSerio(delivery)
	}
	return publishDevices(d, delivery)
}

// serioLine reports whether a device is a serial line that init holds
// an attachment for. The tty node is part of the test because an
// adapter has interfaces that carry no line: the Pulse-Eight's HID
// interface has the same USB identity, and it keeps the default.
func serioLine(d hardware.Device, delivery hardware.Delivery, serio []machine.SerioAttachment) bool {
	if d.Bus != "usb" || !slices.Contains(delivery.Subsystems(), "tty") {
		return false
	}
	return slices.ContainsFunc(serio, func(a machine.SerioAttachment) bool {
		return a.Matches(d.Vendor, d.Product, d.Serial)
	})
}

// serioInputSuffix names the published device that carries the
// remote-control input device of an attached CEC adapter.
const serioInputSuffix = "-input"

// publishSerio publishes one attached serial line as the devices its
// driver created.
//
// The tty is never published, before or after the attachment. A pod
// that received it could set another line discipline or write to the
// line, and either one takes the port down under every other claim.
// This is the same rule the inventory applies to a disk that holds a
// storage role. So before the attachment, the line publishes nothing.
//
// The CEC node is the primary device, with the bare name. It is the
// bus: a claimant sends and receives CEC messages through it. The CEC
// core lets several processes open one adapter, but only one can be
// the exclusive initiator or follower, and two programs that configure
// logical addresses on one adapter undo each other, so it is
// exclusive.
//
// The input node is a second device with the -input suffix. It is the
// TV remote's key presses, which one reader must own, so it is
// exclusive too. It is a device of its own, not part of the primary,
// because it answers a different claim: a remote control claims it
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
	published := []publishedDevice{{Subsystem: "cec", Nodes: cec}}
	if len(input) > 0 {
		published = append(published, publishedDevice{Suffix: serioInputSuffix, Subsystem: "input", Nodes: input})
	}
	return published
}
