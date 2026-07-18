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
// console line, someday a ResourceSlice — describes hardware the
// same way.
type Device struct {
	Bus      string
	Modalias string
	Driver   string
	Name     string
	Class    string
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
			d := Device{Bus: bus, Modalias: modalias, Driver: boundDriver(path)}
			switch bus {
			case "pci":
				d.Name = pciName(path, naming)
				d.Class = pciClassWord(readAttr(path, "class"))
			case "usb":
				d.Name = usbName(dir, entry.Name())
				d.Class = usbClassWord(readAttr(path, "bInterfaceClass"))
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
func pciName(devicePath string, naming *PCIIDs) string {
	vendor := strings.TrimPrefix(readAttr(devicePath, "vendor"), "0x")
	device := strings.TrimPrefix(readAttr(devicePath, "device"), "0x")
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
