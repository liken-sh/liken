package main

// Binding the kernel driver again after a claim ends.
//
// A program that communicates with a USB device through libusb
// cannot share an interface with a kernel driver. The kernel binds
// one driver to an interface at a time, so libusb detaches the kernel
// driver first and claims the interface for itself. Network UPS Tools does this to
// a UPS that usbhid binds. When the program stops, the interface
// keeps no driver, because nothing in the kernel binds one again.
//
// An interface with no driver leaves the node's ResourceSlice, since
// liken publishes driven hardware only (dra.go gives the rule). The
// driver's nodes leave with it, so the next pod's prepare fails: the
// allocated name matches nothing this machine publishes now. Without
// a repair, the device stays out of the inventory until somebody
// unplugs it and plugs it in again, or reboots the machine.
//
// Unprepare is the one safe place for the repair. While a pod runs,
// an interface with no driver under a prepared claim is the correct
// state, and it is the state the program in the pod needs. A
// reconcile pass that bound a driver to every interface with none
// would take the interface away from a running program. The kubelet
// calls unprepare after the last pod that holds the claim stops, so
// the grant is over at that moment and the hardware belongs to the
// machine again.
//
// The repair writes the interface's address to the USB bus's
// drivers_probe attribute, for example "3-4:1.0". A write to one
// driver's bind attribute would need that driver's name, and the walk
// reports no driver here, because the interface has none. Sysfs keeps
// no record of the driver an interface had. drivers_probe runs the
// kernel's own match over every registered USB driver, the same match
// that runs at enumeration, so the interface ends in the state a
// fresh boot gives it.
//
// Every failure here prints one line, and the repair stops. An error
// in the unprepare answer makes the kubelet call unprepare again, and
// the pod stays in Terminating until a call succeeds. A device that
// keeps no driver is a problem for the next pod that claims it, and
// it must never block the deletion of a pod that is already finished.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/liken-sh/liken/hardware"
)

// rebindClaimDevices binds a kernel driver again to each USB
// interface that one claim held.
//
// The kubelet's unprepare call carries the claim's UID and nothing
// else, and the claim can be deleted before the call arrives, so a
// read from the API server can answer nothing. The claim's own CDI
// spec file is the durable record. Prepare names each CDI device
// "<claim UID>-<allocated name>", so the file holds every device name
// the claim was allocated. A file that is already gone belongs to an
// unprepare that succeeded, and the retry needs no repair.
//
// devices supplies the sysfs walk. The caller shares one walk across
// every claim in a request, and builds it only when a claim has a
// spec file to act on.
func rebindClaimDevices(claimUID string, devices func() map[string]hardware.Device) {
	spec, err := claimSpec(claimUID)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "dra: reading claim %s to bind its drivers again: %v\n", claimUID, err)
		return
	}
	for _, granted := range spec.Devices {
		allocated, ok := strings.CutPrefix(granted.Name, claimUID+"-")
		if !ok {
			continue
		}
		device, ok := resolveHardware(allocated, devices())
		if !ok {
			fmt.Fprintf(os.Stderr, "dra: claim %s held device %s, which this machine does not publish now\n",
				claimUID, allocated)
			continue
		}
		if err := rebindInterface(device); err != nil {
			fmt.Fprintf(os.Stderr, "dra: binding a driver to %s again: %v\n", device.Address, err)
		}
	}
}

// claimSpec reads one claim's CDI spec, under the lock that every
// writer of these files takes.
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

// resolveHardware maps an allocated device name back to the physical
// device it names. The rules are the ones resolveAllocated applies: a
// bare name is a device's own name, and every other name is a
// companion, which is a bare name plus a suffix. A companion is one
// wire of its parent, on the same physical device, so both names
// answer with the same hardware. The longest bare name that fits
// wins, because one device's name can be the start of another's.
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

// rebindInterface asks the kernel to match a driver to one USB
// interface again.
//
// It writes nothing in three cases. A device on another bus has no
// drivers_probe of this kind. An address with no colon names a whole
// USB device rather than one of its interfaces, and drivers bind to
// interfaces. A device that reports a driver has one bound now, which
// is what a pod that never detached the driver leaves behind.
func rebindInterface(device hardware.Device) error {
	if device.Bus != "usb" || !strings.Contains(device.Address, ":") || device.Driver != "" {
		return nil
	}
	// The attribute exists for as long as the bus does, so the open
	// creates nothing. A tree with no drivers_probe is a kernel
	// without USB support, and there is no interface to repair.
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
