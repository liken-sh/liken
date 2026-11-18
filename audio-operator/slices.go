package main

// This file writes the node's audio devices to the API server as one
// ResourceSlice under this operator's driver name. liken publishes the
// node's sound card in its own slice under the driver liken.sh. The two
// cannot collide, because a device is identified by
// <driver>/<pool>/<device>, and each slice name ends with its driver
// name: <node>-liken.sh and <node>-audio.liken.sh.
//
// The structs below have only the fields of the upstream ResourceSlice
// API that this operator writes.
//
// Every device is in one slice, so a change is one write that
// increments pool.generation, and a reader never sees a pool that is
// half written.

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// DriverName is this operator's DRA driver name. A driver name is a DNS
// name, and the name of a device operator is <domain>.liken.sh.
const DriverName = "audio.liken.sh"

// ResourceSlicesPath is the API path of the ResourceSlice collection.
// ResourceSlices are cluster-scoped, so the path has no namespace.
const ResourceSlicesPath = "/apis/resource.k8s.io/v1/resourceslices"

// maxSliceDevices is the API limit on devices in one slice when any
// device has a taint. A slice with no taints can hold 128, but this
// operator taints every device that cannot play.
const maxSliceDevices = 64

// maxAttributeLength is the API limit on the length of a string
// attribute.
const maxAttributeLength = 64

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

// OwnerReference names the owner by UID as well as by name, so a Node
// that is deleted and registered again under the same name does not own
// the old Node's slice.
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

// SliceDevice is one claimable endpoint. Name must be a DNS label that
// is unique within the pool. A selector reads an attribute name with no
// domain under the driver's domain, as
// device.attributes["audio.liken.sh"].connectionType.
//
// AllowMultipleAllocations and Capacity are set together, and only on
// an endpoint whose resource declares a share count (shares.go).
type SliceDevice struct {
	Name                     string                     `json:"name"`
	Attributes               map[string]DeviceAttribute `json:"attributes,omitempty"`
	Capacity                 map[string]DeviceCapacity  `json:"capacity,omitempty"`
	AllowMultipleAllocations *bool                      `json:"allowMultipleAllocations,omitempty"`
	Taints                   []DeviceTaint              `json:"taints,omitempty"`
}

// DeviceCapacity is a quantity string. The operator sets no request
// policy, so a claim that does not request the capacity consumes all of
// it.
type DeviceCapacity struct {
	Value string `json:"value"`
}

// DeviceAttribute has exactly one of its fields set.
type DeviceAttribute struct {
	Bool    *bool   `json:"bool,omitempty"`
	Int     *int64  `json:"int,omitempty"`
	String  *string `json:"string,omitempty"`
	Version *string `json:"version,omitempty"`
}

// DeviceTaint keeps new claims off a device. With the NoExecute effect
// it also evicts the pods whose claims already hold the device.
//
// The API server sets TimeAdded on create or update when it is empty.
// This operator never sets it, and sameDevices ignores it.
type DeviceTaint struct {
	Key       string `json:"key"`
	Value     string `json:"value,omitempty"`
	Effect    string `json:"effect"`
	TimeAdded string `json:"timeAdded,omitempty"`
}

func AttrString(s string) DeviceAttribute { return DeviceAttribute{String: &s} }

func AttrInt(i int64) DeviceAttribute { return DeviceAttribute{Int: &i} }

func AttrBool(b bool) DeviceAttribute { return DeviceAttribute{Bool: &b} }

// sameDevices reports whether the published devices equal the devices
// that this pass computed, so the pass can skip the write. It ignores
// TimeAdded, because the API server sets it on every stored taint, and
// a plain comparison would make every pass write the slice. Each
// ResourceSlice write wakes every DRA-pending pod in the cluster.
func sameDevices(published, current []SliceDevice) bool {
	return reflect.DeepEqual(withoutTimeAdded(published), withoutTimeAdded(current))
}

// withoutTimeAdded returns a copy of devices with TimeAdded cleared and
// with each empty taint list, attribute map, and capacity map set to
// nil, because reflect.DeepEqual finds an empty value different from
// nil. It copies the taints, so the caller's devices are not changed.
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
		if len(device.Attributes) == 0 {
			out[i].Attributes = nil
		}
		if len(device.Capacity) == 0 {
			out[i].Capacity = nil
		}
	}
	return out
}

// ErrNoDevices is returned for an empty device list. The operator's pod
// claims a sound card, and a sound card has playback PCM devices, so an
// empty list means that reading them failed. Publishing it would remove
// devices that prepared claims still name.
var ErrNoDevices = errors.New("refusing to publish an empty inventory")

// EnsureResourceSlice creates the slice when none exists, replaces it
// when the devices changed, and writes nothing otherwise.
//
// The operator never deletes the slice, not even on shutdown, because
// prepared claims outlive the operator's pod. A running consumer keeps
// its socket mount, and the next operator pod's prepare call reads an
// allocation that still names these devices. The Node owns the slice,
// so the garbage collector deletes the slice with the Node. To
// uninstall the operator, delete the slice by name.
//
// The replace sends the resourceVersion from the read, so a concurrent
// write makes this one fail with apiclient.ErrConflict instead of
// overwriting it. The next pass reads and writes again.
func EnsureResourceSlice(c *apiclient.Client, nodeName string, owner OwnerReference, devices []SliceDevice) error {
	if len(devices) == 0 {
		return ErrNoDevices
	}
	name := sliceName(nodeName)
	path := ResourceSlicesPath + "/" + name

	current, err := apiclient.Get[ResourceSlice](c, path)
	if err == apiclient.ErrNotFound {
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

	if sameDevices(current.Spec.Devices, devices) {
		sliceLog.unchangedSlice(current.Spec.Pool.Generation, devices)
		return nil
	}

	// sliceLog.wrote reports the difference from the old devices.
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

func sliceName(nodeName string) string {
	return nodeName + "-" + DriverName
}

type nodeObject struct {
	Metadata struct {
		Name string `json:"name"`
		UID  string `json:"uid"`
	} `json:"metadata"`
}

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
