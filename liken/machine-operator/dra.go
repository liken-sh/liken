package main

// The dynamic resource allocation (DRA) inventory: the ResourceSlice
// that lists this machine's devices that have a driver.
//
// This operator is the DRA driver, because the nodes have no memory
// to spare for a second daemon. A device with no driver goes in
// status.hardware.unclaimed of the Machine, so a person can add its
// module to spec.modules. When the driver binds, the device moves to
// the ResourceSlice, where a DeviceClass can select it and a pod can
// claim it.
//
// The operator walks sysfs on each pass instead of reading the facts
// that init writes, and a uevent for a device that arrives, leaves,
// or changes its driver starts a pass (machineevents.go). init's walk
// and this one use the same hardware package, so the Machine status
// and the ResourceSlice agree on what a device is.

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

// Tests point these at fixtures.
var (
	draSysfsRoot  = "/sys"
	draPCIIDsPath = "/usr/share/hwdata/pci.ids"
)

// draNaming loads the PCI name database once. When the database is
// missing, the devices have no name attribute and are still published.
var draNaming = sync.OnceValue(func() *hardware.PCIIDs {
	naming, err := hardware.LoadPCIIDs(draPCIIDsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "device inventory: no PCI naming database: %v\n", err)
		return nil
	}
	return naming
})

// maxSliceDevices is the API's limit on devices in one ResourceSlice.
// The operator writes one slice per node. When a machine has more
// devices, it logs the count and drops the devices past the limit,
// because the slice writer writes each pool as a single slice.
const maxSliceDevices = 128

// publishDeviceInventory writes this node's ResourceSlice from a walk
// of sysfs. A failure is logged and returned, so the pass retries. It
// sets no condition, because a failed write is a fault of the operator
// and not a fact about the machine.
func publishDeviceInventory(r *reader, node *nodeObject, facts *machine.MachineStatus, serio []machine.SerioAttachment, mm *machineMetrics) error {
	reserved := reservedNodes(draSysfsRoot)
	devices := inventoryDevices(
		hardware.DiscoverInventory(draSysfsRoot, draNaming()),
		func(d hardware.Device) hardware.Delivery {
			return withoutReserved(hardware.InspectDelivery(draSysfsRoot, d), reserved)
		},
		protectionOf(facts), serio)
	devices = withVirtualization(devices, virtualizationSliceDevices(draSysfsRoot, sysctlRoot))
	if len(devices) > maxSliceDevices {
		fmt.Fprintf(os.Stderr, "device inventory: %d devices exceed one slice's capacity of %d; dropping the overflow\n",
			len(devices), maxSliceDevices)
		devices = devices[:maxSliceDevices]
	}
	// The metric counts before taintUnplugged adds the devices that are
	// gone, so it counts only hardware that is present.
	mm.observeDevices(devices)
	// A device that was unplugged while a prepared claim held it stays
	// in the slice with a taint that evicts the claim's pods.
	devices = taintUnplugged(devices, unpluggedDevices())

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
		fmt.Fprintf(os.Stderr, "device inventory: writing the ResourceSlice: %v\n", err)
	}
	return err
}

// serioInEffect returns the serio entries whose serial line the slice
// must not offer: those in spec.serio and those that init attached at
// boot. An entry removed from the spec keeps its line attached until
// the next boot. An entry added to the spec withholds its line from
// the first pass after the edit, before the line is attached.
func serioInEffect(spec []machine.SerioAttachment, facts *machine.MachineStatus) []machine.SerioAttachment {
	if facts == nil {
		return slices.Clone(spec)
	}
	return slices.Concat(spec, facts.Boot.Serio)
}

// serioDeclared is the result of serioInEffect for the DRA plugin,
// which prepares claims when the kubelet calls it, outside the
// reconcile pass. main sets it at startup, and each pass sets it from
// the Machine's spec, so the plugin never resolves a claim to a serial
// line that a serio driver is attached to.
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

