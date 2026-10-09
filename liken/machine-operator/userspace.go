package main

// USB devices that a program in userspace drives.
//
// Some USB devices never get a kernel driver, because the vendor's
// driver is a program that reaches the device through libusb. ZWO's
// astronomy cameras, smart card readers that pcscd serves, and many
// software-defined radios are this kind. The kernel enumerates such
// a device, creates its interfaces, binds none of them, and gives the
// device its usbfs node, /dev/bus/usb/<bus>/<device>. The program
// reads sysfs to find the device and opens that node, and it needs
// nothing more.
//
// The inventory publishes such a device whole, as one exclusive slice
// device that delivers the usbfs node, and names it by the device's
// own port path, usb-1-2, not by an interface's. The usbfs node opens
// every interface of the device, so one claim on the device is the
// only claim that can hand it over.
//
// The rule asks that no interface has a driver at all. A device that
// the kernel drives in part already publishes its driven interfaces,
// each with the usbfs node beside its own nodes, and a second claim on
// the whole device would hand the same hardware to a second workload.
// A USB audio adapter whose HID interface no driver bound is the case
// the lab's testbed showed: a player holds the audio interface, and a
// claim on the whole adapter could reset it under the player. The
// rule also refuses an interface that usbfs holds. A program bound to
// an interface while it runs, and a device that a program holds this
// way leaves the slice until the program lets go, so a second claim
// cannot arrive while the first one is using the device.

import (
	"strings"

	"github.com/liken-sh/liken/liken/hardware"
)

// userspaceDevice reports whether d is a USB device whose every
// interface has no driver, and returns it with the first interface's
// class, so that a DeviceClass can select a camera or a card reader by
// what the device says it is. A USB device's own class is most often
// 00, which means that each interface states its own.
func userspaceDevice(d hardware.Device, discovered []hardware.Device) (hardware.Device, bool) {
	if d.Bus != "usb" || d.Driver != "usb" || strings.Contains(d.Address, ":") {
		return d, false
	}
	var interfaces []hardware.Device
	for _, other := range discovered {
		if other.Bus == "usb" && strings.HasPrefix(other.Address, d.Address+":") {
			interfaces = append(interfaces, other)
		}
	}
	if len(interfaces) == 0 {
		return d, false
	}
	for _, i := range interfaces {
		if i.Driver != "" {
			return d, false
		}
	}
	d.Class, d.ClassCode = interfaces[0].Class, interfaces[0].ClassCode
	return d, true
}
