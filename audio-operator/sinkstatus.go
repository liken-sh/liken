package main

// This file builds the status of one endpoint from the facts that a
// pass read about it. The card's control device lists the controls and
// the jacks. The ELD that the graphics driver writes into the card
// describes the monitor on an HDMI or DisplayPort output. bluetoothd
// reports whether a speaker is paired and connected, and the PipeWire
// graph has the node with its gain, mute, and format. Building the
// status reads no hardware and writes nothing. sinkcontrol.go compares
// the result with the published status.
//
// Connected means sound can leave or enter through the endpoint now,
// and Ready means PipeWire has a node for it. The conditions repeat
// some taints of the device for a person who reads the status: Ready is
// false when the device has noSinkTaint, and an HDMI or DisplayPort
// output reports Connected false when its device has noMonitorTaint. An
// analog jack with no plug reports Connected false, and its device has
// no taint for that.

import (
	"time"
)

// endpointFacts is what one pass read about one endpoint, and the input
// of status.
//
// An endpoint is a PCM device of a card, in Endpoint, or a Bluetooth
// speaker, in Speaker. The other field is empty.
type endpointFacts struct {
	Name      string
	Direction pwDirection
	Machine   string

	Endpoint alsaEndpoint
	Speaker  *speakerFacts

	// Node is valid only when HasNode is true. PipeWire is the cookie of
	// the PipeWire daemon that has the node.
	Node     pwNode
	HasNode  bool
	PipeWire uint32

	// Values has the current value of each control in Controls, keyed by
	// control name.
	Controls []control
	Values   map[string]string

	// Sensed is true when the card detects plugs for this endpoint.
	// Plugged means nothing unless Sensed is true.
	Plugged bool
	Sensed  bool

	Claims []EndpointClaim

	// Layout is the channel layout that the PipeWire drop-in gives a
	// card's sink, with its LayoutApplied condition. It is nil for a
	// source, for a speaker, and for a sink with no layout in the
	// drop-in.
	Layout *layoutState

	// Written is the level that the operator last wrote to the current
	// node, from the spec or from a volumeAsk (asks.go). It is nil when
	// the operator wrote none, or when the node has reported its own
	// level since. PipeWire does not announce a write to a suspended
	// node, so observed reports this value for an idle endpoint.
	Written *levelWrite

	// Applied is published as status.applied: the PipeWire cookie and
	// node id of the node that the operator last compared with the spec,
	// and the declared level and mute of that comparison.
	Applied *EndpointApplied
}

// speakerFacts is what bluetoothd reports about a paired speaker, and
// its PipeWire sink when HasSink is true.
type speakerFacts struct {
	Address string
	Paired  speaker
	Sink    bluezSink
	HasSink bool
}

// absoluteRoute returns the device and Route that a level write goes
// to, and false when the level is the software gain of the node.
//
// A write to a speaker's Route becomes AVRCP absolute volume on the
// speaker, so the volume that the speaker displays changes. A speaker
// that reports no volume of its own has no volumeStep on its Route, so
// its level is the software gain of the node, as for every ALSA
// endpoint.
func (f endpointFacts) absoluteRoute() (int, pwRoute, bool) {
	if f.Speaker == nil || f.Speaker.Sink.Route == nil {
		return 0, pwRoute{}, false
	}
	route := *f.Speaker.Sink.Route
	if !route.AbsoluteVolume {
		return 0, pwRoute{}, false
	}
	return f.Speaker.Sink.Device, route, true
}

// suspended reports whether the level that PipeWire prints for the
// endpoint can be stale, which is the case for the software gain of a
// suspended node. The Route of a speaker with absolute volume is
// current whatever the state of the node.
func (f endpointFacts) suspended() bool {
	if _, _, absolute := f.absoluteRoute(); absolute {
		return false
	}
	return f.HasNode && f.Node.Suspended
}

// level returns the volume as a percent, the mute, and whether the
// volume is known. It reads the Route of a speaker with absolute volume
// and the node of every other endpoint.
func (f endpointFacts) level() (volume int, mute bool, known bool) {
	if _, route, absolute := f.absoluteRoute(); absolute {
		volume, known = volumePercent(route.Volumes)
		return volume, route.Mute, known
	}
	if !f.HasNode {
		return 0, false, false
	}
	volume, known = volumePercent(f.Node.Volumes)
	return volume, f.Node.Mute, known
}

// connected reports whether the endpoint can play or record, with the
// reason and message of the Connected condition.
//
// A speaker is connected when bluetoothd reports it connected. An HDMI
// or DisplayPort output is connected when it has an ELD, which the
// graphics driver writes only while a monitor answers. A USB card is
// always connected, because its endpoint exists only while the card is
// on the bus. An analog jack is connected when a plug is in it. When the
// card cannot sense the jack, the endpoint reports connected, because
// no reading shows that it is not.
func (f endpointFacts) connected() (bool, string, string) {
	switch {
	case f.Speaker != nil:
		if f.Speaker.Paired.Connected {
			return true, "SpeakerConnected", "bluetoothd reports the speaker connected"
		}
		return false, "SpeakerDisconnected", "The speaker is paired and not connected"
	case f.Endpoint.HDMI:
		if f.Endpoint.Monitor {
			return true, "MonitorPresent", "A monitor is connected to this output"
		}
		return false, "NoMonitor", "No monitor is connected to this output"
	case f.Endpoint.Identity.Bus == usbBus:
		return true, "CardPresent", "The card is on the USB bus"
	case f.Sensed:
		if f.Plugged {
			return true, "JackPlugged", "A plug is in the jack"
		}
		return false, "JackEmpty", "The card senses no plug in the jack"
	}
	return true, "NoJackSensing", "The card senses no jack for this endpoint"
}

