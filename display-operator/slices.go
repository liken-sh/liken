package main

// This file publishes the card's connectors as a ResourceSlice named
// <node>-display.liken.sh. The name cannot collide with the
// <node>-liken.sh slice that `liken` publishes for the same node. The
// structs below have only the fields of the ResourceSlice API that this
// operator writes.
//
// The pool of each node is one slice with every device in it, so each
// write is a complete snapshot and increases the pool generation.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// DriverName is the DRA driver name of this operator. It is named for
// the devices it publishes, not for the operator's directory.
const DriverName = "display.liken.sh"

// ResourceSlices are cluster-scoped, so the path has no namespace.
const ResourceSlicesPath = "/apis/resource.k8s.io/v1/resourceslices"

// maxSliceDevices is the API's limit on devices in one slice. The API
// allows 128 devices in a slice with no taints and 64 in a slice where
// any device has a taint. This operator taints devices, so it uses 64.
// A connector publishes at most three devices, so one slice fits 21
// connectors, more than a graphics card has.
const maxSliceDevices = 64

// disconnectedTaint marks the devices of an output that cannot serve a
// client. Its NoExecute effect makes the taint-eviction controller
// delete the pods of claims that hold the device. A claim can tolerate
// the taint with tolerationSeconds, so a short unplug does not delete
// the pod.
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

// OwnerReference makes Kubernetes delete the owned object when its
// owner is deleted. The UID identifies one instance of the owner, so a
// Node that is deleted and registered again under the same name does
// not keep the old Node's slices.
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

// SliceDevice is one device that a claim can request. An attribute name
// with no domain belongs to the driver's domain, so a selector reads the
// model as device.attributes["display.liken.sh"].model.
// pairingAttribute has a domain of its own.
type SliceDevice struct {
	Name string `json:"name"`
	// AllowMultipleAllocations lets several claims hold the device at
	// the same time. Only the draw device sets it. When it is nil, the
	// field is left out of the JSON and one claim at a time can hold the
	// device.
	AllowMultipleAllocations *bool                      `json:"allowMultipleAllocations,omitempty"`
	Attributes               map[string]DeviceAttribute `json:"attributes,omitempty"`
	Taints                   []DeviceTaint              `json:"taints,omitempty"`
}

// DeviceAttribute must have exactly one field set. The API keeps the
// types separate, so a selector compares a width with a number and not
// with the string "1920".
type DeviceAttribute struct {
	Bool    *bool   `json:"bool,omitempty"`
	Int     *int64  `json:"int,omitempty"`
	String  *string `json:"string,omitempty"`
	Version *string `json:"version,omitempty"`
}

// The scheduler allocates a tainted device only to a claim that
// tolerates the taint. With the effect NoExecute, the taint-eviction
// controller also deletes the pods of claims that already hold the
// device.
//
// The API server sets TimeAdded when it stores the taint. This operator
// never sets it, and sameDevices ignores it.
type DeviceTaint struct {
	Key       string `json:"key"`
	Value     string `json:"value,omitempty"`
	Effect    string `json:"effect"`
	TimeAdded string `json:"timeAdded,omitempty"`
}

func AttrString(s string) DeviceAttribute { return DeviceAttribute{String: &s} }

func AttrInt(i int) DeviceAttribute { v := int64(i); return DeviceAttribute{Int: &v} }

func AttrBool(b bool) DeviceAttribute { return DeviceAttribute{Bool: &b} }

