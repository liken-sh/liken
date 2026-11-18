package main

// machine-operator publishes each machine's devices in a ResourceSlice
// with the driver liken.sh. When a bare chip is plugged in, it shows up
// as a new device in that machine's slice. A USB device has its port
// path in the attribute address, and the bus number and device number
// that the kernel assigned it in busNumber and deviceNumber.

import (
	"crypto/sha256"
	"fmt"
	"strings"

	resourceapi "k8s.io/api/resource/v1"
)

// driverName is the driver of machine-operator's slices and the domain
// of their attributes.
const driverName = "liken.sh"

type resourceSlice struct {
	Metadata struct {
		Name string `json:"name"`
		UID  string `json:"uid,omitempty"`
	} `json:"metadata"`
	Spec struct {
		Driver   string `json:"driver"`
		NodeName string `json:"nodeName,omitempty"`
		Pool     struct {
			Generation int64 `json:"generation"`
		} `json:"pool"`
		Devices []sliceDevice `json:"devices,omitempty"`
	} `json:"spec"`
}

// sliceDevice keeps its attributes in k8s.io/api's type, because the
// CEL compiler for DeviceClass selectors reads that type.
type sliceDevice struct {
	Name       string                                                    `json:"name"`
	Attributes map[resourceapi.QualifiedName]resourceapi.DeviceAttribute `json:"attributes,omitempty"`
}

// appearance is one device on one machine, from when it enumerates
// until it leaves the bus. The kernel assigns a new device number each
// time a device enumerates, so the machine, the address, and the device
// number together identify one appearance.
type appearance struct {
	machine      string
	address      string
	deviceNumber int64
}

func (a appearance) port() port { return port{machine: a.machine, address: a.address} }

// port is a machine and the address of one USB port on it. A rule's
// status has one entry for each port.
type port struct {
	machine string
	address string
}

func (p port) String() string { return p.address + " on " + p.machine }

// device is one USB device that an upload can reach.
type device struct {
	appearance
	name       string
	busNumber  int64
	attributes map[resourceapi.QualifiedName]resourceapi.DeviceAttribute
}

// usbfsPath is the device's node under /dev/bus/usb. machine-operator's
// CDI spec makes the node available in a pod at the same path.
func (d device) usbfsPath() string {
	return fmt.Sprintf("/dev/bus/usb/%03d/%03d", d.busNumber, d.deviceNumber)
}

// devicesOf returns the devices of a slice that have an address, a bus
// number, and a device number, in a slice that has a nodeName. Any
// other device has no usbfs node for an upload to write to.
func devicesOf(slice resourceSlice) []device {
	if slice.Spec.Driver != driverName || slice.Spec.NodeName == "" {
		return nil
	}
	var out []device
	for _, item := range slice.Spec.Devices {
		address := item.Attributes["address"].StringValue
		bus := item.Attributes["busNumber"].IntValue
		number := item.Attributes["deviceNumber"].IntValue
		if address == nil || bus == nil || number == nil {
			continue
		}
		out = append(out, device{
			appearance: appearance{machine: slice.Spec.NodeName, address: *address, deviceNumber: *number},
			name:       item.Name,
			busNumber:  *bus,
			attributes: item.Attributes,
		})
	}
	return out
}

// deviceSetHash returns a hash of a machine's device names. A new hash
// starts a scan. A device that is added or removed changes the hash. A
// device replugged in the same port comes back with the same name and
// the same hash, because the device number is an attribute and not part
// of the name.
func deviceSetHash(names []string) string {
	sum := sha256.Sum256([]byte(strings.Join(sortedSet(names), "\x00")))
	return shortHex(sum[:])
}
