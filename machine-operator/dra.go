package main

// The DRA driver's inventory half: publishing this machine's driven
// devices as a ResourceSlice.
//
// liken's answer to the question of how workloads reach hardware is
// dynamic resource allocation, and the driver is this operator: one
// more job in a process that already runs on every node, not a
// second daemon, because the memory envelope has no room for one.
// The driver symlink divides the milestone's two halves. A device
// with no driver is init's unclaimed report in Machine status, aimed
// at the person who can declare a module. A device with a driver is
// working equipment, and working equipment belongs in the cluster's
// own inventory API, where DeviceClasses can select it and pods can
// claim it. spec.modules is the gate between the two: declaring a
// driver is what moves a device from one report to the other.
//
// The operator walks sysfs itself, rather than reading init's
// facts. This is deliberate. The facts tree carries what init
// observes for the Machine status, and inventory is not status: it
// is a separate report, to a separate audience, with a separate
// lifetime. Slices end with the Node; status lives with the Machine.
// The walk uses the same shared package init uses, so the two
// reports can never disagree about what a device is. At one walk
// per ten-second pass, this costs the same as init's
// uevent-triggered walks: a cost too small to engineer away.

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

// The operator reads the host's sysfs directly, because this pod
// runs in the host's namespaces, and names PCI devices from the
// database the image ships. These are variables so tests can
// substitute both, the same seam init's hardware watcher leaves.
var (
	draSysfsRoot  = "/sys"
	draPCIIDsPath = "/usr/share/hwdata/pci.ids"
)

// draNaming loads the PCI naming database once per process. The
// file is part of the image, so it cannot change while the operator
// runs. A missing database degrades the device names, but never the
// inventory itself.
var draNaming = sync.OnceValue(func() *hardware.PCIIDs {
	naming, err := hardware.LoadPCIIDs(draPCIIDsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "device inventory: no PCI naming database: %v\n", err)
		return nil
	}
	return naming
})

// maxSliceDevices is the API's limit on devices in one
// ResourceSlice. One slice per node is enough for whole PCI and USB
// devices: a busy server has dozens of devices, and the limit is
// 128. If a machine ever exceeds this limit, the operator drops the
// overflow and reports it loudly, rather than splitting devices
// across slices. This will change if real hardware needs the
// multi-slice pool protocol.
const maxSliceDevices = 128

// publishDeviceInventory converges this node's ResourceSlice with
// what sysfs shows right now. The function logs failures and lets
// the next pass retry them, instead of reporting them as a
// condition. Inventory is a report about hardware, and a failure to
// write it is a problem in the operator's own machinery, not a fact
// about the machine.
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

// platformBlocks returns the block devices the machine depends on:
// every partition that backs a storage role, read straight from the
// facts. The system slots, the boot path, and the state and pod
// filesystems are all storage roles, so this one set covers
// everything the machine cannot lose without failing.
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
// workloads when it passes all three tests:
//
//  1. The device has a driver and is not part of the bus structure
//     itself. Undriven hardware belongs in the unclaimed report
//     instead. usbcore's device nodes, hubs, and PCIe ports are the
//     structure that the peripherals connect to, not peripherals
//     themselves.
//  2. Claiming the device would deliver something: its subtree
//     carries device nodes that a pod could receive. A NIC or a
//     bare controller fails this test, because it is real hardware
//     with nothing to hand to a pod.
//  3. The machine does not depend on the device: nothing in its
//     subtree backs a storage role. A claim on the system disk
//     would hand an unprivileged pod the machine's own root
//     filesystem, so the two claiming systems exclude each other. A
//     disk belongs either to the machine, as a storage role, or to
//     the workloads, through DRA, never both.
//
// A ResourceSlice is an offer, not a full record of the hardware.
// The scheduler can only allocate what a slice lists, so publishing
// the slice is itself the enforcement, ahead of whatever checks a
// deployment's DeviceClasses perform.
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
		// Attribute names are unqualified, so the Kubernetes API
		// places them under the driver's own domain: a DeviceClass
		// selector reads them as device.attributes["liken.sh"].driver
		// and similar names. The code omits absent facts instead of
		// publishing them empty, so a selector like
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
	// The list is sorted, so the same hardware always publishes the
	// same slice. This lets the change detection in
	// EnsureResourceSlice see actual inventory changes, and nothing
	// caused only by the order of the walk.
	slices.SortFunc(out, func(a, b kubernetes.SliceDevice) int {
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// deviceName turns a sysfs address into the DNS label the API
// requires. The function prefixes the label with the bus, because
// addresses are unique only within a bus, lowercases it, and
// replaces the address punctuation (PCI's colons and dots, USB's
// dots and colons) with dashes. The address is the right identity
// for a slice device, because it names the slot, not the individual
// unit. Replacing a failed dongle in the same port produces the
// same device name, which is the behavior a claim against "the UPS
// on this wall" needs. When the hardware carries a serial number,
// the serial attribute is what identifies the individual unit.
func deviceName(d hardware.Device) string {
	sanitized := strings.ToLower(d.Address)
	for _, r := range []string{":", "."} {
		sanitized = strings.ReplaceAll(sanitized, r, "-")
	}
	return d.Bus + "-" + sanitized
}

// attributeString limits a free-text value to the API's
// 64-character limit on attribute strings. Identifiers, such as
// addresses, hex IDs, and modaliases on these buses, always fit
// within this limit. Only the human-readable names that the
// hardware or pci.ids provides can run longer, and a truncated name
// still identifies the device.
func attributeString(s string) string {
	if len(s) <= 64 {
		return s
	}
	return s[:64]
}