// inventoryDevices returns the slice devices for the PCI, USB, and
// board devices that hardware.DiscoverInventory finds. The scheduler
// allocates only what the slice lists, so this filter decides what a
// DeviceClass can claim. A device is published when all of these are
// true:
//
//  1. It has a driver other than `usb`, `hub`, or `pcieport`. `usb`
//     binds each USB device itself, and the drivers of its interfaces
//     are what a pod uses. When no kernel driver binds any interface
//     of a USB device, the device is published whole, because a
//     program in userspace drives it (userspace.go).
//  2. It has a device node that a pod can receive. A NIC has none. A
//     Bluetooth adapter is published by its driver alone, because hci
//     is a socket interface and the adapter has no node.
//  3. None of its nodes is a block device that backs a storage role.
//     When the facts cannot be read, no device with a block node is
//     published (protection.go). inspect removes the console and the
//     clock that init uses, so a device with only those nodes fails
//     test 2 (reserved.go).
//
// For a serial line with a serio attachment, the slice gets the
// devices that the serio driver created, and never the tty
// (publishingserio.go).
func inventoryDevices(discovered []hardware.Device,
	inspect func(hardware.Device) hardware.Delivery, platform protection,
	serio []machine.SerioAttachment) []kubernetes.SliceDevice {
	busDrivers := map[string]bool{"usb": true, "hub": true, "pcieport": true}
	var out []kubernetes.SliceDevice
	for _, d := range discovered {
		if userspace, ok := userspaceDevice(d, discovered); ok {
			d = userspace
		} else if d.Driver == "" || busDrivers[d.Driver] {
			continue
		}
		delivery := inspect(d)
		if len(delivery.DevNodes()) == 0 && !bluetoothAdapter(d) {
			continue
		}
		if platform.withholds(delivery) {
			continue
		}
		// One physical device can become several slice devices. The
		// first keeps the device's own name and each other one adds a
		// suffix, so an allocation made by the device's name stays valid.
		for _, p := range publishFor(d, delivery, serio) {
			attrs := map[string]kubernetes.DeviceAttribute{}
			// An unqualified attribute name is in the driver's domain,
			// so a DeviceClass reads it as
			// device.attributes["liken.sh"].driver. An empty value is
			// left out, so `has(device.attributes["liken.sh"].serial)`
			// tests for a serial.
			//
			// Every slice device of one physical device has its address.
			// On a machine with two GPUs, a claim that requests a render
			// node and a card node puts matchAttribute on the address,
			// so the scheduler allocates both from one card.
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
			// firmware-operator builds a USB device's usbfs path from
			// these, and compares the device number to find out that a
			// device enumerated again. A claim must not select on
			// either, because both change at each enumeration.
			if d.BusNumber > 0 && d.DeviceNumber > 0 {
				attrs["busNumber"] = kubernetes.AttrInt(int64(d.BusNumber))
				attrs["deviceNumber"] = kubernetes.AttrInt(int64(d.DeviceNumber))
			}
			// A transcoder's DeviceClass selects renderNode, so it
			// matches any GPU without vendor and product IDs, and never
			// matches a GPU's i2c device.
			if p.RenderNode {
				attrs["renderNode"] = kubernetes.AttrBool(true)
			}
			// A player or a kiosk selects displayNode, because it needs
			// the card node to set display modes and a transcoder does
			// not. No device has both renderNode and displayNode, so a
			// class that selects one never allocates the other.
			if p.DisplayNode {
				attrs["displayNode"] = kubernetes.AttrBool(true)
			}
			// This attribute is in the sound.liken.sh domain and not the
			// driver's, because other drivers set it too. A DeviceClass
			// that selects it matches every device that can run a sound
			// server, whichever driver published it.
			if p.Subsystem == "sound" {
				attrs["sound.liken.sh/supportsSound"] = kubernetes.AttrBool(true)
			}
			// address is in this driver's domain, so matchAttribute on
			// it pairs only this driver's devices. Another driver, such
			// as media-operator with its devices for what a GPU decodes,
			// pairs with these on the standard PCI attribute. sysfs
			// writes a PCI address in the same extended BDF format.
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
	// The sort makes the same hardware produce the same slice, so the
	// writer finds a change only when the devices change.
	slices.SortFunc(out, func(a, b kubernetes.SliceDevice) int {
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

func withVirtualization(devices, virtual []kubernetes.SliceDevice) []kubernetes.SliceDevice {
	out := append(slices.Clone(devices), virtual...)
	slices.SortFunc(out, func(a, b kubernetes.SliceDevice) int {
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// deviceName turns a bus and a sysfs address into the DNS label that
// the API requires for a device name. The bus is in the name because
// an address is unique only within its bus. A board device's address
// comes from the firmware, such as MSFT0101:00 for a TPM.
//
// A USB address names the port and not the unit, so a replacement
// dongle in the same port gets the same name, and a claim for the UPS
// on that port still matches. The serial attribute identifies the
// unit.
func deviceName(d hardware.Device) string {
	name := []byte(strings.ToLower(d.Bus + "-" + d.Address))
	for i, c := range name {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			name[i] = '-'
		}
	}
	return strings.Trim(string(name[:min(len(name), 63)]), "-")
}

// attributeString cuts a value to the API's limit of 64 characters
// for an attribute string. The names from the hardware and from
// pci.ids can be longer.
func attributeString(s string) string {
	if len(s) <= 64 {
		return s
	}
	return s[:64]
}

// pciBusIDAttribute is the standard Kubernetes attribute for a PCI
// address, in the extended BDF format Domain:Bus:Device.Function. See
// https://kubernetes.io/docs/reference/node/dra-standard-device-attributes/.
const pciBusIDAttribute = "resource.kubernetes.io/pciBusID"
