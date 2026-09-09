package main

// Publishing paired controllers as this operator's own ResourceSlice.
//
// The slice holds two kinds of device: one for each paired
// controller, and one media bus for the adapter itself. The media bus
// is the claimable permission to connect a sound server to this pod's
// bluetoothd, and plans/completed/05-the-media-bus.md records the design.
//
// A device operator publishes under its own driver name, in its own
// slices, beside whatever liken publishes on the same node. The two
// cannot collide: a device's identity is the triple
// <driver>/<pool>/<device>, and the slice name ends with the driver
// name, so this node's two slices are <node>-liken.sh and
// <node>-bluetooth.liken.sh.
//
// Like liken's own client, these structs hold only the part of the
// upstream API that this program writes. The full ResourceSlice can
// describe partitionable devices, shared counters, and per-device
// node selection, and none of that changes what a paired controller
// needs: a name, its identity attributes, and a taint when the radio
// is silent.
//
// One slice holds the whole inventory, so the pool protocol reduces
// to a version counter: bump the generation on every change, and one
// slice is always a consistent snapshot.

import (
	"encoding/json"
	"net/http"
	"reflect"
)

// DriverName identifies this operator as a DRA driver. A driver name
// is a DNS name so that drivers cannot collide, and a device
// operator's name is <domain>.liken.sh. The name states the contract
// the operator implements rather than the repository that builds it.
const DriverName = "bluetooth.liken.sh"

// ResourceSlicesPath names the URL of the DRA inventory. Slices
// are cluster-scoped, like Nodes, because hardware inventory belongs
// to the machine and not to any tenant.
const ResourceSlicesPath = "/apis/resource.k8s.io/v1/resourceslices"

// maxSliceDevices is the API's limit on devices in one slice. The
// limit is 128 for a slice with no taints and 64 for a slice that
// taints any device, and this operator taints every controller that
// is off the air, so 64 is the number that applies. The count is
// devices, not taints, so publishing two taints on one device does not
// lower it further. One adapter pairs far fewer controllers than that
// in practice.
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

// OwnerReference ties one object's lifetime to another's. The UID
// matters: a reference names one instance of the owner, so a Node
// that is deleted and registered again under the same name does not
// inherit the old node's slices.
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
// adapter's media bus. The name must be a DNS
// label, unique within the pool. An attribute name left unqualified
// belongs to the publishing driver's domain, so a selector reads
// these as device.attributes["bluetooth.liken.sh"].address.
type SliceDevice struct {
	Name       string                     `json:"name"`
	Attributes map[string]DeviceAttribute `json:"attributes,omitempty"`
	Taints     []DeviceTaint              `json:"taints,omitempty"`
}

// DeviceAttribute holds exactly one of four typed values. The API
// keeps the types apart so that a selector compares a boolean as a
// boolean, instead of against the string "true".
type DeviceAttribute struct {
	Bool    *bool   `json:"bool,omitempty"`
	Int     *int64  `json:"int,omitempty"`
	String  *string `json:"string,omitempty"`
	Version *string `json:"version,omitempty"`
}

// DeviceTaint keeps a claim off a device, and evicts the pods of the
// claims that already hold it when the effect is NoExecute.
//
// TimeAdded is a field the API server fills in on write. This
// operator never sets it, and reads it back only so that the change
// detection can ignore it (see sameDevices).
type DeviceTaint struct {
	Key       string `json:"key"`
	Value     string `json:"value,omitempty"`
	Effect    string `json:"effect"`
	TimeAdded string `json:"timeAdded,omitempty"`
}

// AttrString builds a string-typed attribute value without repeating
// pointer syntax at every call site.
func AttrString(s string) DeviceAttribute { return DeviceAttribute{String: &s} }

// AttrBool builds a boolean attribute value.
func AttrBool(b bool) DeviceAttribute { return DeviceAttribute{Bool: &b} }

// AttrInt builds an integer attribute value.
func AttrInt(i int64) DeviceAttribute { return DeviceAttribute{Int: &i} }

// sameDevices reports whether the published devices already say what
// this pass would say.
//
// The comparison ignores TimeAdded, which the API server fills in on
// every taint it stores. A plain comparison would compare the stored
// timestamp against an empty one, call every pass a change, and write
// the slice on every pass. Each ResourceSlice write wakes every
// DRA-pending pod in the cluster, so a needless write is a
// cluster-wide cost.
func sameDevices(published, current []SliceDevice) bool {
	return reflect.DeepEqual(withoutTimeAdded(published), withoutTimeAdded(current))
}

// withoutTimeAdded copies the devices with every taint's timestamp
// cleared. The copy is deep enough to leave the caller's own taints
// untouched.
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

// EnsureResourceSlice makes this operator's published slice match
// what the node offers now. It creates the slice on the first
// publish, replaces the slice when anything changed, deletes the
// slice when the device list is empty, and writes nothing when
// nothing moved.
//
// The device list is empty only while no adapter has answered and no
// controller is paired. An adapter that answered once keeps its media
// bus in the slice, so unpairing the last controller shrinks the
// slice to the bus instead of deleting it.
//
// The Node owns the slice. The operator's pod does not, deliberately:
// the pod restarts while claims stay prepared, and a slice that left
// with each restart would strand every consumer. The Node is the
// right owner because the slice is a claim about what this node can
// deliver, so a Node that leaves the cluster takes the slice with it,
// and nothing else has to run for that to happen.
//
// The write includes the resourceVersion from the read, so a
// conflicting writer gets ErrConflict instead of losing its change.
// The next pass reads again and writes again.
func EnsureResourceSlice(c *Client, nodeName string, owner OwnerReference, devices []SliceDevice) error {
	name := sliceName(nodeName)
	path := ResourceSlicesPath + "/" + name

	current, err := get[ResourceSlice](c, path)
	if err == ErrNotFound {
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

	// The published devices are read before the assignment overwrites
	// them, because they are one half of what the line says changed.
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

// DeleteResourceSlice removes this operator's whole offer. The
// operator calls it in one case only: no adapter has answered and no
// controller is paired, so there is nothing to publish.
//
// It does not run at shutdown. An operator's pod restarts for
// ordinary reasons, such as a new image or a node drain, while a
// consumer holds a prepared claim, and a delete on the way out would
// strand that consumer. A person who uninstalls the operator for good
// deletes the slice by name, and the README says so.
func DeleteResourceSlice(c *Client, nodeName string) error {
	err := c.RequestJSON(http.MethodDelete, ResourceSlicesPath+"/"+sliceName(nodeName), nil, nil)
	if err == ErrNotFound {
		return nil
	}
	return err
}

func sliceName(nodeName string) string {
	return nodeName + "-" + DriverName
}

// nodeObject holds the one thing this operator reads from its Node:
// the UID that the slice's owner reference needs.
type nodeObject struct {
	Metadata struct {
		Name string `json:"name"`
		UID  string `json:"uid"`
	} `json:"metadata"`
}

// NodeOwner reads this operator's node and builds the owner reference
// for its slice.
func NodeOwner(c *Client, nodeName string) (OwnerReference, error) {
	node, err := get[nodeObject](c, "/api/v1/nodes/"+nodeName)
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
