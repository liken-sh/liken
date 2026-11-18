package main

// These tests build the status of one endpoint from facts, on the
// liken-1 fixture machine: an Intel HDMI codec, and a USB DAC that plays
// and records through one PCM device.

import (
	"reflect"
	"testing"
	"time"
)

// factsTime is the transition time of every condition in these tests.
var factsTime = time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)

// liken1Endpoints reads and names the endpoints of the liken-1 fixture and
// returns them by name. The card ID, driver, and name come from an
// ioctl on the control device, and the fixture's control device is a
// regular file, so liken1Endpoints sets them to what the real cards of liken-1
// report. cards_test.go tests the ioctl read.
func liken1Endpoints(t *testing.T) map[string]alsaEndpoint {
	t.Helper()
	machineFixture(t, "liken-1")
	endpoints, err := readEndpoints()
	if err != nil {
		t.Fatalf("reading the endpoints of the liken-1 fixture: %v", err)
	}
	named, refused := nameEndpoints("liken-1", endpoints)
	if len(refused) != 0 {
		t.Fatalf("the operator refused to name %v, want every endpoint named", refused)
	}
	byName := map[string]alsaEndpoint{}
	for _, endpoint := range named {
		if endpoint.Card == 0 {
			endpoint.Identity.ID = "PCH"
			endpoint.Identity.Driver = "HDA-Intel"
			endpoint.Identity.Name = "HDA Intel PCH"
		} else {
			endpoint.Identity.ID = "Audio"
			endpoint.Identity.Driver = "USB-Audio"
			endpoint.Identity.Name = "USB Audio and HID"
		}
		byName[endpoint.Name()] = endpoint
	}
	return byName
}

// withMonitor returns the endpoint as it is while the LG monitor is
// connected: HDMI and Monitor set, with the ELD block of that monitor.
func withMonitor(t *testing.T, endpoint alsaEndpoint) alsaEndpoint {
	t.Helper()
	block, err := parseELD(fixture(t, "eld-hdmi-lg-ultrawide.bin"))
	if err != nil {
		t.Fatalf("parsing the ELD fixture: %v", err)
	}
	endpoint.HDMI, endpoint.Monitor, endpoint.ELD = true, true, block
	return endpoint
}

// The whole status of an HDMI slot with a monitor. An HDMI PCM has no
// volume element, so its only control is the IEC958 switch. The monitor
// field matches the slot to the Display that display-operator publishes
// for the same screen.
func TestSinkStatusOfAnHDMISlot(t *testing.T) {
	endpoints := liken1Endpoints(t)
	slot := withMonitor(t, endpoints["liken-1-pci-0000-00-1f-3-hdmi-0"])
	iec958 := switchControl("IEC958 Playback Switch", 0)

	facts := endpointFacts{
		Name:      slot.Name(),
		Direction: directionSink,
		Machine:   "liken-1",
		Endpoint:  slot,
		Node: pwNode{
			ID:      42,
			Name:    sinkNodeName(0, 3),
			Volumes: []float64{1, 1},
			Format:  pwFormat{Rate: 48000, Channels: 2, Positions: []string{"FL", "FR"}},
		},
		HasNode:  true,
		Controls: []control{iec958},
		Values:   map[string]string{"IEC958 Playback Switch": controlOn},
		Claims:   []EndpointClaim{{Namespace: "media", Name: "kitchen"}},
	}

	want := EndpointStatus{
		Node:           "liken-1",
		Location:       "0000:00:1f.3",
		ConnectionType: "hdmi",
		Card:           &EndpointCard{Number: 0, ID: "PCH", Driver: "HDA-Intel", Name: "HDA Intel PCH"},
		PCM:            &EndpointPCM{Device: 3, ID: "HDMI 0"},
		Monitor: &EndpointMonitor{
			Display:      "gsm-5b09-lg-ultrawide",
			Manufacturer: "GSM",
			Product:      "5b09",
			Name:         "LG ULTRAWIDE",
		},
		NodeName:     sinkNodeName(0, 3),
		Format:       &EndpointFormat{Rate: 48000, Channels: 2, Positions: []string{"FL", "FR"}},
		Capabilities: map[string]controlCapability{"IEC958 Playback Switch": iec958.Capability},
		Observed: &EndpointObserved{
			Volume:   pointerTo(100),
			Mute:     pointerTo(false),
			Controls: map[string]string{"IEC958 Playback Switch": controlOn},
		},
		Claims: []EndpointClaim{{Namespace: "media", Name: "kitchen"}},
		Conditions: []EndpointCondition{
			connectedCondition(true, "MonitorPresent", "A monitor is connected to this output"),
			readyCondition(true, "PipeWire has the node "+sinkNodeName(0, 3)),
		},
	}
	if got := facts.status(EndpointStatus{}, factsTime); !reflect.DeepEqual(got, want) {
		t.Errorf("status =\n%+v\nwant\n%+v", got, want)
	}
}

