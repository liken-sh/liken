package hardware

// This file finds the hardware a workload can claim: the devices that
// DiscoverDevices finds on the pci and usb buses, and the devices on
// the board that no bus enumerates.
//
// The unclaimed report reads the pci and usb buses alone, because a
// missing module is a problem to fix only there. The inventory asks a
// different question: which hardware delivers a device node that a
// pod could receive. Some of that hardware is on no pci or usb bus.
// The firmware describes it, through ACPI or the legacy PnP tables,
// and the kernel registers it as a platform or pnp device. A firmware
// TPM, a CMOS clock, a laptop's own keyboard behind the i8042
// controller, the ACPI power and sleep buttons, and a serial port on
// the board are all of this kind. So the inventory starts from the
// nodes themselves, not from a list of buses. It reads every node the
// kernel registered and keeps the ones that no pci or usb device owns.
//
// Each such node belongs to the outermost device above it that has a
// driver. That device is the hardware: the TPM's node hangs under
// the platform device the TPM driver bound, and a keyboard's event
// node hangs under the input device, under the serio port, under the
// i8042 controller. The controller is the outermost driven device, so
// the controller is what a claim names, and the walk from it delivers
// every node beneath, the same way a pci device delivers the nodes
// beneath it.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// DiscoverInventory lists the devices that the inventory considers:
// every device DiscoverDevices finds, the USB devices themselves, and
// then each device outside the pci and usb buses that owns a device
// node. naming may be nil, the same as for DiscoverDevices.
func DiscoverInventory(sysRoot string, naming *PCIIDs) []Device {
	devices := DiscoverDevices(sysRoot, naming)
	devices = append(devices, usbDevices(sysRoot)...)
	return append(devices, boardDevices(sysRoot)...)
}

// usbDevices lists each USB device apart from its interfaces. The
// kernel gives a modalias to an interface and none to the device it
// belongs to, so DiscoverDevices, which reads a modalias as the mark
// of a device a module could drive, lists the interfaces alone. The
// inventory needs the device as well, because a device that no driver
// binds at all publishes whole, by the device's own port path.
func usbDevices(sysRoot string) []Device {
	dir := filepath.Join(sysRoot, "bus", "usb", "devices")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var devices []Device
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(dir, name)
		if strings.Contains(name, ":") || readAttr(path, "modalias") != "" {
			continue
		}
		devices = append(devices, Device{
			Bus:     "usb",
			Address: name,
			Driver:  boundDriver(path),
			Name:    usbName(dir, name),
			Vendor:  readAttr(path, "idVendor"),
			Product: readAttr(path, "idProduct"),
			Serial:  readAttr(path, "serial"),
		})
	}
	return devices
}

// InventoryEvent reports whether a uevent can change what
// DiscoverInventory and the delivery walks read. Every device outside
// /devices/virtual/ can, because the inventory starts from every node
// outside it. Under /devices/virtual/ the walks read only the misc
// class, where /dev/uhid and /dev/uinput appear when their modules
// load (MiscNode).
//
// The rest of /devices/virtual/ is the noise a node that runs
// Kubernetes makes. Each pod that starts or stops adds or removes a
// veth pair, and the pair and each of its queues announce themselves.
// The lab counted 132 such events in two minutes while seven pods
// started and four stopped, and nothing else under /devices/virtual/.
//
// A path outside /devices/ is a kernel object that is no device, and
// the walks read none. An NFS mount adds and removes RPC clients under
// /kernel/sunrpc/: a testbed machine that mounted NFS volumes counted
// 34 such events in 30 minutes. A module announces itself under
// /module/ when it loads, and the devices it creates send events of
// their own.
func InventoryEvent(event Uevent) bool {
	if !strings.HasPrefix(event.DevPath, "/devices/") {
		return false
	}
	if !strings.HasPrefix(event.DevPath, "/devices/virtual/") {
		return true
	}
	return strings.HasPrefix(event.DevPath, "/devices/virtual/misc/")
}

// enumeratedBuses are the buses DiscoverDevices walks. A node under a
// device of either bus already belongs to that device's delivery.
var enumeratedBuses = map[string]bool{"pci": true, "usb": true}

// boardDevices finds the outermost driven device above each node
// that no pci or usb device owns. /sys/dev/char and /sys/dev/block
// hold one link for every node the kernel registered, and each link
// resolves to the node's directory in the device tree.
func boardDevices(sysRoot string) []Device {
	// The links resolve to real paths, so the root they are compared
	// against must be real as well.
	devicesRoot, err := filepath.EvalSymlinks(filepath.Join(sysRoot, "devices"))
	if err != nil {
		return nil
	}
	owners := map[string]Device{}
	for _, kind := range []string{"char", "block"} {
		dir := filepath.Join(sysRoot, "dev", kind)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			node, err := filepath.EvalSymlinks(filepath.Join(dir, entry.Name()))
			if err != nil {
				continue
			}
			owner, ok := boardOwner(devicesRoot, node)
			if !ok {
				continue
			}
			if _, seen := owners[owner]; seen {
				continue
			}
			bus := subsystemName(owner)
			address := filepath.Base(owner)
			// InspectDelivery finds a device through its bus's
			// devices directory, so an owner that its bus does not
			// list is one the delivery walk could not reach.
			if _, err := os.Stat(filepath.Join(sysRoot, "bus", bus, "devices", address)); err != nil {
				continue
			}
			owners[owner] = Device{
				Bus:      bus,
				Address:  address,
				Modalias: readAttr(owner, "modalias"),
				Driver:   boundDriver(owner),
			}
		}
	}
	devices := make([]Device, 0, len(owners))
	for _, d := range owners {
		devices = append(devices, d)
	}
	slices.SortFunc(devices, func(a, b Device) int {
		return strings.Compare(a.Bus+"/"+a.Address, b.Bus+"/"+b.Address)
	})
	return devices
}

// boardOwner answers the device that owns one node's directory: the
// outermost directory between the device tree's root and the node
// that has a driver. A node under /devices/virtual/ has no hardware
// behind it, and a node under a pci or usb device belongs to that
// device, so neither has a board owner. A node with no driven device
// above it has none either, because no driver serves it.
func boardOwner(devicesRoot, node string) (string, bool) {
	rel, err := filepath.Rel(devicesRoot, node)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if parts[0] == "virtual" {
		return "", false
	}
	owner := ""
	dir := devicesRoot
	for _, part := range parts {
		dir = filepath.Join(dir, part)
		if enumeratedBuses[subsystemName(dir)] {
			return "", false
		}
		if owner == "" && boundDriver(dir) != "" {
			owner = dir
		}
	}
	return owner, owner != ""
}