// ready reports whether PipeWire has a node for the endpoint, with the
// reason and message of the Ready condition.
func (f endpointFacts) ready() (bool, string, string) {
	if f.HasNode {
		return true, "NodePresent", "PipeWire has the node " + f.Node.Name
	}
	return false, "NoNode", "PipeWire has no node for this endpoint"
}

// status builds the status of the endpoint from the facts. It reads the
// published status so that a condition whose value did not change keeps
// its last transition time.
func (f endpointFacts) status(published EndpointStatus, now time.Time) EndpointStatus {
	status := EndpointStatus{
		Node:       f.Machine,
		NodeName:   f.nodeName(),
		Claims:     f.Claims,
		Conditions: published.Conditions,
	}
	// Capabilities stay unset when the card lists no control for the
	// endpoint, as for every Bluetooth speaker and for the endpoints
	// of a card that the operator could not open.
	if capabilities := capabilitiesOf(f.Controls); len(capabilities) > 0 {
		status.Capabilities = capabilities
	}
	if f.Speaker != nil {
		status.ConnectionType = bluetoothConnection
		status.Bluetooth = f.bluetooth()
	} else {
		status.ConnectionType = f.Endpoint.connectionType()
		status.Location = f.Endpoint.Identity.Location
		status.Card = &EndpointCard{
			Number: f.Endpoint.Card,
			ID:     f.Endpoint.Identity.ID,
			Driver: f.Endpoint.Identity.Driver,
			Name:   f.Endpoint.Identity.Name,
		}
		status.PCM = &EndpointPCM{Device: f.Endpoint.PCM, ID: f.Endpoint.PCMID}
		status.Monitor = f.monitor()
	}
	status.Format = f.format()
	status.Observed = f.observed()
	status.Applied = f.Applied

	connected, reason, message := f.connected()
	status.Conditions = setCondition(status.Conditions,
		condition(ConnectedCondition, connected, reason, message, now))
	ready, reason, message := f.ready()
	status.Conditions = setCondition(status.Conditions,
		condition(ReadyCondition, ready, reason, message, now))
	if f.Layout != nil {
		status.Layout = f.Layout.Layout.Positions
		status.LayoutSource = string(f.Layout.Layout.source())
		status.Passthrough = f.Layout.Layout.Encoded
		status.Conditions = setCondition(status.Conditions,
			condition(LayoutAppliedCondition, f.Layout.Applied, f.Layout.Reason, f.Layout.Message, now))
	}
	return status
}

// nodeName returns the PipeWire node name, which a consumer receives in
// PIPEWIRE_NODE, or "" when the endpoint has no node.
func (f endpointFacts) nodeName() string {
	if !f.HasNode {
		return ""
	}
	return f.Node.Name
}

// monitor returns the published fields of the ELD block, or nil when
// the endpoint has no monitor or is not HDMI or DisplayPort.
func (f endpointFacts) monitor() *EndpointMonitor {
	if !f.Endpoint.HDMI || !f.Endpoint.Monitor {
		return nil
	}
	block := f.Endpoint.ELD
	return &EndpointMonitor{
		Display:      block.monitorID(),
		Manufacturer: block.Manufacturer,
		Product:      hexProduct(block.Product),
		Name:         block.MonitorName,
	}
}

// bluetooth returns what bluetoothd and the graph report about the
// speaker. Codec and Codecs come from the speaker's PipeWire sink, so
// they are empty when the graph has no sink for it.
func (f endpointFacts) bluetooth() *EndpointBluetooth {
	facts := &EndpointBluetooth{
		Address:    publishedMAC(f.Speaker.Address),
		Name:       f.Speaker.Paired.Name,
		Peripheral: speakerName(f.Speaker.Address),
	}
	if f.Speaker.HasSink {
		facts.Codec = f.Speaker.Sink.Codec
		facts.Codecs = f.Speaker.Sink.codecNames()
	}
	return facts
}

// format returns the format that the node negotiated, or nil when the
// rate is zero, as it is while no stream runs on the node.
func (f endpointFacts) format() *EndpointFormat {
	if !f.HasNode || f.Node.Format.Rate == 0 {
		return nil
	}
	return &EndpointFormat{
		Rate:      f.Node.Format.Rate,
		Channels:  f.Node.Format.Channels,
		Positions: f.Node.Format.Positions,
	}
}

// observed returns the levels, codec, and control values of the
// endpoint, or nil when it has none.
//
// PipeWire applies a Props write to a suspended node but does not
// announce it. In pipewire 1.4.2, spa/plugins/audioconvert/audioadapter.c
// compares the parameter flags and not the serial, so every reader of
// the graph gets a stale copy, with no level or the level the node last
// ran at. So for a suspended node, and for a node with no level,
// observed reports the volume and mute that the operator last wrote,
// because the node runs at that level when it resumes. A field that the
// operator did not write keeps the graph's value.
func (f endpointFacts) observed() *EndpointObserved {
	values := &EndpointObserved{Controls: f.Values}
	volume, mute, known := f.level()
	if known {
		values.Volume, values.Mute = &volume, &mute
	}
	if f.HasNode && f.Written != nil && (!known || f.suspended()) {
		if f.Written.Volume != nil {
			values.Volume = f.Written.Volume
		}
		if f.Written.Mute != nil {
			values.Mute = f.Written.Mute
		}
	}
	if f.Speaker != nil && f.Speaker.HasSink {
		values.Codec = f.Speaker.Sink.Codec
	}
	if values.Volume == nil && values.Mute == nil && values.Codec == "" && len(values.Controls) == 0 {
		return nil
	}
	return values
}