// A slot with no monitor has no monitor field, and its Connected
// condition is false, as its device has noMonitorTaint.
func TestSinkStatusOfAnEmptyHDMISlot(t *testing.T) {
	endpoints := liken1Endpoints(t)
	slot := endpoints["liken-1-pci-0000-00-1f-3-hdmi-1"]
	slot.HDMI = true

	status := endpointFacts{
		Name: slot.Name(), Direction: directionSink, Machine: "liken-1", Endpoint: slot,
	}.status(EndpointStatus{}, factsTime)

	if status.Monitor != nil {
		t.Errorf("monitor = %+v, want none", status.Monitor)
	}
	// The connection type comes from the ELD block. Without a monitor
	// there is no block, so the field is empty and not guessed.
	if status.ConnectionType != "" {
		t.Errorf("connectionType = %q, want none", status.ConnectionType)
	}
	want := []EndpointCondition{
		connectedCondition(false, "NoMonitor", "No monitor is connected to this output"),
		readyCondition(false, "PipeWire has no node for this endpoint"),
	}
	if !reflect.DeepEqual(status.Conditions, want) {
		t.Errorf("conditions = %+v, want %+v", status.Conditions, want)
	}
}

// The whole status of the DAC's playback side. A USB card is connected
// whenever it has an endpoint, because the endpoint disappears when the
// card is unplugged.
func TestSinkStatusOfAUSBDAC(t *testing.T) {
	endpoints := liken1Endpoints(t)
	dac := endpoints["liken-1-usb-0573-1573-a34004801402-usb-audio"]
	volume := levelControl("PCM Playback Volume", 0)
	mute := switchControl("PCM Playback Switch", 0)

	facts := endpointFacts{
		Name:      dac.Name(),
		Direction: directionSink,
		Machine:   "liken-1",
		Endpoint:  dac,
		Node: pwNode{
			ID:      51,
			Name:    sinkNodeName(1, 0),
			Volumes: []float64{0.5, 0.5},
			Format:  pwFormat{Rate: 48000, Channels: 2, Positions: []string{"FL", "FR"}},
		},
		HasNode:  true,
		Controls: []control{volume, mute},
		Values:   map[string]string{"PCM Playback Volume": "64", "PCM Playback Switch": controlOn},
	}

	want := EndpointStatus{
		Node:           "liken-1",
		Location:       "1-6",
		ConnectionType: "usb",
		Card:           &EndpointCard{Number: 1, ID: "Audio", Driver: "USB-Audio", Name: "USB Audio and HID"},
		PCM:            &EndpointPCM{Device: 0, ID: "USB Audio"},
		NodeName:       sinkNodeName(1, 0),
		Format:         &EndpointFormat{Rate: 48000, Channels: 2, Positions: []string{"FL", "FR"}},
		Capabilities: map[string]controlCapability{
			"PCM Playback Volume": volume.Capability,
			"PCM Playback Switch": mute.Capability,
		},
		Observed: &EndpointObserved{
			Volume:   pointerTo(50),
			Mute:     pointerTo(false),
			Controls: map[string]string{"PCM Playback Volume": "64", "PCM Playback Switch": controlOn},
		},
		Conditions: []EndpointCondition{
			connectedCondition(true, "CardPresent", "The card is on the USB bus"),
			readyCondition(true, "PipeWire has the node "+sinkNodeName(1, 0)),
		},
	}
	if got := facts.status(EndpointStatus{}, factsTime); !reflect.DeepEqual(got, want) {
		t.Errorf("status =\n%+v\nwant\n%+v", got, want)
	}
}

