package main

// These tests compose the whole of one endpoint's status from the
// facts a pass reads, on the machine the lab holds: an Intel HDMI
// codec with a monitor on its first slot, and a USB DAC that plays and
// records through one PCM device.

import (
	"reflect"
	"testing"
	"time"
)

// factsTime is the moment every condition below is stamped with.
var factsTime = time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)

// likenOne reads the machine's own enumeration and names it, and
// answers with the endpoints by name.
//
// The card's own identity fields come from an ioctl on the control
// device, and the fixture's control device is an ordinary file, so the
// three the card would state are stamped here from what liken-1's card
// reports. cards_test.go covers the read itself.
func likenOne(t *testing.T) map[string]alsaEndpoint {
	t.Helper()
	machineFixture(t, "liken-1")
	endpoints, err := readEndpoints()
	if err != nil {
		t.Fatal(err)
	}
	named, refused := nameEndpoints("liken-1", endpoints)
	if len(refused) != 0 {
		t.Fatalf("the operator refused to name %v", refused)
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

// withMonitor puts the LG's ELD block on one slot, which is what the
// card reports while that monitor is on the port.
func withMonitor(t *testing.T, endpoint alsaEndpoint) alsaEndpoint {
	t.Helper()
	block, err := parseELD(fixture(t, "eld-hdmi-lg-ultrawide.bin"))
	if err != nil {
		t.Fatal(err)
	}
	endpoint.HDMI, endpoint.Monitor, endpoint.ELD = true, true, block
	return endpoint
}

// The whole status of the HDMI slot the monitor is on. An HDMI PCM
// has no volume element, so the one control it lists is the IEC958
// switch, and the monitor block is what ties the slot to the Display
// the display operator publishes for the same screen.
func TestSinkStatusOfAnHDMISlot(t *testing.T) {
	endpoints := likenOne(t)
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
		Claim:    &EndpointClaim{Namespace: "media", Name: "kitchen"},
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
		Claim: &EndpointClaim{Namespace: "media", Name: "kitchen"},
		Conditions: []EndpointCondition{
			connectedCondition(true, "MonitorPresent", "a monitor answers on this slot"),
			readyCondition(true, "PipeWire holds the node "+sinkNodeName(0, 3)),
		},
	}
	if got := facts.status(EndpointStatus{}, factsTime); !reflect.DeepEqual(got, want) {
		t.Errorf("status =\n%+v\nwant\n%+v", got, want)
	}
}

// A slot with no monitor reports no monitor block and says so in the
// condition, which is the same fact the no-monitor taint carries.
func TestSinkStatusOfAnEmptyHDMISlot(t *testing.T) {
	endpoints := likenOne(t)
	slot := endpoints["liken-1-pci-0000-00-1f-3-hdmi-1"]
	slot.HDMI = true

	status := endpointFacts{
		Name: slot.Name(), Direction: directionSink, Machine: "liken-1", Endpoint: slot,
	}.status(EndpointStatus{}, factsTime)

	if status.Monitor != nil {
		t.Errorf("monitor = %+v, want none", status.Monitor)
	}
	// The connection type comes from the ELD block, and a slot with no
	// monitor has none, so the field is absent rather than guessed.
	if status.ConnectionType != "" {
		t.Errorf("connectionType = %q, want none", status.ConnectionType)
	}
	want := []EndpointCondition{
		connectedCondition(false, "NoMonitor", "no monitor answers on this slot"),
		readyCondition(false, "PipeWire holds no node for this endpoint"),
	}
	if !reflect.DeepEqual(status.Conditions, want) {
		t.Errorf("conditions = %+v, want %+v", status.Conditions, want)
	}
}

// The whole status of the DAC's playback side. A USB card is
// connected whenever it publishes an endpoint at all, because the
// endpoint is gone the moment somebody unplugs the card.
func TestSinkStatusOfAUSBDAC(t *testing.T) {
	endpoints := likenOne(t)
	dac := endpoints["usb-0573-1573-a34004801402-usb-audio"]
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
			connectedCondition(true, "CardPresent", "the card is on the bus"),
			readyCondition(true, "PipeWire holds the node "+sinkNodeName(1, 0)),
		},
	}
	if got := facts.status(EndpointStatus{}, factsTime); !reflect.DeepEqual(got, want) {
		t.Errorf("status =\n%+v\nwant\n%+v", got, want)
	}
}

// The whole status of the DAC's capture side. It is the same PCM
// device in the other direction, so its name carries the capture word
// and its controls are the card's capture controls.
func TestSourceStatusOfAUSBDAC(t *testing.T) {
	endpoints := likenOne(t)
	microphone := endpoints["usb-0573-1573-a34004801402-usb-audio-capture"]
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
			connectedCondition(true, "CardPresent", "the card is on the bus"),
			readyCondition(true, "PipeWire holds the node "+sourceNodeName(1, 0)),
		},
	}
	if got := facts.status(EndpointStatus{}, factsTime); !reflect.DeepEqual(got, want) {
		t.Errorf("status =\n%+v\nwant\n%+v", got, want)
	}
}

// A speaker reports the pairing it belongs to and the codec the
// transport negotiated, and its level comes from the device's Route,
// which is the speaker's own volume.
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
			Address: "A0:AB:51:33:B7:12",
			Name:    "Kitchen Speaker",
			Pairing: testSpeakerName,
			Codec:   "sbc",
			Codecs:  []string{"sbc", "aptx"},
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
			readyCondition(true, "PipeWire holds the node "+testSpeakerNode),
		},
	}
	if got := facts.status(EndpointStatus{}, factsTime); !reflect.DeepEqual(got, want) {
		t.Errorf("status =\n%+v\nwant\n%+v", got, want)
	}
}

// What the Connected condition answers for each kind of endpoint, and
// the two analog cases the card's own jacks decide.
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
			// A card that senses no jack for this endpoint says nothing
			// about whether a plug is in, and an endpoint that plays is
			// the honest reading of nothing.
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

// Where the level comes from: a speaker whose transport reports a
// volume of its own answers from the Route, and every other endpoint
// answers from the node's gain.
func TestObservedLevelComesFromTheRouteOnASpeakerThatReportsOne(t *testing.T) {
	node := pwNode{ID: 63, Volumes: []float64{1, 1}}
	absolute := endpointFacts{HasNode: true, Node: node, Speaker: &speakerFacts{
		HasSink: true,
		Sink:    bluezSink{Volumes: []float64{1, 1}, Route: &pwRoute{Volumes: []float64{0.4, 0.4}, AbsoluteVolume: true}},
	}}
	if volume, _, known := absolute.level(); !known || volume != 40 {
		t.Errorf("volume = %d (known %v), want the Route's 40", volume, known)
	}

	// A speaker with no absolute volume publishes no volumeStep, and
	// its level is the software gain on its node.
	software := endpointFacts{HasNode: true, Node: node, Speaker: &speakerFacts{
		HasSink: true,
		Sink:    bluezSink{Volumes: []float64{1, 1}, Route: &pwRoute{Volumes: []float64{0.4, 0.4}}},
	}}
	if volume, _, known := software.level(); !known || volume != 100 {
		t.Errorf("volume = %d (known %v), want the node's 100", volume, known)
	}
}

// A suspended node reports no levels at all, so the status reports
// none rather than zero, which a reader would take for silence.
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

// A suspended node reports no level, and PipeWire announces no
// change to one, so an idle endpoint reports the level the operator
// last wrote, which is the level the node will run at.
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
