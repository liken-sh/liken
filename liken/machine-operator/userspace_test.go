package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/liken-sh/liken/liken/hardware"
)

// usbfsOnly is the delivery of a whole USB device: its own usbfs
// node, which the walk reports from the device's directory.
var usbfsOnly = delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{
	{Path: "/dev/bus/usb/001/002", Subsystem: "usb"},
}})

// A smart card reader has no kernel driver: pcscd reaches it through
// libusb. The inventory publishes the device whole, named by its port
// path, with the class its interface states.
func TestInventoryPublishesAUSBDeviceThatNoDriverBinds(t *testing.T) {
	devices := inventoryDevices([]hardware.Device{
		{Bus: "usb", Address: "1-2", Driver: "usb", Name: "QEMU QEMU USB CCID", Vendor: "08e6", Product: "4433"},
		{Bus: "usb", Address: "1-2:1.0", Class: "smart-card", ClassCode: "0b"},
	}, usbfsOnly, nil, nil)

	if len(devices) != 1 {
		t.Fatalf("devices = %+v, want the reader", devices)
	}
	reader := devices[0]
	if reader.Name != "usb-1-2" {
		t.Errorf("name = %q, want the device's port path", reader.Name)
	}
	if got := reader.Attributes["classCode"].String; got == nil || *got != "0b" {
		t.Errorf("classCode = %v, want the interface's 0b", got)
	}
	if reader.AllowMultipleAllocations != nil {
		t.Error("usbfs carries raw transfers with no arbitration, so the device is exclusive")
	}
}

// The whole device publishes only while no interface has a driver.
// A driver on one interface means that interface publishes, with the
// usbfs node beside it, and a second claim on the whole device would
// hand the same hardware to a second workload. usbfs on an interface
// means a program holds the device now.
func TestInventoryLeavesAUSBDeviceThatADriverServes(t *testing.T) {
	cases := []struct {
		name   string
		driver string
	}{
		{"a kernel driver on one interface", "usbhid"},
		{"a program holding one interface", "usbfs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			devices := inventoryDevices([]hardware.Device{
				{Bus: "usb", Address: "1-6", Driver: "usb", Name: "USB Audio and HID"},
				{Bus: "usb", Address: "1-6:1.3", Class: "hid", ClassCode: "03"},
				{Bus: "usb", Address: "1-6:1.0", Driver: tc.driver, Class: "audio", ClassCode: "01"},
			}, usbfsOnly, nil, nil)

			for _, d := range devices {
				if d.Name == "usb-1-6" {
					t.Errorf("published the whole device beside %s", tc.name)
				}
			}
		})
	}
}

func TestUserspaceDeviceNeedsAnInterface(t *testing.T) {
	cases := []struct {
		name   string
		device hardware.Device
	}{
		{"a device whose interfaces have not appeared", hardware.Device{Bus: "usb", Address: "1-2", Driver: "usb"}},
		{"an interface", hardware.Device{Bus: "usb", Address: "1-2:1.0"}},
		{"a pci device", hardware.Device{Bus: "pci", Address: "0000:00:02.0"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := userspaceDevice(tc.device, []hardware.Device{tc.device}); ok {
				t.Errorf("userspaceDevice(%+v) = true, want false", tc.device)
			}
		})
	}
}

// plugInCardReader writes a smart card reader into a fake sysfs the
// way the kernel lays it out: the device, with its usbfs node and no
// modalias, and its one interface, with a modalias and no driver.
func plugInCardReader(t *testing.T, sysRoot string) {
	t.Helper()
	devices := filepath.Join(sysRoot, "bus", "usb", "devices")
	reader, iface := filepath.Join(devices, "1-2"), filepath.Join(devices, "1-2:1.0")
	usbcore := filepath.Join(sysRoot, "bus", "usb", "drivers", "usb")
	for _, dir := range []string{reader, iface, usbcore} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(reader, "busnum"):         "1",
		filepath.Join(reader, "devnum"):         "2",
		filepath.Join(reader, "dev"):            "189:1",
		filepath.Join(reader, "uevent"):         "DEVNAME=bus/usb/001/002",
		filepath.Join(reader, "product"):        "QEMU USB CCID",
		filepath.Join(iface, "modalias"):        "usb:v08E6p4433d0000dc00dsc00dp00ic0Bisc00ip00in00",
		filepath.Join(iface, "bInterfaceClass"): "0b",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, link := range []string{filepath.Join(reader, "subsystem"), filepath.Join(iface, "subsystem")} {
		if err := os.Symlink(filepath.Join(sysRoot, "bus", "usb"), link); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(usbcore, filepath.Join(reader, "driver")); err != nil {
		t.Fatal(err)
	}
}

// The reader reaches the slice from sysfs as the kernel lays it out,
// and a claim on it resolves to the reader's usbfs node.
func TestACardReaderInSysfsPublishesWholeAndResolvesToItsUsbfsNode(t *testing.T) {
	sysRoot := t.TempDir()
	plugInCardReader(t, sysRoot)
	discovered := hardware.DiscoverInventory(sysRoot, nil)

	devices := inventoryDevices(discovered, func(d hardware.Device) hardware.Delivery {
		return hardware.InspectDelivery(sysRoot, d)
	}, nil, nil)
	byName := map[string]hardware.Device{}
	for _, d := range discovered {
		byName[deviceName(d)] = d
	}
	published, ok := resolveAllocated("usb-1-2", sysRoot, byName, nil)

	var names []string
	for _, d := range devices {
		names = append(names, d.Name)
	}
	if !slices.Equal(names, []string{"usb-1-2"}) {
		t.Errorf("the slice holds %q, want the reader", names)
	}
	if !ok || !slices.Equal(published.Nodes, []string{"/dev/bus/usb/001/002"}) {
		t.Errorf("a claim on the reader resolves to %q (%v), want its usbfs node", published.Nodes, ok)
	}
}
