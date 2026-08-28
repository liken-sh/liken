package main

// One endpoint as a claimable device: the attributes a selector
// reads, and the taints that say the endpoint cannot play now. The
// set is the card's PCM devices, in both directions, and the radio's
// paired speakers together, and slices.go is what writes the set to
// the API server.

import (
	"slices"
	"strings"
)

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
// noSinkTaint says PipeWire holds no node for this device. Either
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
// monitor is a cable, and somebody plugs it back in. A missing node
// has one repair for each kind of device: on an ALSA output it is a
// node PipeWire could not create, which the nofail flag on each
// declared object leaves possible, and only a replacement pod
// creates it; on a Bluetooth speaker it is a speaker that is not
// connected, and WirePlumber builds the node when somebody switches
// the speaker on.
const (
	disconnectedTaint = DriverName + "/disconnected"
	noMonitorTaint    = DriverName + "/no-monitor"
	noSinkTaint       = DriverName + "/no-sink"
)

// bluetoothConnection is the connectionType a speaker publishes,
// beside the hdmi, displayport, and analog forms an ALSA output
// takes.
const bluetoothConnection = "bluetooth"

// The two attributes that carry an endpoint's device name and say
// which direction it runs in. A DeviceClass selects a direction by
// the presence of one of them, and the consumer classes that do so
// belong to the cluster owner, not to this operator.
const (
	sinkAttribute   = "sink"
	sourceAttribute = "source"
)

// nodeNameAttribute carries the PipeWire node name in both
// directions. It is the name a consumer's PIPEWIRE_NODE holds, and it
// is spelled the way status.nodeName spells it, so a reader of the
// slice and a reader of the resource see one word.
const nodeNameAttribute = "nodeName"

