package hardware

// This file tests what claiming a device would hand over: the /dev
// nodes beneath it in sysfs. The walk stops at nested bus devices,
// which get their own inventory decision, and at bluetooth devices,
// whose nodes belong to the peripherals connected over the air.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// child adds a nested child directory under an existing device. The
// subsystem names the kernel class the directory belongs to, and an
// empty subsystem builds a plain container directory, the shape sysfs
// gives the grouping directories between real devices. The devname
// adds a device node: a dev file and the uevent DEVNAME that the
// kernel publishes. A directory with a subsystem and no devname is a
// device the kernel registers no node for, such as an hci object.
func (f *fakeSysfs) child(bus, device, rel, subsystem, devname string) {
	f.t.Helper()
	dir := filepath.Join(f.root, "bus", bus, "devices", device, rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	if subsystem != "" {
		target := filepath.Join(f.root, "class", subsystem)
		if err := os.MkdirAll(target, 0o755); err != nil {
			f.t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "subsystem")); err != nil {
			f.t.Fatal(err)
		}
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
}

func TestDeliveryFindsBlockNodesUnderAUSBInterface(t *testing.T) {
	sysfs := newFakeSysfs(t)
	sysfs.device("usb", "2-1:1.0", "usb-storage", map[string]string{
		"modalias": "usb:v46F4p0001d0100dc00dsc00dp00ic08isc06ip50in00",
	})
	sysfs.child("usb", "2-1:1.0", "host0/target0:0:0/0:0:0:0/block/sda", "block", "sda")
	sysfs.child("usb", "2-1:1.0", "host0/target0:0:0/0:0:0:0/block/sda/sda1", "block", "sda1")

	delivery := InspectDelivery(sysfs.root, Device{Bus: "usb", Address: "2-1:1.0"})

	if !slices.Equal(delivery.DevNodes(), []string{"/dev/sda", "/dev/sda1"}) {
		t.Errorf("DevNodes = %v", delivery.DevNodes())
	}
	if !slices.Equal(delivery.Blocks(), []string{"sda", "sda1"}) {
		t.Errorf("Blocks = %v", delivery.Blocks())
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

	if !slices.Equal(delivery.DevNodes(), []string{"/dev/dri/card0", "/dev/dri/renderD128"}) {
		t.Errorf("DevNodes = %v", delivery.DevNodes())
	}
	if len(delivery.Blocks()) != 0 {
		t.Errorf("Blocks = %v, want none for character devices", delivery.Blocks())
	}
	if !slices.Equal(delivery.Subsystems(), []string{"drm"}) {
		t.Errorf("Subsystems = %v, want the one kind these nodes are", delivery.Subsystems())
	}
}

func TestDeliveryReportsEachSubsystemOnce(t *testing.T) {
	// A device can deliver nodes of more than one kind. The list names
	// each kind once, sorted, so the same hardware always reports the
	// same list.
	sysfs := newFakeSysfs(t)
	sysfs.device("usb", "3-1:1.0", "cdc_acm", map[string]string{
		"modalias": "usb:v10C4p0001d0100dc00dsc00dp00ic02isc02ip01in00",
	})
	sysfs.child("usb", "3-1:1.0", "tty/ttyACM0", "tty", "ttyACM0")
	sysfs.child("usb", "3-1:1.0", "usbmisc/cdc-wdm0", "usbmisc", "cdc-wdm0")
	sysfs.child("usb", "3-1:1.0", "tty/ttyACM1", "tty", "ttyACM1")

	delivery := InspectDelivery(sysfs.root, Device{Bus: "usb", Address: "3-1:1.0"})

	if !slices.Equal(delivery.Subsystems(), []string{"tty", "usbmisc"}) {
		t.Errorf("Subsystems = %v", delivery.Subsystems())
	}
}

func TestDeliveryNamesTheUsbfsNodeAboveAnInterface(t *testing.T) {
	// usbhid binds the UPS's HID interface and registers hidraw and
	// hiddev nodes for it. Neither node carries raw USB transfers, so a
	// libusb program in the pod opens the usbfs node of the device the
	// interface belongs to. The walk never reaches that node, because
	// it is above the interface, not below it.
	sysfs := newFakeSysfs(t)
	sysfs.device("usb", "3-3", "usb", map[string]string{
		"modalias": "usb:v0764p0601d0100dc00dsc00dp00ic00isc00ip00in00",
		"busnum":   "3",
		"devnum":   "4",
	})
	sysfs.device("usb", "3-3:1.0", "usbhid", map[string]string{
		"modalias": "usb:v0764p0601d0100dc00dsc00dp00ic03isc00ip00in00",
	})
	sysfs.child("usb", "3-3:1.0", "0003:0764:0601.0001/hidraw/hidraw0", "hidraw", "hidraw0")
	sysfs.child("usb", "3-3:1.0", "usbmisc/hiddev0", "usbmisc", "usb/hiddev0")

	delivery := InspectDelivery(sysfs.root, Device{Bus: "usb", Address: "3-3:1.0"})

	if delivery.BusNode != "/dev/bus/usb/003/004" {
		t.Errorf("BusNode = %q, want the usbfs node with three digits in each number", delivery.BusNode)
	}
	if !slices.Equal(delivery.DevNodes(), []string{"/dev/hidraw0", "/dev/usb/hiddev0"}) {
		t.Errorf("DevNodes = %v, want the driver's own nodes as well", delivery.DevNodes())
	}
}

func TestDeliveryFollowsADeviceThatEnumeratedAgain(t *testing.T) {
	// The bus assigns a device number at each enumeration, so the same
	// hardware in the same port gets a different usbfs node after a
	// replug. Every walk reads the number again.
	sysfs := newFakeSysfs(t)
	sysfs.device("usb", "3-3", "usb", map[string]string{
		"modalias": "usb:v0764p0601d0100dc00dsc00dp00ic00isc00ip00in00",
		"busnum":   "3",
		"devnum":   "4",
	})
	sysfs.device("usb", "3-3:1.0", "usbhid", map[string]string{
		"modalias": "usb:v0764p0601d0100dc00dsc00dp00ic03isc00ip00in00",
	})
	sysfs.child("usb", "3-3:1.0", "0003:0764:0601.0001/hidraw/hidraw0", "hidraw", "hidraw0")
	devnum := filepath.Join(sysfs.root, "bus", "usb", "devices", "3-3", "devnum")
	if err := os.WriteFile(devnum, []byte("17\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	delivery := InspectDelivery(sysfs.root, Device{Bus: "usb", Address: "3-3:1.0"})

	if delivery.BusNode != "/dev/bus/usb/003/017" {
		t.Errorf("BusNode = %q, want the number this enumeration assigned", delivery.BusNode)
	}
}

func TestDeliveryNamesNoUsbfsNodeOffTheUSBBus(t *testing.T) {
	// usbfs is a USB facility. A PCI function has no equivalent node,
	// and a claim on one delivers its subtree and nothing else.
	sysfs := newFakeSysfs(t)
	sysfs.device("pci", "0000:00:02.0", "i915", map[string]string{
		"modalias": "pci:v00008086d000046D2sv00000301sd000002F3bc03sc00i00",
	})
	sysfs.child("pci", "0000:00:02.0", "drm/card0", "drm", "dri/card0")

	delivery := InspectDelivery(sysfs.root, Device{Bus: "pci", Address: "0000:00:02.0"})

	if delivery.BusNode != "" {
		t.Errorf("BusNode = %q, want none", delivery.BusNode)
	}
}

func TestDeliveryIsEmptyForADeviceWithNoNodes(t *testing.T) {
	sysfs := newFakeSysfs(t)
	sysfs.device("pci", "0000:00:04.0", "virtio-pci", map[string]string{
		"modalias": "pci:v00001AF4d00001000sv00001AF4sd00000001bc02sc00i00",
	})
	sysfs.child("pci", "0000:00:04.0", "virtio1/net/eth0", "", "")

	delivery := InspectDelivery(sysfs.root, Device{Bus: "pci", Address: "0000:00:04.0"})

	if len(delivery.DevNodes()) != 0 || len(delivery.Blocks()) != 0 {
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

	if len(delivery.DevNodes()) != 0 {
		t.Errorf("DevNodes = %v, want none: the stick's nodes are the stick's, not the controller's", delivery.DevNodes())
	}
}

func TestDeliveryPrunesAtABluetoothSubtree(t *testing.T) {
	// A game controller that connects over Bluetooth gets its HID
	// device under the adapter's own USB interface, and that HID device
	// carries the input and hidraw nodes the controller is driven by.
	// The nodes belong to the controller, so a claim on the adapter
	// must not receive them, and a second controller must not add nodes
	// to the same claim.
	//
	// The nesting also depends on how BlueZ drives the kernel. With
	// /dev/uhid present, BlueZ 5.73 and later put the same HID device
	// under /sys/devices/virtual/misc/uhid, where nothing connects it
	// to the adapter at all. A claim that delivered these nodes would
	// deliver them only under one of the two arrangements.
	sysfs := newFakeSysfs(t)
	sysfs.device("usb", "1-8", "usb", map[string]string{
		"modalias": "usb:v8087p0033d0001dcE0dsc01dp01ic00isc00ip00in00",
		"busnum":   "1",
		"devnum":   "8",
	})
	sysfs.device("usb", "1-8:1.0", "btusb", map[string]string{
		"modalias": "usb:v8087p0033d0001dcE0dsc01dp01icE0isc01ip01in00",
	})
	sysfs.child("usb", "1-8:1.0", "bluetooth/hci0", "bluetooth", "")
	controller := "bluetooth/hci0/hci0:11/0005:054C:0CE6.0004"
	sysfs.child("usb", "1-8:1.0", controller+"/input/input25/event20", "input", "input/event20")
	sysfs.child("usb", "1-8:1.0", controller+"/hidraw/hidraw3", "hidraw", "hidraw3")

	delivery := InspectDelivery(sysfs.root, Device{Bus: "usb", Address: "1-8:1.0"})

	if len(delivery.DevNodes()) != 0 {
		t.Errorf("DevNodes = %v, want none: the controller's nodes are the controller's, not the adapter's", delivery.DevNodes())
	}
	if delivery.BusNode != "/dev/bus/usb/001/008" {
		t.Errorf("BusNode = %q, want the adapter's usbfs node", delivery.BusNode)
	}
}

// miscDevice registers a node in the misc class, the shape sysfs
// gives a driver that owns one character node and no bus device of
// its own.
func (f *fakeSysfs) miscDevice(name, devname string) {
	f.t.Helper()
	dir := filepath.Join(f.root, "class", "misc", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dev"), []byte("10:239\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "uevent"), []byte("DEVNAME="+devname+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func TestMiscNodeReportsTheNodeTheClassPublishes(t *testing.T) {
	// A misc device has no place in any bus device's subtree, so the
	// walk never reaches it, and the class directory is where the
	// kernel publishes its node.
	sysfs := newFakeSysfs(t)
	sysfs.miscDevice("uhid", "uhid")

	if node := MiscNode(sysfs.root, "uhid"); node != "/dev/uhid" {
		t.Errorf("MiscNode = %q, want /dev/uhid", node)
	}
}

func TestMiscNodeReportsNothingForAModuleThatIsNotLoaded(t *testing.T) {
	// The class directory appears when the module registers the
	// device, so its absence is the answer: the node does not exist.
	sysfs := newFakeSysfs(t)

	if node := MiscNode(sysfs.root, "uhid"); node != "" {
		t.Errorf("MiscNode = %q, want nothing", node)
	}
}

func TestEvdevNodesCoverTheLegacyMinors(t *testing.T) {
	// The kernel gives the first 32 event devices a fixed character number,
	// major 13 and minor 64 plus the index, so the list is the same on every
	// machine and needs no sysfs walk.
	nodes := EvdevNodes()

	if len(nodes) != 32 {
		t.Fatalf("nodes = %d, want the 32 legacy event minors", len(nodes))
	}
	for i, node := range nodes {
		want := fmt.Sprintf("/dev/input/event%d", i)
		if node.Path != want || node.Subsystem != "input" {
			t.Errorf("node %d = %+v, want %s in the input subsystem", i, node, want)
		}
	}
}

func TestEvdevNumbersReportsTheKernelsNumbering(t *testing.T) {
	for _, test := range []struct {
		path  string
		major int
		minor int
		ok    bool
	}{
		{path: "/dev/input/event0", major: 13, minor: 64, ok: true},
		{path: "/dev/input/event31", major: 13, minor: 95, ok: true},
		// Above the legacy range the kernel allocates a minor when the device
		// registers, so no fixed number exists to state.
		{path: "/dev/input/event32"},
		{path: "/dev/input/event07"},
		{path: "/dev/input/mice"},
		{path: "/dev/uinput"},
	} {
		t.Run(test.path, func(t *testing.T) {
			major, minor, ok := EvdevNumbers(test.path)
			if major != test.major || minor != test.minor || ok != test.ok {
				t.Errorf("EvdevNumbers(%q) = %d, %d, %v, want %d, %d, %v",
					test.path, major, minor, ok, test.major, test.minor, test.ok)
			}
		})
	}
}

func TestDeliveryKeepsNodesUnderAUSBHIDDevice(t *testing.T) {
	// The boundary is the bluetooth subsystem, and not the HID device
	// below it. usbhid registers the UPS's hidraw node inside a HID
	// device of its own, and that node is the whole point of the
	// claim.
	sysfs := newFakeSysfs(t)
	sysfs.device("usb", "3-3", "usb", map[string]string{
		"modalias": "usb:v0764p0601d0100dc00dsc00dp00ic00isc00ip00in00",
		"busnum":   "3",
		"devnum":   "4",
	})
	sysfs.device("usb", "3-3:1.0", "usbhid", map[string]string{
		"modalias": "usb:v0764p0601d0100dc00dsc00dp00ic03isc00ip00in00",
	})
	sysfs.child("usb", "3-3:1.0", "0003:0764:0601.0001", "hid", "")
	sysfs.child("usb", "3-3:1.0", "0003:0764:0601.0001/hidraw/hidraw0", "hidraw", "hidraw0")

	delivery := InspectDelivery(sysfs.root, Device{Bus: "usb", Address: "3-3:1.0"})

	if !slices.Equal(delivery.DevNodes(), []string{"/dev/hidraw0"}) {
		t.Errorf("DevNodes = %v, want the UPS's hidraw node", delivery.DevNodes())
	}
}

func TestDeliveryToleratesAMissingDevice(t *testing.T) {
	delivery := InspectDelivery(t.TempDir(), Device{Bus: "usb", Address: "9-9"})
	if len(delivery.DevNodes()) != 0 {
		t.Errorf("delivery = %+v, want empty", delivery)
	}
}

func TestDeliveryRecordsEachNodesSubsystem(t *testing.T) {
	// The publish policy groups a delivery by subsystem, so the walk
	// must keep the pairing of node and kind, not two flat lists.
	sysfs := newFakeSysfs(t)
	sysfs.device("pci", "0000:00:02.0", "i915", map[string]string{
		"modalias": "pci:v00008086d000046D2sv00000301sd000002F3bc03sc00i00",
	})
	sysfs.child("pci", "0000:00:02.0", "drm/card0", "drm", "dri/card0")
	sysfs.child("pci", "0000:00:02.0", "i2c-0/i2c-dev/i2c-0", "i2c-dev", "i2c-0")

	delivery := InspectDelivery(sysfs.root, Device{Bus: "pci", Address: "0000:00:02.0"})

	want := map[string]string{"/dev/dri/card0": "drm", "/dev/i2c-0": "i2c-dev"}
	if len(delivery.Nodes) != 2 {
		t.Fatalf("Nodes = %+v, want 2", delivery.Nodes)
	}
	for _, node := range delivery.Nodes {
		if want[node.Path] != node.Subsystem {
			t.Errorf("node %s has subsystem %q, want %q", node.Path, node.Subsystem, want[node.Path])
		}
	}
}
