package main

// The operator publishes one ResourceSlice for its node. The slice
// lists a device for each paired controller and, once the adapter has
// been read, a media bus device. A claim on the media bus lets a sound
// server connect to this pod's bluetoothd.
//
// The slice is published under this operator's driver name, next to
// the slice that liken's machine-operator publishes for the same node.
// The two do not collide, because a device is identified by
// <driver>/<pool>/<device> and the slice name ends with the driver
// name: <node>-liken.sh and <node>-bluetooth.liken.sh.
//
// These structs cover only the fields of the ResourceSlice API that
// this program writes. A paired controller needs a name, attributes
// that identify it, and a taint while it is disconnected. It needs no
// partitionable devices, shared counters, or per-device node selection.
//
// The pool of each node has one slice, so ResourceSliceCount is always
// 1 and each change increments the pool generation.

import (
	"encoding/json"
	"net/http"
	"reflect"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// DriverName is the DRA driver name of this operator. A device
// operator's driver name is <domain>.liken.sh, named for the kind of
// device it serves.
const DriverName = "bluetooth.liken.sh"

// ResourceSlicesPath is the API path of ResourceSlices, which are
// cluster-scoped.
const ResourceSlicesPath = "/apis/resource.k8s.io/v1/resourceslices"

// maxSliceDevices is the API's limit on devices in one slice. The API
// allows 128 devices, or 64 when any device in the slice has a taint.
// This operator taints each controller that is disconnected, so the
// limit of 64 applies.
const maxSliceDevices = 64

type ResourceSlice struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   ResourceSliceMeta `json:"metadata"`
	Spec       ResourceSliceSpec `json:"spec"`
}

type ResourceSliceMeta struct {
	Name            string           `json:"name"`
	ResourceVersion string           `json:"resourceVersion,omitempty"`
	OwnerReferences []OwnerReference `json:"ownerReferences,omitempty"`
}

// OwnerReference makes the API server's garbage collector delete one
// object when another is deleted. The UID is part of it, so a Node
// that is deleted and registered again under the same name does not
// inherit the old Node's slices.
type OwnerReference struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
}

type ResourceSliceSpec struct {
	Driver   string        `json:"driver"`
	Pool     ResourcePool  `json:"pool"`
	NodeName string        `json:"nodeName,omitempty"`
	Devices  []SliceDevice `json:"devices,omitempty"`
}

type ResourcePool struct {
	Name               string `json:"name"`
	Generation         int64  `json:"generation"`
	ResourceSliceCount int64  `json:"resourceSliceCount"`
}

// SliceDevice is one claimable device: a paired controller, or the
// adapter's media bus. The name must be a DNS label and unique within
// the pool. An attribute name with no domain belongs to the driver's
// domain, so a CEL selector reads them as
// device.attributes["bluetooth.liken.sh"].address.
type SliceDevice struct {
	Name       string                     `json:"name"`
	Attributes map[string]DeviceAttribute `json:"attributes,omitempty"`
	Taints     []DeviceTaint              `json:"taints,omitempty"`
}

// DeviceAttribute holds exactly one of four typed values. The API
// types attributes so that a selector compares a boolean as a boolean
// and not as the string "true".
type DeviceAttribute struct {
	Bool    *bool   `json:"bool,omitempty"`
	Int     *int64  `json:"int,omitempty"`
	String  *string `json:"string,omitempty"`
	Version *string `json:"version,omitempty"`
}

// DeviceTaint keeps new claims off a device. With the effect NoExecute
// it also evicts the pods of claims that already hold the device.
type DeviceTaint struct {
	Key       string `json:"key"`
	Value     string `json:"value,omitempty"`
	Effect    string `json:"effect"`
	TimeAdded string `json:"timeAdded,omitempty"`
}

// AttrString returns a string attribute value.
func AttrString(s string) DeviceAttribute { return DeviceAttribute{String: &s} }

// AttrBool returns a boolean attribute value.
func AttrBool(b bool) DeviceAttribute { return DeviceAttribute{Bool: &b} }

// AttrInt returns an integer attribute value.
func AttrInt(i int64) DeviceAttribute { return DeviceAttribute{Int: &i} }

// sameDevices reports whether the published devices equal the devices
// this pass computed.
//
// It ignores TimeAdded. The API server sets it on every taint it
// stores, and this operator never does, so a plain comparison would
// find a difference on every pass and rewrite the slice every time.
// Each ResourceSlice write makes the scheduler try again to place
// every pod that waits for a DRA allocation.
func sameDevices(published, current []SliceDevice) bool {
	return reflect.DeepEqual(withoutTimeAdded(published), withoutTimeAdded(current))
}

// withoutTimeAdded returns a copy of the devices with every taint's
// TimeAdded cleared. The caller's devices are not modified.
func withoutTimeAdded(devices []SliceDevice) []SliceDevice {
	out := make([]SliceDevice, len(devices))
	for i, device := range devices {
		out[i] = device
		out[i].Taints = make([]DeviceTaint, len(device.Taints))
		for j, taint := range device.Taints {
			taint.TimeAdded = ""
			out[i].Taints[j] = taint
		}
		if len(device.Taints) == 0 {
			out[i].Taints = nil
		}
	}
	return out
}

