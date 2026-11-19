package main

// When a device that a prepared claim holds is unplugged, the operator
// taints the device in the node's ResourceSlice.
//
// A claim on a USB device is allocated a device whose name comes from
// the port, and Kubernetes does not allocate a claim again while a pod
// reserves it. So when someone moves the cable to another port or
// machine, the claim keeps the old device, and its pod fails with
// device-absent until a person deletes the pod. Deleting the pod is
// what repairs it: a claim that a ResourceClaimTemplate made for a pod
// is deleted with the pod, and the replacement pod gets a new claim,
// which the scheduler allocates by its selector to wherever the device
// is now.
//
// So while a prepared claim holds a device that was unplugged, the
// ResourceSlice publishes that device's name with a NoExecute taint,
// and the taint-eviction controller deletes the claim's pods. The
// controller that owns each pod creates it again. A workload that finds
// its hardware again by itself, such as PipeWire across the sound cards
// of a node, tolerates the taint in its claim's request and keeps
// running.
//
// The operator records the unplug in the claim's CDI spec file under
// /var/run/cdi, which already describes what was prepared for the claim
// and is gone after a reboot. When the plugin prepares a claim, it
// writes a unitRecord for each USB or PCI device beside its device
// nodes. refreshCDISpecs marks a device unplugged when its sysfs
// address is gone, or when the device there is not the one that the
// record describes. A device that enumerates again in the same port
// counts as a different device, because the claim's running container
// still has the device nodes of the old enumeration.
//
// The mark stays until the kubelet unprepares the claim, even when the
// device returns to its port, because a container that kept running
// still has the old device nodes. refreshCDISpecs still writes the
// device's current nodes into the spec, so a pod that tolerates the
// taint gets them when its container next starts.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liken-sh/liken/liken/hardware"
	"github.com/liken-sh/liken/liken/kubernetes"
)

// unpluggedTaint is the key of the taint on a device that a prepared
// claim held when it was unplugged. A request tolerates the taint by
// this key.
const unpluggedTaint = kubernetes.DriverName + "/unplugged"

// The annotations that this operator writes on a CDI device. They stay
// in the spec file and do not reach the container. unitAnnotation is
// the unitRecord that prepareClaim wrote, claimAnnotation is the
// claim's namespace and name for the Event, and unpluggedAnnotation is
// the mark that refreshCDISpecs writes once.
const (
	unitAnnotation      = kubernetes.DriverName + "/unit"
	claimAnnotation     = kubernetes.DriverName + "/claim"
	unpluggedAnnotation = kubernetes.DriverName + "/unplugged"
)

// unitRecord identifies one enumeration of a device at its sysfs
// address: the IDs and serial that the device reports, and the bus and
// device numbers that the kernel assigned. A device that enumerates
// again in the same port gets a new device number.
type unitRecord struct {
	Bus          string `json:"bus"`
	Address      string `json:"address"`
	Vendor       string `json:"vendor,omitempty"`
	Product      string `json:"product,omitempty"`
	Serial       string `json:"serial,omitempty"`
	BusNumber    int    `json:"busNumber,omitempty"`
	DeviceNumber int    `json:"deviceNumber,omitempty"`
}

// deviceAnnotations returns the annotations that prepareClaim writes
// for one allocated device. Only a USB or PCI device gets a
// unitRecord, because only those have an address under /sys/bus that
// disappears with the hardware. A device on the board cannot be unplugged.
//
// Only a claim that a pod owns gets the record, because only such a
// claim is replaced after the eviction. A claim with its own name
// outlives its pods. When the taint evicts its pod, the pod's
// controller creates the next pod at once, and that pod reserves the
// claim before the evicted pod releases it. The claim then always has
// a pod, so the controller manager never clears its allocation, and
// the taint would evict each new pod in turn. Such a claim keeps
// failing with device-absent until a person repairs it.
func deviceAnnotations(namespace, name string, podOwned bool, d hardware.Device) map[string]string {
	annotations := map[string]string{claimAnnotation: namespace + "/" + name}
	if !podOwned || d.Bus != "usb" && d.Bus != "pci" {
		return annotations
	}
	raw, err := json.Marshal(unitRecord{
		Bus: d.Bus, Address: d.Address,
		Vendor: d.Vendor, Product: d.Product, Serial: d.Serial,
		BusNumber: d.BusNumber, DeviceNumber: d.DeviceNumber,
	})
	if err == nil {
		annotations[unitAnnotation] = string(raw)
	}
	return annotations
}

