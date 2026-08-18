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
	"slices"
	"strings"
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
// cannot play, so 64 is the number that applies. One card has far
// fewer PCM devices than that.
const maxSliceDevices = 64

// maxAttributeLength is the API's limit on the length of a string
// attribute.
const maxAttributeLength = 64

// The three taints this operator applies. One says what happens to a
// pod that holds the output now, and the other two each name one
// reason the output cannot play.
//
// disconnectedTaint says the output is silent right now. The effect
// is NoExecute, so the taint-eviction controller ends the pod that
// holds the claim, and the consumer's own tolerationSeconds sets how
// long an output may be silent first. A consumer tolerates this one,
// because a monitor that a person unplugs for a moment is not a loss.
//
// noMonitorTaint says no monitor answers on this HDMI output, and
// noSinkTaint says PipeWire holds no node for this PCM device. Either
// one alone makes the output unusable, so both have the NoSchedule
// effect, and no consumer should ever tolerate either. This is what
// makes a claim ahead of a monitor park instead of loop. With only
// the NoExecute taint, a consumer that tolerated it would be
// scheduled onto an output that plays into nothing. The pod would be
// evicted when the toleration ran out, and scheduled again. An
// untolerated NoSchedule taint holds the pod Unschedulable until the
// output can play.
//
// The two reasons have separate keys because they are separate facts
// about the machine, and each one has its own repair. A missing
// monitor is a cable, and somebody plugs it back in. A missing node is
// an output PipeWire could not create, which the nofail flag on each
// declared object leaves possible, and only a restart creates it.
const (
	disconnectedTaint = DriverName + "/disconnected"
	noMonitorTaint    = DriverName + "/no-monitor"
	noSinkTaint       = DriverName + "/no-sink"
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

// sliceDevices turns the card's outputs into the devices the slice
// publishes, one for each output, sorted by name so that the same
// hardware always produces the same slice.
//
// Membership is the card's playback PCM devices. An output whose
// monitor is unplugged is still a device a person can claim, and the
// pod parks Unschedulable until somebody plugs the monitor back in.
// What a monitor takes with it is the attributes it supplied and the
// taints that follow it, never the device. Deleting a device that a
// claim holds strands the next consumer: the allocation still names
// the device, and the kubelet's prepare call retries against a device
// that is in no slice, with no bound on the retry.
func sliceDevices(outputs []alsaOutput, sinks map[pcmAddress]string) []SliceDevice {
	devices := make([]SliceDevice, 0, len(outputs))
	for _, output := range outputs {
		sink, hasSink := sinks[pcmAddress{Card: output.Card, PCM: output.PCM}]
		// The device name is not selectable. A DeviceClass and a claim
		// select with CEL over device.driver, device.attributes,
		// device.capacity, and device.allowMultipleAllocations, and
		// there is no device.name among them, so an identity that exists
		// only in the name is one a selector cannot read. The
		// output attribute holds the same string, and card and pcm
		// hold its two halves as numbers, so a claim can name one
		// output and a claim can ask for every output of one card.
		device := SliceDevice{
			Name: output.Name(),
			Attributes: map[string]DeviceAttribute{
				"output": AttrString(output.Name()),
				"card":   AttrInt(int64(output.Card)),
				"pcm":    AttrInt(int64(output.PCM)),
			},
		}
		if connection := output.connectionType(); connection != "" {
			device.Attributes["connectionType"] = AttrString(connection)
		}
		if output.Monitor {
			addMonitorAttributes(device.Attributes, output.ELD)
		}

		// An output plays only when PipeWire holds a sink for it, and an
		// HDMI output plays only when a monitor answers on it. Either one
		// missing means the output cannot serve a stream now, which is
		// what the NoExecute taint says, and each one publishes its own
		// NoSchedule taint below to say which of them it is.
		unplugged := output.HDMI && !output.Monitor
		if !hasSink || unplugged {
			device.Taints = append(device.Taints,
				DeviceTaint{Key: disconnectedTaint, Effect: "NoExecute"})
		}
		if unplugged {
			device.Taints = append(device.Taints,
				DeviceTaint{Key: noMonitorTaint, Effect: "NoSchedule"})
		}
		publishSink(&device, sink, hasSink)
		devices = append(devices, device)
	}
	slices.SortFunc(devices, func(a, b SliceDevice) int {
		return strings.Compare(a.Name, b.Name)
	})
	return devices
}

// publishSink writes what PipeWire holds for one output: the sink
// node's name as an attribute, or the no-sink taint when there is no
// node. One branch sets both, so no device can have the name of a
// sink and the taint that says it has none. Those two would state
// opposite facts about one output.
//
// A name past the attribute limit is left out, and the output keeps
// its sink. The name still reaches the consumer, because the delivery
// reads the current name from PipeWire rather than from the slice, so
// a truncated one would only give a selector a name that names
// nothing.
func publishSink(device *SliceDevice, name string, hasSink bool) {
	if !hasSink {
		device.Taints = append(device.Taints,
			DeviceTaint{Key: noSinkTaint, Effect: "NoSchedule"})
		return
	}
	if len(name) > maxAttributeLength {
		return
	}
	device.Attributes["sinkName"] = AttrString(name)
}

// addMonitorAttributes publishes what the monitor's ELD block says.
//
// The pairing attribute has its own domain, monitor.liken.sh/id,
// because the display operator publishes the same name for the same
// monitor and a matchAttribute constraint compares the two. An
// attribute written without a domain belongs to the driver that
// published it, so two bare names from two drivers never match.
func addMonitorAttributes(attributes map[string]DeviceAttribute, block eld) {
	if block.Manufacturer != "" {
		attributes["manufacturer"] = AttrString(block.Manufacturer)
	}
	attributes["product"] = AttrString(hexProduct(block.Product))
	if name := attributeString(block.MonitorName); name != "" {
		attributes["monitorName"] = AttrString(name)
	}
	if block.LPCMChannels > 0 {
		attributes["lpcmChannels"] = AttrInt(int64(block.LPCMChannels))
	}
	if block.Speakers != "" {
		attributes["speakers"] = AttrString(attributeString(block.Speakers))
	}
	if id := block.monitorID(); id != "" {
		attributes[PairingAttribute] = AttrString(attributeString(id))
	}
}

// attributeString limits a value to the API's limit on the length of
// a string attribute. The monitor name is the only value here that a
// manufacturer can make long, and the ELD block holds at most 16
// characters of it.
func attributeString(s string) string {
	if len(s) <= maxAttributeLength {
		return s
	}
	return s[:maxAttributeLength]
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