// sliceDevices returns the devices of the slice, sorted by name. Each
// connector publishes an output device and a draw device, and also a
// control device when its panel responds to DDC/CI.
//
// Every connector publishes its devices whether or not a monitor is
// connected, so a claim on an empty connector waits until a monitor
// arrives. The compositor configuration has an [output] section for
// every connector, so a newly connected monitor can show a client with
// no restart and needs no taint. getsDisconnectedTaint decides which
// outputs get the taint.
//
// The attributes describe the monitor, so a claim can select one screen
// by model or serial, or any output that fits.
func sliceDevices(outputs []Output) []SliceDevice {
	// When two connectors report the same monitor with different CEC
	// physical addresses, neither device publishes an address, because
	// a CEC client that read one of them could switch the receiver to
	// the wrong input.
	ambiguous := ambiguousAddresses(outputs)
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
		// While a connector is disconnected, output.monitor() returns the
		// monitor it remembers, so a claim that selects this screen by
		// model still matches it. The attributes inside the Connected
		// branch come from the EDID on the wire, so a disconnected
		// connector publishes none of them.
		monitor := output.monitor()
		addAttribute(device.Attributes, "manufacturer", monitor.Manufacturer)
		addAttribute(device.Attributes, "model", monitor.ModelName)
		addAttribute(device.Attributes, "serial", monitor.Serial)
		addAttribute(device.Attributes, pairingAttribute, monitorID(monitor))
		if output.Connected {
			addSize(device.Attributes, "widthPixels", monitor.WidthPixels)
			addSize(device.Attributes, "heightPixels", monitor.HeightPixels)
			// 60 Hz is 60000, and a monitor can report 59999 for it.
			addSize(device.Attributes, "refreshMillihertz", monitor.RefreshMillihertz)
			addSize(device.Attributes, "widthMillimeters", monitor.WidthMillimeters)
			addSize(device.Attributes, "heightMillimeters", monitor.HeightMillimeters)
			// The mode names that a claim's mode parameter can select. The
			// list is cut to maxAttributeLength, so applyMode validates a
			// claim's mode against the mode list that the card returns,
			// not against this attribute.
			addAttribute(device.Attributes, "modes", attributeList(output.Modes))
			// The mode that the output runs now, such as 3840x1600@24. It
			// is absent when the output drives no mode, when the card
			// cannot be read, and while the operator has no connection to
			// a compositor.
			addAttribute(device.Attributes, "currentMode", output.CurrentMode)
			// The CEC physical address of the input that the cable is
			// plugged into, such as 1.2.0.0. A disconnected connector
			// publishes none, because the scheduler allocates against the
			// hardware as it is now. The Display's status keeps the last
			// address.
			address := output.Monitor.PhysicalAddress
			if _, isAmbiguous := ambiguous[monitorID(monitor)]; isAmbiguous {
				address = ""
			}
			addAttribute(device.Attributes, "physicalAddress", address)
			// The scheduler does not read a claim's opaque parameters, so
			// a workload that needs a panel control selects on these
			// attributes to get a panel that supports it.
			addControl(device.Attributes, "controlsBrightness", output.Controls.Brightness)
			addControl(device.Attributes, "controlsPower", output.Controls.Power)
		}
		if getsDisconnectedTaint(output) {
			device.Taints = disconnectedTaints()
		}
		devices = append(devices, device)
		if control, ok := controlDevice(output, device.Taints); ok {
			devices = append(devices, control)
		}
		devices = append(devices, drawDevice(output, device.Taints))
	}
	slices.SortFunc(devices, func(a, b SliceDevice) int {
		return strings.Compare(a.Name, b.Name)
	})
	return devices
}

// controlDevice returns the connector's control device and whether the
// connector has one. The control device is for a pod that changes the
// panel's brightness, power, or input while it runs. Its claim gives the
// pod the connector's i2c device node, so the pod runs its own ddcutil
// and needs no Wayland connection.
//
// The device exists only while a monitor is connected and its DDC/CI
// probe read at least one control. A claim on a panel without
// DDC/CI stays unallocated, and its pod does not get an i2c node that
// no panel responds on.
//
// The output, control, and draw devices all publish the monitor
// identity, so one claim can request a screen and its controls with a
// matchAttribute constraint on monitor.liken.sh/id.
//
// The control device has the output device's taints. Because it exists
// only while a monitor is connected, that taint is set only when a
// different monitor replaced the remembered one. compositorDown leaves
// the control device's taints unchanged.
func controlDevice(output Output, taints []DeviceTaint) (SliceDevice, bool) {
	if !output.Connected || (!output.Controls.Brightness && !output.Controls.Power) {
		return SliceDevice{}, false
	}
	attributes := map[string]DeviceAttribute{
		"connector": AttrString(output.Connector),
		// A DeviceClass selects control devices on this attribute. The
		// absence of appId cannot select them, because the draw device
		// has no appId either.
		"control": AttrBool(true),
	}
	addAttribute(attributes, pairingAttribute, monitorID(output.Monitor))
	addControl(attributes, "controlsBrightness", output.Controls.Brightness)
	addControl(attributes, "controlsPower", output.Controls.Power)
	return SliceDevice{
		Name:       controlName(output.Connector),
		Attributes: attributes,
		Taints:     taints,
	}, true
}

