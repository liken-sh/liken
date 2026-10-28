package main

// Tainting a device that a prepared claim held when it was unplugged.
//
// Kubernetes allocates a claim once, and does not allocate it again
// while a pod reserves it. A claim on a USB device names the device's
// port, so when someone moves the cable to another port or another
// machine, the claim still names the old port, and its pod fails on
// device-absent until a person deletes the pod. The repair that
// Kubernetes offers is to end the pod: when a claim's last pod is gone,
// the controller manager clears the allocation, and the next pod that
// names the claim is allocated again by its selector, wherever the
// device is now.
//
// So while a prepared claim holds a device that was unplugged, the
// slice publishes the device's name with a NoExecute taint, and the
// taint-eviction controller deletes the claim's pods. A pod's
// controller creates it again. A claim whose program follows its
// hardware by itself, such as PipeWire over every sound card of a
// node, tolerates the taint in its request, and keeps running.
//
// The record of the unplug is in the claim's CDI spec file, because
// the file is already the one record of what the kubelet prepared on
// this machine, and it lasts exactly one boot. prepare writes the
// identity of each allocated unit beside its nodes. The refresh marks
// a device as unplugged when the unit at the recorded sysfs address is
// gone, or is not the unit that prepare recorded. A new enumeration in
// the same port counts, because the claim's running container still
// holds the nodes of the old one.
//
// The mark stays until the kubelet unprepares the claim, even when the
// device comes back to its port, because a container that kept
// running still holds the old nodes. The refresh still writes the
// device's current nodes into the spec, so a pod that tolerates the
// taint receives them at its next container start, as it did before
// the taint existed.

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
// claim held when it was unplugged. A claim's request tolerates it by
// this key.
const unpluggedTaint = kubernetes.DriverName + "/unplugged"

// The annotations on a CDI device that this operator writes. CDI
// carries them in the spec and does not give them to the container.
// unitAnnotation holds the unit that prepare resolved, claimAnnotation
// the claim's namespace and name for the Event, and
// unpluggedAnnotation the mark that the refresh writes once.
const (
	unitAnnotation      = kubernetes.DriverName + "/unit"
	claimAnnotation     = kubernetes.DriverName + "/claim"
	unpluggedAnnotation = kubernetes.DriverName + "/unplugged"
)

// unitRecord is what identifies one enumeration of a device at its
// sysfs address: the IDs and serial that the unit reports, and the
// numbers the bus gave it. A unit that enumerates again in the same
// port gets a new device number.
type unitRecord struct {
	Bus          string `json:"bus"`
	Address      string `json:"address"`
	Vendor       string `json:"vendor,omitempty"`
	Product      string `json:"product,omitempty"`
	Serial       string `json:"serial,omitempty"`
	BusNumber    int    `json:"busNumber,omitempty"`
	DeviceNumber int    `json:"deviceNumber,omitempty"`
}

// deviceAnnotations answers the annotations that prepare writes for one
// allocated device. Only a USB or a PCI device gets a unit record,
// because only those have an address under /sys/bus that leaves with
// the hardware. A device on the board cannot be unplugged.
//
// Only a claim that a pod owns gets the record, because only such a
// claim is allocated again after the eviction. A named claim outlives
// its pods. When its pod is evicted, the pod's controller creates the
// next pod at once, and that pod reserves the claim before the evicted
// pod releases it. The claim never has no pod, so the controller
// manager never clears its allocation, and the taint evicts each new
// pod in turn. A named claim on an unplugged device keeps
// device-absent, and a person repairs it.
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

// markUnplugged writes the mark on one CDI device when its unit has
// left, and answers whether it wrote it. A device already marked, and a
// device whose spec has no unit record, are left alone. A spec that an
// older operator wrote has no record, and the kubelet prepares it again
// at the next boot.
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

// left answers whether the unit is gone from its address. The walk can
// miss a device whose attribute it could not read, so only the absence
// of the sysfs directory, or a value that the walk read and that
// differs from the record, counts. A read that fails counts as present,
// because a wrong answer evicts a pod.
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

// differs answers whether a value the walk read is not the recorded
// one. A zero value on either side is a value nobody read.
func differs[T comparable](recorded, now T) bool {
	var zero T
	return recorded != zero && now != zero && recorded != now
}

// unpluggedMessage is the message of the Event that a mark posts.
func unpluggedMessage(allocated string, device cdiDevice) string {
	claim := device.Annotations[claimAnnotation]
	return fmt.Sprintf("device %s of claim %s was unplugged, and %s evicts the pods that hold the claim",
		allocated, claim, unpluggedTaint)
}

// unpluggedDevices answers the allocated names that a marked spec
// names, sorted and once each. Two claims can hold one shareable
// device.
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

// taintUnplugged adds the taint to each unplugged device. A device
// that came back keeps its attributes and takes the taint. A device
// that is gone is published by its name alone, so no DeviceClass
// selector matches it, and the taint still reaches the claims that
// hold it.
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
