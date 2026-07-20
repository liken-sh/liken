package kubernetes

// This file publishes device inventory as ResourceSlices.
//
// Dynamic resource allocation (the resource.k8s.io API group) is how
// workloads reach hardware. A per-node driver publishes each usable
// device in a ResourceSlice. DeviceClasses select over the devices'
// attributes, and pods claim from classes. This file implements the
// publishing side: liken's machine operator is the driver, and the
// slice it maintains is the one Kubernetes-native inventory of what
// this machine's hardware can actually do. Contrast this with
// Machine status.hardware.unclaimed, which carries only what does
// not work and what would fix it. The record of working devices
// lives here, in the API built for that purpose.
//
// Like every type in this package, these structs carry only the part
// of the upstream API that liken uses: the fields liken writes,
// nothing more. The full ResourceSlice can describe partitionable
// devices, shared counters, and per-device node selection, machinery
// built for GPUs split many ways. None of that changes what a whole
// PCI or USB device on one node needs: a name, some attributes, and
// the node's identity.
//
// A slice belongs to a pool, and the pool's generation tells readers
// which slices are current. The scheduler distrusts any slice whose
// generation lags behind the newest generation it can see. This
// protects the scheduler from acting on a multi-slice inventory that
// is only partly updated. liken publishes one slice per node,
// because the whole inventory fits in one slice. Because of this,
// the protocol reduces to a version counter: bump the counter on
// every change, and one slice is always a consistent snapshot.

import (
	"encoding/json"
	"net/http"
	"reflect"
)

// ResourceSlicesPath names the URL where the DRA inventory lives.
// Slices are cluster-scoped, like Nodes. A namespace marks a
// workload boundary, and hardware inventory belongs to the machine,
// not to any tenant.
const ResourceSlicesPath = "/apis/resource.k8s.io/v1/resourceslices"

// DriverName identifies liken as a DRA driver. By convention, driver
// names are DNS domains, so vendors cannot collide with each other;
// liken owns liken.sh. Every slice this operator publishes carries
// this name. Every DeviceClass that a deployment writes selects on
// this name. The kubelet routes prepare calls for claims allocated
// from these slices to the plugin registered under this name.
const DriverName = "liken.sh"

type ResourceSlice struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   ResourceSliceMeta `json:"metadata"`
	Spec       ResourceSliceSpec `json:"spec"`
}

// ResourceSliceMeta carries the one piece of metadata that
// api.ObjectMeta does not: an owner reference. Owning a slice does
// necessary work; it is not decoration. See EnsureResourceSlice.
type ResourceSliceMeta struct {
	Name            string           `json:"name"`
	ResourceVersion string           `json:"resourceVersion,omitempty"`
	OwnerReferences []OwnerReference `json:"ownerReferences,omitempty"`
}

// OwnerReference ties one object's lifetime to another object's
// lifetime. When the owner is deleted, the garbage collector deletes
// the owned object. The UID matters: a reference names one specific
// instance of the owner, so a Node that is deleted and registered
// again under the same name does not inherit the old node's slices.
// This type is shared across the package. Slices are written with
// all four fields, while the drain (pods.go) only ever reads Kind to
// recognize DaemonSet pods.
type OwnerReference struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind"`
	Name       string `json:"name,omitempty"`
	UID        string `json:"uid,omitempty"`
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

// SliceDevice is one claimable device. The name must be a DNS label,
// unique within the pool. The attributes are the values that
// DeviceClass CEL selectors match against. An attribute name left
// unqualified belongs to the publishing driver's domain. A selector
// reads these as device.attributes["liken.sh"].driver, and so on.
type SliceDevice struct {
	Name       string                     `json:"name"`
	Attributes map[string]DeviceAttribute `json:"attributes,omitempty"`
}

// DeviceAttribute holds exactly one of four typed values. The API
// keeps these types separate so that selectors can compare numbers
// as numbers, and versions by version rules, instead of treating
// everything as a string.
type DeviceAttribute struct {
	Bool    *bool   `json:"bool,omitempty"`
	Int     *int64  `json:"int,omitempty"`
	String  *string `json:"string,omitempty"`
	Version *string `json:"version,omitempty"`
}

// EnsureResourceSlice makes one node's published slice match its
// actual inventory. It creates the slice when the node first has
// devices, replaces the slice when the inventory changed, deletes
// the slice when the last device is gone, and changes nothing when
// nothing moved. This is the same read-compare-write pattern as
// every other liken reconcile, so a steady machine costs one GET
// request per pass.
//
// The Node owns the slice. Neither the Machine nor the operator pod
// owns it. The inventory is a claim about what is ready to use on
// this node. If the node leaves the cluster, the claim must be
// deleted with it: a slice that remains after its node is gone would
// offer the scheduler hardware that nobody can deliver. Owner-based
// garbage collection also cleans up after this operator crashes or
// exits abruptly, when no code runs to delete anything.
//
// The write carries the resourceVersion from the read. If a
// conflicting writer changed the object in the meantime, this update
// returns ErrConflict instead of overwriting that change. The next
// pass reads the object again and tries again. This is the ordinary
// optimistic-concurrency loop, and at a ten-second cadence, it needs
// no retry logic of its own.
func EnsureResourceSlice(c *Client, nodeName string, owner OwnerReference, devices []SliceDevice) error {
	// Each node gets one predictable name, with the driver name added
	// as a suffix. This keeps other DRA drivers on the same node from
	// colliding with ours. Slices are cluster-scoped, and nothing
	// stops a deployment from adding a GPU vendor's driver.
	name := nodeName + "-" + DriverName
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
		return c.RequestJSON(http.MethodPost, ResourceSlicesPath, body, nil)
	}
	if err != nil {
		return err
	}

	if len(devices) == 0 {
		return c.RequestJSON(http.MethodDelete, path, nil, nil)
	}
	if reflect.DeepEqual(current.Spec.Devices, devices) {
		return nil
	}

	current.Spec.NodeName = nodeName
	current.Spec.Driver = DriverName
	current.Spec.Pool = ResourcePool{
		Name:               nodeName,
		Generation:         current.Spec.Pool.Generation + 1,
		ResourceSliceCount: 1,
	}
	current.Spec.Devices = devices
	body, err := json.Marshal(current)
	if err != nil {
		return err
	}
	return c.RequestJSON(http.MethodPut, path, body, nil)
}

// AttrString builds a string-typed attribute value without repeating
// pointer syntax at every call site.
func AttrString(s string) DeviceAttribute { return DeviceAttribute{String: &s} }
