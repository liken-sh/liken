package main

// This file publishes the capabilities of each render node on a node
// in a ResourceSlice under this program's own DRA driver name, beside
// the slice that `liken` publishes for the same node. A DRA device is
// identified by <driver>/<pool>/<device>, and the slice name ends with
// the driver name, so the two slices of a node are <node>-liken.sh and
// <node>-media.liken.sh and do not collide.
//
// The structs here have only the fields of the upstream API that this
// program reads and writes. Each node's pool is one slice that holds
// all of the node's devices, so the scheduler never reads a pool whose
// slices have different generations.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// DriverName is the DRA driver name of this program. The driver name of
// a device operator is <domain>.liken.sh.
const DriverName = "media.liken.sh"

// likenDriver is the DRA driver name under which `liken` publishes the
// render nodes that this program describes.
const likenDriver = "liken.sh"

const resourceSlicesPath = "/apis/resource.k8s.io/v1/resourceslices"

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

// OwnerReference makes the Node the owner of the slice, so Kubernetes
// deletes the slice with the Node. The UID identifies one Node object,
// so a Node that registers again under the same name does not own the
// old Node's slice.
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

// SliceDevice is one device in a slice. An attribute name without a
// domain belongs to the publishing driver, so a CEL selector reads this
// program's attributes as device.attributes["media.liken.sh"].<name>.
type SliceDevice struct {
	Name                     string                     `json:"name"`
	Attributes               map[string]DeviceAttribute `json:"attributes,omitempty"`
	AllowMultipleAllocations *bool                      `json:"allowMultipleAllocations,omitempty"`
}

// DeviceAttribute holds one value, of one type. A selector compares a
// bool attribute as a boolean, not as the string "true".
type DeviceAttribute struct {
	Bool   *bool   `json:"bool,omitempty"`
	String *string `json:"string,omitempty"`
}

func attrString(s string) DeviceAttribute { return DeviceAttribute{String: &s} }
func attrBool(b bool) DeviceAttribute     { return DeviceAttribute{Bool: &b} }

// stringAttribute returns a string attribute, or "" when it is absent.
func (d SliceDevice) stringAttribute(name string) string {
	if value := d.Attributes[name].String; value != nil {
		return *value
	}
	return ""
}

func sliceName(nodeName, driver string) string {
	return nodeName + "-" + driver
}

// ensureResourceSlice makes this driver's slice for the node list
// exactly devices. It creates the slice when none exists and there are
// devices, replaces it with a higher generation when the devices
// changed, deletes it when there are no devices, and writes nothing when
// nothing changed. Each ResourceSlice write makes the scheduler try
// again every pod that waits for a device, so a write that changes
// nothing costs the whole cluster.
//
// The Node owns the slice, not the agent's pod. The pod restarts on an
// image update while claims stay prepared, and a slice deleted with the
// pod would remove every device from the scheduler until the pod
// returned.
//
// The replace sends the resourceVersion that the read returned. When
// another writer changed the slice in between, the replace fails with
// apiclient.ErrConflict, and the agent's next pass reads the slice
// again.
func ensureResourceSlice(c *apiclient.Client, nodeName string, owner OwnerReference, devices []SliceDevice) error {
	name := sliceName(nodeName, DriverName)
	path := resourceSlicesPath + "/" + name
	current, err := apiclient.Get[ResourceSlice](c, path)
	if err == apiclient.ErrNotFound {
		if len(devices) == 0 {
			return nil
		}
		slice := &ResourceSlice{
			APIVersion: "resource.k8s.io/v1",
			Kind:       "ResourceSlice",
			Metadata:   ResourceSliceMeta{Name: name, OwnerReferences: []OwnerReference{owner}},
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
		if err := c.RequestJSON(http.MethodPost, resourceSlicesPath, body, nil); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "slice: created %s with %d devices\n", name, len(devices))
		return nil
	}
	if err != nil {
		return err
	}
	if len(devices) == 0 {
		err := c.RequestJSON(http.MethodDelete, path, nil, nil)
		if err != nil && err != apiclient.ErrNotFound {
			return err
		}
		fmt.Fprintf(os.Stderr, "slice: deleted %s, because this node has no render node\n", name)
		return nil
	}
	if reflect.DeepEqual(current.Spec.Devices, devices) {
		return nil
	}
	current.Spec.Driver = DriverName
	current.Spec.NodeName = nodeName
	current.Spec.Pool = ResourcePool{Name: nodeName, Generation: current.Spec.Pool.Generation + 1, ResourceSliceCount: 1}
	current.Spec.Devices = devices
	body, err := json.Marshal(current)
	if err != nil {
		return err
	}
	if err := c.RequestJSON(http.MethodPut, path, body, nil); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "slice: wrote %s at generation %d with %d devices\n",
		name, current.Spec.Pool.Generation, len(devices))
	return nil
}

// nodeObject has the only fields this program reads from its Node.
type nodeObject struct {
	Metadata struct {
		Name string `json:"name"`
		UID  string `json:"uid"`
	} `json:"metadata"`
}

func nodeOwner(c *apiclient.Client, nodeName string) (OwnerReference, error) {
	node, err := apiclient.Get[nodeObject](c, "/api/v1/nodes/"+nodeName)
	if err != nil {
		return OwnerReference{}, err
	}
	return OwnerReference{APIVersion: "v1", Kind: "Node", Name: node.Metadata.Name, UID: node.Metadata.UID}, nil
}