// The whole status of the DAC's capture side. It is the same PCM device
// in the other direction, so its name ends in -capture and its controls
// are the capture controls of the card.
func TestSourceStatusOfAUSBDAC(t *testing.T) {
	endpoints := liken1Endpoints(t)
	microphone := endpoints["liken-1-usb-0573-1573-a34004801402-usb-audio-capture"]
	gain := levelControl("Mic Capture Volume", 0)

	facts := endpointFacts{
		Name:      microphone.Name(),
		Direction: directionSource,
		Machine:   "liken-1",
		Endpoint:  microphone,
		Node: pwNode{
			ID:      52,
			Name:    sourceNodeName(1, 0),
			Mute:    true,
			Volumes: []float64{1},
			Format:  pwFormat{Rate: 48000, Channels: 1, Positions: []string{"MONO"}},
		},
		HasNode:  true,
		Controls: []control{gain},
		Values:   map[string]string{"Mic Capture Volume": "16"},
	}

	want := EndpointStatus{
		Node:           "liken-1",
		Location:       "1-6",
		ConnectionType: "usb",
		Card:           &EndpointCard{Number: 1, ID: "Audio", Driver: "USB-Audio", Name: "USB Audio and HID"},
		PCM:            &EndpointPCM{Device: 0, ID: "USB Audio"},
		NodeName:       sourceNodeName(1, 0),
		Format:         &EndpointFormat{Rate: 48000, Channels: 1, Positions: []string{"MONO"}},
		Capabilities:   map[string]controlCapability{"Mic Capture Volume": gain.Capability},
		Observed: &EndpointObserved{
			Volume:   pointerTo(100),
			Mute:     pointerTo(true),
			Controls: map[string]string{"Mic Capture Volume": "16"},
		},
		Conditions: []EndpointCondition{
			connectedCondition(true, "CardPresent", "The card is on the USB bus"),
			readyCondition(true, "PipeWire has the node "+sourceNodeName(1, 0)),
		},
	}
	if got := facts.status(EndpointStatus{}, factsTime); !reflect.DeepEqual(got, want) {
		t.Errorf("status =\n%+v\nwant\n%+v", got, want)
	}
}

// A speaker reports its Peripheral and the codec that its transport
// negotiated. Its level comes from the Route of the device, which is the
// volume of the speaker itself.
func TestSinkStatusOfASpeaker(t *testing.T) {
	facts := endpointFacts{
		Name:      testSpeakerName,
		Direction: directionSink,
		Machine:   "liken-1",
		Speaker: &speakerFacts{
			Address: testSpeakerAddress,
			Paired:  speaker{Name: "Kitchen Speaker", Connected: true},
			Sink: bluezSink{
				Node:    testSpeakerNode,
				NodeID:  63,
				Codec:   "sbc",
				Codecs:  twoCodecs(),
				Volumes: []float64{1, 1},
				Format:  pwFormat{Rate: 44100, Channels: 2, Positions: []string{"FL", "FR"}},
				Route:   &pwRoute{Index: 1, Volumes: []float64{0.25, 0.25}, AbsoluteVolume: true},
			},
			HasSink: true,
		},
		HasNode: true,
	}
	facts.Node = facts.Speaker.Sink.sinkNode()

	want := EndpointStatus{
		Node:           "liken-1",
		ConnectionType: "bluetooth",
		Bluetooth: &EndpointBluetooth{
			Address:    "A0:AB:51:33:B7:12",
			Name:       "Kitchen Speaker",
			Peripheral: testSpeakerName,
			Codec:      "sbc",
			Codecs:     []string{"sbc", "aptx"},
		},
		NodeName: testSpeakerNode,
		Format:   &EndpointFormat{Rate: 44100, Channels: 2, Positions: []string{"FL", "FR"}},
		Observed: &EndpointObserved{
			Volume: pointerTo(25),
			Mute:   pointerTo(false),
			Codec:  "sbc",
		},
		Conditions: []EndpointCondition{
			connectedCondition(true, "SpeakerConnected", "bluetoothd reports the speaker connected"),
			readyCondition(true, "PipeWire has the node "+testSpeakerNode),
		},
	}
	if got := facts.status(EndpointStatus{}, factsTime); !reflect.DeepEqual(got, want) {
		t.Errorf("status =\n%+v\nwant\n%+v", got, want)
	}
}

