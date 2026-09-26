package main

// Finding the serial lines a spec.serio entry can match.
//
// Every tty the kernel registers has an entry under /sys/class/tty,
// and the entry resolves to the tty's directory in the device tree.
// A tty that belongs to hardware carries a device link to its parent.
// For a CDC ACM adapter the parent is the USB interface that cdc_acm
// bound, and the USB device above the interface holds the identity:
// idVendor, idProduct, and serial. A usb-serial converter puts one
// more level between them, so the search climbs until a directory
// holds the identity. The console's virtual terminals and the
// pseudo-terminals have no device link, and the walk passes over them.

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/liken-sh/liken/machine"
)

// serialLineInfo is one tty with a USB device above it.
type serialLineInfo struct {
	tty                     string
	dir                     string
	vendor, product, serial string
}

// usbIdentityDepth bounds the climb from a tty's parent to the USB
// device: the interface's parent for CDC ACM, and one level more for a
// usb-serial port.
const usbIdentityDepth = 3

// discoverSerialLines lists the ttys whose hardware is a USB device.
// The list follows the class directory's order, which is sorted by
// name, so ttyACM0 comes before ttyACM1.
func discoverSerialLines() []serialLineInfo {
	classDir := filepath.Join(sysfsRoot, "class", "tty")
	entries, err := os.ReadDir(classDir)
	if err != nil {
		return nil
	}
	var lines []serialLineInfo
	for _, entry := range entries {
		dir, err := filepath.EvalSymlinks(filepath.Join(classDir, entry.Name()))
		if err != nil {
			continue
		}
		parent, err := filepath.EvalSymlinks(filepath.Join(dir, "device"))
		if err != nil {
			continue
		}
		usb := usbDeviceAbove(parent)
		if usb == "" {
			continue
		}
		lines = append(lines, serialLineInfo{
			tty:     entry.Name(),
			dir:     dir,
			vendor:  sysfsString(usb, "idVendor"),
			product: sysfsString(usb, "idProduct"),
			serial:  sysfsString(usb, "serial"),
		})
	}
	return lines
}

// usbDeviceAbove climbs from a directory to the first one that holds a
// USB device's identity, or returns "" when none does within the
// bound.
func usbDeviceAbove(dir string) string {
	for range usbIdentityDepth {
		if sysfsString(dir, "idVendor") != "" {
			return dir
		}
		up := filepath.Dir(dir)
		if up == dir {
			return ""
		}
		dir = up
	}
	return ""
}

// usbDevicePresent reports whether a USB device with an entry's
// identity is plugged in, with or without a serial line. It reads the
// bus's own list, where a device's directory name has no colon and an
// interface's has one.
func usbDevicePresent(entry machine.SerioAttachment) bool {
	busDir := filepath.Join(sysfsRoot, "bus", "usb", "devices")
	entries, err := os.ReadDir(busDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ":") {
			continue
		}
		dir := filepath.Join(busDir, e.Name())
		if entry.Matches(sysfsString(dir, "idVendor"), sysfsString(dir, "idProduct"), sysfsString(dir, "serial")) {
			return true
		}
	}
	return false
}
