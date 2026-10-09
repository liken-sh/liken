package hardware

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// treeDevice creates one directory in the device tree, the way the
// kernel lays out /sys/devices: a subsystem link, a driver link when
// a driver has bound it, and, for a device on a bus, the bus's own
// link back to it. The board walk reads the tree, and the delivery
// walk reaches a device through its bus link, so a test needs both.
func (f *fakeSysfs) treeDevice(path, subsystem, driver string, bus bool) string {
	f.t.Helper()
	dir := filepath.Join(f.root, "devices", path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	if subsystem != "" {
		if err := os.Symlink(filepath.Join(f.root, "bus", subsystem), filepath.Join(dir, "subsystem")); err != nil {
			f.t.Fatal(err)
		}
	}
	if driver != "" {
		if err := os.Symlink(filepath.Join(f.root, "bus", subsystem, "drivers", driver), filepath.Join(dir, "driver")); err != nil {
			f.t.Fatal(err)
		}
	}
	if bus {
		devices := filepath.Join(f.root, "bus", subsystem, "devices")
		if err := os.MkdirAll(devices, 0o755); err != nil {
			f.t.Fatal(err)
		}
		if err := os.Symlink(dir, filepath.Join(devices, filepath.Base(dir))); err != nil {
			f.t.Fatal(err)
		}
	}
	return dir
}

// treeNode creates one node's directory in the device tree, with the
// dev and uevent files a node carries, and the /sys/dev link that
// names it by its numbers. attrs adds attribute files, such as a
// serial port's type.
func (f *fakeSysfs) treeNode(path, subsystem, devname, numbers string, attrs map[string]string) {
	f.t.Helper()
	dir := f.treeDevice(path, subsystem, "", false)
	files := map[string]string{"dev": numbers, "uevent": "DEVNAME=" + devname}
	for name, content := range attrs {
		files[name] = content
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content+"\n"), 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
	links := filepath.Join(f.root, "dev", "char")
	if subsystem == "block" {
		links = filepath.Join(f.root, "dev", "block")
	}
	if err := os.MkdirAll(links, 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Symlink(dir, filepath.Join(links, numbers)); err != nil {
		f.t.Fatal(err)
	}
}

// boardMachine is a sysfs tree in the shape the lab's machines have:
// a firmware TPM and a CMOS clock on the platform bus, a laptop
// keyboard two driven devices below the i8042 controller, a power
// button, a GPU on the pci bus, a speaker with no driver, and the
// misc node that a module registers under /devices/virtual.
func boardMachine(t *testing.T) *fakeSysfs {
	sysfs := newFakeSysfs(t)
	sysfs.treeDevice("platform/MSFT0101:00", "platform", "tpm_crb_acpi", true)
	sysfs.treeNode("platform/MSFT0101:00/tpm/tpm0", "tpm", "tpm0", "10:224", nil)
	sysfs.treeNode("platform/MSFT0101:00/tpmrm/tpmrm0", "tpmrm", "tpmrm0", "252:65536", nil)
	sysfs.treeDevice("platform/i8042", "platform", "i8042", true)
	sysfs.treeDevice("platform/i8042/serio0", "serio", "atkbd", true)
	sysfs.treeDevice("platform/i8042/serio0/input/input1", "input", "", false)
	sysfs.treeNode("platform/i8042/serio0/input/input1/event1", "input", "input/event1", "13:65", nil)
	sysfs.treeDevice("platform/PNP0C0C:00", "platform", "acpi-button", true)
	sysfs.treeNode("platform/PNP0C0C:00/input/input0/event0", "input", "input/event0", "13:64", nil)
	sysfs.treeDevice("pci0000:00/0000:00:02.0", "pci", "i915", true)
	sysfs.treeNode("pci0000:00/0000:00:02.0/drm/card1", "drm", "dri/card1", "226:1", nil)
	sysfs.treeDevice("pci0000:00/acpi.video_bus.0", "platform", "video", true)
	sysfs.treeNode("pci0000:00/acpi.video_bus.0/input/input3/event3", "input", "input/event3", "13:67", nil)
	sysfs.treeDevice("platform/pcspkr", "platform", "", true)
	sysfs.treeNode("platform/pcspkr/input/input4/event4", "input", "input/event4", "13:68", nil)
	sysfs.treeNode("virtual/misc/uhid", "misc", "uhid", "10:239", nil)
	return sysfs
}

// The board walk names the outermost driven device above each node
// that no pci or usb device owns, and names it once however many
// nodes it owns. The GPU's node belongs to the pci walk, the
// speaker has no driver to serve it, and the misc node has no
// hardware behind it, so none of them adds a device here. A device
// under the pci root that is not itself a pci device is still board
// hardware.
func TestBoardDevicesNameTheOutermostDrivenDevice(t *testing.T) {
	sysfs := boardMachine(t)

	got := boardDevices(sysfs.root)

	want := []Device{
		{Bus: "platform", Address: "MSFT0101:00", Driver: "tpm_crb_acpi"},
		{Bus: "platform", Address: "PNP0C0C:00", Driver: "acpi-button"},
		{Bus: "platform", Address: "acpi.video_bus.0", Driver: "video"},
		{Bus: "platform", Address: "i8042", Driver: "i8042"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("boardDevices =\n%+v\nwant\n%+v", got, want)
	}
}

// The delivery walk from a board device collects every node beneath
// it, through the driven devices between, the same way it does from
// a pci device.
func TestABoardDeviceDeliversTheNodesBeneathIt(t *testing.T) {
	sysfs := boardMachine(t)

	keyboard := InspectDelivery(sysfs.root, Device{Bus: "platform", Address: "i8042"})
	tpm := InspectDelivery(sysfs.root, Device{Bus: "platform", Address: "MSFT0101:00"})

	if got, want := keyboard.DevNodes(), []string{"/dev/input/event1"}; !slices.Equal(got, want) {
		t.Errorf("the keyboard delivers %q, want %q", got, want)
	}
	if got, want := tpm.DevNodes(), []string{"/dev/tpm0", "/dev/tpmrm0"}; !slices.Equal(got, want) {
		t.Errorf("the TPM delivers %q, want %q", got, want)
	}
}

// DiscoverInventory adds the board devices after the bus walk's, so
// the pci and usb devices keep the order they always had.
func TestDiscoverInventoryAddsTheBoardDevicesAfterTheBuses(t *testing.T) {
	sysfs := boardMachine(t)
	sysfs.device("pci", "0000:00:03.0", "virtio-pci", map[string]string{"modalias": "pci:v00001AF4d00001041"})

	var got []string
	for _, d := range DiscoverInventory(sysfs.root, nil) {
		got = append(got, d.Bus+"/"+d.Address)
	}

	want := []string{"pci/0000:00:03.0", "platform/MSFT0101:00", "platform/PNP0C0C:00", "platform/acpi.video_bus.0", "platform/i8042"}
	if !slices.Equal(got, want) {
		t.Errorf("DiscoverInventory = %q, want %q", got, want)
	}
}

// The 8250 driver reserves 32 ports at boot and registers a tty for
// each, and a port where no UART answers reports type 0. Only a port
// with a UART delivers its node.
func TestASerialPortWithNoUARTDeliversNothing(t *testing.T) {
	sysfs := newFakeSysfs(t)
	sysfs.treeDevice("platform/serial8250", "platform", "serial8250", true)
	sysfs.treeNode("platform/serial8250/serial8250:0/serial8250:0.0/tty/ttyS0", "tty", "ttyS0", "4:64", map[string]string{"type": "4"})
	sysfs.treeNode("platform/serial8250/serial8250:0/serial8250:0.1/tty/ttyS1", "tty", "ttyS1", "4:65", map[string]string{"type": "0"})

	delivery := InspectDelivery(sysfs.root, Device{Bus: "platform", Address: "serial8250"})

	if got, want := delivery.DevNodes(), []string{"/dev/ttyS0"}; !slices.Equal(got, want) {
		t.Errorf("the serial ports deliver %q, want %q", got, want)
	}
}

// A sysfs root with no /sys/dev has no board devices, the same as a
// root with no bus directory has no bus devices.
func TestBoardDevicesTolerateAMissingTree(t *testing.T) {
	if got := boardDevices(t.TempDir()); len(got) != 0 {
		t.Errorf("boardDevices = %+v, want none", got)
	}
}

// The inventory's uevent match keeps every event outside
// /devices/virtual/, and under it, only the misc class's. The veth
// pairs and their queues are what pods starting and stopping send.
func TestInventoryEvent(t *testing.T) {
	cases := []struct {
		devpath string
		want    bool
	}{
		{"/devices/pci0000:00/0000:00:03.0/usb1/1-2", true},
		{"/devices/platform/MSFT0101:00/tpm/tpm0", true},
		{"/devices/pnp0/00:04/tty/ttyS0", true},
		{"/devices/virtual/misc/uhid", true},
		{"/devices/virtual/net/veth0f1c75eb", false},
		{"/devices/virtual/net/veth0f1c75eb/queues/rx-0", false},
		{"/devices/virtual/block/loop3", false},
	}
	for _, tc := range cases {
		t.Run(tc.devpath, func(t *testing.T) {
			if got := InventoryEvent(Uevent{Action: "add", DevPath: tc.devpath}); got != tc.want {
				t.Errorf("InventoryEvent(%s) = %v, want %v", tc.devpath, got, tc.want)
			}
		})
	}
}

// A USB device has no modalias, so DiscoverDevices lists its
// interfaces alone, and DiscoverInventory lists the device too, with
// the identity the device carries.
func TestDiscoverInventoryListsTheUSBDeviceBesideItsInterfaces(t *testing.T) {
	sysfs := newFakeSysfs(t)
	sysfs.device("usb", "1-2", "usb", map[string]string{
		"idVendor": "08e6", "idProduct": "4433", "manufacturer": "QEMU", "product": "QEMU USB CCID", "serial": "1-0000:00:03.0-2",
	})
	sysfs.device("usb", "1-2:1.0", "", map[string]string{"modalias": "usb:v08E6p4433d0000dc00dsc00dp00ic0Bisc00ip00in00", "bInterfaceClass": "0b"})

	var got []Device
	for _, d := range DiscoverInventory(sysfs.root, nil) {
		if d.Address == "1-2" {
			got = append(got, d)
		}
	}

	want := []Device{{Bus: "usb", Address: "1-2", Driver: "usb", Name: "QEMU QEMU USB CCID", Vendor: "08e6", Product: "4433", Serial: "1-0000:00:03.0-2"}}
	if !slices.Equal(got, want) {
		t.Errorf("DiscoverInventory listed %+v for the device, want %+v", got, want)
	}
}
