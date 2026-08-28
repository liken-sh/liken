package main

// What one pass reads about one endpoint, and the status it composes
// from that.
//
// The facts come from four sources: the card's control device, for
// the controls and the jacks; the ELD the graphics driver wrote into
// the card, for the monitor an HDMI slot feeds; bluetoothd, for a
// speaker's pairing and connection; and PipeWire's graph, for the
// node, its gain, its mute, and its format. The composition reads no
// hardware and writes no byte. It turns the facts into the status the
// resource carries, and sinkcontrol.go decides whether that status
// differs from the published one.
//
// The two conditions report what the endpoint can do now, not what a
// claim would get. Connected says sound can leave or enter here, and
// Ready says PipeWire holds a node for it. They carry the same facts
// as the no-monitor and no-sink taints on the device, for a person
// rather than the scheduler.

import (
	"time"
)

// endpointFacts is what one pass read about one endpoint.
//
// An endpoint is either one of a card's PCM devices or a Bluetooth
// speaker. Endpoint carries the first and Speaker the second, and one
// of the two is always empty. Everything below them is what the card
// and the graph said about whichever it is.
type endpointFacts struct {
	Name      string
	Direction pwDirection
	Machine   string

	// The card's half. It is the zero value on a Bluetooth speaker.
	Endpoint alsaEndpoint

	// The radio's half. It is nil on a card's endpoint.
	Speaker *speakerFacts

	// The node PipeWire holds for the endpoint now.
	Node    pwNode
	HasNode bool

	// The card's controls that belong to this endpoint, and the value
	// each one reads now.
	Controls []control
	Values   map[string]string

	// What the card's jacks say: whether a plug is in, and whether the
	// card senses a jack for this endpoint at all.
	Plugged bool
	Sensed  bool

	// The claim that holds the endpoint now, and nil between holders.
	Claim *EndpointClaim
}

// speakerFacts is one Bluetooth speaker: what bluetoothd says about
// the peer, and what the graph holds for it.
type speakerFacts struct {
	Address string
	Paired  speaker
	Sink    bluezSink
	HasSink bool
}

// absoluteRoute answers the device and the Route a level write lands
// on, and nothing at all for every endpoint whose level is a software
// gain.
//
// A write to a speaker's Route becomes AVRCP absolute volume on the
// speaker itself, so the number on its display moves. A speaker that
// reports no volume of its own publishes no volumeStep on the Route,
// and its level is then the node's software gain, like every ALSA
// endpoint's.
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

// level is the volume and the mute the endpoint reads now, from
// wherever its level lives.
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

// connected reports whether the endpoint can play or record now, with
// the reason and the line a person reads.
//
// Each kind of endpoint has its own witness. A speaker answers with
// the connection bluetoothd reports. An HDMI or DisplayPort slot
// answers with the ELD, which the graphics driver writes only while a
// monitor answers. A USB card is on the bus or it publishes nothing,
// so it has no cable state to report. An analog jack answers with the
// plug, where the card senses one, and a card that senses no jack for
// an endpoint reports it connected, because nothing says otherwise
// and a claim on it should not park.
func (f endpointFacts) connected() (bool, string, string) {
	switch {
	case f.Speaker != nil:
		if f.Speaker.Paired.Connected {
			return true, "SpeakerConnected", "bluetoothd reports the speaker connected"
		}
		return false, "SpeakerDisconnected", "the speaker is paired and not connected"
	case f.Endpoint.HDMI:
		if f.Endpoint.Monitor {
			return true, "MonitorPresent", "a monitor answers on this slot"
		}
		return false, "NoMonitor", "no monitor answers on this slot"
	case f.Endpoint.Identity.Bus == usbBus:
		return true, "CardPresent", "the card is on the bus"
	case f.Sensed:
		if f.Plugged {
			return true, "JackPlugged", "a plug is in the jack"
		}
		return false, "JackEmpty", "the card senses no plug in the jack"
	}
	return true, "NoJackSensing", "the card senses no jack for this endpoint"
}

// ready reports whether PipeWire holds a node for the endpoint, which
// is the same fact the no-sink taint carries.
func (f endpointFacts) ready() (bool, string, string) {
	if f.HasNode {
		return true, "NodePresent", "PipeWire holds the node " + f.Node.Name
	}
	return false, "NoNode", "PipeWire holds no node for this endpoint"
}

// status composes what the resource reports, from the facts alone.
// The published status comes in so that a condition that did not
// change keeps the time it changed last.
func (f endpointFacts) status(published EndpointStatus, now time.Time) EndpointStatus {
	status := EndpointStatus{
		Node:       f.Machine,
		NodeName:   f.nodeName(),
		Claim:      f.Claim,
		Conditions: published.Conditions,
	}
	// An endpoint whose card lists no control for it carries no
	// capabilities at all, which is every Bluetooth speaker and every
	// endpoint of a card this operator could not open.
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

	connected, reason, message := f.connected()
	status.Conditions = setCondition(status.Conditions,
		condition(ConnectedCondition, connected, reason, message, now))
	ready, reason, message := f.ready()
	status.Conditions = setCondition(status.Conditions,
		condition(ReadyCondition, ready, reason, message, now))
	return status
}

// nodeName is the name a consumer's PIPEWIRE_NODE holds, and nothing
// while PipeWire holds no node.
func (f endpointFacts) nodeName() string {
	if !f.HasNode {
		return ""
	}
	return f.Node.Name
}

// monitor is what the ELD block says about the screen this slot
// feeds, and nothing on a slot with no monitor and on every endpoint
// that is not an HDMI or a DisplayPort one.
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

// bluetooth is what bluetoothd and the graph say about the speaker.
// The codec and the offered set come from the transport, so a speaker
// that is not connected reports the pairing alone.
func (f endpointFacts) bluetooth() *EndpointBluetooth {
	facts := &EndpointBluetooth{
		Address: publishedMAC(f.Speaker.Address),
		Name:    f.Speaker.Paired.Name,
		Pairing: speakerName(f.Speaker.Address),
	}
	if f.Speaker.HasSink {
		facts.Codec = f.Speaker.Sink.Codec
		facts.Codecs = f.Speaker.Sink.codecNames()
	}
	return facts
}

// format is what the node negotiated, and nothing while the node is
// suspended: a node that runs no stream reports a rate of zero.
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

// observed is the last value the operator read for each setting, and
// nothing at all when it read none.
func (f endpointFacts) observed() *EndpointObserved {
	values := &EndpointObserved{Controls: f.Values}
	if volume, mute, known := f.level(); known {
		values.Volume, values.Mute = &volume, &mute
	}
	if f.Speaker != nil && f.Speaker.HasSink {
		values.Codec = f.Speaker.Sink.Codec
	}
	if values.Volume == nil && values.Mute == nil && values.Codec == "" && len(values.Controls) == 0 {
		return nil
	}
	return values
}
