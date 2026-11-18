package main

// discoverHIDDevices finds the evdev nodes of the connected Bluetooth
// controllers in sysfs. When a paired controller connects, the kernel
// creates a HID device for it, and that HID device registers the input
// nodes that a claiming pod receives. The walk lists /sys/bus/hid/devices
// and keeps the devices whose bus type is 0005 (BUS_BLUETOOTH).
//
// The walk does not require a device to be below the adapter in sysfs.
// BlueZ 5.73 and later run the input plugin in uhid mode by default, and
// the parent of a uhid device is /sys/devices/virtual/misc/uhid, not hci0.
//
// HID_UNIQ in a device's uevent file is the controller's MAC address,
// which the ResourceSlice publishes as the device's identity. HID_PHYS is
// the address of the local adapter that the controller connected through.
// A device on another adapter belongs to the operator pod that claims
// that adapter, so the walk drops it.
//
// The walk also reads the battery level from the kernel's power supply
// class. A driver such as hid-playstation decodes the charge from the HID
// reports and registers a power supply. bluetoothd passes those reports to
// the kernel without decoding them, so BlueZ publishes no Battery1 for
// such a device.
//
// The walk returns only input/eventN nodes. It skips the input/jsN nodes
// of joydev, which the kernel documents as legacy, and which exposes a
// DualSense's motion sensors as a second joystick.

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
)

// draSysfsRoot is the sysfs mount that the operator reads. It is a
// variable so that a test can point it at a tree that the test builds.
var draSysfsRoot = "/sys"

// busBluetooth is BUS_BLUETOOTH from the kernel's input.h, in the form
// of the first field of HID_ID.
const busBluetooth = "0005"

// hidDevice is one Bluetooth HID device found in sysfs. DevPath has the
// form of a uevent's DEVPATH, so a device from a uevent and the same
// device from a walk compare equal. Battery is nil when the device has no
// power supply.
type hidDevice struct {
	MAC     string
	DevPath string
	Nodes   []string
	Battery *hidBattery
}

// hidBattery is one entry of the kernel's power supply class. Name is
// the entry's directory name, which the Peripheral reports as the source
// of the level. Charging is nil when the status is not one that
// chargingFrom recognizes.
type hidBattery struct {
	Name       string
	Percentage int
	Charging   *bool
}

// discoverHIDDevices returns the Bluetooth HID devices on adapter, with
// their evdev nodes, sorted by DevPath so that two walks of the same
// hardware compare equal. A zero adapter returns the devices on every
// adapter. A device whose HID_UNIQ is not a MAC address is skipped,
// because a claim selects a device by its address.
func discoverHIDDevices(sysRoot string, adapter bonds.Address) []hidDevice {
	entries, err := os.ReadDir(filepath.Join(sysRoot, "bus", "hid", "devices"))
	if err != nil {
		// bus/hid does not exist until the first HID device registers.
		return nil
	}
	var devices []hidDevice
	for _, entry := range entries {
		resolved, err := filepath.EvalSymlinks(filepath.Join(sysRoot, "bus", "hid", "devices", entry.Name()))
		if err != nil {
			continue
		}
		values := readUeventFile(filepath.Join(resolved, "uevent"))
		bus, _, _ := strings.Cut(values["HID_ID"], ":")
		if bus != busBluetooth {
			continue
		}
		mac := normalizeMAC(values["HID_UNIQ"])
		if !validMAC(mac) {
			continue
		}
		if !onOperatorAdapter(values["HID_PHYS"], adapter) {
			continue
		}
		devices = append(devices, hidDevice{
			MAC:     mac,
			DevPath: devPath(sysRoot, resolved),
			Nodes:   evdevNodes(resolved),
			Battery: powerSupply(resolved),
		})
	}
	slices.SortFunc(devices, func(a, b hidDevice) int {
		return strings.Compare(a.DevPath, b.DevPath)
	})
	return devices
}

