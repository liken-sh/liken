package main

// machine-operator publishes each machine's devices in one
// ResourceSlice, with the driver liken.sh. The operator reads the
// devices of every such slice, because a bare chip that appears on a
// machine is a new device in that machine's slice. Each USB device
// carries its port path in address, and the numbers that the kernel
// gave it on its bus in busNumber and deviceNumber.

import (
	"fmt"

	resourceapi "k8s.io/api/resource/v1"
)

// driverName is the driver of the slices that machine-operator
// publishes, and the domain of their attributes.
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

// sliceDevice holds its attributes in k8s.io/api's own type, because
// that is the type the CEL compiler of a DeviceClass selector reads.
type sliceDevice struct {
	Name       string                                                    `json:"name"`
	Attributes map[resourceapi.QualifiedName]resourceapi.DeviceAttribute `json:"attributes,omitempty"`
}

// appearance is one device on one machine, from the time it
// enumerates until it leaves. The kernel gives a device a new device
// number each time it enumerates, so the machine, the address, and the
// device number together identify one appearance.
type appearance struct {
	machine      string
	address      string
	deviceNumber int64
}

func (a appearance) port() port { return port{machine: a.machine, address: a.address} }

// port is one place where a device connects: a machine and the address
// of a USB port on it. A status has one entry for each port.
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
// CDI spec delivers the node to a pod at the same path.
func (d device) usbfsPath() string {
	return fmt.Sprintf("/dev/bus/usb/%03d/%03d", d.busNumber, d.deviceNumber)
}

// devicesOf answers the devices of a slice that an upload can reach: a
// device with an address, a bus number, and a device number, on a slice
// that names its node. Any other device has no usbfs node to write to.
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