// The Connected condition for each kind of endpoint, including the
// analog cases that depend on jack detection in the card.
func TestConnectedCondition(t *testing.T) {
	cases := []struct {
		name   string
		facts  endpointFacts
		met    bool
		reason string
	}{
		{
			name:   "an analog jack with a plug in it",
			facts:  endpointFacts{Plugged: true, Sensed: true},
			met:    true,
			reason: "JackPlugged",
		},
		{
			name:   "an analog jack with nothing in it",
			facts:  endpointFacts{Sensed: true},
			reason: "JackEmpty",
		},
		{
			name:   "a card that senses no jack",
			facts:  endpointFacts{},
			met:    true,
			reason: "NoJackSensing",
		},
		{
			name:   "a USB card",
			facts:  endpointFacts{Endpoint: alsaEndpoint{Identity: cardIdentity{Bus: usbBus}}},
			met:    true,
			reason: "CardPresent",
		},
		{
			name:   "a speaker that is switched off",
			facts:  endpointFacts{Speaker: &speakerFacts{Paired: speaker{Name: "Kitchen Speaker"}}},
			reason: "SpeakerDisconnected",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			met, reason, _ := c.facts.connected()
			if met != c.met || reason != c.reason {
				t.Errorf("connected = %v, %q, want %v, %q", met, reason, c.met, c.reason)
			}
		})
	}
}

// A speaker whose transport reports its own volume gets its level from
// the Route. Every other endpoint gets it from the gain of the node.
func TestObservedLevelComesFromTheRouteOnASpeakerThatReportsOne(t *testing.T) {
	node := pwNode{ID: 63, Volumes: []float64{1, 1}}
	absolute := endpointFacts{HasNode: true, Node: node, Speaker: &speakerFacts{
		HasSink: true,
		Sink:    bluezSink{Volumes: []float64{1, 1}, Route: &pwRoute{Volumes: []float64{0.4, 0.4}, AbsoluteVolume: true}},
	}}
	if volume, _, known := absolute.level(); !known || volume != 40 {
		t.Errorf("volume = %d (known %v), want the Route's 40", volume, known)
	}

	// A speaker with no absolute volume has no volumeStep on its Route,
	// so its level is the software gain of its node.
	software := endpointFacts{HasNode: true, Node: node, Speaker: &speakerFacts{
		HasSink: true,
		Sink:    bluezSink{Volumes: []float64{1, 1}, Route: &pwRoute{Volumes: []float64{0.4, 0.4}}},
	}}
	if volume, _, known := software.level(); !known || volume != 100 {
		t.Errorf("volume = %d (known %v), want the node's 100", volume, known)
	}
}

// A node that reports no levels gives the status no observed levels. A
// zero would read as silence.
func TestObservedIsAbsentWhileTheNodeReportsNoLevels(t *testing.T) {
	facts := endpointFacts{HasNode: true, Node: pwNode{ID: 7, Name: sinkNodeName(0, 0)}}
	status := facts.status(EndpointStatus{}, factsTime)
	if status.Observed != nil {
		t.Errorf("observed = %+v, want none", status.Observed)
	}
	if status.Format != nil {
		t.Errorf("format = %+v, want none", status.Format)
	}
}

// A suspended node can report no level, and PipeWire does not announce
// changes to one. An idle endpoint therefore reports the level that the
// operator last wrote, which is the level the node will run at.
func TestObservedIsTheLastWriteWhileTheNodeReportsNoLevels(t *testing.T) {
	facts := endpointFacts{
		HasNode: true,
		Node:    pwNode{ID: 7, Name: sinkNodeName(0, 0)},
		Written: &levelWrite{Mute: pointerTo(true)},
	}
	status := facts.status(EndpointStatus{}, factsTime)
	if status.Observed == nil || status.Observed.Mute == nil || !*status.Observed.Mute {
		t.Errorf("observed = %+v, want the written mute", status.Observed)
	}
	if status.Observed != nil && status.Observed.Volume != nil {
		t.Errorf("observed volume = %d, want none: the write carried no volume", *status.Observed.Volume)
	}
}

