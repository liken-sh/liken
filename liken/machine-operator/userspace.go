package main

// Some USB devices have no kernel driver, because the vendor's driver
// is a program that opens the device through libusb. ZWO astronomy
// cameras, smart card readers that pcscd serves, and many
// software-defined radios work this way. The kernel enumerates such a
// device, creates its interfaces, binds no driver to any of them, and
// creates the usbfs node /dev/bus/usb/<bus>/<device>. The program finds
// the device in sysfs and opens that node.
//
// The inventory publishes such a device as one exclusive slice device,
// named by its port path such as usb-1-2, that delivers the usbfs node.
// The usbfs node gives access to every interface of the device, so one
// claim must cover the whole device.
//
// The inventory publishes the whole device only when no interface has
// a driver. When the kernel drives some interfaces, the inventory
// already publishes each of them with the usbfs node beside its own
// nodes, and a claim on the whole device would give the same hardware
// to a second workload. For example, a USB audio adapter can have a HID
// interface with no driver: a player holds the audio interface, and a
// claim on the whole adapter could reset it during playback. A program
// that claims an interface through libusb makes the kernel bind the
// usbfs driver to it, so the same rule removes the device from the
// slice while a program uses it.

import (
	"strings"

	"github.com/liken-sh/liken/liken/hardware"
)

// userspaceDevice reports whether d is a USB device that has at least
// one interface and no driver on any interface. It returns d with the
// class of its first interface, so that a DeviceClass can select a
// camera or a card reader by class. A USB device's own class is usually
// 00, which means that each interface declares its own.
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
