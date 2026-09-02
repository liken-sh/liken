package main

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

// fakeHID describes one HID device to build into a fake sysfs tree.
// Dir is the path below /devices, Uevent is the device's own uevent
// file, and Nodes are the DEVNAME values of the input nodes below it.
// Battery is the power supply the kernel registers under a device
// that reports its charge, and is absent for a device that reports
// none.
type fakeHID struct {
	Dir     string
	Uevent  map[string]string
	Nodes   []string
	Battery *fakePowerSupply
}

// fakePowerSupply is one entry in the kernel's power supply class,
// as it appears under a HID device: a directory named for the supply,
// with the two files this operator reads.
type fakePowerSupply struct {
	Name     string
	Capacity string
	Status   string
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
		if device.Battery != nil {
			supply := filepath.Join(dir, "power_supply", device.Battery.Name)
			if err := os.MkdirAll(supply, 0o755); err != nil {
				t.Fatal(err)
			}
			for file, value := range map[string]string{
				"capacity": device.Battery.Capacity,
				"status":   device.Battery.Status,
			} {
				if value == "" {
					continue
				}
				if err := os.WriteFile(filepath.Join(supply, file), []byte(value+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
		base := filepath.Base(device.Dir)
		target := filepath.Join("..", "..", "..", "devices", device.Dir)
		if err := os.Symlink(target, filepath.Join(busDir, base)); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// sysfsFor points the walk at a tree this test owns, and puts the
// fake devices in it.
func sysfsFor(t *testing.T, devices ...fakeHID) {
	t.Helper()
	previous := draSysfsRoot
	draSysfsRoot = fakeSysfs(t, devices...)
	t.Cleanup(func() { draSysfsRoot = previous })
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
// present it: a uhid device with no ancestry to the adapter. HID_PHYS
// is the test adapter, so the device reads as one on the operator's own
// radio.
func dualSense(instance string, mac string, nodes ...string) fakeHID {
	return fakeHID{
		Dir: "virtual/misc/uhid/0005:054C:0CE6." + instance,
		Uevent: map[string]string{
			"HID_ID":   "0005:0000054C:00000CE6",
			"HID_NAME": "DualSense Wireless Controller",
			"HID_PHYS": "14:b4:57:91:2f:c8",
			"HID_UNIQ": mac,
		},
		Nodes: nodes,
	}
}

// hidOn builds a HID device whose HID_PHYS is the given adapter
// address. An empty phys removes HID_PHYS, which is the device the
// filter keeps because it cannot place it on an adapter.
func hidOn(instance, mac, phys string, nodes ...string) fakeHID {
	device := dualSense(instance, mac, nodes...)
	if phys == "" {
		delete(device.Uevent, "HID_PHYS")
	} else {
		device.Uevent["HID_PHYS"] = phys
	}
	return device
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

	devices := discoverHIDDevices(root, bonds.Address{})
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
	if devices := discoverHIDDevices(root, bonds.Address{}); len(devices) != 0 {
		t.Fatalf("got %d devices, want 0: %+v", len(devices), devices)
	}
}

func TestDiscoverHIDDevicesWithoutHIDBus(t *testing.T) {
	if devices := discoverHIDDevices(t.TempDir(), bonds.Address{}); devices != nil {
		t.Fatalf("got %+v, want nil", devices)
	}
}

// The operator holds one adapter, and a machine with a second adapter
// registers HID devices on it that belong to another operator. A device
// whose HID_PHYS names a different adapter is left out. A device with no
// HID_PHYS, or one that does not parse, is kept, so a single-adapter
// machine never regresses.
func TestDiscoverHIDDevicesFiltersByAdapter(t *testing.T) {
	ours := "14:b4:57:91:2f:c8"
	adapter, err := bonds.ParseAddress(ours)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		phys string
		kept bool
	}{
		{name: "on this adapter", phys: ours, kept: true},
		{name: "on this adapter in uppercase", phys: "14:B4:57:91:2F:C8", kept: true},
		{name: "on another adapter", phys: "aa:bb:cc:dd:ee:ff", kept: false},
		{name: "no HID_PHYS", phys: "", kept: true},
		{name: "an unparsable HID_PHYS", phys: "not-an-address", kept: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := fakeSysfs(t, hidOn("0001", "a0:ab:51:33:b7:12", c.phys, "input/event5"))
			devices := discoverHIDDevices(root, adapter)
			if kept := len(devices) == 1; kept != c.kept {
				t.Fatalf("kept = %v, want %v: %+v", kept, c.kept, devices)
			}
		})
	}
}

func TestNodesByMACMergesOneControllersDevices(t *testing.T) {
	root := fakeSysfs(t,
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"),
		dualSense("0002", "a0:ab:51:33:b7:12", "input/event6"),
		dualSense("0003", "b4:8c:9d:11:22:33", "input/event7"),
	)

	nodes := nodesByMAC(discoverHIDDevices(root, bonds.Address{}))
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

// The controller battery the DualSense registers, as the kernel names
// it: ps-controller-battery-<address>.
func withBattery(device fakeHID, capacity, status string) fakeHID {
	device.Battery = &fakePowerSupply{
		Name:     "ps-controller-battery-" + device.Uevent["HID_UNIQ"],
		Capacity: capacity,
		Status:   status,
	}
	return device
}

// A controller that states its charge in its HID reports has no
// Battery1 in BlueZ, so the walk is where its level is read.
func TestDiscoverHIDDevicesReadsTheKernelBattery(t *testing.T) {
	charging, discharging := true, false
	cases := []struct {
		name     string
		capacity string
		status   string
		want     *hidBattery
	}{
		{
			name:     "charging",
			capacity: "40",
			status:   "Charging",
			want:     &hidBattery{Percentage: 40, Charging: &charging},
		},
		{
			name:     "full",
			capacity: "100",
			status:   "Full",
			want:     &hidBattery{Percentage: 100, Charging: &charging},
		},
		{
			name:     "discharging",
			capacity: "88",
			status:   "Discharging",
			want:     &hidBattery{Percentage: 88, Charging: &discharging},
		},
		{
			name:     "not charging",
			capacity: "55",
			status:   "Not charging",
			want:     &hidBattery{Percentage: 55, Charging: &discharging},
		},
		{
			name:     "unknown",
			capacity: "55",
			status:   "Unknown",
			want:     &hidBattery{Percentage: 55},
		},
		{
			name:     "no status file",
			capacity: "55",
			status:   "",
			want:     &hidBattery{Percentage: 55},
		},
		{name: "no capacity file", capacity: "", status: "Discharging"},
		{name: "an unreadable capacity", capacity: "unknown", status: "Discharging"},
		{name: "a capacity above 100", capacity: "127", status: "Discharging"},
		{name: "a capacity below 0", capacity: "-1", status: "Discharging"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := fakeSysfs(t, withBattery(
				dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"), c.capacity, c.status))

			devices := discoverHIDDevices(root, bonds.Address{})
			if len(devices) != 1 {
				t.Fatalf("got %d devices, want 1: %+v", len(devices), devices)
			}
			got := devices[0].Battery
			if c.want == nil {
				if got != nil {
					t.Fatalf("battery = %+v, want none", got)
				}
				return
			}
			if got == nil {
				t.Fatal("the walk read no battery")
			}
			c.want.Name = "ps-controller-battery-a0:ab:51:33:b7:12"
			if got.Name != c.want.Name || got.Percentage != c.want.Percentage {
				t.Errorf("battery = %+v, want %+v", got, c.want)
			}
			if !reflect.DeepEqual(got.Charging, c.want.Charging) {
				t.Errorf("charging = %v, want %v", show(got.Charging), show(c.want.Charging))
			}
		})
	}
}

// show prints an optional flag for a failure message.
func show(flag *bool) string {
	if flag == nil {
		return "unstated"
	}
	return strconv.FormatBool(*flag)
}

// Most controllers register no power supply at all, and a level of
// zero for them would read as an empty battery.
func TestDiscoverHIDDevicesWithoutAPowerSupply(t *testing.T) {
	root := fakeSysfs(t, dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"))
	devices := discoverHIDDevices(root, bonds.Address{})
	if len(devices) != 1 {
		t.Fatalf("got %d devices, want 1: %+v", len(devices), devices)
	}
	if devices[0].Battery != nil {
		t.Errorf("battery = %+v, want none", devices[0].Battery)
	}
}

// A controller that registers more than one HID device carries its
// battery under one of them, and the pass keys the level by the
// controller.
func TestKernelBatteriesKeysByAddress(t *testing.T) {
	root := fakeSysfs(t,
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"),
		withBattery(dualSense("0002", "a0:ab:51:33:b7:12", "input/event6"), "40", "Discharging"),
		dualSense("0003", "b4:8c:9d:11:22:33", "input/event7"),
	)

	batteries := kernelBatteries(root, bonds.Address{})
	if len(batteries) != 1 {
		t.Fatalf("got %d batteries, want 1: %+v", len(batteries), batteries)
	}
	battery := batteries[testAddress(t, "a0:ab:51:33:b7:12")]
	if battery == nil || battery.Percentage != 40 {
		t.Errorf("battery = %+v", battery)
	}
}
