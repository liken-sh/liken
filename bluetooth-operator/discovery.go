package main

// Finding a connected controller's evdev nodes in sysfs.
//
// A paired controller becomes a HID device when it connects, and the
// HID device registers the input nodes that a consumer's pod
// receives. The walk starts at /sys/bus/hid/devices, which lists
// every HID device the kernel has, and keeps the ones whose bus type
// is 0005, BUS_BLUETOOTH.
//
// The walk must not filter by sysfs ancestry to the adapter. BlueZ
// 5.73 and later run the input plugin in uhid mode by default, and a
// uhid device's parent is /sys/devices/virtual/misc/uhid, which has
// no ancestry to hci0 at all. An ancestry filter finds nothing on a
// current BlueZ, and it finds everything on an older one, so the bus
// type is the test that works under both arrangements.
//
// HID_UNIQ in the device's uevent file holds the peer MAC address,
// which is the identity the ResourceSlice publishes. HID_PHYS holds
// the local adapter address the controller connects through, and the
// walk keeps a device only when HID_PHYS names the adapter this
// operator holds. A machine with a second adapter registers HID
// devices on it too, and those belong to another operator. A device
// with no HID_PHYS, or one that does not parse, is kept, so a
// single-adapter machine never regresses.
//
// The node paths come from DEVNAME, the same way liken's own delivery
// walk reads them. DEVNAME for an evdev node is input/eventN, so the
// node is /dev/input/eventN. joydev registers input/jsN through the
// same subsystem, and the DEVNAME prefix leaves it out: the
// kernel's own documentation calls joydev legacy, liken's kernel may
// not enable CONFIG_INPUT_JOYDEV at all, and joydev publishes a
// DualSense's motion sensors as a second jsN device.

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

// draSysfsRoot is the sysfs mount this driver reads: this walk reads
// it for a controller's real nodes, and uinput.go reads it for the
// node the kernel gave a virtual device. The pod runs in the host's
// network namespace and reads the host's own sysfs. It is a variable
// so the tests can point it at a tree they built.
var draSysfsRoot = "/sys"

// busBluetooth is BUS_BLUETOOTH from the kernel's input.h, as the
// first field of HID_ID spells it.
const busBluetooth = "0005"

// hidDevice is one Bluetooth HID device as sysfs shows it now.
// DevPath is the path below sysfs, the same string a uevent carries
// as DEVPATH, so an add event and a walk name the same device the
// same way.
type hidDevice struct {
	MAC     string
	DevPath string
	Nodes   []string
}

// discoverHIDDevices lists the Bluetooth HID devices on the operator's
// own adapter, with the evdev nodes each one registers. The result is
// sorted by DevPath, so the same hardware always produces the same list
// and the slice comparison reports real changes only.
//
// adapter is the address of the radio this operator holds. A device on
// another adapter is left out. A zero adapter turns the filter off, so
// a caller that has no adapter address keeps every device, which is the
// single-adapter machine's behavior.
//
// A HID device with no valid HID_UNIQ is skipped. Bluetooth HID
// always has one, and a device without it has no identity that a
// claim could name.
func discoverHIDDevices(sysRoot string, adapter bonds.Address) []hidDevice {
	entries, err := os.ReadDir(filepath.Join(sysRoot, "bus", "hid", "devices"))
	if err != nil {
		// No HID bus means no controller has ever connected on this
		// boot. That is an ordinary state, not a failure.
		return nil
	}
	var devices []hidDevice
	for _, entry := range entries {
		// The bus entry is a symlink into the devices tree. The walk
		// needs the real directory, so that its children are also real
		// directories.
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
		})
	}
	slices.SortFunc(devices, func(a, b hidDevice) int {
		return strings.Compare(a.DevPath, b.DevPath)
	})
	return devices
}

// onOperatorAdapter reports whether a HID device's HID_PHYS names the
// adapter this operator holds. A zero adapter turns the test off, and a
// HID_PHYS that is absent or does not parse passes it, so the filter
// only ever removes a device it can place on a different adapter. Both
// sides parse into a bonds.Address, so the comparison ignores case and
// the colon or dash form, the same way an address comparison does
// everywhere else.
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

// nodesByMAC collects the evdev nodes of every HID device, keyed by
// the controller that registered them. One controller can register
// more than one HID device, and a DualSense registers a second input
// device for its motion sensors, so a claim on one controller
// receives every node under that address.
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

// evdevNodes walks one HID device's subtree and returns the evdev
// nodes below it. A directory that holds a `dev` file registers a
// node, and its uevent file names the node's path under /dev.
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

// devPath returns a resolved sysfs directory as the kernel spells it
// in a uevent's DEVPATH: the path below the sysfs mount point, with a
// leading slash.
func devPath(sysRoot, resolved string) string {
	relative, err := filepath.Rel(sysRoot, resolved)
	if err != nil {
		return resolved
	}
	return "/" + relative
}

// readUeventFile reads a sysfs uevent file, whose lines are KEY=VALUE.
// A missing file gives an empty map, because a device can disappear
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
