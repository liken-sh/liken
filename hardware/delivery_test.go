package hardware

// This file tests what claiming a device would hand over: the /dev
// nodes beneath it in sysfs. The walk stops at nested bus devices,
// which get their own inventory decision.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// child adds a nested, non-bus child directory under an existing
// device. It can optionally carry a device node: a dev file, the
// uevent DEVNAME that the kernel publishes, and a subsystem symlink
// that names the node's class.
func (f *fakeSysfs) child(bus, device, rel, subsystem, devname string) {
	f.t.Helper()
	dir := filepath.Join(f.root, "bus", bus, "devices", device, rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	if devname == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "dev"), []byte("8:0\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "uevent"), []byte("DEVNAME="+devname+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	target := filepath.Join(f.root, "class", subsystem)
	if err := os.MkdirAll(target, 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "subsystem")); err != nil {
		f.t.Fatal(err)
	}
}

func TestDeliveryFindsBlockNodesUnderAUSBInterface(t *testing.T) {
	sysfs := newFakeSysfs(t)
	sysfs.device("usb", "2-1:1.0", "usb-storage", map[string]string{
		"modalias": "usb:v46F4p0001d0100dc00dsc00dp00ic08isc06ip50in00",
	})
	sysfs.child("usb", "2-1:1.0", "host0/target0:0:0/0:0:0:0/block/sda", "block", "sda")
	sysfs.child("usb", "2-1:1.0", "host0/target0:0:0/0:0:0:0/block/sda/sda1", "block", "sda1")

	delivery := InspectDelivery(sysfs.root, Device{Bus: "usb", Address: "2-1:1.0"})

	if !slices.Equal(delivery.DevNodes, []string{"/dev/sda", "/dev/sda1"}) {
		t.Errorf("DevNodes = %v", delivery.DevNodes)
	}
	if !slices.Equal(delivery.Blocks, []string{"sda", "sda1"}) {
		t.Errorf("Blocks = %v", delivery.Blocks)
	}
}

func TestDeliveryFindsCharNodesAndReportsNoBlocks(t *testing.T) {
	sysfs := newFakeSysfs(t)
	sysfs.device("pci", "0000:00:02.0", "virtio-pci", map[string]string{
		"modalias": "pci:v00001AF4d00001050sv00001AF4sd00001100bc03sc80i00",
		"vendor":   "0x1af4",
		"device":   "0x1050",
	})
	sysfs.child("pci", "0000:00:02.0", "virtio0/drm/card0", "drm", "dri/card0")
	sysfs.child("pci", "0000:00:02.0", "virtio0/drm/renderD128", "drm", "dri/renderD128")

	delivery := InspectDelivery(sysfs.root, Device{Bus: "pci", Address: "0000:00:02.0"})

	if !slices.Equal(delivery.DevNodes, []string{"/dev/dri/card0", "/dev/dri/renderD128"}) {
		t.Errorf("DevNodes = %v", delivery.DevNodes)
	}
	if len(delivery.Blocks) != 0 {
		t.Errorf("Blocks = %v, want none for character devices", delivery.Blocks)
	}
}

func TestDeliveryIsEmptyForADeviceWithNoNodes(t *testing.T) {
	sysfs := newFakeSysfs(t)
	sysfs.device("pci", "0000:00:04.0", "virtio-pci", map[string]string{
		"modalias": "pci:v00001AF4d00001000sv00001AF4sd00000001bc02sc00i00",
	})
	sysfs.child("pci", "0000:00:04.0", "virtio1/net/eth0", "", "")

	delivery := InspectDelivery(sysfs.root, Device{Bus: "pci", Address: "0000:00:04.0"})

	if len(delivery.DevNodes) != 0 || len(delivery.Blocks) != 0 {
		t.Errorf("delivery = %+v, want empty: a NIC has nothing to hand a pod", delivery)
	}
}

func TestDeliveryPrunesAtNestedBusDevices(t *testing.T) {
	// The XHCI controller's subtree contains every USB device on the
	// bus, and each of those devices has its own /dev nodes. Those
	// nodes belong to the USB devices' own inventory decisions. The
	// controller itself delivers nothing.
	sysfs := newFakeSysfs(t)
	sysfs.device("pci", "0000:00:03.0", "xhci_hcd", map[string]string{
		"modalias": "pci:v00001B36d0000000Dsv00001AF4sd00001100bc0Csc03i30",
	})
	// The root hub is a usb bus device nested inside the controller.
	sysfs.child("pci", "0000:00:03.0", "usb2", "usb", "bus/usb/002/001")
	sysfs.child("pci", "0000:00:03.0", "usb2/2-1", "usb", "bus/usb/002/002")
	sysfs.child("pci", "0000:00:03.0", "usb2/2-1/2-1:1.0/host0/target0:0:0/0:0:0:0/block/sda", "block", "sda")

	delivery := InspectDelivery(sysfs.root, Device{Bus: "pci", Address: "0000:00:03.0"})

	if len(delivery.DevNodes) != 0 {
		t.Errorf("DevNodes = %v, want none: the stick's nodes are the stick's, not the controller's", delivery.DevNodes)
	}
}

func TestDeliveryToleratesAMissingDevice(t *testing.T) {
	delivery := InspectDelivery(t.TempDir(), Device{Bus: "usb", Address: "9-9"})
	if len(delivery.DevNodes) != 0 {
		t.Errorf("delivery = %+v, want empty", delivery)
	}
}