// PipeWire applies a write to a suspended node without announcing it,
// so the node can keep printing the channelVolumes it last ran at. A
// suspended node reports the level that the operator last wrote, or the
// graph's value when the operator wrote none. A running node reports
// the graph's value.
func TestObservedOfANodeThatPrintsAStaleLevel(t *testing.T) {
	stale := pwNode{ID: 7, Name: sinkNodeName(0, 0), Volumes: []float64{1, 1}, Suspended: true}
	running := pwNode{ID: 7, Name: sinkNodeName(0, 0), Volumes: []float64{0.5, 0.5}}
	written := &levelWrite{Volume: pointerTo(70), Mute: pointerTo(false)}
	cases := []struct {
		name    string
		node    pwNode
		written *levelWrite
		volume  int
	}{
		{name: "a suspended node the operator wrote", node: stale, written: written, volume: 70},
		{name: "a suspended node the operator did not write", node: stale, volume: 100},
		{name: "a running node", node: running, written: written, volume: 50},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			facts := endpointFacts{HasNode: true, Node: c.node, Written: c.written}
			observed := facts.status(EndpointStatus{}, factsTime).Observed
			if observed == nil || observed.Volume == nil || *observed.Volume != c.volume {
				t.Errorf("observed = %+v, want %d percent", observed, c.volume)
			}
		})
	}
}

func connectedCondition(met bool, reason, message string) EndpointCondition {
	return condition(ConnectedCondition, met, reason, message, factsTime)
}

func readyCondition(met bool, message string) EndpointCondition {
	reason := "NoNode"
	if met {
		reason = "NodePresent"
	}
	return condition(ReadyCondition, met, reason, message, factsTime)
}

// A card's sink reports the layout that the drop-in gives it, the
// source of that layout, and whether PipeWire applies it.
func TestSinkStatusReportsTheLayout(t *testing.T) {
	cases := []struct {
		name      string
		state     layoutState
		positions []string
		source    string
		condition EndpointCondition
	}{
		{"a layout from the ELD that PipeWire runs",
			layoutState{Layout: channelLayout{Source: layoutFromELD, Positions: surround71Layout},
				Applied: true, Reason: layoutReasonApplied, Message: "runs"},
			surround71Layout, "ELD",
			condition(LayoutAppliedCondition, true, layoutReasonApplied, "runs", factsTime)},
		{"a receiver that decodes encoded formats itself",
			layoutState{Layout: channelLayout{Source: layoutFromELD, Positions: surround71Layout,
				Encoded: []string{"AC3", "EAC3", "TrueHD"}},
				Applied: true, Reason: layoutReasonApplied, Message: "runs"},
			surround71Layout, "ELD",
			condition(LayoutAppliedCondition, true, layoutReasonApplied, "runs", factsTime)},
		{"a layout that waits for the film to end",
			layoutState{Layout: channelLayout{Source: layoutNone}, Reason: layoutReasonAwaitingIdle, Message: "waits"},
			nil, "None",
			condition(LayoutAppliedCondition, false, layoutReasonAwaitingIdle, "waits", factsTime)},
		{"the channel map of a USB card",
			layoutState{Layout: channelLayout{Source: layoutFromChannelMap}, Applied: true, Reason: layoutReasonApplied},
			nil, "ChannelMap",
			condition(LayoutAppliedCondition, true, layoutReasonApplied, "", factsTime)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			state := c.state
			status := endpointFacts{Name: "sink", Direction: directionSink, Layout: &state}.
				status(EndpointStatus{}, factsTime)
			if !reflect.DeepEqual(status.Passthrough, c.state.Layout.Encoded) {
				t.Errorf("passthrough = %v, want %v", status.Passthrough, c.state.Layout.Encoded)
			}
			if !reflect.DeepEqual(status.Layout, c.positions) || status.LayoutSource != c.source {
				t.Errorf("layout = %v from %q, want %v from %q", status.Layout, status.LayoutSource, c.positions, c.source)
			}
			if got := status.Conditions[len(status.Conditions)-1]; !reflect.DeepEqual(got, c.condition) {
				t.Errorf("condition = %+v, want %+v", got, c.condition)
			}
		})
	}
}

// An endpoint with no layout state reports no layout fields and no
// LayoutApplied condition.
func TestAnEndpointWithNoLayoutReportsNone(t *testing.T) {
	status := endpointFacts{Name: "speaker", Direction: directionSink}.status(EndpointStatus{}, factsTime)
	if status.Layout != nil || status.LayoutSource != "" {
		t.Errorf("layout = %v from %q, want none", status.Layout, status.LayoutSource)
	}
	for _, reported := range status.Conditions {
		if reported.Type == LayoutAppliedCondition {
			t.Errorf("the status reports %+v, want no LayoutApplied condition", reported)
		}
	}
}
