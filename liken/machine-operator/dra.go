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
// reports can never disagree about what a device is. The walk runs
// on each pass, and a uevent for a device that arrives, leaves, or
// changes its driver wakes a pass (machineevents.go), so a device
// reaches the slice about a second after the kernel reports it.

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/liken-sh/liken/liken/hardware"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
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
// ResourceSlice. One slice per node is enough: a physical device
// publishes a primary and a few companions, four in the widest shape
// the fleet has met, so a busy server's dozens of physical devices
// stay far under the limit of 128. If a machine ever
// exceeds this limit, the operator drops the overflow and reports
// it, rather than splitting devices across slices. This will change
// if real hardware needs the multi-slice pool protocol.
const maxSliceDevices = 128

// publishDeviceInventory converges this node's ResourceSlice with
// what sysfs shows right now. The function logs failures and answers
// them, so the next pass that is not the ticker's walks again, instead
// of reporting them as a condition. Inventory is a report about hardware, and a failure to
// write it is a problem in the operator's own machinery, not a fact
// about the machine.
func publishDeviceInventory(r *reader, node *nodeObject, facts *machine.MachineStatus, serio []machine.SerioAttachment, mm *machineMetrics) error {
	held := heldNodes(draSysfsRoot)
	devices := inventoryDevices(
		hardware.DiscoverInventory(draSysfsRoot, draNaming()),
		func(d hardware.Device) hardware.Delivery {
			return withoutHeld(hardware.InspectDelivery(draSysfsRoot, d), held)
		},
		platformBlocks(facts), serio)
	if len(devices) > maxSliceDevices {
		fmt.Fprintf(os.Stderr, "device inventory: %d devices exceed one slice's capacity of %d; dropping the overflow\n",
			len(devices), maxSliceDevices)
		devices = devices[:maxSliceDevices]
	}
	// The device metric counts the very list the slice carries, so
	// the count comes from this pass's one sysfs walk and never from
	// a second one (metrics.go).
	mm.observeDevices(devices)

	owner := kubernetes.OwnerReference{
		APIVersion: "v1",
		Kind:       "Node",
		Name:       node.Metadata.Name,
		UID:        node.Metadata.UID,
	}
	current, err := r.resourceSlice(node.Metadata.Name)
	if err == nil {
		err = r.sliceWriter().Write(r.client, node.Metadata.Name, current, owner, devices)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "device inventory: %v\n", err)
	}
	return err
}

// serioInEffect is the spec.serio list the machine holds attachments
// for: the spec's entries, and the boot record's. A retracted entry
// stays in the boot record until the next boot, and its holder keeps
// the port until then, so its tty must stay withheld until then too.
// An entry added since the boot is in the spec before init reports it,
// so its tty is withheld from the first pass after the edit.
func serioInEffect(spec []machine.SerioAttachment, facts *machine.MachineStatus) []machine.SerioAttachment {
	if facts == nil {
		return slices.Clone(spec)
	}
	return slices.Concat(spec, facts.Boot.Serio)
}

// serioDeclared carries serioInEffect to the DRA plugin, which
// prepares claims on the kubelet's schedule, apart from the reconcile
// pass. main seeds it from the boot manifest and the boot's facts
// before the plugin serves, and every pass sets it again from the
// live spec, so the plugin never resolves a claim to a serial line
// that init holds.
var serioDeclared atomic.Pointer[[]machine.SerioAttachment]

func setDeclaredSerio(entries []machine.SerioAttachment) {
	serioDeclared.Store(&entries)
}

