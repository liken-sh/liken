package main

// This file builds a SliceDevice for each PCM device of the sound card,
// in both directions, and for each paired Bluetooth speaker. slices.go
// writes them to the API server.

import (
	"slices"
	"strings"
)

// A device that cannot play gets disconnectedTaint, with the NoExecute
// effect, and a NoSchedule taint for each reason: noMonitorTaint when
// an HDMI or DisplayPort output has no monitor, and noSinkTaint when
// PipeWire has no node for the device.
//
// The claim guide tells a consumer to tolerate disconnectedTaint with
// its own tolerationSeconds, so that a cable that is unplugged and
// plugged back in does not evict the pod, and to tolerate neither
// NoSchedule taint. The NoSchedule taints keep a new claim Pending until
// the device can play. Without them, the scheduler would allocate a
// device that cannot play to a claim that tolerates disconnectedTaint,
// the pod would be evicted when its tolerationSeconds ran out, and the
// scheduler would allocate the device again.
//
// Each reason has its own key because each has a different repair. A
// missing monitor needs a cable. An ALSA output has no node when
// PipeWire could not create it, and the nofail flag lets PipeWire start
// without it, so the node stays missing until PipeWire starts again. A
// Bluetooth speaker has no node until it connects, and WirePlumber
// creates the node then.
const (
	disconnectedTaint = DriverName + "/disconnected"
	noMonitorTaint    = DriverName + "/no-monitor"
	noSinkTaint       = DriverName + "/no-sink"
)

// bluetoothConnection is the connectionType of a paired speaker. An
// ALSA output has hdmi, displayport, analog, or usb.
const bluetoothConnection = "bluetooth"

// A playback device has sinkAttribute and a capture device has
// sourceAttribute, never both, and the value is the device name. The
// DeviceClasses that the cluster owner writes select one direction by
// which attribute is present.
const (
	sinkAttribute   = "sink"
	sourceAttribute = "source"
)

// nodeNameAttribute is the PipeWire node name of a sink or a source:
// the name that prepare puts in the consumer's PIPEWIRE_NODE, and the
// name in status.nodeName of the matching Sink or Source.
const nodeNameAttribute = "nodeName"

// sliceDevices sorts the devices by name, because speakerDevices reads
// the speakers from a map in random order, and sameDevices would report
// a different order as a change.
func sliceDevices(outputs []alsaEndpoint, speakers map[string]speaker, graph pwGraph) []SliceDevice {
	devices := make([]SliceDevice, 0, len(outputs)+len(speakers))
	for _, output := range outputs {
		attribute := sinkAttribute
		if output.Capture {
			attribute = sourceAttribute
		}
		node, hasNode := graph.Nodes[output.graphAddress()]
		// A CEL selector cannot read the device name, so the name is also
		// an attribute. card and pcm are the numbers of this boot, so a
		// claim can select every endpoint of one card.
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

		// An output plays only when PipeWire has a node for it, and an
		// HDMI or DisplayPort output also needs a monitor. publishNode
		// adds noSinkTaint.
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

// speakerDevices returns one device for each paired speaker. The
// connected attribute comes from bluetoothd, and a claim needs the sink
// node that WirePlumber creates after the speaker connects. So a
// speaker that bluetoothd reports as connected is still tainted until
// PipeWire has its node.
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
		// codecs lists every codec that the speaker supports, so a claim
		// that requests a codec can select a speaker that accepts it.
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

// publishNode sets nodeNameAttribute when PipeWire has a node for the
// endpoint and noSinkTaint when it has none, so a device never has
// both. A source with no node gets noSinkTaint too, because a missing
// node means the same in either direction.
//
// A node name longer than maxAttributeLength is left out, because a
// truncated name would match no node. The device gets no taint, because
// the node exists, and prepare reads the node name from the PipeWire
// graph, not from the slice.
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

// addMonitorAttributes copies the fields of the monitor's ELD block
// into attributes. PairingAttribute has its own domain,
// monitor.liken.sh, so that a matchAttribute constraint can compare it
// with the attribute that display-operator publishes for the same
// monitor. An attribute name with no domain belongs to the domain of
// the driver that publishes it, so the same bare name from two drivers
// never matches.
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
	// A claim that needs high-rate playback can select
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
	// encodedFormatsAttribute lists the encoded formats that the monitor
	// or receiver decodes itself, so a claim that plays a TrueHD track
	// can select on it instead of naming a receiver model.
	if formats := block.encodedFormats(); len(formats) > 0 {
		attributes[encodedFormatsAttribute] = AttrString(strings.Join(formats, " "))
	}
	attributes["eldVersion"] = AttrInt(int64(block.Version))
	// The ELD's Port_ID identifies the display connector that this
	// output plays into.
	if block.PortID != 0 {
		attributes["portID"] = AttrInt(int64(block.PortID))
	}
	if id := block.monitorID(); id != "" {
		attributes[PairingAttribute] = AttrString(attributeString(id))
	}
}

// attributeString cuts s to maxAttributeLength, because the API server
// rejects a longer string attribute. A speaker's alias, which a person
// types, can be longer.
func attributeString(s string) string {
	if len(s) <= maxAttributeLength {
		return s
	}
	return s[:maxAttributeLength]
}