// drawDevice returns the connector's draw device. Many claims can hold
// it at the same time, and each gets its own Wayland socket to the
// compositor on this output, so other clients can draw on a screen that
// an output claim drives. The output device allows one claim, because
// that claim sets the panel's mode.
//
// Like the output device, the draw device exists for every connector
// and has the output device's taints.
//
// It publishes no appId, because the output DeviceClass selects on
// has(appId). The client still receives an app-id at prepare, built
// from the connector.
func drawDevice(output Output, taints []DeviceTaint) SliceDevice {
	shared := true
	attributes := map[string]DeviceAttribute{
		"connector": AttrString(output.Connector),
		"draw":      AttrBool(true),
	}
	monitor := output.monitor()
	addAttribute(attributes, "manufacturer", monitor.Manufacturer)
	addAttribute(attributes, "model", monitor.ModelName)
	addAttribute(attributes, "serial", monitor.Serial)
	addAttribute(attributes, pairingAttribute, monitorID(monitor))
	return SliceDevice{
		Name:                     drawName(output.Connector),
		AllowMultipleAllocations: &shared,
		Attributes:               attributes,
		Taints:                   taints,
	}
}

// getsDisconnectedTaint reports whether the output's devices get the
// disconnected taint. That is true for a connector that never had a
// monitor, and for a connector that reconnected with a different
// monitor. A disconnected connector that remembers its monitor is not
// tainted, because the monitor is usually still there, showing another
// input, and a taint would delete the pod that draws on it.
func getsDisconnectedTaint(output Output) bool {
	if output.Replaced {
		return true
	}
	// A connected monitor whose EDID cannot be read has no monitorID,
	// but it can still show a client.
	if output.Connected {
		return false
	}
	return monitorID(output.Remembered) == ""
}

func disconnectedTaints() []DeviceTaint {
	return []DeviceTaint{
		{Key: disconnectedTaint, Effect: "NoExecute"},
	}
}

// compositorDown returns the devices with the disconnected taint on
// every output and draw device, whether or not a monitor is connected.
// The operator publishes this on every pass that finds no compositor on
// its socket: before the compositor container starts, and while it
// restarts. No output can show a Wayland client then, so new claims
// wait until the first pass after the socket appears removes the taint.
//
// The taint also makes the taint-eviction controller delete the pods
// that were drawing, because they lost their Wayland connections when
// the compositor exited. The restarted compositor serves the same
// devices, so without this write the slice would not change and
// nothing would delete those pods.
//
// Control devices keep their own taints. A control pod uses the i2c
// bus, which works without a compositor, so a taint would delete it on
// every compositor restart, and would keep a control claim unallocated
// on a machine where no compositor runs.
//
// An output device whose claim is still preparing gets no taint. A
// prepare that changes the mode restarts the compositor and waits for
// it, and the claim's pod does not start until prepare returns. A taint
// would delete that pod. The Display pass would then find the screen
// free and restore its declared mode with another restart, and the
// replacement pod would change the mode again (screenholds.go). The
// draw device of that output still gets the taint, because preparing
// has only output device names.
func compositorDown(devices []SliceDevice, preparing map[string]bool) []SliceDevice {
	out := make([]SliceDevice, len(devices))
	for i, device := range devices {
		out[i] = device
		if _, control := outputOfControl(device.Name); control {
			continue
		}
		if preparing[device.Name] {
			continue
		}
		out[i].Taints = disconnectedTaints()
	}
	return out
}

// addAttribute sets a string attribute unless the value is empty. An
// absent attribute and an empty one differ for a selector: has() is
// false for the absent one, and a comparison with an empty string
// matches every monitor that reported nothing.
func addAttribute(attributes map[string]DeviceAttribute, name, value string) {
	if value == "" {
		return
	}
	attributes[name] = AttrString(attributeString(value))
}

