package main

// Publishing the card's outputs as this operator's own ResourceSlice.
//
// A device operator publishes under its own driver name, in its own
// slices, beside whatever liken publishes on the same node. The two
// cannot collide: a device's identity is the triple
// <driver>/<pool>/<device>, and the slice name ends with the driver
// name, so this node's two slices are <node>-liken.sh and
// <node>-display.liken.sh.
//
// Like liken's own client, these structs hold only the part of the
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
// operator's name is <domain>.liken.sh. The name states the contract
// the operator implements rather than the repository that builds it.
const DriverName = "display.liken.sh"

// ResourceSlicesPath names the URL of the DRA inventory. Slices
// are cluster-scoped, like Nodes, because hardware inventory belongs
// to the machine and not to any tenant.
const ResourceSlicesPath = "/apis/resource.k8s.io/v1/resourceslices"

// maxSliceDevices is the API's limit on devices in one slice. The
// limit is 128 for a slice with no taints and 64 for a slice that
// taints any device, and this operator taints every dark output, so 64
// is the number that applies. A graphics card registers far fewer
// connectors than that.
const maxSliceDevices = 64

// disconnectedTaint is the one a consumer tolerates. Its NoExecute
// effect makes the taint-eviction controller end the pod that holds
// the claim, and the claim's own tolerationSeconds says how long a
// monitor may be dark first. A five second unplug should not end a
// video.
const disconnectedTaint = DriverName + "/disconnected"

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
// the pairing identity, which has its own domain.
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
// its EDID attributes with it and leaves the device in place with its
// taint on it, because deleting a device that a claim holds strands
// the next consumer.
//
// The attributes are the monitor's own facts, so a claim can name one
// screen by model or by serial, or select any output that fits and
// take whichever one is free.
//
// The compositor's config has an [output] section for every
// connector, so only one fact taints a device: whether a monitor is
// connected. A monitor that arrives on any connector can serve a
// client as soon as the compositor enables its head.
func sliceDevices(outputs []Output) []SliceDevice {
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
			// The refresh is in millihertz. A selector that wants 60 Hz
			// exactly must ask for 60000, and a real monitor may answer
			// 59999.
			addSize(device.Attributes, "refreshMillihertz", monitor.RefreshMillihertz)
			addSize(device.Attributes, "widthMillimeters", monitor.WidthMillimeters)
			addSize(device.Attributes, "heightMillimeters", monitor.HeightMillimeters)
			// The modes list shows the alternatives to the preferred
			// mode, and a claim's mode parameter selects one of them.
			// The list is cut to fit the API's limit on a string
			// attribute, so it advertises and the connector's own sysfs
			// list is what a claim is validated against.
			addAttribute(device.Attributes, "modes", attributeList(output.Modes))
			// The mode this output runs right now, with its refresh,
			// 3840x1600@24, read from the card and not from sysfs.
			// The modes list above stays name-only. It follows a
			// claim's mode, and it is what makes a mode a released
			// claim left behind visible instead of hidden. It is
			// absent while the output drives nothing and when the
			// card could not answer.
			addAttribute(device.Attributes, "currentMode", output.CurrentMode)
		}
		if !output.Connected {
			device.Taints = unservableTaints()
		}
		devices = append(devices, device)
	}
	slices.SortFunc(devices, func(a, b SliceDevice) int {
		return strings.Compare(a.Name, b.Name)
	})
	return devices
}

// unservableTaints is the taint set of an output that can serve
// nobody: the NoExecute taint that ends the pod holding it.
func unservableTaints() []DeviceTaint {
	return []DeviceTaint{
		{Key: disconnectedTaint, Effect: "NoExecute"},
	}
}

// compositorDown taints every device, whatever is plugged in.
//
// The operator publishes this form on every pass that finds no
// compositor answering on the socket, which covers the start before
// the compositor's container creates it and every restart of that
// container after.
//
// No compositor holds the screens, so no output can serve a client.
// The first reconcile after the socket appears removes the taint from
// every screen that has a monitor. If the compositor never starts, the
// taint stays, and a claim parks instead of taking a screen that no
// compositor drives.
//
// This write is also what ends the clients that were drawing. Each
// one already lost its Wayland connection when the compositor died,
// and the restarted compositor serves the same devices again, so a
// slice that never changed would raise no event and nothing else
// would ever evict them.
func compositorDown(devices []SliceDevice) []SliceDevice {
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

// maxAttributeLength is the API's limit on the length of a string
// attribute's value. A write that exceeds it fails the whole slice,
// so every string this operator publishes is cut to fit first.
const maxAttributeLength = 64

// attributeString limits a free-text value to the API's limit on
// attribute strings. A monitor writes at most 13 characters into a
// descriptor, so nothing from an EDID reaches the limit, and the limit
// is what keeps a malformed answer from failing the whole write.
func attributeString(s string) string {
	if len(s) <= maxAttributeLength {
		return s
	}
	return s[:maxAttributeLength]
}

// attributeList joins a list of values into the one string that
// carries it, and ends the string on the last whole value that fits
// under the API's limit.
//
// A list is a string because the attribute language has no array
// type: a device attribute holds one bool, int, string, or version.
// So a list publishes space joined and a selector asks with
// .contains(), the same convention the audio operator's
// lpcmBitDepths follows.
//
// The cut keeps whole values only. Half a mode name names a mode no
// monitor accepts, and .contains() on the fragment would match the
// wrong modes. The caller passes values best first, so the cut drops
// the tail nobody selects on.
func attributeList(values []string) string {
	var joined string
	for _, value := range values {
		next := value
		if joined != "" {
			next = joined + " " + value
		}
		if len(next) > maxAttributeLength {
			break
		}
		joined = next
	}
	return joined
}

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

// ErrNoDevices refuses a write that would publish nothing.
//
// An empty inventory is never a real state of the card. The card
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
// The write includes the resourceVersion from the read, so a
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