// markUnplugged writes the unplugged mark on one CDI device when its
// device has left, and returns whether it wrote the mark. It skips a
// device that is already marked and a device with no unitRecord. A
// spec that an older operator wrote has no record, and the next boot
// prepares the claim again with one.
func markUnplugged(device *cdiDevice, sysRoot string, byName map[string]hardware.Device) bool {
	if device.Annotations[unpluggedAnnotation] != "" {
		return false
	}
	var unit unitRecord
	if err := json.Unmarshal([]byte(device.Annotations[unitAnnotation]), &unit); err != nil || unit.Address == "" {
		return false
	}
	if !unit.left(sysRoot, byName) {
		return false
	}
	device.Annotations[unpluggedAnnotation] = "true"
	return true
}

// left returns whether the recorded device is gone from its address.
// The hardware walk can skip a device when it cannot read an
// attribute. So the device has left only when its sysfs directory does
// not exist, or when the walk read a value that differs from the
// record. Any other result counts as present, because a wrong answer
// evicts a pod.
func (u unitRecord) left(sysRoot string, byName map[string]hardware.Device) bool {
	_, err := os.Lstat(filepath.Join(sysRoot, "bus", u.Bus, "devices", u.Address))
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	for _, d := range byName {
		if d.Bus != u.Bus || d.Address != u.Address {
			continue
		}
		return differs(u.Vendor, d.Vendor) || differs(u.Product, d.Product) || differs(u.Serial, d.Serial) ||
			differs(u.BusNumber, d.BusNumber) || differs(u.DeviceNumber, d.DeviceNumber)
	}
	return false
}

// differs returns whether the value that the walk read is different
// from the recorded one. A zero value on either side means the value
// was never read, so it does not differ.
func differs[T comparable](recorded, now T) bool {
	var zero T
	return recorded != zero && now != zero && recorded != now
}

// unpluggedMessage is the message of the DeviceUnplugged Event.
func unpluggedMessage(allocated string, device cdiDevice) string {
	claim := device.Annotations[claimAnnotation]
	return fmt.Sprintf("Device %s of claim %s was unplugged, and the taint %s evicts the pods that use the claim",
		allocated, claim, unpluggedTaint)
}

// unpluggedDevices returns the allocated device names that have the
// unplugged mark in a CDI spec, sorted and without duplicates. Two
// claims can hold one shareable device.
func unpluggedDevices() []string {
	entries, err := os.ReadDir(cdiDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		claimUID, ok := claimUIDFromSpecName(entry.Name())
		if !ok {
			continue
		}
		spec, err := claimSpec(claimUID)
		if err != nil {
			continue
		}
		for _, device := range spec.Devices {
			allocated, ok := strings.CutPrefix(device.Name, claimUID+"-")
			if ok && device.Annotations[unpluggedAnnotation] != "" {
				names = append(names, allocated)
			}
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// taintUnplugged adds the taint to each unplugged device in the slice.
// A device that came back keeps its attributes and gets the taint. A
// device that is gone is published with only its name, so no
// DeviceClass selector matches it, and the taint still reaches the
// claims that hold it.
func taintUnplugged(devices []kubernetes.SliceDevice, unplugged []string) []kubernetes.SliceDevice {
	taint := kubernetes.DeviceTaint{Key: unpluggedTaint, Effect: "NoExecute"}
	for _, name := range unplugged {
		i := slices.IndexFunc(devices, func(d kubernetes.SliceDevice) bool { return d.Name == name })
		if i < 0 {
			devices = append(devices, kubernetes.SliceDevice{Name: name, Taints: []kubernetes.DeviceTaint{taint}})
			continue
		}
		devices[i].Taints = append(devices[i].Taints, taint)
	}
	return devices
}
