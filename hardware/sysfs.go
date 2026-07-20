package hardware

// This file implements the sysfs walk. It reads the kernel's device
// tree the way udev's coldplug replay does, but only to observe, not
// to act.
//
// Sysfs is the kernel's live model of the hardware, with one
// directory per device, exported at /sys. Two facts about each
// device answer this package's whole question. The modalias file
// holds the device's identity fingerprint. The driver symlink exists
// exactly when a driver has bound the device. When the fingerprint
// is present and the symlink is absent, the device is undriven, and
// this is the raw material for the unclaimed report.
//
// The walk covers the pci and usb buses only. These are the buses
// where hardware arrives, either soldered to the board or plugged
// in at runtime, and where a missing module is a problem for the
// operator to fix. The other buses that sysfs shows are either
// devices built into the kernel itself (platform, acpi, and their
// many firmware-described stubs, most of which have no driver by
// design) or children of devices that the pci and usb buses already
// cover. Walking these other buses would report information that no
// change to spec.modules could act on.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Device is one bus device as sysfs presents it. It holds the
// device's fingerprint, the name of its driver (empty when it has
// none), and its identity in words. Every consumer gathers this data
// here — the unclaimed report, a console line, a ResourceSlice — so
// that each one describes hardware the same way.
//
// Address is the device's sysfs directory name: a PCI
// domain:bus:device.function or a USB port path. This name stays
// stable for as long as the hardware stays in place. PCI addresses
// are fixed by the board, and a USB path names the physical port
// chain, not the order in which devices were plugged in. This
// stability is what makes Address usable as a device name in a
// ResourceSlice.
//
// Serial is the identity that the hardware itself carries, when it
// carries one. USB devices often carry a serial number; PCI
// functions rarely do. Serial lets a claim pin one physical unit,
// rather than any unit of the same model.
//
// Vendor and Product are the numeric identity underneath Name: bare
// lowercase hex, without the 0x prefix. The struct keeps both forms
// because selectors match on numbers, while people read words.
type Device struct {
	Bus      string
	Address  string
	Modalias string
	Driver   string
	Name     string
	Class    string
	Serial   string
	Vendor   string
	Product  string
}

// DiscoverDevices walks the pci and usb buses under one sysfs root.
// The root is a parameter so that tests can supply their own; a real
// machine has exactly one sysfs. naming may be nil. When it is nil,
// PCI devices fall back to their numeric IDs.
func DiscoverDevices(sysRoot string, naming *PCIIDs) []Device {
	var devices []Device
	for _, bus := range []string{"pci", "usb"} {
		dir := filepath.Join(sysRoot, "bus", bus, "devices")
		entries, err := os.ReadDir(dir)
		if err != nil {
			// A bus with no directory is not compiled into this
			// kernel. There is nothing to report.
			continue
		}
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			modalias := readAttr(path, "modalias")
			if modalias == "" {
				continue
			}
			d := Device{Bus: bus, Address: entry.Name(), Modalias: modalias, Driver: boundDriver(path)}
			switch bus {
			case "pci":
				d.Vendor = strings.TrimPrefix(readAttr(path, "vendor"), "0x")
				d.Product = strings.TrimPrefix(readAttr(path, "device"), "0x")
				d.Name = pciName(d.Vendor, d.Product, naming)
				d.Class = pciClassWord(readAttr(path, "class"))
			case "usb":
				d.Vendor = parentAttr(dir, entry.Name(), "idVendor")
				d.Product = parentAttr(dir, entry.Name(), "idProduct")
				d.Name = usbName(dir, entry.Name())
				d.Class = usbClassWord(readAttr(path, "bInterfaceClass"))
				d.Serial = parentAttr(dir, entry.Name(), "serial")
			}
			devices = append(devices, d)
		}
	}
	return devices
}

// boundDriver reports which driver claimed a device: the driver
// symlink's target name. It returns empty when the symlink does not
// exist, which is how sysfs shows that no driver has bound the
// device.
func boundDriver(devicePath string) string {
	target, err := os.Readlink(filepath.Join(devicePath, "driver"))
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

// readAttr reads one sysfs attribute file as a trimmed string. Sysfs
// attributes are single values with a trailing newline. A missing
// attribute reads as empty, because absence is normal. For example,
// only USB interfaces have bInterfaceClass, and only devices with
// strings have manufacturer.
func readAttr(devicePath, attr string) string {
	raw, err := os.ReadFile(filepath.Join(devicePath, attr))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// pciName names a PCI device from the pci.ids database, when one is
// loaded. Otherwise, it falls back to the bare vendor:device hex.
// This fallback is numeric, but it is still enough to search by.
func pciName(vendor, device string, naming *PCIIDs) string {
	if vendor == "" {
		return ""
	}
	if naming != nil {
		if name := naming.Name(vendor, device); name != "" {
			return name
		}
	}
	return fmt.Sprintf("%s:%s", vendor, device)
}

// usbName names a USB device from the strings that the hardware
// itself carries. These strings live on the device, but the
// undriven part is usually one of the device's interfaces. Leaf
// drivers bind interfaces, and the device node itself always
// belongs to usbcore, so an interface borrows its parent's name. The
// parent's directory name is the interface's directory name minus
// the :config.interface suffix, which is the USB sysfs naming
// convention.
func usbName(busDir, name string) string {
	parent, _, isInterface := strings.Cut(name, ":")
	dir := filepath.Join(busDir, name)
	if isInterface {
		dir = filepath.Join(busDir, parent)
	}
	words := strings.TrimSpace(
		readAttr(dir, "manufacturer") + " " + readAttr(dir, "product"))
	return words
}

// parentAttr reads a USB attribute from the parent device when the
// entry is an interface. This works for the same reason that usbName
// borrows the parent's strings: identity attributes such as serial,
// idVendor, and idProduct live on the device, while the driver binds
// the interface.
func parentAttr(busDir, name, attr string) string {
	parent, _, isInterface := strings.Cut(name, ":")
	dir := filepath.Join(busDir, name)
	if isInterface {
		dir = filepath.Join(busDir, parent)
	}
	return readAttr(dir, attr)
}