// EnsureResourceSlice makes the slice in the API server match the
// devices. It creates the slice when none exists, replaces it when the
// devices differ, deletes it when the device list is empty, and writes
// nothing when the devices are the same.
//
// The list is empty only while no adapter has been read and no
// controller is paired. Once an adapter has been read, the list always
// holds its media bus, so unpairing the last controller shrinks the
// slice to the bus and does not delete it.
//
// The Node owns the slice, not the operator's pod. The pod restarts
// while claims stay prepared, and a slice deleted on each restart
// would break every pod that uses a controller. When a Node is
// deleted, the garbage collector deletes the slice.
//
// The replacement carries the resourceVersion from the read. If
// another writer changed the slice in between, the API server rejects
// the write and the function returns apiclient.ErrConflict. The next
// pass reads the slice again and retries.
func EnsureResourceSlice(c *apiclient.Client, nodeName string, owner OwnerReference, devices []SliceDevice) error {
	name := sliceName(nodeName)
	path := ResourceSlicesPath + "/" + name

	// This is a GET on every pass instead of a watch. A watch needs
	// the list and watch verbs, and RBAC cannot limit them to one
	// slice, so it would let this operator read every other driver's
	// slices (see deploy/rbac.yaml).
	current, err := apiclient.Get[ResourceSlice](c, path)
	if err == apiclient.ErrNotFound {
		if len(devices) == 0 {
			return nil
		}
		slice := &ResourceSlice{
			APIVersion: "resource.k8s.io/v1",
			Kind:       "ResourceSlice",
			Metadata: ResourceSliceMeta{
				Name:            name,
				OwnerReferences: []OwnerReference{owner},
			},
			Spec: ResourceSliceSpec{
				Driver:   DriverName,
				NodeName: nodeName,
				Pool:     ResourcePool{Name: nodeName, Generation: 1, ResourceSliceCount: 1},
				Devices:  devices,
			},
		}
		body, err := json.Marshal(slice)
		if err != nil {
			return err
		}
		if err := c.RequestJSON(http.MethodPost, ResourceSlicesPath, body, nil); err != nil {
			return err
		}
		sliceLog.created(1, devices)
		return nil
	}
	if err != nil {
		return err
	}

	if len(devices) == 0 {
		if err := DeleteResourceSlice(c, nodeName); err != nil {
			return err
		}
		sliceLog.deletedSlice()
		return nil
	}
	if sameDevices(current.Spec.Devices, devices) {
		sliceLog.unchangedSlice(current.Spec.Pool.Generation, devices)
		return nil
	}

	// Keep the published devices before the assignment below replaces
	// them, because sliceLog.wrote reports the difference.
	published := current.Spec.Devices
	generation := current.Spec.Pool.Generation + 1

	current.Spec.NodeName = nodeName
	current.Spec.Driver = DriverName
	current.Spec.Pool = ResourcePool{
		Name:               nodeName,
		Generation:         generation,
		ResourceSliceCount: 1,
	}
	current.Spec.Devices = devices
	body, err := json.Marshal(current)
	if err != nil {
		return err
	}
	if err := c.RequestJSON(http.MethodPut, path, body, nil); err != nil {
		return err
	}
	sliceLog.wrote(generation, published, devices)
	return nil
}

// DeleteResourceSlice deletes this node's slice, and succeeds if it is
// already gone. EnsureResourceSlice calls it when no adapter has been
// read and no controller is paired.
//
// The operator does not call it at shutdown. The pod restarts for
// ordinary reasons, such as a new image or a node drain, while a
// consumer holds a prepared claim, and deleting the slice then would
// break that consumer. To uninstall the operator, delete the slice by
// name, as the install guide describes.
func DeleteResourceSlice(c *apiclient.Client, nodeName string) error {
	err := c.RequestJSON(http.MethodDelete, ResourceSlicesPath+"/"+sliceName(nodeName), nil, nil)
	if err == apiclient.ErrNotFound {
		return nil
	}
	return err
}

func sliceName(nodeName string) string {
	return nodeName + "-" + DriverName
}

// nodeObject is the part of a Node that this operator reads: the UID
// for the slice's owner reference.
type nodeObject struct {
	Metadata struct {
		Name string `json:"name"`
		UID  string `json:"uid"`
	} `json:"metadata"`
}

// NodeOwner reads this operator's Node and returns the owner reference
// for its slice.
func NodeOwner(c *apiclient.Client, nodeName string) (OwnerReference, error) {
	node, err := apiclient.Get[nodeObject](c, "/api/v1/nodes/"+nodeName)
	if err != nil {
		return OwnerReference{}, err
	}
	return OwnerReference{
		APIVersion: "v1",
		Kind:       "Node",
		Name:       node.Metadata.Name,
		UID:        node.Metadata.UID,
	}, nil
}
