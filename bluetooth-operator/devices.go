package main

// The devices this node offers: one for each paired controller and
// one for the media bus, with the attributes a selector reads and the
// taints that hold a claim back. slices.go holds the ResourceSlice
// object and the calls that write it; this file decides what goes in
// it, built from the paired set and from what the relay holds for
// each controller. The rest of this comment covers the attributes a
// selector reads, and the taints that say a device cannot serve a
// claim right now.

import (
	"slices"
	"strings"
)

// The three taints this operator applies, which answer three
// different questions.
//
// disconnectedTaint says the controller is off the air. The effect is
// NoExecute, so the taint-eviction controller ends the pod that holds
// the claim, and the consumer's own tolerationSeconds sets how long a
// radio may be silent first. A consumer tolerates this one, because a
// controller that drops for a moment is not a loss.
//
// noInputNodeTaint says the operator holds no virtual input device for
// the controller, so a claim on it would deliver nothing and
// NodePrepareResources would fail. That is the state of a bond that has
// never connected since it was made: the relay reads a controller's
// capabilities from its real evdev node, and it has never had one to
// read. The effect is NoSchedule, and no consumer should ever tolerate
// it. It parks such a claim instead of looping it. With only the
// NoExecute taint, a consumer that tolerated it would be scheduled onto
// a controller the operator cannot deliver. It would fail in prepare,
// get evicted when the toleration ran out, and be scheduled again. An
// untolerated NoSchedule taint holds the pod Unschedulable until the
// controller has connected once.
//
// nodeMovedTaint says the consumer's container holds nodes that are
// not the nodes the operator delivers for this controller now, which a
// restart of this operator leaves behind. The effect is NoExecute and
// no consumer tolerates it, because the container may be reading
// another controller's device. The eviction is the repair: the kubelet
// unprepares the claim, the consumer's operator creates the pod again,
// and the new prepare delivers the current nodes. The taint lifts on
// the pass after the stale file is gone.
const (
	disconnectedTaint = DriverName + "/disconnected"
	noInputNodeTaint  = DriverName + "/no-input-node"
	nodeMovedTaint    = DriverName + "/node-moved"
)

// sliceDevices turns the paired set into the devices the slice
// publishes, one for each controller, sorted by name so that the same
// hardware always produces the same slice.
//
// Membership is the paired set. A controller that is switched off is
// still a device a person can claim, and the pod parks Unschedulable
// until somebody turns it on. Connection state is an attribute and a
// taint, never membership. Deleting a device that a claim holds
// strands the next consumer: the allocation still names the device,
// and the kubelet's prepare call retries against a device that is in
// no slice, with no bound on the retry. A device leaves
// the slice only when it is unpaired.
//
// nodes are the virtual input nodes each controller's relay delivers,
// keyed by the controller's address. A controller that is off the air
// still has them, because the relay holds them open.
//
// classes are the input classes each controller carries, keyed by
// its address. Each class publishes as one boolean attribute named as
// udev names the class, so a DeviceClass selects a gamepad with
// `has(device.attributes["bluetooth.liken.sh"].joystick)` and never
// by the device's name. A controller carries the union of what its
// nodes carry. A bond that has never connected carries no class,
// because the relay has never read a node for it.
//
// The `connected` attribute and the `disconnected` taint report the
// same fact. The taint is present exactly when bluetoothd reports the
// device is not connected. The `no-input-node` taint reports a
// different fact the `connected` attribute does not carry: whether an
// input claim would deliver anything. The two part company for a bond
// that has never connected, which has no relay and no virtual node.
//
// moved names the controllers whose prepared claims deliver nodes that
// are not the relay's current nodes, and each of them carries the
// node-moved taint.
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
		// The identity facts publish in two layers: the raw code, so
		// no unforeseen question is lost, and the unpacked names and
		// flags, so a selector never does bit arithmetic. classify.go
		// holds the decode. Every layer is absent when BlueZ reported
		// nothing. The class gate matters most: a class word of zero
		// decodes to miscellaneous, and publishing that would turn a
		// device's silence into an answer it never gave.
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

// supportsSoundAttribute is the one attribute this driver publishes
// in a domain it does not own. liken stamps the same attribute on
// each sound card it publishes, and the audio operator's class
// selects the attribute and names no driver, so one claim collects
// every device on a node that can serve a sound server.
const supportsSoundAttribute = "sound.liken.sh/supportsSound"

// mediaBusKind is the kind attribute's value on the media bus. The
// attribute gives a selector a direct name for the bus, instead of a
// test on the attributes the bus lacks.
const mediaBusKind = "mediaBus"

// mediaBusDevice builds the one device that is not a peer: the
// claimable permission to connect to this pod's bluetoothd over its
// private D-Bus. A Bluetooth speaker creates no kernel device, so its
// audio exists only while a sound server holds this bus and keeps a
// media endpoint registered with bluetoothd.
//
// The device is exclusive, which is resource.k8s.io/v1's default and
// takes no field here: one radio serves one sound server, because two
// media endpoints registered on one bluetoothd have no contract over
// the streams.
//
// The bus never publishes an input attribute. The cluster's input
// class guards on that attribute, and a bus that matched it could be
// allocated to a workload's input claim.
//
// reachable is false when the adapter has departed. The taint's
// effect is NoSchedule, never NoExecute: the holder is the machine's
// one sound server, so an eviction would end its ALSA playback too,
// and that playback does not need the radio. NoSchedule parks the
// next claim and leaves the running one alone.
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

// attributeString limits a free-text value to the API's 64-character
// limit on attribute strings. A controller's alias is the only value
// here that a person can make long, and a truncated alias still
// identifies the controller to a reader. A PairingRequest's status
// holds a device's name under the same limit, so both cut with the
// same function.
func attributeString(s string) string {
	return truncateRunes(s, maxSeenNameBytes)
}
