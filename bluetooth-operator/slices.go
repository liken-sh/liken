package main

// Publishing paired controllers as this operator's own ResourceSlice.
//
// A device operator publishes under its own driver name, in its own
// slices, beside whatever liken publishes on the same node. The two
// cannot collide: a device's identity is the triple
// <driver>/<pool>/<device>, and the slice name carries the driver
// name as a suffix, so this node's two slices are <node>-liken.sh and
// <node>-bluetooth.liken.sh.
//
// Like liken's own client, these structs carry only the part of the
// upstream API that this program writes. The full ResourceSlice can
// describe partitionable devices, shared counters, and per-device
// node selection, and none of that changes what a paired controller
// needs: a name, three attributes, and a taint when the radio is
// silent.
//
// One slice holds the whole inventory, so the pool protocol reduces
// to a version counter: bump the generation on every change, and one
// slice is always a consistent snapshot.

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
)

// DriverName identifies this operator as a DRA driver. A driver name
// is a DNS name so that drivers cannot collide, and a device
// operator's name is <domain>.liken.sh. The name states which
// contract family the operator implements, not which repository
// builds it.
const DriverName = "bluetooth.liken.sh"

// ResourceSlicesPath names the URL where DRA inventory lives. Slices
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

// The two taints this operator applies, which answer two different
// questions.
//
// disconnectedTaint says the controller is off the air. The effect is
// NoExecute, so the taint-eviction controller ends the pod that holds
// the claim, and the consumer's own tolerationSeconds sets how long a
// radio may be silent first. A consumer tolerates this one, because a
// controller that drops for a moment is not a loss.
//
// noInputNodeTaint says the controller registers no evdev node, so a
// claim on it would deliver nothing and NodePrepareResources would
// fail. The effect is NoSchedule, and no consumer should ever tolerate
// it. This is what makes a claim ahead of a connect park instead of
// loop: with only the NoExecute taint, a consumer that tolerated it
// would be scheduled onto a controller that is switched off, fail in
// prepare, get evicted when the toleration ran out, and be scheduled
// again. An untolerated NoSchedule taint holds the pod Unschedulable
// until the controller is really there.
const (
	disconnectedTaint = DriverName + "/disconnected"
	noInputNodeTaint  = DriverName + "/no-input-node"
)

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

// SliceDevice is one claimable controller. The name must be a DNS
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

// sliceDevices turns the paired set into the devices the slice
// publishes, one for each controller, sorted by name so that the same
// hardware always produces the same slice.
//
// Membership is the paired set. A controller that is switched off is
// still a device a person can claim, and the pod parks Unschedulable
// until somebody turns it on. Connection state is an attribute and a
// taint, never membership, because deleting a device that a claim
// holds strands the next consumer: the allocation still names the
// device, and the kubelet's prepare call retries against a device
// that is in no slice, with no bound on the retry. A device leaves
// the slice only when it is unpaired.
//
// The connected attribute reports what bluetoothd says. The taints
// report what a claim on the device would actually deliver, which is
// the stricter fact: the two differ for the moment between the
// connection and the HID device's registration, and for a controller
// that connects without its HID driver bound.
func sliceDevices(controllers map[string]controller, nodes map[string][]string) []SliceDevice {
	devices := make([]SliceDevice, 0, len(controllers))
	for mac, c := range controllers {
		device := SliceDevice{
			Name: deviceName(mac),
			Attributes: map[string]DeviceAttribute{
				"address":   AttrString(publishedMAC(mac)),
				"connected": AttrBool(c.Connected),
			},
		}
		if name := attributeString(c.Name); name != "" {
			device.Attributes["name"] = AttrString(name)
		}
		usable := len(nodes[mac]) > 0
		if !c.Connected || !usable {
			device.Taints = append(device.Taints, DeviceTaint{
				Key:    disconnectedTaint,
				Effect: "NoExecute",
			})
		}
		if !usable {
			device.Taints = append(device.Taints, DeviceTaint{
				Key:    noInputNodeTaint,
				Effect: "NoSchedule",
			})
		}
		devices = append(devices, device)
	}
	slices.SortFunc(devices, func(a, b SliceDevice) int {
		return strings.Compare(a.Name, b.Name)
	})
	return devices
}

// attributeString limits a free-text value to the API's 64-character
// limit on attribute strings. A controller's alias is the only value
// here that a person can make long, and a truncated alias still
// identifies the controller to a reader. A PairingRequest's status
// carries a device's name under the same limit, so both cut with the
// same function.
func attributeString(s string) string {
	return truncateRunes(s, maxSeenNameBytes)
}

// sameDevices reports whether the published devices already say what
// this pass would say.
//
// The comparison ignores TimeAdded, which the API server fills in on
// every taint it stores. A plain comparison would see the stored
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

// EnsureResourceSlice makes this operator's published slice match the
// paired set. It creates the slice when the first controller is
// paired, replaces the slice when anything changed, deletes the slice
// when the last controller is unpaired, and writes nothing when
// nothing moved.
//
// The empty case reaches here only from an answer that bluetoothd
// gave with an adapter present (bluez.go's ErrNoAdapter covers the
// rest), so an empty set means every controller was unpaired.
// Unpairing is the one sanctioned removal.
//
// The Node owns the slice. The operator's pod does not, deliberately:
// the pod restarts while claims stay prepared, and a slice that left
// with each restart would strand every consumer. The Node is the
// right owner because the slice is a claim about what this node can
// deliver, so a Node that leaves the cluster takes the slice with it,
// and nothing else has to run for that to happen.
//
// The write carries the resourceVersion from the read, so a
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
// operator calls it in one case only: the last controller was
// unpaired, so the slice has nothing left to publish.
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

// nodeObject carries the one thing this operator reads from its Node:
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
