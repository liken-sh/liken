package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeHID describes one HID device to build into a fake sysfs tree.
// Dir is the path below /devices, Uevent is the device's own uevent
// file, and Nodes are the DEVNAME values of the input nodes below it.
type fakeHID struct {
	Dir    string
	Uevent map[string]string
	Nodes  []string
}

// fakeSysfs builds a sysfs tree in a temporary directory and returns
// its root. The tree mirrors the real one: each device is a real
// directory under devices/, and bus/hid/devices holds a symlink to
// it, so the walk resolves a link the same way it does on a machine.
func fakeSysfs(t *testing.T, devices ...fakeHID) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	busDir := filepath.Join(root, "bus", "hid", "devices")
	if err := os.MkdirAll(busDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, device := range devices {
		dir := filepath.Join(root, "devices", device.Dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeUevent(t, dir, device.Uevent)
		for i, devname := range device.Nodes {
			// The kernel puts an evdev node two levels below the HID
			// device: input/inputN/eventM.
			nodeDir := filepath.Join(dir, "input", "input"+string(rune('0'+i)), filepath.Base(devname))
			if err := os.MkdirAll(nodeDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(nodeDir, "dev"), []byte("13:64\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			writeUevent(t, nodeDir, map[string]string{"DEVNAME": devname, "MAJOR": "13"})
		}
		base := filepath.Base(device.Dir)
		target := filepath.Join("..", "..", "..", "devices", device.Dir)
		if err := os.Symlink(target, filepath.Join(busDir, base)); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func writeUevent(t *testing.T, dir string, values map[string]string) {
	t.Helper()
	var lines []string
	for key, value := range values {
		lines = append(lines, key+"="+value)
	}
	slices.Sort(lines)
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "uevent"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// dualSense is a DualSense over Bluetooth, as BlueZ 5.73 and later
// present it: a uhid device with no ancestry to the adapter.
func dualSense(instance string, mac string, nodes ...string) fakeHID {
	return fakeHID{
		Dir: "virtual/misc/uhid/0005:054C:0CE6." + instance,
		Uevent: map[string]string{
			"HID_ID":   "0005:0000054C:00000CE6",
			"HID_NAME": "DualSense Wireless Controller",
			"HID_PHYS": "00:1a:7d:da:71:13",
			"HID_UNIQ": mac,
		},
		Nodes: nodes,
	}
}

func TestDiscoverHIDDevicesKeepsBluetoothOnly(t *testing.T) {
	root := fakeSysfs(t,
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5", "input/js0"),
		// A USB keyboard: bus type 0003, BUS_USB.
		fakeHID{
			Dir: "pci0000:00/0000:00:14.0/usb1/1-3/1-3:1.0/0003:046D:C31C.0002",
			Uevent: map[string]string{
				"HID_ID":   "0003:0000046D:0000C31C",
				"HID_NAME": "Logitech Keyboard",
				"HID_UNIQ": "",
			},
			Nodes: []string{"input/event2"},
		},
	)

	devices := discoverHIDDevices(root)
	if len(devices) != 1 {
		t.Fatalf("got %d devices, want 1: %+v", len(devices), devices)
	}
	if devices[0].MAC != "a0:ab:51:33:b7:12" {
		t.Errorf("MAC = %q", devices[0].MAC)
	}
	want := "/devices/virtual/misc/uhid/0005:054C:0CE6.0001"
	if devices[0].DevPath != want {
		t.Errorf("DevPath = %q, want %q", devices[0].DevPath, want)
	}
	// joydev's node is left out: the delivery is evdev only.
	if !slices.Equal(devices[0].Nodes, []string{"/dev/input/event5"}) {
		t.Errorf("Nodes = %v, want [/dev/input/event5]", devices[0].Nodes)
	}
}

func TestDiscoverHIDDevicesSkipsDeviceWithoutAddress(t *testing.T) {
	root := fakeSysfs(t, dualSense("0001", "", "input/event5"))
	if devices := discoverHIDDevices(root); len(devices) != 0 {
		t.Fatalf("got %d devices, want 0: %+v", len(devices), devices)
	}
}

func TestDiscoverHIDDevicesWithoutHIDBus(t *testing.T) {
	if devices := discoverHIDDevices(t.TempDir()); devices != nil {
		t.Fatalf("got %+v, want nil", devices)
	}
}

func TestNodesByMACMergesOneControllersDevices(t *testing.T) {
	root := fakeSysfs(t,
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"),
		dualSense("0002", "a0:ab:51:33:b7:12", "input/event6"),
		dualSense("0003", "b4:8c:9d:11:22:33", "input/event7"),
	)

	nodes := nodesByMAC(discoverHIDDevices(root))
	if len(nodes) != 2 {
		t.Fatalf("got %d controllers, want 2: %+v", len(nodes), nodes)
	}
	if !slices.Equal(nodes["a0:ab:51:33:b7:12"], []string{"/dev/input/event5", "/dev/input/event6"}) {
		t.Errorf("nodes = %v", nodes["a0:ab:51:33:b7:12"])
	}
	if !slices.Equal(nodes["b4:8c:9d:11:22:33"], []string{"/dev/input/event7"}) {
		t.Errorf("nodes = %v", nodes["b4:8c:9d:11:22:33"])
	}
}

func TestReadUeventFile(t *testing.T) {
	dir := t.TempDir()
	writeUevent(t, dir, map[string]string{"HID_ID": "0005:0000054C:00000CE6", "HID_UNIQ": "a0:ab:51:33:b7:12"})
	values := readUeventFile(filepath.Join(dir, "uevent"))
	if values["HID_UNIQ"] != "a0:ab:51:33:b7:12" {
		t.Errorf("HID_UNIQ = %q", values["HID_UNIQ"])
	}
	if missing := readUeventFile(filepath.Join(dir, "absent")); len(missing) != 0 {
		t.Errorf("a missing file gave %v, want an empty map", missing)
	}
}
