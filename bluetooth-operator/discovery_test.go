package main

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
)

// fakeHID describes one HID device to build into a fake sysfs tree.
// Dir is the path below /devices, Uevent holds the device's uevent
// file, and Nodes are the DEVNAME values of the input nodes below it.
// Battery is nil for a device that registers no power supply.
type fakeHID struct {
	Dir     string
	Uevent  map[string]string
	Nodes   []string
	Battery *fakePowerSupply
}

// fakePowerSupply is one power supply directory under a HID device.
// Capacity and Status are the contents of its capacity and status
// files, and an empty value leaves the file out.
type fakePowerSupply struct {
	Name     string
	Capacity string
	Status   string
}

// fakeSysfs builds a sysfs tree in a temporary directory and returns
// its root. Like the real one, each device is a directory under
// devices/ and bus/hid/devices has a symlink to it.
func fakeSysfs(t *testing.T, devices ...fakeHID) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving the temporary directory: %v", err)
	}
	busDir := filepath.Join(root, "bus", "hid", "devices")
	if err := os.MkdirAll(busDir, 0o755); err != nil {
		t.Fatalf("creating %s: %v", busDir, err)
	}
	for _, device := range devices {
		addHID(t, root, device)
	}
	return root
}

// addHID adds one device to a tree that fakeSysfs built.
func addHID(t *testing.T, root string, device fakeHID) {
	t.Helper()
	dir := filepath.Join(root, "devices", device.Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating the HID device directory: %v", err)
	}
	writeUevent(t, dir, device.Uevent)
	for i, devname := range device.Nodes {
		// An evdev node is two levels below its HID device:
		// input/inputN/eventM.
		nodeDir := filepath.Join(dir, "input", "input"+string(rune('0'+i)), filepath.Base(devname))
		if err := os.MkdirAll(nodeDir, 0o755); err != nil {
			t.Fatalf("creating the evdev node directory: %v", err)
		}
		if err := os.WriteFile(filepath.Join(nodeDir, "dev"), []byte("13:64\n"), 0o644); err != nil {
			t.Fatalf("writing the dev file of %s: %v", devname, err)
		}
		writeUevent(t, nodeDir, map[string]string{"DEVNAME": devname, "MAJOR": "13"})
	}
	if device.Battery != nil {
		supply := filepath.Join(dir, "power_supply", device.Battery.Name)
		if err := os.MkdirAll(supply, 0o755); err != nil {
			t.Fatalf("creating the power supply directory: %v", err)
		}
		for file, value := range map[string]string{
			"capacity": device.Battery.Capacity,
			"status":   device.Battery.Status,
		} {
			if value == "" {
				continue
			}
			if err := os.WriteFile(filepath.Join(supply, file), []byte(value+"\n"), 0o644); err != nil {
				t.Fatalf("writing the power supply file %s: %v", file, err)
			}
		}
	}
	base := filepath.Base(device.Dir)
	target := filepath.Join("..", "..", "..", "devices", device.Dir)
	if err := os.Symlink(target, filepath.Join(root, "bus", "hid", "devices", base)); err != nil {
		t.Fatalf("linking the HID device into bus/hid/devices: %v", err)
	}
}

// sysfsFor builds a fake sysfs tree with the devices and points
// draSysfsRoot at it for the test.
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
		t.Fatalf("writing the uevent file: %v", err)
	}
}

// dualSense is a DualSense connected over Bluetooth, as BlueZ 5.73 and
// later register it: a uhid device that is not under the adapter in
// sysfs. Its HID_PHYS is the test adapter's address.
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

// hidOn builds a HID device whose HID_PHYS is phys. An empty phys
// leaves HID_PHYS out.
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
		t.Fatalf("discovered %d devices, want 1: %+v", len(devices), devices)
	}
	if devices[0].MAC != "a0:ab:51:33:b7:12" {
		t.Errorf("MAC = %q, want a0:ab:51:33:b7:12", devices[0].MAC)
	}
	want := "/devices/virtual/misc/uhid/0005:054C:0CE6.0001"
	if devices[0].DevPath != want {
		t.Errorf("DevPath = %q, want %q", devices[0].DevPath, want)
	}
	// The joydev node input/js0 is not returned, only evdev nodes.
	if !slices.Equal(devices[0].Nodes, []string{"/dev/input/event5"}) {
		t.Errorf("Nodes = %v, want [/dev/input/event5]", devices[0].Nodes)
	}
}

func TestDiscoverHIDDevicesSkipsDeviceWithoutAddress(t *testing.T) {
	root := fakeSysfs(t, dualSense("0001", "", "input/event5"))
	if devices := discoverHIDDevices(root, bonds.Address{}); len(devices) != 0 {
		t.Fatalf("discovered %d devices, want 0: %+v", len(devices), devices)
	}
}

