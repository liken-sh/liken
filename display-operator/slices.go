package main

// Publishing the card's outputs as this operator's own ResourceSlice.
//
// A device operator publishes under its own driver name, in its own
// slices, beside whatever liken publishes on the same node. The two
// cannot collide: a device's identity is the triple
// <driver>/<pool>/<device>, and the slice name carries the driver name
// as a suffix, so this node's two slices are <node>-liken.sh and
// <node>-display.liken.sh.
//
// Like liken's own client, these structs carry only the part of the
// upstream API that this program writes. The full ResourceSlice can
// describe partitionable devices, shared counters, and per-device node
// selection, and none of that changes what a monitor output needs: a
// name, the EDID facts, and taints when the output can serve nobody.
//
// One slice holds the whole inventory, so the pool protocol reduces to
// a version counter: bump the generation on every change, and one
// slice is always a consistent snapshot.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
)

// DriverName identifies this operator as a DRA driver. A driver name
// is a DNS name so that drivers cannot collide, and a device
// operator's name is <domain>.liken.sh. The name states which contract
// family the operator implements, not which repository builds it.
const DriverName = "display.liken.sh"

// ResourceSlicesPath names the URL where DRA inventory lives. Slices
// are cluster-scoped, like Nodes, because hardware inventory belongs
// to the machine and not to any tenant.
const ResourceSlicesPath = "/apis/resource.k8s.io/v1/resourceslices"

// maxSliceDevices is the API's limit on devices in one slice. The
// limit is 128 for a slice with no taints and 64 for a slice that
// taints any device, and this operator taints every dark output, so 64
// is the number that applies. A graphics card registers far fewer
// connectors than that.
const maxSliceDevices = 64

