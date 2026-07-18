package hardware

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeSysfs builds the corner of sysfs this package reads: bus
// device directories with the attribute files and driver symlinks a
// real kernel populates. Tests describe devices; the builder owns
// the layout.
type fakeSysfs struct {
	t    *testing.T
	root string
}

func newFakeSysfs(t *testing.T) *fakeSysfs {
	t.Helper()
	return &fakeSysfs{t: t, root: t.TempDir()}
}

// device creates one device directory on a bus, with the given
// attribute files, and optionally a driver symlink named for the
// driver that claimed it.
func (f *fakeSysfs) device(bus, name, driver string, attrs map[string]string) {
	f.t.Helper()
	dir := filepath.Join(f.root, "bus", bus, "devices", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	for file, content := range attrs {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content+"\n"), 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
	if driver != "" {
		target := filepath.Join(f.root, "bus", bus, "drivers", driver)
		if err := os.MkdirAll(target, 0o755); err != nil {
			f.t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "driver")); err != nil {
			f.t.Fatal(err)
		}
	}
}

func TestDiscoverFindsUnboundDevices(t *testing.T) {
	sysfs := newFakeSysfs(t)
	sysfs.device("usb", "2-1", "usb", map[string]string{
		"modalias":     "usb:v46F4p0001d0100dc00dsc00dp00ic00isc00ip00in00",
		"manufacturer": "QEMU",
		"product":      "QEMU USB HARDDRIVE",
	})
	sysfs.device("usb", "2-1:1.0", "", map[string]string{
		"modalias":        "usb:v46F4p0001d0100dc00dsc00dp00ic08isc06ip50in00",
		"bInterfaceClass": "08",
	})
	sysfs.device("pci", "0000:00:02.0", "", map[string]string{
		"modalias": "pci:v00001AF4d00001050sv00001AF4sd00001100bc03sc80i00",
		"vendor":   "0x1af4",
		"device":   "0x1050",
		"class":    "0x038000",
	})
	sysfs.device("pci", "0000:00:03.0", "virtio-pci", map[string]string{
		"modalias": "pci:v00001AF4d00001041sv00001AF4sd00001100bc02sc00i00",
		"vendor":   "0x1af4",
		"device":   "0x1041",
		"class":    "0x020000",
	})

	devices := DiscoverDevices(sysfs.root, nil)

	unbound := []Device{}
	for _, d := range devices {
		if d.Driver == "" {
			unbound = append(unbound, d)
		}
	}
	if len(unbound) != 2 {
		t.Fatalf("unbound devices = %+v, want 2", unbound)
	}
}

func TestDiscoverNamesUSBDevicesFromTheirOwnStrings(t *testing.T) {
	sysfs := newFakeSysfs(t)
	sysfs.device("usb", "2-1", "usb", map[string]string{
		"modalias":     "usb:v46F4p0001d0100dc00dsc00dp00ic00isc00ip00in00",
		"manufacturer": "QEMU",
		"product":      "QEMU USB HARDDRIVE",
	})
	sysfs.device("usb", "2-1:1.0", "", map[string]string{
		"modalias":        "usb:v46F4p0001d0100dc00dsc00dp00ic08isc06ip50in00",
		"bInterfaceClass": "08",
	})

	devices := DiscoverDevices(sysfs.root, nil)

	var iface *Device
	for i := range devices {
		if devices[i].Driver == "" {
			iface = &devices[i]
		}
	}
	if iface == nil {
		t.Fatal("no unbound interface found")
	}
	if iface.Name != "QEMU QEMU USB HARDDRIVE" {
		t.Errorf("Name = %q, want the parent device's strings", iface.Name)
	}
	if iface.Class != "mass-storage" {
		t.Errorf("Class = %q, want mass-storage", iface.Class)
	}
	if iface.Bus != "usb" {
		t.Errorf("Bus = %q, want usb", iface.Bus)
	}
}

func TestDiscoverNamesPCIDevicesNumericallyWithoutADatabase(t *testing.T) {
	sysfs := newFakeSysfs(t)
	sysfs.device("pci", "0000:00:02.0", "", map[string]string{
		"modalias": "pci:v00001AF4d00001050sv00001AF4sd00001100bc03sc80i00",
		"vendor":   "0x1af4",
		"device":   "0x1050",
		"class":    "0x038000",
	})

	devices := DiscoverDevices(sysfs.root, nil)

	if len(devices) != 1 {
		t.Fatalf("devices = %+v, want 1", devices)
	}
	if devices[0].Name != "1af4:1050" {
		t.Errorf("Name = %q, want the numeric fallback 1af4:1050", devices[0].Name)
	}
	if devices[0].Class != "display" {
		t.Errorf("Class = %q, want display", devices[0].Class)
	}
}

