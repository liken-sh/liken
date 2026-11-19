package main

// When a claim ends, the operator binds kernel drivers to the claim's
// USB interfaces again.
//
// A program that drives a USB device through libusb, such as Network
// UPS Tools with a UPS that usbhid would drive, detaches the kernel
// driver from the interface so that it can claim the interface
// itself. When the program stops, the kernel does not bind a driver
// to the interface again. liken publishes only hardware that has a
// driver (dra.go), so the device drops out of the node's ResourceSlice
// and stays out until it is unplugged or the machine reboots.
//
// The rebind runs only in unprepare. While a pod runs, the program in
// the pod has claimed the interface through usbfs, and binding a kernel
// driver then would take the device away from the program. The
// kubelet calls unprepare after the last pod of the claim stops.
//
// The rebind writes the interface's address, such as "3-4:1.0", to the
// USB bus's drivers_probe attribute, and the kernel matches every
// registered USB driver against the interface as it does when the
// device is plugged in. Writing to one driver's bind attribute would
// need the driver's name, and sysfs does not record which driver was
// detached.
//
// A failure prints a line and never fails unprepare. If unprepare
// returned an error, the kubelet would retry it and the pod would stay
// Terminating, and a device without a driver affects only the next
// pod that claims it.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/liken-sh/liken/liken/hardware"
)

// rebindClaimDevices rebinds a kernel driver to each USB interface
// that the claim was allocated.
//
// The unprepare request has only the claim's UID, and the
// ResourceClaim can already be deleted, so the devices come from the
// claim's CDI spec file. prepare names each CDI device
// "<claim UID>-<allocated name>". When the file is missing, an earlier
// unprepare already finished, and there is nothing to rebind.
//
// devices returns the sysfs inventory. It is called only after the
// spec file is read, so a repeated unprepare does not walk sysfs.
func rebindClaimDevices(claimUID string, devices func() map[string]hardware.Device) {
	spec, err := claimSpec(claimUID)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "dra: reading the CDI spec of claim %s to rebind its drivers: %v\n", claimUID, err)
		return
	}
	for _, granted := range spec.Devices {
		allocated, ok := strings.CutPrefix(granted.Name, claimUID+"-")
		if !ok {
			continue
		}
		if _, virtual := virtualizationNamed(allocated); virtual {
			// A virtualization device has no hardware to rebind.
			continue
		}
		device, ok := resolveHardware(allocated, devices())
		if !ok {
			fmt.Fprintf(os.Stderr, "dra: claim %s held device %s, which this machine does not publish now\n",
				claimUID, allocated)
			continue
		}
		if err := rebindInterface(device); err != nil {
			fmt.Fprintf(os.Stderr, "dra: rebinding a driver to %s: %v\n", device.Address, err)
		}
	}
}

// claimSpec reads one claim's CDI spec file, holding the lock that
// every writer of these files takes.
func claimSpec(claimUID string) (cdiSpec, error) {
	cdiWrites.Lock()
	defer cdiWrites.Unlock()
	raw, err := os.ReadFile(cdiSpecPath(claimUID))
	if err != nil {
		return cdiSpec{}, err
	}
	var spec cdiSpec
	err = json.Unmarshal(raw, &spec)
	return spec, err
}

// resolveHardware returns the physical device for an allocated device
// name. A companion device is named as its parent plus "-" and a
// suffix, so a name that is not in byName resolves to the longest
// device name that is a prefix of it. The longest match wins because
// one device's name can be the start of another's.
func resolveHardware(name string, byName map[string]hardware.Device) (hardware.Device, bool) {
	if device, ok := byName[name]; ok {
		return device, true
	}
	var match string
	var found hardware.Device
	for bare, device := range byName {
		if !strings.HasPrefix(name, bare+"-") || len(bare) <= len(match) {
			continue
		}
		match, found = bare, device
	}
	return found, match != ""
}

// rebindInterface makes the kernel match a driver to one USB
// interface. It writes nothing for a device on another bus, for a
// whole USB device, whose address has no colon, or for an interface
// that still has its driver.
func rebindInterface(device hardware.Device) error {
	if device.Bus != "usb" || !strings.Contains(device.Address, ":") || device.Driver != "" {
		return nil
	}
	// Opening without O_CREATE fails when the kernel has no USB
	// support, and then there is no interface to rebind.
	probe, err := os.OpenFile(filepath.Join(draSysfsRoot, "bus", "usb", "drivers_probe"), os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if _, err := probe.WriteString(device.Address); err != nil {
		probe.Close()
		return err
	}
	return probe.Close()
}