func TestDiscoverHIDDevicesWithoutHIDBus(t *testing.T) {
	if devices := discoverHIDDevices(t.TempDir(), bonds.Address{}); devices != nil {
		t.Fatalf("discovered %+v, want nil", devices)
	}
}

// A device whose HID_PHYS is another adapter's address belongs to
// another operator pod and is left out. A device with no HID_PHYS, or one
// that does not parse, is kept.
func TestDiscoverHIDDevicesFiltersByAdapter(t *testing.T) {
	ours := "14:b4:57:91:2f:c8"
	adapter, err := bonds.ParseAddress(ours)
	if err != nil {
		t.Fatalf("parsing the adapter address: %v", err)
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
		t.Fatalf("found nodes for %d controllers, want 2: %+v", len(nodes), nodes)
	}
	if !slices.Equal(nodes["a0:ab:51:33:b7:12"], []string{"/dev/input/event5", "/dev/input/event6"}) {
		t.Errorf("nodes of a0:ab:51:33:b7:12 = %v, want event5 and event6", nodes["a0:ab:51:33:b7:12"])
	}
	if !slices.Equal(nodes["b4:8c:9d:11:22:33"], []string{"/dev/input/event7"}) {
		t.Errorf("nodes of b4:8c:9d:11:22:33 = %v, want event7", nodes["b4:8c:9d:11:22:33"])
	}
}

func TestReadUeventFile(t *testing.T) {
	dir := t.TempDir()
	writeUevent(t, dir, map[string]string{"HID_ID": "0005:0000054C:00000CE6", "HID_UNIQ": "a0:ab:51:33:b7:12"})
	values := readUeventFile(filepath.Join(dir, "uevent"))
	if values["HID_UNIQ"] != "a0:ab:51:33:b7:12" {
		t.Errorf("HID_UNIQ = %q, want a0:ab:51:33:b7:12", values["HID_UNIQ"])
	}
	if missing := readUeventFile(filepath.Join(dir, "absent")); len(missing) != 0 {
		t.Errorf("a missing file gave %v, want an empty map", missing)
	}
}

// withBattery adds the power supply that hid-playstation registers for
// a DualSense, named ps-controller-battery-<address>.
func withBattery(device fakeHID, capacity, status string) fakeHID {
	device.Battery = &fakePowerSupply{
		Name:     "ps-controller-battery-" + device.Uevent["HID_UNIQ"],
		Capacity: capacity,
		Status:   status,
	}
	return device
}

// A controller that reports its charge in HID reports has no Battery1
// in BlueZ, so the level comes from the kernel's power supply.
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
				t.Fatalf("discovered %d devices, want 1: %+v", len(devices), devices)
			}
			got := devices[0].Battery
			if c.want == nil {
				if got != nil {
					t.Fatalf("battery = %+v, want none", got)
				}
				return
			}
			if got == nil {
				t.Fatal("the walk read no battery, want one")
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

// show formats an optional flag for a failure message.
func show(flag *bool) string {
	if flag == nil {
		return "unstated"
	}
	return strconv.FormatBool(*flag)
}

// Most controllers register no power supply, and reporting a level of
// zero for them would show an empty battery.
func TestDiscoverHIDDevicesWithoutAPowerSupply(t *testing.T) {
	root := fakeSysfs(t, dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"))
	devices := discoverHIDDevices(root, bonds.Address{})
	if len(devices) != 1 {
		t.Fatalf("discovered %d devices, want 1: %+v", len(devices), devices)
	}
	if devices[0].Battery != nil {
		t.Errorf("battery = %+v, want none", devices[0].Battery)
	}
}

// A controller can register several HID devices with the battery under
// only one of them, so kernelBatteries keys the batteries by the
// controller's address.
func TestKernelBatteriesKeysByAddress(t *testing.T) {
	root := fakeSysfs(t,
		dualSense("0001", "a0:ab:51:33:b7:12", "input/event5"),
		withBattery(dualSense("0002", "a0:ab:51:33:b7:12", "input/event6"), "40", "Discharging"),
		dualSense("0003", "b4:8c:9d:11:22:33", "input/event7"),
	)

	batteries := kernelBatteries(discoverHIDDevices(root, bonds.Address{}))
	if len(batteries) != 1 {
		t.Fatalf("found %d batteries, want 1: %+v", len(batteries), batteries)
	}
	battery := batteries[testAddress(t, "a0:ab:51:33:b7:12")]
	if battery == nil || battery.Percentage != 40 {
		t.Errorf("battery = %+v, want 40 percent", battery)
	}
}
