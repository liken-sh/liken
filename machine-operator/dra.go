package main

// The DRA driver's inventory half: publishing this machine's driven
// devices as a ResourceSlice.
//
// liken's answer to "how do workloads reach hardware" is dynamic
// resource allocation, and the driver is this operator — one more
// job in a process already standing on every node, not a second
// daemon (the memory envelope has no room for one). The division of
// the milestone's two halves runs through the driver symlink: a
// device nothing drives is init's unclaimed report in Machine
// status, aimed at the person who can declare a module; a device
// with a driver is working equipment, and working equipment belongs
// in the cluster's own inventory API where DeviceClasses can select
// it and pods can claim it. spec.modules is the gate between the
// two — declaring a driver is what moves a device from one report
// to the other.
//
// The operator walks sysfs itself rather than riding init's facts,
// deliberately. The facts channel carries what init observes for
// the Machine status, and inventory is not status: it is a separate
// report to a separate audience with a separate lifetime (slices
// die with the Node, status lives with the Machine). The walk is
// the same shared package init uses, so the two reports can never
// disagree about what a device is, and at one walk per ten-second
// pass it costs what init's uevent-triggered walks cost: nothing
// worth engineering away.

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/liken-sh/liken/hardware"
	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

// The operator reads the host's sysfs directly (this pod runs in the
// host's namespaces) and names PCI devices from the database the
// image ships. Variables so tests can substitute both, the same seam
// init's hardware watcher leaves.
var (
	draSysfsRoot  = "/sys"
	draPCIIDsPath = "/usr/share/hwdata/pci.ids"
)

// draNaming loads the PCI naming database once per process: the file
// is part of the image, so it cannot change under a running operator.
// A missing database degrades naming, never inventory.
var draNaming = sync.OnceValue(func() *hardware.PCIIDs {
	naming, err := hardware.LoadPCIIDs(draPCIIDsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "device inventory: no PCI naming database: %v\n", err)
		return nil
	}
	return naming
})

// maxSliceDevices is the API's ceiling on devices in one
// ResourceSlice. One slice per node is comfortably enough for whole
// PCI and USB devices (a busy server has dozens, the limit is 128);
// if a machine ever exceeds it, the overflow is dropped loudly
// rather than split across slices, until real hardware demands the
// multi-slice pool protocol.
const maxSliceDevices = 128

// publishDeviceInventory converges this node's ResourceSlice with
// what sysfs shows right now. Failures are logged and retried by the
// next pass rather than surfaced as a condition: inventory is a
// report about hardware, and a hiccup writing it is operator
// plumbing, not a fact about the machine.
func publishDeviceInventory(c *kubernetes.Client, node *nodeObject, facts *machine.MachineStatus) {
	devices := inventoryDevices(
		hardware.DiscoverDevices(draSysfsRoot, draNaming()),
		func(d hardware.Device) hardware.Delivery {
			return hardware.InspectDelivery(draSysfsRoot, d)
		},
		platformBlocks(facts))
	if len(devices) > maxSliceDevices {
		fmt.Fprintf(os.Stderr, "device inventory: %d devices exceed one slice's capacity of %d; dropping the overflow\n",
			len(devices), maxSliceDevices)
		devices = devices[:maxSliceDevices]
	}
	owner := kubernetes.OwnerReference{
		APIVersion: "v1",
		Kind:       "Node",
		Name:       node.Metadata.Name,
		UID:        node.Metadata.UID,
	}
	if err := kubernetes.EnsureResourceSlice(c, node.Metadata.Name, owner, devices); err != nil {
		fmt.Fprintf(os.Stderr, "device inventory: %v\n", err)
	}
}

// platformBlocks is the set of block devices the machine stands on:
// every partition a storage role is backed by, straight from the
// facts. The system slots, the boot path, and the state and pod
// filesystems are all storage roles, so this one set covers
// everything whose loss would take the machine down.
func platformBlocks(facts *machine.MachineStatus) map[string]bool {
	blocks := map[string]bool{}
	if facts == nil {
		return blocks
	}
	for _, name := range machine.StorageRoleNames {
		role := facts.Storage.Role(name)
		if role != nil && role.Device != "" {
			blocks[role.Device] = true
		}
	}
	return blocks
}

