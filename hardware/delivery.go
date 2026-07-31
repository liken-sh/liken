package hardware

// This file finds what claiming a device would hand over.
//
// Delivering hardware to a workload means giving it device nodes:
// the /dev entries that a container can receive without any
// privilege. Sysfs records exactly which descendants of a device
// have nodes. Any directory that holds a `dev` file is one of these
// descendants, and its uevent file publishes the node's path under
// /dev as DEVNAME. So the question "what would claiming this device
// deliver" is answered by a subtree walk. A device whose subtree
// delivers nothing, such as a NIC or a bare controller, is not
// claimable inventory, even though the hardware is real.
//
// The walk stops at nested bus devices. Sysfs nests the physical
// topology: a USB controller's directory contains the hubs, which
// contain the devices, which contain the interfaces. Without this
// limit, every device node on the bus would count toward the
// controller that hosts it. Each PCI and USB device gets its own
// inventory decision. For this reason, a walk claims only the nodes
// between this device and the next bus device below it.

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// DeliveredNode is one /dev entry a claim on a device would inject.
// The subsystem is the kernel's category for the node, and the
// category decides how the node behaves when two processes open it.
// Block carries the sysfs base name when the node is a block device,
// because the platform test matches storage roles by that name.
type DeliveredNode struct {
	Path      string
	Subsystem string
	Block     string
}

// Delivery is the report that the walk produces: every device node
// that a claim on this device would inject, each paired with its
// kernel subsystem. The publish policy groups these nodes by
// subsystem, so the pairing is the record, and the flat views below
// derive from it.
type Delivery struct {
	Nodes []DeliveredNode
}

// DevNodes lists the /dev paths in walk order.
func (d Delivery) DevNodes() []string {
	var paths []string
	for _, n := range d.Nodes {
		paths = append(paths, n.Path)
	}
	return paths
}

// Blocks lists the block-device base names among the nodes. The
// platform test checks these against the storage roles' partitions.
func (d Delivery) Blocks() []string {
	var blocks []string
	for _, n := range d.Nodes {
		if n.Block != "" {
			blocks = append(blocks, n.Block)
		}
	}
	return blocks
}

// Subsystems names each kind of node the delivery holds, once,
// sorted, so the same hardware always reports the same list.
func (d Delivery) Subsystems() []string {
	var kinds []string
	for _, n := range d.Nodes {
		if n.Subsystem != "" && !slices.Contains(kinds, n.Subsystem) {
			kinds = append(kinds, n.Subsystem)
		}
	}
	slices.Sort(kinds)
	return kinds
}

// InspectDelivery walks one device's sysfs subtree and finds its
// device nodes. If the device is missing, InspectDelivery reports an
// empty delivery. Hardware can unplug between a discovery walk and
// this one, and an empty result is correct for hardware that is no
// longer there.
func InspectDelivery(sysRoot string, d Device) Delivery {
	root := filepath.Join(sysRoot, "bus", d.Bus, "devices", d.Address)
	// The bus entry is a symlink into the devices tree. The walk
	// needs the real directory, so that its children are also real
	// directories.
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
		node := DeliveredNode{Path: "/dev/" + devname, Subsystem: subsystemName(path)}
		if node.Subsystem == "block" {
			node.Block = filepath.Base(path)
		}
		delivery.Nodes = append(delivery.Nodes, node)
		return nil
	})
	return delivery
}

// isBusDevice reports whether a sysfs directory is itself a PCI or
// USB device. This is the subtree boundary where another inventory
// decision begins.
func isBusDevice(path string) bool {
	name := subsystemName(path)
	return name == "pci" || name == "usb"
}

// subsystemName reads the subsystem symlink that every sysfs device
// carries, and returns its base name: the kernel subsystem that the
// directory belongs to.
func subsystemName(path string) string {
	target, err := os.Readlink(filepath.Join(path, "subsystem"))
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

// ueventDevName extracts DEVNAME from a device's uevent file.
// DEVNAME is the node's path relative to /dev, and devtmpfs mirrors
// this path exactly.
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