// addControl sets a control attribute to true when the panel responded to
// that control's DDC/CI code. It never writes false, so a selector
// tests only whether the attribute is present.
func addControl(attributes map[string]DeviceAttribute, name string, supported bool) {
	if !supported {
		return
	}
	attributes[name] = AttrBool(true)
}

// addSize sets an integer attribute unless the value is zero or less,
// which means the monitor reported nothing.
func addSize(attributes map[string]DeviceAttribute, name string, value int) {
	if value <= 0 {
		return
	}
	attributes[name] = AttrInt(value)
}

// maxAttributeLength is the API's limit on the length of a string
// attribute value. A longer value makes the API reject the whole
// slice.
const maxAttributeLength = 64

// attributeString cuts a value to maxAttributeLength. An EDID text
// descriptor holds at most 13 characters, so only a malformed EDID
// reaches the limit.
func attributeString(s string) string {
	if len(s) <= maxAttributeLength {
		return s
	}
	return s[:maxAttributeLength]
}

// attributeList joins values with spaces and stops before the first
// value that would take the string past maxAttributeLength. A device
// attribute has no list type, so a selector tests the string with
// .contains(), as with audio-operator's lpcmBitDepths.
//
// The cut falls between values, because half of a mode name could match
// a different mode in .contains(). The caller passes the best value
// first, so the cut drops the values that a claim is least likely to
// select.
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

// sameDevices reports whether the published devices equal the current
// ones, ignoring TimeAdded. The API server sets TimeAdded on every
// taint it stores, so a plain comparison would find a difference on
// every pass and write the slice each time. Each ResourceSlice write
// makes the scheduler retry every pod in the cluster that waits on a
// claim.
func sameDevices(published, current []SliceDevice) bool {
	return reflect.DeepEqual(withoutTimeAdded(published), withoutTimeAdded(current))
}

// withoutTimeAdded returns a copy of the devices with TimeAdded cleared
// on every taint, and an empty taint list as nil.
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

// ErrNoDevices is the error for a pass that found no connectors. A card
// registers its connectors when its driver binds and keeps them until
// the card is removed, so an empty list means the card is going away or
// the operator read the wrong path. Publishing an empty slice would
// delete every device that claims hold.
var ErrNoDevices = errors.New("the card reports no connectors")

// EnsureResourceSlice makes the node's ResourceSlice match the devices.
// It creates the slice when it does not exist, replaces it with a
// higher pool generation when the devices changed, and writes nothing
// when they did not.
//
// It never deletes the slice, so the devices stay through an operator
// restart. The Node owns the slice, so Kubernetes deletes it with the
// Node.
//
// The PUT sends the resourceVersion that the GET returned, so a
// concurrent write makes it fail with apiclient.ErrConflict, and the
// next pass tries again.
func EnsureResourceSlice(c *apiclient.Client, nodeName string, owner OwnerReference, devices []SliceDevice) error {
	if len(devices) == 0 {
		return ErrNoDevices
	}
	if len(devices) > maxSliceDevices {
		return fmt.Errorf("%d devices exceed the %d that one ResourceSlice holds", len(devices), maxSliceDevices)
	}
	name := sliceName(nodeName)
	path := ResourceSlicesPath + "/" + name

	// The operator reads its slice on each publishing pass and does not
	// watch it. One ClusterRole for every node cannot name each node's
	// slice, so a watch would need list and watch on every ResourceSlice
	// in the cluster. deploy/rbac.yaml grants only get, create, and
	// update, so this operator cannot list other drivers' slices.
	current, err := apiclient.Get[ResourceSlice](c, path)
	if err == apiclient.ErrNotFound {
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

	// The log line compares the published devices with the new ones.
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

// nodeObject is the part of a Node that the slice's owner reference
// needs.
type nodeObject struct {
	Metadata struct {
		Name string `json:"name"`
		UID  string `json:"uid"`
	} `json:"metadata"`
}

// NodeOwner returns an owner reference to the Node that this operator
// runs on.
func NodeOwner(c *apiclient.Client, nodeName string) (OwnerReference, error) {
	node, err := apiclient.Get[nodeObject](c, "/api/v1/nodes/"+nodeName)
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