// The two taints an output carries while it can serve nobody. Both go
// on together, and they are separate keys because they do separate
// jobs.
//
// disconnectedTaint is the one a consumer tolerates. Its NoExecute
// effect makes the taint-eviction controller end the pod that holds
// the claim, and the claim's own tolerationSeconds says how long a
// monitor may be dark first. A five second unplug should not end a
// video.
//
// noOutputTaint is the one nothing tolerates. A tolerated NoExecute
// taint still lets the scheduler allocate the device, and a pod that
// allocates an output the operator cannot prepare loops: the kubelet
// holds it in ContainerCreating, the eviction controller ends it when
// the tolerationSeconds runs out, and the scheduler allocates the same
// dark output to the replacement. An untolerated NoSchedule taint
// stops that at the front: the pod parks Unschedulable, visibly, until
// a monitor comes back.
const (
	disconnectedTaint = DriverName + "/disconnected"
	noOutputTaint     = DriverName + "/no-output"
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
// matters: a reference names one instance of the owner, so a Node that
// is deleted and registered again under the same name does not inherit
// the old node's slices.
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
// device.attributes["display.liken.sh"].model. The one exception is
// the pairing identity, which carries its own domain.
type SliceDevice struct {
	Name       string                     `json:"name"`
	Attributes map[string]DeviceAttribute `json:"attributes,omitempty"`
	Taints     []DeviceTaint              `json:"taints,omitempty"`
}

// DeviceAttribute holds exactly one of four typed values. The API
// keeps the types apart so that a selector compares a number as a
// number, instead of against the string "1920".
type DeviceAttribute struct {
	Bool    *bool   `json:"bool,omitempty"`
	Int     *int64  `json:"int,omitempty"`
	String  *string `json:"string,omitempty"`
	Version *string `json:"version,omitempty"`
}

// DeviceTaint keeps a claim off a device, and evicts the pods of the
// claims that already hold it when the effect is NoExecute.
//
// TimeAdded is a field the API server fills in on write. This operator
// never sets it, and reads it back only so that the change detection
// can ignore it (see sameDevices).
type DeviceTaint struct {
	Key       string `json:"key"`
	Value     string `json:"value,omitempty"`
	Effect    string `json:"effect"`
	TimeAdded string `json:"timeAdded,omitempty"`
}

// AttrString builds a string-typed attribute value without repeating
// pointer syntax at every call site.
func AttrString(s string) DeviceAttribute { return DeviceAttribute{String: &s} }

// AttrInt builds an integer attribute value.
func AttrInt(i int) DeviceAttribute { v := int64(i); return DeviceAttribute{Int: &v} }

// sliceDevices turns the card's connectors into the devices the slice
// publishes, one for each connector.
//
// Membership is every connector, and it never depends on what is
// plugged in. A dark output is still a device a person can claim, and
// the pod parks until a monitor arrives. A monitor that leaves takes
// its EDID attributes with it and leaves the device in place with two
// taints on it, because deleting a device that a claim holds strands
// the next consumer.
//
// The attributes are the monitor's own facts, so a claim can name one
// screen by model or by serial, or select any output that fits and
// take whichever one is free.
//
// routed holds the device names the compositor's config carries an
// [output] section for, which the operator writes once at startup. A
// connector that is not in it cannot be routed to, whatever is plugged
// into it, and that is the one case where an untainted device would be
// worse than useless: the kiosk shell sends a surface whose app-id
// matches no output to the first output it enumerated, on top of the
// client that owns that screen. The NoSchedule taint is what keeps a
// claim off it until the operator restarts and writes the section.
func sliceDevices(outputs []Output, routed map[string]bool) []SliceDevice {
	devices := make([]SliceDevice, 0, len(outputs))
	for _, output := range outputs {
		name := deviceName(output.Connector)
		device := SliceDevice{
			Name: name,
			Attributes: map[string]DeviceAttribute{
				"connector": AttrString(output.Connector),
				"appId":     AttrString(appID(output.Connector)),
			},
		}
		monitor := output.Monitor
		if output.Connected {
			addAttribute(device.Attributes, "manufacturer", monitor.Manufacturer)
			addAttribute(device.Attributes, "model", monitor.ModelName)
			addAttribute(device.Attributes, "serial", monitor.Serial)
			addAttribute(device.Attributes, pairingAttribute, monitorID(monitor))
			addSize(device.Attributes, "widthPixels", monitor.WidthPixels)
			addSize(device.Attributes, "heightPixels", monitor.HeightPixels)
			addSize(device.Attributes, "widthMillimeters", monitor.WidthMillimeters)
			addSize(device.Attributes, "heightMillimeters", monitor.HeightMillimeters)
		}
		switch {
		case !output.Connected:
			device.Taints = unservableTaints()
		case !routed[name]:
			// The monitor is on the wire and the compositor has no
			// section for it. Nothing is running on this screen, so
			// there is no pod to evict and the NoExecute taint would
			// say something untrue.
			device.Taints = []DeviceTaint{{Key: noOutputTaint, Effect: "NoSchedule"}}
		}
		devices = append(devices, device)
	}
	slices.SortFunc(devices, func(a, b SliceDevice) int {
		return strings.Compare(a.Name, b.Name)
	})
	return devices
}

// unservableTaints is what an output that can serve nobody carries:
// the NoExecute taint that ends the pod holding it, and the NoSchedule
// taint that keeps the next pod from allocating it.
func unservableTaints() []DeviceTaint {
	return []DeviceTaint{
		{Key: disconnectedTaint, Effect: "NoExecute"},
		{Key: noOutputTaint, Effect: "NoSchedule"},
	}
}

// beforeTheCompositor is what the operator publishes at startup. It
// adds the untolerated NoSchedule taint to every device and keeps every
// taint the hardware read already produced.
//
// The two taints answer two questions, and startup has an answer for
// only one of them.
//
// Nothing routes to a screen until the compositor enumerates its
// heads, so no output can serve a client yet and no new claim may
// land on one. Last boot's slice may still say they all can. That is
// the noOutputTaint, and it goes on every device.
//
// Whether a connector is dark is the other question, and sysfs and
// the EDID answer it with no compositor running at all. sliceDevices
// has already read them, so a dark connector arrives here with its
// NoExecute taint on it and keeps it. Its holder is evicted even if
// the compositor never starts, so nothing waits on a reconcile that
// never runs.
//
// A connector with a monitor on it arrives with no NoExecute taint,
// and none is added. That taint ends the pod holding the output, and
// a restart of this operator is no reason to end a client whose
// monitor never moved.
func beforeTheCompositor(devices []SliceDevice) []SliceDevice {
	out := make([]SliceDevice, len(devices))
	for i, device := range devices {
		out[i] = device
		out[i].Taints = slices.Clone(device.Taints)
		if !carries(out[i].Taints, noOutputTaint) {
			out[i].Taints = append(out[i].Taints,
				DeviceTaint{Key: noOutputTaint, Effect: "NoSchedule"})
		}
	}
	return out
}

// carries reports whether the taints already name this key.
func carries(taints []DeviceTaint, key string) bool {
	return slices.ContainsFunc(taints, func(taint DeviceTaint) bool {
		return taint.Key == key
	})
}

// afterTheCompositor taints every device, whatever is plugged in. It is
// what the operator publishes as the compositor it runs exits.
//
// Both taints are facts here. The compositor held the screens and the
// socket, so every client has already lost its connection, and this
// write is the only thing that ends them: a replacement pod that
// published the same untainted devices would raise no scheduler event
// at all, so nothing would ever evict the pods that are drawing into a
// socket that is gone.
func afterTheCompositor(devices []SliceDevice) []SliceDevice {
	out := make([]SliceDevice, len(devices))
	for i, device := range devices {
		out[i] = device
		out[i].Taints = unservableTaints()
	}
	return out
}

// addAttribute publishes a string value, and publishes nothing when
// the monitor stated nothing. An absent attribute and an empty one
// read the same to a person and not to a selector: has() answers false
// for the absent one, and a selector that compares an empty string
// matches every monitor that stated nothing.
func addAttribute(attributes map[string]DeviceAttribute, name, value string) {
	if value == "" {
		return
	}
	attributes[name] = AttrString(attributeString(value))
}

// addSize publishes a measurement, and publishes nothing when the
// monitor stated zero. Zero pixels and zero millimeters are both the
// absence of an answer, never a size.
func addSize(attributes map[string]DeviceAttribute, name string, value int) {
	if value <= 0 {
		return
	}
	attributes[name] = AttrInt(value)
}

// attributeString limits a free-text value to the API's 64-character
// limit on attribute strings. A monitor writes at most 13 characters
// into a descriptor, so nothing from an EDID reaches the limit, and
// the limit is what keeps a malformed answer from failing the whole
// write.
func attributeString(s string) string {
	if len(s) <= 64 {
		return s
	}
	return s[:64]
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

// ErrNoDevices refuses a write that would publish nothing.
//
// An empty inventory is never a fact this operator learns. The card
// registers its connectors when the driver binds and keeps them until
// the card leaves, so a pass that finds none read a card that is going
// away, or read the wrong path. Writing that would replace a slice
// that consumers hold with an empty one, and an empty slice is a
// delete of every device in it.
var ErrNoDevices = errors.New("the card reports no connectors")

// EnsureResourceSlice makes this operator's published slice match the
// card's outputs. It creates the slice on the first pass, replaces the
// slice when anything changed, and writes nothing when nothing moved.
//
// It never deletes. A monitor that leaves is a taint, the operator's
// own shutdown retracts nothing, and a slice outlives every restart of
// the pod. The Node owns the slice, so a node that leaves the cluster
// takes the slice with it, and that is the only automatic removal.
//
// The write carries the resourceVersion from the read, so a
// conflicting writer gets ErrConflict instead of losing its change.
// The next pass reads again and writes again.
func EnsureResourceSlice(c *Client, nodeName string, owner OwnerReference, devices []SliceDevice) error {
	if len(devices) == 0 {
		return ErrNoDevices
	}
	if len(devices) > maxSliceDevices {
		return fmt.Errorf("%d outputs exceed one slice's capacity of %d", len(devices), maxSliceDevices)
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