// sliceDevices turns the card's endpoints into the devices the slice
// publishes, one for each endpoint, sorted by name so that the same
// hardware always produces the same slice.
//
// A capture PCM device publishes beside the playback ones, with the
// source attribute in place of sink. The two attributes are how a
// DeviceClass tells a microphone from a speaker, and a device carries
// exactly one of them.
func sliceDevices(outputs []alsaEndpoint, speakers map[string]speaker, graph pwGraph) []SliceDevice {
	devices := make([]SliceDevice, 0, len(outputs)+len(speakers))
	for _, output := range outputs {
		attribute := sinkAttribute
		if output.Capture {
			attribute = sourceAttribute
		}
		node, hasNode := graph.Nodes[output.graphAddress()]
		// The device name is not selectable. A DeviceClass and a claim
		// select with CEL over device.driver, device.attributes,
		// device.capacity, and device.allowMultipleAllocations, and
		// there is no device.name among them, so an identity that exists
		// only in the name is one a selector cannot read. The sink or
		// source attribute holds the same string, and card and pcm hold
		// this boot's numbers, so a claim can name one endpoint and a
		// claim can ask for every endpoint of one card.
		device := SliceDevice{
			Name: output.Name(),
			Attributes: map[string]DeviceAttribute{
				attribute: AttrString(output.Name()),
				"card":    AttrInt(int64(output.Card)),
				"pcm":     AttrInt(int64(output.PCM)),
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
		if !hasNode || unplugged {
			device.Taints = append(device.Taints,
				DeviceTaint{Key: disconnectedTaint, Effect: "NoExecute"})
		}
		if unplugged {
			device.Taints = append(device.Taints,
				DeviceTaint{Key: noMonitorTaint, Effect: "NoSchedule"})
		}
		publishNode(&device, node.Name, hasNode)
		devices = append(devices, device)
	}
	devices = append(devices, speakerDevices(speakers, graph.Speakers)...)
	slices.SortFunc(devices, func(a, b SliceDevice) int {
		return strings.Compare(a.Name, b.Name)
	})
	return devices
}

// speakerDevices turns the paired set into the devices the slice
// publishes, one for each speaker.
//
// The connected attribute reports what bluetoothd says. The taints
// report the stricter fact, which is what a claim on the device
// would actually deliver: the two differ for the moment between the
// connection and WirePlumber's build of the sink node.
//
// The taints reuse this driver's own keys, because the states are
// the same ones an ALSA output has: the device cannot serve a stream
// now, and no node exists for a prepare call to name.
func speakerDevices(speakers map[string]speaker, sinks map[string]bluezSink) []SliceDevice {
	devices := make([]SliceDevice, 0, len(speakers))
	for address, paired := range speakers {
		sink, hasSink := sinks[address]
		device := SliceDevice{
			Name: speakerName(address),
			Attributes: map[string]DeviceAttribute{
				sinkAttribute:    AttrString(speakerName(address)),
				"address":        AttrString(publishedMAC(address)),
				"connectionType": AttrString(bluetoothConnection),
				"connected":      AttrBool(paired.Connected),
			},
		}
		if name := attributeString(paired.Name); name != "" {
			device.Attributes["name"] = AttrString(name)
		}
		if hasSink && sink.Codec != "" {
			device.Attributes["codec"] = AttrString(attributeString(sink.Codec))
		}
		// The set the speaker offers publishes beside the codec it
		// negotiated, so a reader of the slice can see a choice
		// exists, and a claim can name one the driver will accept.
		if list := codecList(sink.Codecs); hasSink && list != "" {
			device.Attributes["codecs"] = AttrString(list)
		}
		if !paired.Connected || !hasSink {
			device.Taints = append(device.Taints,
				DeviceTaint{Key: disconnectedTaint, Effect: "NoExecute"})
		}
		publishNode(&device, sink.Node, hasSink)
		devices = append(devices, device)
	}
	return devices
}

// publishNode writes what PipeWire holds for one endpoint: the
// node's name as an attribute, or the no-sink taint when there is no
// node. One branch sets both, so no device can have the name of a
// node and the taint that says it has none. Those two would state
// opposite facts about one endpoint.
//
// A name past the attribute limit is left out, and the endpoint keeps
// its node. The name still reaches the consumer, because the delivery
// reads the current name from PipeWire rather than from the slice, so
// a truncated one would only give a selector a name that names
// nothing.
//
// A source with no node carries the no-sink taint too, rather than a
// key of its own. The taint says PipeWire holds no node for this
// device, which is one fact in both directions, and a second key
// would make a consumer tolerate two things to mean one.
func publishNode(device *SliceDevice, name string, hasNode bool) {
	if !hasNode {
		device.Taints = append(device.Taints,
			DeviceTaint{Key: noSinkTaint, Effect: "NoSchedule"})
		return
	}
	if len(name) > maxAttributeLength {
		return
	}
	device.Attributes[nodeNameAttribute] = AttrString(name)
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
	// The rate and the depths bound what a stream can ask of this
	// output. A claim that needs high-rate playback selects
	// lpcmMaxRateHz >= 96000 instead of naming a monitor model.
	if block.LPCMMaxRateHz > 0 {
		attributes["lpcmMaxRateHz"] = AttrInt(int64(block.LPCMMaxRateHz))
	}
	if depths := block.bitDepths(); depths != "" {
		attributes["lpcmBitDepths"] = AttrString(attributeString(depths))
	}
	if block.Speakers != "" {
		attributes["speakers"] = AttrString(attributeString(block.Speakers))
	}
	// The ELD version is the format version of the block. parseELD
	// rejects any value it cannot read, so a published block always
	// carries one of the versions this operator reads.
	attributes["eldVersion"] = AttrInt(int64(block.Version))
	// The Port_ID the graphics driver stamps into the ELD block ties
	// this audio sink to the display connector it plays into.
	if block.PortID != 0 {
		attributes["portID"] = AttrInt(int64(block.PortID))
	}
	if id := block.monitorID(); id != "" {
		attributes[PairingAttribute] = AttrString(attributeString(id))
	}
}

// attributeString limits a value to the API's limit on the length of
// a string attribute. The ELD block holds at most 16 characters of
// a monitor's name, so the values that can reach the limit are a
// speaker's alias, which a person types, and a sink node's name.
func attributeString(s string) string {
	if len(s) <= maxAttributeLength {
		return s
	}
	return s[:maxAttributeLength]
}
