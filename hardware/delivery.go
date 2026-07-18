package hardware

// What claiming a device would actually hand over.
//
// Delivering hardware to a workload means device nodes: the /dev
// entries a container can be given without any privilege. Sysfs
// records exactly which descendants of a device have nodes — any
// directory holding a `dev` file is one, and its uevent publishes
// the node's path under /dev as DEVNAME. So "what would claiming
// this device deliver" is a subtree walk, and a device whose subtree
// delivers nothing (a NIC, a bare controller) is not claimable
// inventory at all, however real the hardware is.
//
// The walk prunes at nested bus devices. Sysfs nests the physical
// topology — a USB controller's directory contains the hubs, which
// contain the devices, which contain the interfaces — so without
// pruning, every device node on the bus would count toward the
// controller that hosts it. Each PCI and USB device gets its own
// inventory decision, so a walk claims only the nodes between this
// device and the next bus device down.

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Delivery is the walk's report: the device nodes a claim on this
// device would inject, and the block-device names among them, which
// is what the platform test checks against the storage roles'
// partitions.
type Delivery struct {
	DevNodes []string
	Blocks   []string
}

// InspectDelivery walks one device's sysfs subtree for device nodes.
// Missing devices report an empty delivery: hardware can unplug
// between a discovery walk and this one, and an empty answer is the
// truthful one for hardware that isn't there anymore.
func InspectDelivery(sysRoot string, d Device) Delivery {
	root := filepath.Join(sysRoot, "bus", d.Bus, "devices", d.Address)
	// The bus entry is a symlink into the devices tree; the walk
	// needs the real directory so its children are real directories
	// too.
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return Delivery{}
	}
	var delivery Delivery
	_ = filepath.WalkDir(resolved, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return nil
		}
		if path != resolved && isBusDevice(path) {
			return fs.SkipDir
		}
		if _, err := os.Stat(filepath.Join(path, "dev")); err != nil {
			return nil
		}
		devname := ueventDevName(path)
		if devname == "" {
			return nil
		}
		delivery.DevNodes = append(delivery.DevNodes, "/dev/"+devname)
		if subsystemName(path) == "block" {
			delivery.Blocks = append(delivery.Blocks, filepath.Base(path))
		}
		return nil
	})
	return delivery
}

// isBusDevice reports whether a sysfs directory is itself a PCI or
// USB device — the subtree boundary where another inventory decision
// begins.
func isBusDevice(path string) bool {
	name := subsystemName(path)
	return name == "pci" || name == "usb"
}

// subsystemName is the base of the subsystem symlink every sysfs
// device carries: which kernel subsystem the directory belongs to.
func subsystemName(path string) string {
	target, err := os.Readlink(filepath.Join(path, "subsystem"))
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

// ueventDevName extracts DEVNAME from a device's uevent file: the
// node's path relative to /dev, which devtmpfs mirrors exactly.
func ueventDevName(path string) string {
	raw, err := os.ReadFile(filepath.Join(path, "uevent"))
	if err != nil {
		return ""
	}
	for line := range strings.Lines(string(raw)) {
		if name, ok := strings.CutPrefix(strings.TrimSpace(line), "DEVNAME="); ok {
			return name
		}
	}
	return ""
}
