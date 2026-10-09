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
	"slices"
	"sync"

	"github.com/liken-sh/liken/kubernetes/apiclient"
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
// necessary work; it is not decoration. See WriteResourceSlice.
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
// AllowMultipleAllocations lets more than one claim allocate the same
// device. Without it the API allocates a device once, which is the
// safe default and the right one for a device that one process must
// hold. Only the driver that publishes a device can set this: a
// DeviceClass or a claim can select a device, but neither can say
// that the hardware divides. The field is a pointer so that a device
// that does not divide publishes nothing at all, rather than an
// explicit false that reads as a claim about the hardware.
//
// The API server honors the field when its DRAConsumableCapacity
// feature is on, which is the default in the k3s that liken ships. An
// API server with the feature off drops the field on write, and every
// device allocates once, which is the safe direction to fail in.
type SliceDevice struct {
	Name                     string                     `json:"name"`
	Attributes               map[string]DeviceAttribute `json:"attributes,omitempty"`
	AllowMultipleAllocations *bool                      `json:"allowMultipleAllocations,omitempty"`
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
// actual inventory: it reads the slice (GetResourceSlice) and then
// writes what differs (WriteResourceSlice). A caller that already
// holds a copy of the slice, from a watch, calls WriteResourceSlice
// with that copy and sends no read.
func EnsureResourceSlice(c *apiclient.Client, nodeName string, owner OwnerReference, devices []SliceDevice) error {
	current, err := GetResourceSlice(c, nodeName)
	if err != nil {
		return err
	}
	return WriteResourceSlice(c, nodeName, current, owner, devices)
}

// ResourceSliceName is the name of one node's slice. Each node gets
// one predictable name, with the driver name added as a suffix. This
// keeps other DRA drivers on the same node from colliding with ours.
// Slices are cluster-scoped, and nothing stops a deployment from
// adding a GPU vendor's driver.
func ResourceSliceName(nodeName string) string {
	return nodeName + "-" + DriverName
}

// GetResourceSlice reads one node's slice. An absent slice returns
// nil, nil, because a node with no devices has none.
func GetResourceSlice(c *apiclient.Client, nodeName string) (*ResourceSlice, error) {
	current, err := apiclient.Get[ResourceSlice](c, ResourceSlicesPath+"/"+ResourceSliceName(nodeName))
	if err == apiclient.ErrNotFound {
		return nil, nil
	}
	return current, err
}

// WriteResourceSlice makes one node's published slice match its
// actual inventory, given the slice as it is now (nil when it does
// not exist). It is SliceWriter.Write with no memory of an earlier
// write, for a caller that writes once.
func WriteResourceSlice(c *apiclient.Client, nodeName string, current *ResourceSlice, owner OwnerReference, devices []SliceDevice) error {
	return (&SliceWriter{}).Write(c, nodeName, current, owner, devices)
}

// A SliceWriter writes one node's slice and remembers its own last
// write: the resourceVersion the API server answered, and the devices
// the write sent.
//
// The memory answers two questions. The first is whether a slice still
// holds this writer's last write. The API server can return a slice
// that differs from what was sent: one with DRAConsumableCapacity off
// drops allowMultipleAllocations from each device. A comparison of the
// desired devices with the returned ones would then differ on every
// pass, and each pass would write again. A slice at the version of the
// writer's own last write, with the same devices desired, is current,
// whatever the server kept of them. The upstream DRA slice controller
// meets the same problem (k8s.io/dynamic-resource-allocation/
// resourceslice), and copies the dropped fields back before it compares.
//
// The second is whether a change that a watch delivers is this
// writer's own echo (Wrote). A watch that woke the loop on its own
// echo, with a write that never compares equal, would write as fast as
// the API server answers.
type SliceWriter struct {
	mu      sync.Mutex
	version string
	sent    []SliceDevice
}

// Wrote answers whether version is the resourceVersion the API server
// answered for this writer's last write.
func (w *SliceWriter) Wrote(version string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return version != "" && version == w.version
}

// Write creates the slice when the node first has devices, replaces the
// slice when the inventory changed, deletes the slice when the last
// device is gone, and changes nothing when nothing moved. This is the
// same compare-then-write pattern as every other liken reconcile, so a
// steady machine sends no request here.
//
// The Node owns the slice. Neither the Machine nor the operator pod
// owns it. The inventory is a claim about what is ready to use on
// this node. If the node leaves the cluster, the claim must be
// deleted with it: a slice that remains after its node is gone would
// offer the scheduler hardware that nobody can deliver. Owner-based
// garbage collection also cleans up after this operator crashes or
// exits abruptly, when no code runs to delete anything.
//
// The write carries the resourceVersion of the copy it compared. If a
// conflicting writer changed the object in the meantime, or the copy
// from a watch is behind this operator's own last write, this update
// returns apiclient.ErrConflict instead of overwriting that change. A
// create from a copy that says the slice is absent returns
// apiclient.ErrConflict too, when the slice exists. The next pass
// compares against a newer copy and tries again.
func (w *SliceWriter) Write(c *apiclient.Client, nodeName string, current *ResourceSlice, owner OwnerReference, devices []SliceDevice) error {
	name := ResourceSliceName(nodeName)
	path := ResourceSlicesPath + "/" + name

	if current == nil {
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
		return w.send(c, http.MethodPost, ResourceSlicesPath, slice, devices)
	}

	if len(devices) == 0 {
		w.remember("", nil)
		return c.RequestJSON(http.MethodDelete, path, nil, nil)
	}
	if reflect.DeepEqual(current.Spec.Devices, devices) || w.holds(current, devices) {
		return nil
	}

	updated := *current
	updated.Spec.NodeName = nodeName
	updated.Spec.Driver = DriverName
	updated.Spec.Pool = ResourcePool{
		Name:               nodeName,
		Generation:         current.Spec.Pool.Generation + 1,
		ResourceSliceCount: 1,
	}
	updated.Spec.Devices = devices
	return w.send(c, http.MethodPut, path, &updated, devices)
}

// holds answers whether current is this writer's last write, and the
// write sent the devices desired now.
func (w *SliceWriter) holds(current *ResourceSlice, devices []SliceDevice) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return current.Metadata.ResourceVersion != "" && current.Metadata.ResourceVersion == w.version &&
		reflect.DeepEqual(w.sent, devices)
}

// send writes the slice and remembers the version the API server
// answered and the devices sent. A write that fails remembers nothing,
// because a request that timed out can still have landed, and the next
// pass then compares the devices themselves.
func (w *SliceWriter) send(c *apiclient.Client, method, path string, slice *ResourceSlice, devices []SliceDevice) error {
	body, err := json.Marshal(slice)
	if err != nil {
		return err
	}
	var answer ResourceSlice
	if err := c.RequestJSON(method, path, body, &answer); err != nil {
		w.remember("", nil)
		return err
	}
	w.remember(answer.Metadata.ResourceVersion, devices)
	return nil
}

func (w *SliceWriter) remember(version string, devices []SliceDevice) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.version, w.sent = version, slices.Clone(devices)
}

// AttrString builds a string-typed attribute value without repeating
// pointer syntax at every call site.
func AttrString(s string) DeviceAttribute { return DeviceAttribute{String: &s} }

// AttrBool builds a boolean attribute value. A selector reads it as a
// boolean, so a DeviceClass asks device.attributes["liken.sh"].x
// rather than comparing it against the string "true".
func AttrBool(b bool) DeviceAttribute { return DeviceAttribute{Bool: &b} }