// onOperatorAdapter reports whether phys, a device's HID_PHYS, is
// adapter. It returns true when adapter is zero or phys does not parse,
// so a machine with one adapter keeps a device that reports no HID_PHYS.
func onOperatorAdapter(phys string, adapter bonds.Address) bool {
	if adapter.IsZero() {
		return true
	}
	local, err := bonds.ParseAddress(phys)
	if err != nil {
		return true
	}
	return local == adapter
}

// nodesByMAC returns the evdev nodes of the devices, keyed by MAC
// address. A controller can register more than one HID device, and a
// DualSense registers a second one for its motion sensors, so a claim on
// a controller gets the nodes of all of them.
func nodesByMAC(devices []hidDevice) map[string][]string {
	nodes := map[string][]string{}
	for _, device := range devices {
		nodes[device.MAC] = append(nodes[device.MAC], device.Nodes...)
	}
	for mac := range nodes {
		slices.Sort(nodes[mac])
		nodes[mac] = slices.Compact(nodes[mac])
	}
	return nodes
}

// kernelBatteries returns the battery of each device that has one,
// keyed by the device's address.
func kernelBatteries(devices []hidDevice) map[bonds.Address]*hidBattery {
	batteries := map[bonds.Address]*hidBattery{}
	for _, device := range devices {
		if device.Battery == nil {
			continue
		}
		address, err := bonds.ParseAddress(device.MAC)
		if err != nil {
			continue
		}
		batteries[address] = device.Battery
	}
	return batteries
}

// powerSupply returns the first power supply under a HID device whose
// capacity is from 0 to 100. The CRD limits the percentage to that
// range, and the API server rejects a status write with a value outside
// it.
func powerSupply(dir string) *hidBattery {
	supplies := filepath.Join(dir, "power_supply")
	entries, err := os.ReadDir(supplies)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		percentage, err := strconv.Atoi(readSysfsValue(filepath.Join(supplies, entry.Name(), "capacity")))
		if err != nil || percentage < 0 || percentage > 100 {
			continue
		}
		return &hidBattery{
			Name:       entry.Name(),
			Percentage: percentage,
			Charging:   chargingFrom(readSysfsValue(filepath.Join(supplies, entry.Name(), "status"))),
		}
	}
	return nil
}

// chargingFrom converts the status file of a power supply. It returns
// nil for Unknown and for any other value it does not recognize.
func chargingFrom(status string) *bool {
	charging, discharging := true, false
	switch status {
	case "Charging", "Full":
		return &charging
	case "Discharging", "Not charging":
		return &discharging
	}
	return nil
}

func readSysfsValue(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// evdevNodes returns the sorted /dev paths of the evdev nodes below a
// HID device's directory. A directory with a dev file is a device node,
// and DEVNAME in its uevent file is its path under /dev.
func evdevNodes(dir string) []string {
	var nodes []string
	_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return nil
		}
		if _, err := os.Stat(filepath.Join(path, "dev")); err != nil {
			return nil
		}
		devname := readUeventFile(filepath.Join(path, "uevent"))["DEVNAME"]
		if !strings.HasPrefix(devname, "input/event") {
			return nil
		}
		nodes = append(nodes, "/dev/"+devname)
		return nil
	})
	slices.Sort(nodes)
	return nodes
}

// devPath returns a sysfs directory in the form of a uevent's DEVPATH:
// the path below the sysfs mount, with a leading slash.
func devPath(sysRoot, resolved string) string {
	relative, err := filepath.Rel(sysRoot, resolved)
	if err != nil {
		return resolved
	}
	return "/" + relative
}

// readUeventFile parses a uevent file of KEY=VALUE lines. It returns an
// empty map when the file is missing, because a device can disappear
// between the directory listing and this read.
func readUeventFile(path string) map[string]string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	values := map[string]string{}
	for line := range strings.Lines(string(raw)) {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found {
			values[key] = value
		}
	}
	return values
}
