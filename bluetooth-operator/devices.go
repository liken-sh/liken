package main

// This file builds the devices of the node's ResourceSlice: one for
// each paired controller, and one for the media bus. slices.go writes
// the slice.

import (
	"slices"
	"strings"
)

// disconnectedTaint is set when bluetoothd reports the controller as
// not connected. Its effect is NoExecute, so the taint-eviction
// controller evicts the pod that holds the claim unless the pod
// tolerates the taint. A consumer sets tolerationSeconds to keep its
// pod through a short dropout.
//
// noInputNodeTaint is set when the relay (relay.go) has no virtual
// input device for the controller, so NodePrepareResources would fail.
// A bond that has never connected is in this state, because the relay
// copies a controller's capabilities from its real evdev node. The
// effect is NoSchedule and no consumer tolerates it, so the pod stays
// Unschedulable until the controller connects once. With a tolerated
// NoExecute taint, the pod would be scheduled, fail to prepare, be
// evicted when its toleration ran out, and be scheduled again.
//
// nodeMovedTaint is set when a prepared claim's CDI file lists device
// nodes that differ from the nodes the relay delivers for the
// controller now, as after an operator restart. The container may be
// reading another controller's device, so the effect is NoExecute and
// no consumer tolerates it. After the eviction, the kubelet unprepares
// the claim, which deletes the CDI file. The next pass removes the
// taint, and the replacement pod gets the current nodes.
const (
	disconnectedTaint = DriverName + "/disconnected"
	noInputNodeTaint  = DriverName + "/no-input-node"
	nodeMovedTaint    = DriverName + "/node-moved"
)

// sliceDevices returns one device for each paired controller, sorted by
// name so that the same paired set always produces the same slice.
//
// A controller's device stays in the slice for as long as it is
// paired, and a disconnect only changes `connected` and the taints. If
// the device left the slice while a claim held it, the allocation would
// name a device that is in no slice, and the kubelet would retry
// prepare against it with no limit.
//
// nodes maps each controller's address to the virtual input nodes of
// its relay. The relay keeps them open while the controller is
// disconnected.
//
// classes maps each address to the input classes of the controller's
// nodes. Each class is published as a boolean attribute named as udev
// names the class, so a DeviceClass can select a gamepad with
// `has(device.attributes["bluetooth.liken.sh"].joystick)` and not by
// the device's name.
//
// moved is the set of controllers that get nodeMovedTaint.
func sliceDevices(controllers map[string]controller, nodes map[string][]string, classes map[string]inputClasses, moved map[string]bool) []SliceDevice {
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
		// The raw class word and appearance are published beside the names
		// and flags that classify.go decodes from them, so a selector can
		// test either. Each is left out when BlueZ reported nothing, because
		// a class word of zero decodes to the major class miscellaneous,
		// which the controller never reported.
		if c.Appearance != 0 {
			device.Attributes["appearance"] = AttrInt(int64(c.Appearance))
		}
		for name, value := range map[string]string{
			"modalias":    c.Modalias,
			"icon":        c.Icon,
			"addressType": c.AddressType,
		} {
			if value := attributeString(value); value != "" {
				device.Attributes[name] = AttrString(value)
			}
		}
		if c.Class != 0 {
			device.Attributes["classOfDevice"] = AttrInt(int64(c.Class))
			if major := majorClass(c.Class); major != "" {
				device.Attributes["majorClass"] = AttrString(major)
			}
			if minor := minorClass(c.Class); minor != "" {
				device.Attributes["minorClass"] = AttrString(minor)
			}
			for _, flag := range serviceClassFlags(c.Class) {
				device.Attributes[flag] = AttrBool(true)
			}
		}
		for _, flag := range profileFlags(c.UUIDs) {
			device.Attributes[flag] = AttrBool(true)
		}
		for _, class := range classes[mac].names() {
			device.Attributes[class] = AttrBool(true)
		}
		usable := len(nodes[mac]) > 0
		if !c.Connected {
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
		if moved[mac] {
			device.Taints = append(device.Taints, DeviceTaint{
				Key:    nodeMovedTaint,
				Effect: "NoExecute",
			})
		}
		devices = append(devices, device)
	}
	slices.SortFunc(devices, func(a, b SliceDevice) int {
		return strings.Compare(a.Name, b.Name)
	})
	return devices
}

// supportsSoundAttribute is in the sound.liken.sh domain, which no one
// driver owns. machine-operator sets it on each sound card, and the
// DeviceClass of audio-operator selects on it without naming a driver,
// so its claim matches the sound cards and this media bus.
const supportsSoundAttribute = "sound.liken.sh/supportsSound"

// mediaBusKind is the value of the `kind` attribute on the media bus,
// so a selector can match the bus directly and not by the attributes
// it lacks.
const mediaBusKind = "mediaBus"

// mediaBusDevice returns the device for the adapter itself. A claim on
// it lets a pod connect to this pod's bluetoothd over its private
// D-Bus. A Bluetooth speaker creates no kernel device, so it plays only
// through a sound server that holds this claim and has registered a
// media endpoint with bluetoothd.
//
// The device is exclusive, the default in resource.k8s.io/v1, because
// two media endpoints registered with one bluetoothd have no defined
// behavior.
//
// The bus has no `input` attribute. The cluster's input DeviceClass
// selects on that attribute, and the bus must not be allocated to an
// input claim.
//
// When reachable is false, the adapter has gone, and the bus gets
// disconnectedTaint with the effect NoSchedule, which keeps new claims
// off it. NoExecute would evict the sound server that holds the claim,
// and that would also end its ALSA playback, which does not use the
// radio.
func mediaBusDevice(address string, reachable bool) SliceDevice {
	device := SliceDevice{
		Name: mediaBusName(address),
		Attributes: map[string]DeviceAttribute{
			supportsSoundAttribute: AttrBool(true),
			"kind":                 AttrString(mediaBusKind),
			"address":              AttrString(publishedMAC(address)),
		},
	}
	if !reachable {
		device.Taints = []DeviceTaint{{Key: disconnectedTaint, Effect: "NoSchedule"}}
	}
	return device
}

// attributeString cuts a free-text value, on a rune boundary, to the
// API's 64-byte limit on attribute strings. PairingRequest status.seen
// names use the same limit.
func attributeString(s string) string {
	return truncateToBytes(s, maxSeenNameBytes)
}