func declaredSerio() []machine.SerioAttachment {
	if entries := serioDeclared.Load(); entries != nil {
		return *entries
	}
	return nil
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

// inventoryDevices applies the publish rule to the devices
// hardware.DiscoverInventory finds: the pci and usb devices, and the
// devices on the board that own a node, such as a firmware TPM or a
// laptop's keyboard. A device is offered to workloads when it passes
// all three tests:
//
//  1. The device has a driver and is not part of the bus structure
//     itself. Undriven hardware belongs in the unclaimed report
//     instead. usbcore's device nodes, hubs, and PCIe ports are the
//     structure that the peripherals connect to, not peripherals
//     themselves. A USB device that no driver binds at all is the
//     exception: a program in userspace drives it, and the device
//     publishes whole (userspace.go).
//  2. Claiming the device would deliver something: its subtree
//     carries device nodes that a pod could receive. A NIC or a
//     bare controller fails this test, because it is real hardware
//     with nothing to hand to a pod. A Bluetooth adapter is the one
//     device that passes on its driver instead. hci is a socket
//     interface, so the radio registers no node, and a working
//     adapter has an empty subtree. This test alone would refuse
//     hardware that workloads do claim. publishing.go carries the
//     rest of that story.
//  3. The machine does not depend on the device: nothing in its
//     subtree backs a storage role. A claim on the system disk
//     would hand an unprivileged pod the machine's own root
//     filesystem, so the two claiming systems exclude each other. A
//     disk belongs either to the machine, as a storage role, or to
//     the workloads, through DRA, never both. The console and the
//     clock that init writes leave the delivery before this test,
//     so a device whose only nodes they are has nothing to deliver
//     (held.go).
//
// A serial line that init holds a serio attachment for passes the
// three tests with its tty node, and the policy then publishes the
// devices the attached driver created and never the tty
// (publishingserio.go). serio names those lines.
//
// A ResourceSlice is an offer, not a full record of the hardware.
// The scheduler can only allocate what a slice lists, so publishing
// the slice is itself the enforcement, ahead of whatever checks a
// deployment's DeviceClasses perform.
func inventoryDevices(discovered []hardware.Device,
	inspect func(hardware.Device) hardware.Delivery, platform map[string]bool,
	serio []machine.SerioAttachment) []kubernetes.SliceDevice {
	plumbing := map[string]bool{"usb": true, "hub": true, "pcieport": true}
	var out []kubernetes.SliceDevice
	for _, d := range discovered {
		if userspace, ok := userspaceDevice(d, discovered); ok {
			d = userspace
		} else if d.Driver == "" || plumbing[d.Driver] {
			continue
		}
		delivery := inspect(d)
		if len(delivery.DevNodes()) == 0 && !bluetoothAdapter(d) {
			continue
		}
		if slices.ContainsFunc(delivery.Blocks(), func(b string) bool { return platform[b] }) {
			continue
		}
		// One physical device can publish more than one slice
		// device: the loop publishes one slice device for each device
		// the policy derived from this delivery. Its suffix names the
		// slice device, joined to the physical device's own name, so
		// the primary keeps the bare name and an allocation made
		// before a split stays valid.
		for _, p := range publishFor(d, delivery, serio) {
			attrs := map[string]kubernetes.DeviceAttribute{}
			// Attribute names are unqualified, so the Kubernetes API
			// places them under the driver's own domain: a DeviceClass
			// selector reads them as device.attributes["liken.sh"].driver
			// and similar names. The code omits absent facts instead of
			// publishing them empty, so a selector like
			// `has(device.attributes["liken.sh"].serial)` means what it
			// says. Every published device carries its physical parent's
			// identifying attributes: the companion is the same silicon,
			// the same driver, and the same address as its primary.
			//
			// The address is also what pairs one physical device's
			// published devices back together. On a machine with two
			// GPUs, a claim that asks for a render node and a card node
			// constrains its two requests with matchAttribute, and
			// matchAttribute reads an attribute, never a name. The
			// address is equal across the devices of one card and
			// different across cards, so it is the fact that constraint
			// needs.
			for name, value := range map[string]string{
				"bus":       d.Bus,
				"address":   d.Address,
				"driver":    d.Driver,
				"class":     d.Class,
				"classCode": d.ClassCode,
				"subsystem": p.Subsystem,
				"name":      attributeString(d.Name),
				"modalias":  d.Modalias,
				"serial":    attributeString(d.Serial),
				"vendor":    d.Vendor,
				"product":   d.Product,
			} {
				if value != "" {
					attrs[name] = kubernetes.AttrString(value)
				}
			}
			// A render node is the fact a workload actually selects on. A
			// person who deploys a transcoder asks for any GPU that can
			// encode, and asking for a vendor and a product ID instead
			// names one machine's hardware in a document meant for a
			// fleet. A DeviceClass that selects on it can never allocate
			// the monitor buses by mistake.
			if p.RenderNode {
				attrs["renderNode"] = kubernetes.AttrBool(true)
			}
			// A display node is the other fact a workload selects on: the
			// card node that carries modesetting authority, which a
			// player or a kiosk needs and a transcoder never does. The two
			// facts are exclusive of each other, so a DeviceClass that
			// asks for one can never allocate the other by mistake.
			if p.DisplayNode {
				attrs["displayNode"] = kubernetes.AttrBool(true)
			}
			// The sound attribute states that a sound server can run
			// against this device. It is qualified where every other
			// attribute here is bare, because sound.liken.sh belongs to
			// no single driver: any driver may stamp the attribute, and
			// a DeviceClass that selects it names no driver, so a device
			// that supports a sound server joins that class by stamping
			// this one field. monitor.liken.sh/id takes the same form
			// for pairing a monitor's outputs.
			if p.Subsystem == "sound" {
				attrs["sound.liken.sh/supportsSound"] = kubernetes.AttrBool(true)
			}
			// The bare address pairs the devices of one card only
			// inside this driver, because a bare name belongs to the
			// driver that published it. Another driver's device for
			// the same card, such as media-operator's statement of
			// what a GPU decodes, pairs with these devices through
			// the attribute Kubernetes defines for a PCI device's
			// address. A sysfs PCI address is already the extended
			// BDF that the attribute's format names.
			if d.Bus == "pci" {
				attrs[pciBusIDAttribute] = kubernetes.AttrString(d.Address)
			}
			device := kubernetes.SliceDevice{
				Name:       deviceName(d) + p.Suffix,
				Attributes: attrs,
			}
			if p.Shareable {
				shared := true
				device.AllowMultipleAllocations = &shared
			}
			out = append(out, device)
		}
	}
	// The list is sorted, so the same hardware always publishes the
	// same slice. This lets the change detection in
	// WriteResourceSlice see actual inventory changes, and nothing
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
//
// A device on the board takes its name from the firmware, such as
// MSFT0101:00 for a TPM or acpi.video_bus.0 for the display's
// brightness keys, so every character a DNS label cannot hold
// becomes a dash, and the name stops at the label's 63 characters.
func deviceName(d hardware.Device) string {
	name := []byte(strings.ToLower(d.Bus + "-" + d.Address))
	for i, c := range name {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			name[i] = '-'
		}
	}
	return strings.Trim(string(name[:min(len(name), 63)]), "-")
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

// pciBusIDAttribute is the standard attribute that Kubernetes defines
// for the address of a PCI device, in the `resource.kubernetes.io`
// domain that belongs to no single driver. Its value is the extended
// BDF notation, Domain:Bus:Device.Function, so it identifies one
// device on a node. The reference is
// https://kubernetes.io/docs/reference/node/dra-standard-device-attributes/.
const pciBusIDAttribute = "resource.kubernetes.io/pciBusID"
