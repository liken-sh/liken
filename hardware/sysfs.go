package hardware

// The sysfs walk: reading the kernel's device tree the way udev's
// coldplug replay does, but for observation only.
//
// Sysfs is the kernel's live model of the hardware, one directory
// per device, exported at /sys. Two facts per device answer this
// package's whole question. The modalias file is the device's
// identity fingerprint; the driver symlink exists exactly when some
// driver has bound the device. Fingerprint present, symlink absent:
// that is an undriven device, the raw material of the unclaimed
// report.
//
// The walk covers the pci and usb buses and stops there. Those are
// the buses where hardware arrives — soldered to the board or
// plugged in at runtime — and where a missing module is the
// operator's problem to fix. The other buses sysfs shows are either
// the kernel's own furniture (platform, acpi, and their many
// firmware-described stubs, most legitimately driverless) or
// children of devices these two already cover; walking them would
// report noise no spec.modules edit could act on.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Device is one bus device as sysfs presents it: its fingerprint,
// who drives it (empty when nothing does), and its identity in
// words, gathered here so every consumer — the unclaimed report, a
// console line, a ResourceSlice — describes hardware the same way.
//
// Address is the device's sysfs directory name — a PCI
// domain:bus:device.function or a USB port path — which is stable
// for as long as the hardware stays where it is: PCI addresses are
// fixed by the board, and a USB path names the physical port chain,
// not the plug order. That stability is what makes the address a
// usable device name in a ResourceSlice. Serial is the identity the
// hardware itself carries, when it carries one (USB devices often
// do, PCI functions rarely), and is what lets a claim pin one
// physical unit rather than any unit of the same model. Vendor and
// Product are the numeric identity underneath Name — bare lowercase
// hex, no 0x — kept because selectors match on numbers while people
// read words.
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

// DiscoverDevices walks the interesting buses under one sysfs root.
// The root is a parameter for the tests' sake; a real machine has
// exactly one sysfs. naming may be nil, in which case PCI devices
// fall back to their numeric IDs.
func DiscoverDevices(sysRoot string, naming *PCIIDs) []Device {
	var devices []Device
	for _, bus := range []string{"pci", "usb"} {
		dir := filepath.Join(sysRoot, "bus", bus, "devices")
		entries, err := os.ReadDir(dir)
		if err != nil {
			// A bus with no directory just isn't compiled into this
			// kernel; nothing to report.
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

// boundDriver reports who claimed a device: the driver symlink's
// target name, or empty when the symlink doesn't exist, which is
// sysfs's way of saying nothing has bound it.
func boundDriver(devicePath string) string {
	target, err := os.Readlink(filepath.Join(devicePath, "driver"))
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

// readAttr reads one sysfs attribute file as a trimmed string; sysfs
// attributes are single values with a trailing newline. Missing
// attributes read as empty, because absence is normal (only USB
// interfaces have bInterfaceClass, only devices with strings have
// manufacturer).
func readAttr(devicePath, attr string) string {
	raw, err := os.ReadFile(filepath.Join(devicePath, attr))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// pciName names a PCI device from the pci.ids database when one is
// loaded, and falls back to the bare vendor:device hex otherwise —
// numeric, but still enough to search by.
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

// usbName names a USB device from the strings the hardware itself
// carries. The strings live on the device; the undriven thing is
// usually one of its interfaces (leaf drivers bind interfaces, and
// the device node itself always belongs to usbcore) — so an
// interface borrows its parent's name. The parent's directory is
// the interface's minus the :config.interface suffix, USB sysfs
// naming convention.
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
// entry is an interface, for the same reason usbName borrows the
// parent's strings: identity (serial, idVendor, idProduct) lives on
// the device, while the driver binds the interface.
func parentAttr(busDir, name, attr string) string {
	parent, _, isInterface := strings.Cut(name, ":")
	dir := filepath.Join(busDir, name)
	if isInterface {
		dir = filepath.Join(busDir, parent)
	}
	return readAttr(dir, attr)
}