// inventoryDevices applies the publish rule. A device is offered to
// workloads when all three tests pass:
//
//  1. It is driven and not bus plumbing. Undriven hardware is the
//     unclaimed report's story; usbcore's device nodes, hubs, and
//     PCIe ports are the fabric the peripherals hang from.
//  2. Claiming it would deliver something: its subtree carries
//     device nodes a pod could be handed. A NIC or a bare
//     controller fails here — real hardware, nothing to inject.
//  3. The machine does not stand on it: nothing in its subtree
//     backs a storage role. A claim on the system disk would hand
//     an unprivileged pod the machine's own root, so the two
//     claiming systems are made mutually exclusive — a disk is
//     either the machine's (a storage role) or the workloads'
//     (DRA), never both.
//
// A ResourceSlice is the offer, not a census: the scheduler can
// only allocate what a slice lists, which makes publication itself
// the enforcement, ahead of whatever care a deployment's
// DeviceClasses take.
func inventoryDevices(discovered []hardware.Device,
	inspect func(hardware.Device) hardware.Delivery, platform map[string]bool) []kubernetes.SliceDevice {
	plumbing := map[string]bool{"usb": true, "hub": true, "pcieport": true}
	var out []kubernetes.SliceDevice
	for _, d := range discovered {
		if d.Driver == "" || plumbing[d.Driver] {
			continue
		}
		delivery := inspect(d)
		if len(delivery.DevNodes) == 0 {
			continue
		}
		if slices.ContainsFunc(delivery.Blocks, func(b string) bool { return platform[b] }) {
			continue
		}
		attrs := map[string]kubernetes.DeviceAttribute{}
		// Attribute names are unqualified, so they live under the
		// driver's own domain: a DeviceClass selector reads them as
		// device.attributes["liken.sh"].driver and friends. Absent
		// facts are omitted, not published empty, so a selector like
		// `has(device.attributes["liken.sh"].serial)` means what it
		// says.
		for name, value := range map[string]string{
			"bus":      d.Bus,
			"driver":   d.Driver,
			"class":    d.Class,
			"name":     attributeString(d.Name),
			"modalias": d.Modalias,
			"serial":   attributeString(d.Serial),
			"vendor":   d.Vendor,
			"product":  d.Product,
		} {
			if value != "" {
				attrs[name] = kubernetes.AttrString(value)
			}
		}
		out = append(out, kubernetes.SliceDevice{
			Name:       deviceName(d),
			Attributes: attrs,
		})
	}
	// Sorted, so the same hardware always publishes the same slice
	// and the change detection in EnsureResourceSlice sees inventory
	// changes, never walk-order noise.
	slices.SortFunc(out, func(a, b kubernetes.SliceDevice) int {
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// deviceName turns a sysfs address into the DNS label the API
// demands: bus-prefixed (addresses are only unique within a bus),
// lowercased, with the address punctuation (PCI's colons and dots,
// USB's dots and colons) dashed. The address is the right identity
// for a slice device because it names the slot, not the unit:
// replacing a failed dongle in the same port yields the same device
// name, which is exactly how a claim against "the UPS on this wall"
// should behave. Unit identity, when the hardware carries one, is
// the serial attribute's job.
func deviceName(d hardware.Device) string {
	sanitized := strings.ToLower(d.Address)
	for _, r := range []string{":", "."} {
		sanitized = strings.ReplaceAll(sanitized, r, "-")
	}
	return d.Bus + "-" + sanitized
}

// attributeString bounds a free-text value to the API's 64-character
// limit on attribute strings. Identifiers (addresses, hex IDs,
// modaliases on these buses) fit by construction; only the
// human-readable names the hardware or pci.ids provides can run
// long, and a truncated name still names.
func attributeString(s string) string {
	if len(s) <= 64 {
		return s
	}
	return s[:64]
}
