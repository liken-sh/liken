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
//
// The walk stops at a bluetooth device as well. Sysfs puts the HID
// device of a connected peripheral under the adapter's own USB
// interface, so the nodes below a bluetooth device are the
// peripherals' nodes. They come and go as people turn controllers and
// headsets on, and the air is not a bus this walk can descend to give
// each peripheral its own inventory decision.
//
// One node comes from above the walk instead of below it. A USB
// interface's driver publishes its own nodes, but the device that the
// interface belongs to also has a usbfs node, and that is the node
// every libusb program opens. The walk reports it beside the subtree's
// nodes, because both belong to the same claim.
//
// One more node can belong to a claim from outside any subtree. The
// kernel registers a misc device such as /dev/uhid under no bus
// device, so the walk cannot reach it, and MiscNode below reads the
// misc class directly. The publish policy decides which claim
// receives such a node.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
//
// BusNode is the node that carries transfers to the whole device on
// its bus, rather than to one driver's interface: on USB, the usbfs
// node. It is not part of Nodes because it is not one of the kinds
// the policy sorts by. If it were among them, the policy could
// deliver it with a companion device, and a claim on one wire would
// reach the whole device.
type Delivery struct {
	Nodes   []DeliveredNode
	BusNode string
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
		if path != resolved && isSubtreeBoundary(path) {
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
	delivery.BusNode = usbfsNode(sysRoot, d)
	return delivery
}

// usbfsNode reports the usbfs node of the USB device that an
// interface belongs to. usbfs gives every USB device one character
// node, /dev/bus/usb/<busnum>/<devnum>, with three digits in each
// number, and that node carries raw transfers to any endpoint of the
// device. A userspace driver built on libusb reads sysfs to enumerate
// the hardware, which needs no privilege, and then opens this node to
// talk to it. The nodes a kernel driver registers carry that driver's
// own protocol, so hidraw or a tty cannot take its place, and a
// libusb program in a container without it enumerates the device and
// then fails to open it.
//
// The walk adds this node only for an interface. Leaf drivers bind
// interfaces, usbfs names the usb_device above them, and the walk
// already reports that node for the usb_device itself.
//
// The kernel assigns devnum at enumeration, so the same hardware in
// the same port gets a different node after a replug. liken does not
// store the number: each walk reads it again.
func usbfsNode(sysRoot string, d Device) string {
	parent, _, isInterface := strings.Cut(d.Address, ":")
	if d.Bus != "usb" || !isInterface {
		return ""
	}
	dir := filepath.Join(sysRoot, "bus", "usb", "devices", parent)
	bus, err := strconv.Atoi(readAttr(dir, "busnum"))
	if err != nil {
		return ""
	}
	device, err := strconv.Atoi(readAttr(dir, "devnum"))
	if err != nil {
		return ""
	}
	return fmt.Sprintf("/dev/bus/usb/%03d/%03d", bus, device)
}

// MiscNode reports the /dev node of a named misc device, or nothing
// while the module that registers the device is not loaded. The
// kernel lists every misc device under /sys/class/misc, where its
// uevent file publishes the node's path as DEVNAME, the same way a
// subtree's entries do, and devtmpfs creates the node the moment the
// class entry appears.
func MiscNode(sysRoot, name string) string {
	devname := ueventDevName(filepath.Join(sysRoot, "class", "misc", name))
	if devname == "" {
		return ""
	}
	return "/dev/" + devname
}

// isSubtreeBoundary reports whether a sysfs directory ends the walk.
//
// A PCI or USB device is the boundary because another inventory
// decision begins there: the device below gets its own entry in the
// slice, and its nodes go with that entry.
//
// A bluetooth device is the boundary for a different reason. The
// nodes below it belong to the peripherals that are connected to the
// radio right now, not to the radio. A game controller's HID device
// lands under the adapter's own USB interface, so without this
// boundary a claim on the adapter receives the input and hidraw nodes
// of every controller a person turns on. The peripherals reach the
// radio over the air, which is not a bus this walk can descend, and
// they connect and disconnect while a pod holds the adapter.
func isSubtreeBoundary(path string) bool {
	switch subsystemName(path) {
	case "pci", "usb", "bluetooth":
		return true
	}
	return false
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
