package main

// Publishing for a USB serial line that init attaches to the kernel's
// serio layer, such as a Pulse-Eight CEC adapter. publishing.go
// describes the general publish policy and init/serio.go the
// attachment.

import (
	"slices"
	"strings"

	"github.com/liken-sh/liken/liken/hardware"
	"github.com/liken-sh/liken/liken/machine"
)

// publishFor returns the devices to publish for one hardware device.
// The tty interface of an adapter that a spec.serio entry matches goes
// through publishSerio, and every other device goes through
// publishDevices. serio is the list that serioInEffect returns.
//
// The other interfaces of a matched adapter keep their own devices but
// lose the usbfs node. That node opens the whole USB device, so a pod
// that claimed the adapter's HID interface could send USBDEVFS_RESET
// through it, and the reset ends the attachment under every CEC claim.
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
// device that a spec.serio entry matches. An interface has the vendor,
// product, and serial number of its USB device, so every interface of
// the adapter matches, not only the one with the serial line.
func serioAdapter(d hardware.Device, serio []machine.SerioAttachment) bool {
	if d.Bus != "usb" {
		return false
	}
	return slices.ContainsFunc(serio, func(a machine.SerioAttachment) bool {
		return a.Matches(d.Vendor, d.Product, d.Serial)
	})
}

// The name suffixes of the devices that an attached CEC adapter
// publishes. Without a spec.serio entry, the interface's bare name is
// the tty's device. A claim allocated to that name before the entry was
// added must resolve to nothing, not to the CEC node, so neither device
// uses the bare name.
const (
	serioCECSuffix   = "-cec"
	serioInputSuffix = "-input"
)

// serioAbsentNode is the device node that a claim's CDI spec names
// while its serio device is absent, because the adapter is unplugged,
// not yet attached, or unbound. The path does not exist, so the
// container runtime fails to create the container and the kubelet
// retries with its restart backoff. The spec does not keep the old
// nodes, because another input device can take an event node's device
// number, and a restarted container would then open the wrong device.
const serioAbsentNode = "/dev/liken.sh/serio-device-absent"

// serioFailsClosed reports whether a claim's device that no longer
// resolves gets serioAbsentNode in place of its old nodes. It does when
// the name ends in -cec or -input, and when the name is an interface of
// a matched adapter. The second case is a claim allocated before the
// spec.serio entry existed, whose old nodes are the tty or the usbfs
// node. A restarted container with those nodes could set another line
// discipline or reset the adapter, and either one ends the attachment
// under every CEC claim.
func serioFailsClosed(name string, byName map[string]hardware.Device, serio []machine.SerioAttachment) bool {
	if strings.HasSuffix(name, serioCECSuffix) || strings.HasSuffix(name, serioInputSuffix) {
		return true
	}
	device, ok := byName[name]
	return ok && serioAdapter(device, serio)
}

// publishSerio returns the devices for one attached serial line: an
// exclusive CEC device and, when the CEC adapter has one, an exclusive
// input device. It returns nothing until the attachment has created
// the CEC node.
//
// It never publishes the tty, because a pod with the tty could change
// the line discipline or write to the line, and either one ends the
// attachment under every CEC claim. It never publishes the usbfs node,
// because a pod with it could detach cdc_acm from the interface, which
// hangs up the tty. It also leaves out any other kind of node under the
// line, because no such hardware has been tested. The delivery has no
// LIRC node to leave out, because the CEC core registers none for the
// input device.
//
// The -cec device has the CEC node. The CEC core lets several processes
// open one adapter, but only one can be the exclusive initiator or
// follower, and two programs that configure logical addresses on one
// adapter undo each other's work. So the device is exclusive.
//
// The -input device has the event node of the TV remote's key presses.
// That node must have one reader, so this device is exclusive too. It
// is a separate device because a different pod claims it: a
// remote-control program claims it the way it claims a Bluetooth
// remote. Both devices have the adapter's attributes, so a claim can
// pair them with matchAttribute on address.
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
