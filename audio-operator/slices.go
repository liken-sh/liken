package main

// Publishing the card's outputs as this operator's own ResourceSlice.
//
// A device operator publishes under its own driver name, in its own
// slices, beside whatever liken publishes on the same node. The two
// cannot collide: a device's identity is the triple
// <driver>/<pool>/<device>, and the slice name ends with the driver
// name, so this node's two slices are <node>-liken.sh and
// <node>-audio.liken.sh.
//
// Like liken's own client, these structs hold only the part of the
// upstream API that this program writes. The full ResourceSlice can
// describe partitionable devices, shared counters, and per-device
// node selection, and none of that changes what an output needs: a
// name, its attributes, and taints when it cannot play.
//
// One slice holds the whole inventory, so the pool protocol reduces
// to a version counter: bump the generation on every change, and one
// slice is always a consistent snapshot.

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
)

// DriverName identifies this operator as a DRA driver. A driver name
// is a DNS name so that drivers cannot collide, and a device
// operator's name is <domain>.liken.sh. The name states the contract
// the operator implements rather than the repository that builds it.
const DriverName = "audio.liken.sh"

// ResourceSlicesPath names the URL of the DRA inventory. Slices
// are cluster-scoped, like Nodes, because hardware inventory belongs
// to the machine and not to any tenant.
const ResourceSlicesPath = "/apis/resource.k8s.io/v1/resourceslices"

// maxSliceDevices is the API's limit on devices in one slice. The
// limit is 128 for a slice with no taints and 64 for a slice that
// taints any device, and this operator taints every output that
// cannot play, so 64 is the number that applies. One card's PCM
// devices and one adapter's paired speakers together stay far under
// that in practice.
const maxSliceDevices = 64

// maxAttributeLength is the API's limit on the length of a string
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

// SliceDevice is one claimable output. The name must be a DNS label,
// unique within the pool. An attribute name left unqualified belongs
// to the publishing driver's domain, so a selector reads these as
// device.attributes["audio.liken.sh"].connectionType. The pairing
// attribute is the one exception: its own name includes the domain.
type SliceDevice struct {
	Name       string                     `json:"name"`
	Attributes map[string]DeviceAttribute `json:"attributes,omitempty"`
	Taints     []DeviceTaint              `json:"taints,omitempty"`
}

// DeviceAttribute holds exactly one of four typed values. The API
// keeps the types apart so that a selector compares a number as a
// number, instead of against the string "2".
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

// AttrInt builds a number attribute value.
func AttrInt(i int64) DeviceAttribute { return DeviceAttribute{Int: &i} }

// AttrBool builds a boolean attribute value.
func AttrBool(b bool) DeviceAttribute { return DeviceAttribute{Bool: &b} }

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
		if len(device.Attributes) == 0 {
			out[i].Attributes = nil
		}
	}
	return out
}

// ErrNoDevices refuses a write that would publish nothing.
//
// An empty inventory is never a real state of a machine this operator
// runs on. The operator holds an exclusive claim on a card,
// and a card has playback PCM devices, so an empty list means the
// enumeration failed. Publishing it would retract every device that a
// prepared claim still names, which is the one thing a device
// operator must never do.
var ErrNoDevices = errors.New("refusing to publish an empty inventory")

// EnsureResourceSlice makes this operator's published slice match the
// card's outputs. It creates the slice on the first pass, replaces
// the slice when anything changed, and writes nothing when nothing
// moved.
//
// The slice is never deleted. The operator's shutdown does not
// retract it, because the prepared claims outlive the pod: a
// consumer that is already running keeps its socket mount, and the
// next pod's prepare call reads an allocation that still names these
// devices. The Node owns the slice, so a Node that leaves the cluster
// takes the slice with it, and that is the only retraction this
// operator relies on. A person who uninstalls the operator for good
// deletes the slice by name.
//
// The write includes the resourceVersion from the read, so a
// conflicting writer gets ErrConflict instead of losing its change.
// The next pass reads again and writes again.
func EnsureResourceSlice(c *Client, nodeName string, owner OwnerReference, devices []SliceDevice) error {
	if len(devices) == 0 {
		return ErrNoDevices
	}
	name := sliceName(nodeName)
	path := ResourceSlicesPath + "/" + name

	current, err := get[ResourceSlice](c, path)
	if err == ErrNotFound {
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