func TestDiscoverRecordsEachDeviceAddress(t *testing.T) {
	sysfs := newFakeSysfs(t)
	sysfs.device("pci", "0000:00:02.0", "", map[string]string{
		"modalias": "pci:v00001AF4d00001050sv00001AF4sd00001100bc03sc80i00",
		"vendor":   "0x1af4",
		"device":   "0x1050",
		"class":    "0x038000",
	})
	sysfs.device("usb", "2-1:1.0", "uas", map[string]string{
		"modalias":        "usb:v46F4p0001d0100dc00dsc00dp00ic08isc06ip50in00",
		"bInterfaceClass": "08",
	})

	devices := DiscoverDevices(sysfs.root, nil)

	if len(devices) != 2 {
		t.Fatalf("devices = %+v, want 2", devices)
	}
	if devices[0].Address != "0000:00:02.0" {
		t.Errorf("Address = %q, want the sysfs directory name", devices[0].Address)
	}
	if devices[1].Address != "2-1:1.0" {
		t.Errorf("Address = %q, want the sysfs directory name", devices[1].Address)
	}
}

func TestDiscoverBorrowsTheParentSerialForUSBInterfaces(t *testing.T) {
	sysfs := newFakeSysfs(t)
	sysfs.device("usb", "2-1", "usb", map[string]string{
		"modalias":     "usb:v46F4p0001d0100dc00dsc00dp00ic00isc00ip00in00",
		"manufacturer": "QEMU",
		"product":      "QEMU USB HARDDRIVE",
		"serial":       "1-0000:00:04.0-1",
	})
	sysfs.device("usb", "2-1:1.0", "uas", map[string]string{
		"modalias":        "usb:v46F4p0001d0100dc00dsc00dp00ic08isc06ip50in00",
		"bInterfaceClass": "08",
	})

	devices := DiscoverDevices(sysfs.root, nil)

	var iface *Device
	for i := range devices {
		if devices[i].Address == "2-1:1.0" {
			iface = &devices[i]
		}
	}
	if iface == nil {
		t.Fatal("the interface was not discovered")
	}
	if iface.Serial != "1-0000:00:04.0-1" {
		t.Errorf("Serial = %q, want the parent device's serial", iface.Serial)
	}
}

func TestDiscoverRecordsVendorAndProductIDs(t *testing.T) {
	sysfs := newFakeSysfs(t)
	sysfs.device("pci", "0000:00:02.0", "bochs", map[string]string{
		"modalias": "pci:v00001234d00001111sv00001AF4sd00001100bc03sc00i00",
		"vendor":   "0x1234",
		"device":   "0x1111",
		"class":    "0x030000",
	})
	sysfs.device("usb", "2-1", "usb", map[string]string{
		"modalias":  "usb:v46F4p0001d0100dc00dsc00dp00ic00isc00ip00in00",
		"idVendor":  "46f4",
		"idProduct": "0001",
	})
	sysfs.device("usb", "2-1:1.0", "uas", map[string]string{
		"modalias":        "usb:v46F4p0001d0100dc00dsc00dp00ic08isc06ip50in00",
		"bInterfaceClass": "08",
	})

	devices := DiscoverDevices(sysfs.root, nil)

	byAddress := map[string]Device{}
	for _, d := range devices {
		byAddress[d.Address] = d
	}
	pci := byAddress["0000:00:02.0"]
	if pci.Vendor != "1234" || pci.Product != "1111" {
		t.Errorf("pci ids = %q:%q, want 1234:1111", pci.Vendor, pci.Product)
	}
	iface := byAddress["2-1:1.0"]
	if iface.Vendor != "46f4" || iface.Product != "0001" {
		t.Errorf("interface ids = %q:%q, want the parent's 46f4:0001", iface.Vendor, iface.Product)
	}
}

func TestDiscoverLeavesSerialEmptyWhenTheHardwareCarriesNone(t *testing.T) {
	sysfs := newFakeSysfs(t)
	sysfs.device("pci", "0000:00:02.0", "bochs", map[string]string{
		"modalias": "pci:v00001234d00001111sv00001AF4sd00001100bc03sc00i00",
		"vendor":   "0x1234",
		"device":   "0x1111",
		"class":    "0x030000",
	})

	devices := DiscoverDevices(sysfs.root, nil)

	if len(devices) != 1 || devices[0].Serial != "" {
		t.Errorf("devices = %+v, want one with no serial", devices)
	}
}

func TestDiscoverSkipsDevicesWithoutAModalias(t *testing.T) {
	sysfs := newFakeSysfs(t)
	sysfs.device("usb", "usb2", "usb", map[string]string{
		"manufacturer": "Linux Foundation",
	})

	if devices := DiscoverDevices(sysfs.root, nil); len(devices) != 0 {
		t.Errorf("devices = %+v, want none", devices)
	}
}

func TestDiscoverToleratesAMissingBus(t *testing.T) {
	if devices := DiscoverDevices(t.TempDir(), nil); devices != nil {
		t.Errorf("devices = %+v, want nil on an empty tree", devices)
	}
}
