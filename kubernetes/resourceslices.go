package kubernetes

// Publishing device inventory as ResourceSlices.
//
// Dynamic resource allocation (the resource.k8s.io API group) is how
// workloads reach hardware: a per-node driver publishes each usable
// device in a ResourceSlice, DeviceClasses select over the devices'
// attributes, and pods claim from classes. This file is the
// publishing verb: liken's machine operator is the driver, and the
// slice it maintains is the one Kubernetes-native inventory of what
// this machine's hardware can actually do. (Contrast Machine
// status.hardware.unclaimed, which carries only what *doesn't* work
// and what would fix it; the census of working devices lives here,
// in the API purpose-built for it.)
//
// Like every type in this package, the structs are the honest subset
// of the upstream API: the fields liken writes, nothing more. The
// full ResourceSlice can describe partitionable devices, shared
// counters, per-device node selection — machinery for GPUs sliced
// eight ways — and none of it changes what a whole PCI or USB device
// on one node needs, which is a name, some attributes, and the
// node's identity.
//
// A slice belongs to a pool, and the pool's generation is how
// readers know which slices are current: the scheduler distrusts any
// slice whose generation lags the newest it can see, which protects
// it from acting on half-updated multi-slice inventories. liken
// publishes one slice per node (the whole inventory fits), so the
// protocol collapses to a version counter: bump it on every change,
// and one slice is always a consistent snapshot.

import (
	"encoding/json"
	"net/http"
	"reflect"
)

// ResourceSlicesPath is where the DRA inventory lives. Slices are
// cluster-scoped like Nodes: a namespace is a workload boundary, and
// hardware inventory belongs to the machine, not to any tenant.
const ResourceSlicesPath = "/apis/resource.k8s.io/v1/resourceslices"

// DriverName identifies liken as a DRA driver. Driver names are DNS
// domains by convention so that vendors can't collide; liken owns
// liken.sh. Every slice this operator publishes carries it, every
// DeviceClass a deployment writes selects on it, and the kubelet
// routes prepare calls for claims allocated from these slices to the
// plugin registered under this name.
const DriverName = "liken.sh"

type ResourceSlice struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   ResourceSliceMeta `json:"metadata"`
	Spec       ResourceSliceSpec `json:"spec"`
}

// ResourceSliceMeta carries the one piece of metadata api.ObjectMeta
// doesn't: an owner reference. Owning a slice is load-bearing, not
// decorative — see EnsureResourceSlice.
type ResourceSliceMeta struct {
	Name            string           `json:"name"`
	ResourceVersion string           `json:"resourceVersion,omitempty"`
	OwnerReferences []OwnerReference `json:"ownerReferences,omitempty"`
}

// OwnerReference ties one object's lifetime to another's: when the
// owner is deleted, the garbage collector deletes the owned. The UID
// matters — a reference names one specific incarnation of the owner,
// so a Node deleted and re-registered under the same name does not
// inherit the old node's slices. The type is shared across the
// package: slices are written with all four fields, while the drain
// (pods.go) only ever reads Kind to recognize DaemonSet pods.
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
// unique within the pool; the attributes are what DeviceClass CEL
// selectors match over. Attribute names left unqualified belong to
// the publishing driver's domain — a selector reads these as
// device.attributes["liken.sh"].driver and so on.
type SliceDevice struct {
	Name       string                     `json:"name"`
	Attributes map[string]DeviceAttribute `json:"attributes,omitempty"`
}

// DeviceAttribute is a one-of: exactly one of the four typed values
// is set. The API distinguishes them so selectors can compare
// numbers numerically and versions semantically instead of
// everything being a string.
type DeviceAttribute struct {
	Bool    *bool   `json:"bool,omitempty"`
	Int     *int64  `json:"int,omitempty"`
	String  *string `json:"string,omitempty"`
	Version *string `json:"version,omitempty"`
}

// EnsureResourceSlice converges one node's inventory: create the
// slice when it first has devices, replace it when the inventory
// changed, delete it when the last device is gone, and touch nothing
// when nothing moved — the same read-compare-write shape as every
// other liken reconcile, so a steady machine costs one GET per pass.
//
// The slice is owned by the Node (not by the Machine, and not by the
// operator pod): inventory is a claim about what stands ready on
// this node, and if the node leaves the cluster the claim must die
// with it — a slice that outlives its node would offer the scheduler
// hardware nobody can deliver. Owner-based garbage collection is
// also what cleans up after this operator's least graceful exits,
// where no code gets a chance to delete anything.
//
// The write carries the read's resourceVersion, so a conflicting
// writer turns this update into ErrConflict instead of a lost
// update; the next pass re-reads and tries again. That is the
// ordinary optimistic-concurrency loop, and at a ten-second cadence
// it needs no retry machinery of its own.
func EnsureResourceSlice(c *Client, nodeName string, owner OwnerReference, devices []SliceDevice) error {
	// One deterministic name per node, suffixed with the driver so
	// that other DRA drivers on the same node (slices are
	// cluster-scoped, and nothing stops a deployment adding a GPU
	// vendor's driver) can never collide with ours.
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

// AttrString builds a string-typed one-of attribute value without
// the pointer noise at every call site.
func AttrString(s string) DeviceAttribute { return DeviceAttribute{String: &s} }
