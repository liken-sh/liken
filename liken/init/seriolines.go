package main

// discoverSerialLines finds the serial lines that a spec.serio entry
// can match.
//
// Each tty has an entry under /sys/class/tty that resolves to the
// tty's directory in the device tree. A tty that belongs to hardware
// has a device link to its parent. For a CDC ACM adapter the parent is
// the USB interface that cdc_acm bound, and the USB device above the
// interface has the files idVendor, idProduct, and serial. The
// console's virtual terminals and the pseudo-terminals have no device
// link, so discoverSerialLines skips them.

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
)

// serialLineInfo is one tty with a USB device above it. identity is
// the inode of the tty's sysfs directory. The kernel creates a new
// directory each time it registers the tty, so a tty that registers
// again under the same name has a new identity and counts as new
// hardware. usbPath is the sysfs path of the USB device. It identifies
// the port the adapter is plugged into, and stays the same when the
// adapter enumerates again.
type serialLineInfo struct {
	tty                     string
	dir                     string
	identity                uint64
	usbPath                 string
	vendor, product, serial string
}

// usbIdentityDepth is how many directories usbDeviceAbove checks,
// starting at the tty's parent: the interface and the USB device for
// CDC ACM, and one more for a usb-serial converter, whose port
// directory is between the interface and the tty.
const usbIdentityDepth = 3

// discoverSerialLines lists the ttys that have a USB device above
// them, in the byte order of their names that os.ReadDir returns, so
// ttyACM10 comes before ttyACM2.
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
		var stat unix.Stat_t
		if err := unix.Stat(dir, &stat); err != nil {
			continue
		}
		lines = append(lines, serialLineInfo{
			tty:      entry.Name(),
			dir:      dir,
			identity: stat.Ino,
			usbPath:  usb,
			vendor:   sysfsString(usb, "idVendor"),
			product:  sysfsString(usb, "idProduct"),
			serial:   sysfsString(usb, "serial"),
		})
	}
	return lines
}

// usbDeviceAbove returns the first directory, starting at dir and
// climbing toward the root, that has an idVendor file, or "" when none
// of the first usbIdentityDepth directories does.
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

// usbDevicePresent reports whether a USB device that matches the entry
// is plugged in, whether or not it has a tty. It lists
// /sys/bus/usb/devices, where an interface's name contains a colon and
// a device's does not.
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
