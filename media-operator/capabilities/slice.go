package main

// Publishing each render node's capabilities as this driver's own
// ResourceSlice.
//
// A device operator publishes under its own driver name, in its own
// slice, beside what `liken` publishes on the same node. A device's
// identity is the triple <driver>/<pool>/<device>, and the slice name
// ends with the driver name, so this node's two slices are
// <node>-liken.sh and <node>-media.liken.sh, and the two never
// collide.
//
// These structs hold only the part of the upstream API that this
// program reads and writes. One slice holds the node's whole
// inventory, so the pool protocol reduces to a version counter: the
// generation goes up on every change, and the one slice is always a
// consistent snapshot.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// DriverName identifies this program as a DRA driver. A device
// operator's driver name is <domain>.liken.sh.
const DriverName = "media.liken.sh"

// likenDriver is the driver whose render nodes this program measures.
const likenDriver = "liken.sh"

// resourceSlicesPath names the URL of the DRA inventory. Slices are
// cluster-scoped, like Nodes, because hardware belongs to the machine
// and not to any tenant.
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

// OwnerReference ties the slice's lifetime to the Node's. The UID names
// one instance of the Node, so a Node that registers again under the
// same name does not inherit the old one's slice.
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

// SliceDevice is one device. An attribute name with no domain belongs
// to the publishing driver, so a selector reads this program's
// attributes as device.attributes["media.liken.sh"].<name>.
type SliceDevice struct {
	Name                     string                     `json:"name"`
	Attributes               map[string]DeviceAttribute `json:"attributes,omitempty"`
	AllowMultipleAllocations *bool                      `json:"allowMultipleAllocations,omitempty"`
}

// DeviceAttribute holds exactly one typed value. A selector compares a
// boolean as a boolean, not against the string "true".
type DeviceAttribute struct {
	Bool   *bool   `json:"bool,omitempty"`
	String *string `json:"string,omitempty"`
}

func attrString(s string) DeviceAttribute { return DeviceAttribute{String: &s} }
func attrBool(b bool) DeviceAttribute     { return DeviceAttribute{Bool: &b} }

// stringAttribute reads a string attribute, and "" when it is absent.
func (d SliceDevice) stringAttribute(name string) string {
	if value := d.Attributes[name].String; value != nil {
		return *value
	}
	return ""
}

func sliceName(nodeName, driver string) string {
	return nodeName + "-" + driver
}

// ensureResourceSlice makes this driver's slice for the node hold
// exactly the devices. It creates the slice on the first publish,
// replaces it when a device changed, deletes it when there is no
// device, and writes nothing when nothing changed. Each ResourceSlice
// write wakes every pod in the cluster that waits on DRA, so a write
// with no change is a cost to the whole cluster.
//
// The Node owns the slice, not the agent's pod. The pod restarts for a
// new image while claims stay prepared, and a slice that went with the
// pod would take every device away from the scheduler for that time.
//
// The write carries the resourceVersion of the read, so a conflicting
// writer gets apiclient.ErrConflict, and the agent's retry reads and
// writes again.
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
		fmt.Fprintf(os.Stderr, "slice: deleted %s, because this node holds no render node\n", name)
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

// nodeObject holds the one fact this program reads from its Node: the
// UID that the slice's owner reference needs.
type nodeObject struct {
	Metadata struct {
		Name string `json:"name"`
		UID  string `json:"uid"`
	} `json:"metadata"`
}

// nodeOwner reads the node and builds the owner reference of its slice.
func nodeOwner(c *apiclient.Client, nodeName string) (OwnerReference, error) {
	node, err := apiclient.Get[nodeObject](c, "/api/v1/nodes/"+nodeName)
	if err != nil {
		return OwnerReference{}, err
	}
	return OwnerReference{APIVersion: "v1", Kind: "Node", Name: node.Metadata.Name, UID: node.Metadata.UID}, nil
}
