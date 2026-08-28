package main

// These tests cover what one endpoint publishes as a device: the
// attributes a selector reads, the node name, and the taints that say
// the endpoint cannot play.

import (
	"reflect"
	"testing"
)

func hdmiOutput(t *testing.T, card, pcm int) alsaEndpoint {
	t.Helper()
	block, err := parseELD(fixture(t, "eld-hdmi-lg-ultrawide.bin"))
	if err != nil {
		t.Fatal(err)
	}
	return named(alsaEndpoint{Card: card, PCM: pcm, HDMI: true, Monitor: true, ELD: block})[0]
}

// labNames maps the lab card's PCM devices to the names a reconcile
// pass stamps on them, so a test can build an endpoint literal with
// no machine fixture behind it.
var labNames = map[pcmAddress]string{
	{Card: 0, PCM: 0}: testAnalogName,
	{Card: 0, PCM: 3}: testSinkName,
	{Card: 0, PCM: 8}: "liken-1-pci-0000-00-1f-3-hdmi-1",
	{Card: 0, PCM: 9}: "liken-1-pci-0000-00-1f-3-hdmi-2",
	{Card: 1, PCM: 0}: "usb-0573-1573-a34004801402-usb-audio",
}

// named stamps each endpoint with the name a pass would give it. A
// device with no name reaches no slice, so every endpoint a test
// publishes has to carry one.
func named(outputs ...alsaEndpoint) []alsaEndpoint {
	for i, output := range outputs {
		outputs[i].DeviceName = labNames[pcmAddress{Card: output.Card, PCM: output.PCM}]
		if output.Capture {
			outputs[i].DeviceName += captureSuffix
		}
	}
	return outputs
}

func TestSliceDevicesPublishesEachOutput(t *testing.T) {
	outputs := named(alsaEndpoint{Card: 0, PCM: 0}, hdmiOutput(t, 0, 3))
	sinks := map[pcmAddress]string{
		{Card: 0, PCM: 0}: "alsa_output.pci-0000_00_1f.3.analog-stereo",
		{Card: 0, PCM: 3}: "alsa_output.pci-0000_00_1f.3.hdmi-stereo",
	}

	devices := sliceDevices(outputs, nil, outputGraph(sinks))
	if len(devices) != 2 {
		t.Fatalf("got %d devices, want 2", len(devices))
	}
	// The list is sorted, so the same hardware always makes the same
	// slice and the change detection reports real changes only.
	if devices[0].Name != testAnalogName || devices[1].Name != testSinkName {
		t.Fatalf("names = %q, %q", devices[0].Name, devices[1].Name)
	}

	analog := devices[0]
	if got := *analog.Attributes["connectionType"].String; got != "analog" {
		t.Errorf("the analog output's connection type = %q", got)
	}
	// A selector cannot read a device's name, so the name is an
	// attribute as well, and its two halves are numbers.
	if got := stringAttribute(t, analog, sinkAttribute); got != testAnalogName {
		t.Errorf("sink = %q", got)
	}
	if got := intAttribute(t, analog, "card"); got != 0 {
		t.Errorf("card = %d", got)
	}
	if got := intAttribute(t, analog, "pcm"); got != 0 {
		t.Errorf("pcm = %d", got)
	}
	if _, published := analog.Attributes[PairingAttribute]; published {
		t.Error("the analog output published a pairing attribute")
	}
	if len(analog.Taints) != 0 {
		t.Errorf("the analog output has taints: %+v", analog.Taints)
	}

	hdmi := devices[1]
	attributes := map[string]string{
		sinkAttribute:     testSinkName,
		"connectionType":  "hdmi",
		"manufacturer":    "GSM",
		"product":         "5b09",
		"monitorName":     "LG ULTRAWIDE",
		"speakers":        "FL/FR",
		"lpcmBitDepths":   "16 20 24",
		nodeNameAttribute: "alsa_output.pci-0000_00_1f.3.hdmi-stereo",
		PairingAttribute:  "gsm-5b09-lg-ultrawide",
	}
	for name, want := range attributes {
		attribute, published := hdmi.Attributes[name]
		if !published {
			t.Errorf("the HDMI output published no %s", name)
			continue
		}
		if got := *attribute.String; got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if got := intAttribute(t, hdmi, "lpcmChannels"); got != 2 {
		t.Errorf("lpcmChannels = %d, want 2", got)
	}
	if got := intAttribute(t, hdmi, "lpcmMaxRateHz"); got != 48000 {
		t.Errorf("lpcmMaxRateHz = %d, want 48000", got)
	}
	if got := intAttribute(t, hdmi, "pcm"); got != 3 {
		t.Errorf("pcm = %d, want 3", got)
	}
	if got := intAttribute(t, hdmi, "eldVersion"); got != 2 {
		t.Errorf("eldVersion = %d, want 2", got)
	}
	if got := intAttribute(t, hdmi, "portID"); got != 0x800 {
		t.Errorf("portID = %#x, want 0x800", got)
	}
	if len(hdmi.Taints) != 0 {
		t.Errorf("the HDMI output has taints: %+v", hdmi.Taints)
	}
}

// A capture PCM publishes as a device of its own, with the source
// attribute in place of sink. A DeviceClass selects a direction by
// which of the two a device carries, so no device may carry both.
func TestSliceDevicesPublishesACapturePCMAsASource(t *testing.T) {
	outputs := named(
		alsaEndpoint{Card: 1, PCM: 0, Identity: cardIdentity{Bus: "usb"}},
		alsaEndpoint{Card: 1, PCM: 0, Capture: true, Identity: cardIdentity{Bus: "usb"}},
	)
	address := pcmAddress{Card: 1, PCM: 0}
	graph := pwGraph{
		Nodes: map[nodeAddress]pwNode{
			{pcmAddress: address, Direction: directionSink}:   {Name: sinkNodeName(1, 0)},
			{pcmAddress: address, Direction: directionSource}: {Name: sourceNodeName(1, 0)},
		},
		Speakers: map[string]bluezSink{},
	}

	devices := sliceDevices(outputs, nil, graph)
	if len(devices) != 2 {
		t.Fatalf("devices = %+v, want the sink and the source", devices)
	}
	sink, source := devices[0], devices[1]
	if sink.Name != labNames[address] || source.Name != labNames[address]+captureSuffix {
		t.Fatalf("names = %q, %q", sink.Name, source.Name)
	}

	if got := stringAttribute(t, source, sourceAttribute); got != source.Name {
		t.Errorf("source = %q, want %q", got, source.Name)
	}
	// One attribute carries the node name in both directions, and it
	// names the source node here.
	if got := stringAttribute(t, source, nodeNameAttribute); got != sourceNodeName(1, 0) {
		t.Errorf("nodeName = %q", got)
	}
	// A USB card publishes usb in both directions, which is what tells
	// a claim that no cable can come out of it.
	if got := stringAttribute(t, source, "connectionType"); got != "usb" {
		t.Errorf("connectionType = %q, want usb", got)
	}
	if len(source.Taints) != 0 {
		t.Errorf("a source with a node is tainted: %+v", source.Taints)
	}
	// The direction is the one attribute a DeviceClass selects on, so
	// no device carries both.
	if _, published := source.Attributes[sinkAttribute]; published {
		t.Errorf("the source published %s", sinkAttribute)
	}
	if _, published := sink.Attributes[sourceAttribute]; published {
		t.Errorf("the sink published %s", sourceAttribute)
	}
}

// A source whose node PipeWire holds none of carries the same taints
// a sink does, because the fact is one fact in both directions: no
// node exists for a prepare call to name.
func TestSliceDevicesTaintsASourceWithNoNode(t *testing.T) {
	outputs := named(alsaEndpoint{Card: 1, PCM: 0, Capture: true, Identity: cardIdentity{Bus: "usb"}})

	// The playback node of the same PCM device is in the graph, and it
	// says nothing about the capture side.
	graph := outputGraph(map[pcmAddress]string{{Card: 1, PCM: 0}: sinkNodeName(1, 0)})

	devices := sliceDevices(outputs, nil, graph)
	want := []DeviceTaint{
		{Key: disconnectedTaint, Effect: "NoExecute"},
		{Key: noSinkTaint, Effect: "NoSchedule"},
	}
	if got := devices[0].Taints; !reflect.DeepEqual(got, want) {
		t.Fatalf("taints = %+v, want %+v", got, want)
	}
	if _, published := devices[0].Attributes[nodeNameAttribute]; published {
		t.Error("a source with no node published a node name")
	}
}

// The output attribute retires. A consumer that still selects on it
// allocates nothing, which is the honest answer for a vocabulary this
// driver no longer publishes.
func TestSliceDevicesPublishesNoOutputAttribute(t *testing.T) {
	outputs := named(alsaEndpoint{Card: 0, PCM: 0}, hdmiOutput(t, 0, 3))
	graph := outputGraph(map[pcmAddress]string{
		{Card: 0, PCM: 0}: sinkNodeName(0, 0),
		{Card: 0, PCM: 3}: sinkNodeName(0, 3),
	})
	graph.Speakers[testSpeakerAddress] = bluezSink{Node: testSpeakerNode, Codec: "sbc"}

	for _, device := range sliceDevices(outputs, testSpeakers(), graph) {
		if _, published := device.Attributes["output"]; published {
			t.Errorf("%s still publishes the output attribute", device.Name)
		}
	}
}

func TestMonitorAttributesOmitTheLPCMFactsWithoutAnLPCMDescriptor(t *testing.T) {
	block, err := parseELD(withByte(fixture(t, "eld-hdmi-lg-ultrawide.bin"), 32, 0x11))
	if err != nil {
		t.Fatal(err)
	}

	attributes := map[string]DeviceAttribute{}
	addMonitorAttributes(attributes, block)

	for _, name := range []string{"lpcmChannels", "lpcmMaxRateHz", "lpcmBitDepths"} {
		if _, published := attributes[name]; published {
			t.Errorf("a block with no LPCM descriptor published %s", name)
		}
	}
	if got := *attributes["monitorName"].String; got != "LG ULTRAWIDE" {
		t.Errorf("monitor name = %q", got)
	}
}

func TestSliceDevicesTaintsAnOutputThatCannotPlay(t *testing.T) {
	// Each case names the taints the output must have, in the order
	// the slice publishes them. The NoExecute taint says the output
	// cannot serve a stream now, and each NoSchedule taint names one
	// reason, so the set states the whole condition.
	cases := []struct {
		name   string
		output alsaEndpoint
		sink   bool
		taints []DeviceTaint
	}{
		{
			name:   "an HDMI output with a monitor and a sink",
			output: alsaEndpoint{Card: 0, PCM: 3, HDMI: true, Monitor: true},
			sink:   true,
		},
		{
			name:   "the analog jack with a sink",
			output: alsaEndpoint{Card: 0, PCM: 0},
			sink:   true,
		},
		{
			name:   "an unplugged monitor whose sink node is still there",
			output: alsaEndpoint{Card: 0, PCM: 3, HDMI: true},
			sink:   true,
			taints: []DeviceTaint{
				{Key: disconnectedTaint, Effect: "NoExecute"},
				{Key: noMonitorTaint, Effect: "NoSchedule"},
			},
		},
		{
			name:   "an output with a monitor and no sink node",
			output: alsaEndpoint{Card: 0, PCM: 3, HDMI: true, Monitor: true},
			taints: []DeviceTaint{
				{Key: disconnectedTaint, Effect: "NoExecute"},
				{Key: noSinkTaint, Effect: "NoSchedule"},
			},
		},
		{
			name:   "the analog jack with no sink node",
			output: alsaEndpoint{Card: 0, PCM: 0},
			taints: []DeviceTaint{
				{Key: disconnectedTaint, Effect: "NoExecute"},
				{Key: noSinkTaint, Effect: "NoSchedule"},
			},
		},
		{
			name:   "an unplugged monitor with no sink node either",
			output: alsaEndpoint{Card: 0, PCM: 3, HDMI: true},
			taints: []DeviceTaint{
				{Key: disconnectedTaint, Effect: "NoExecute"},
				{Key: noMonitorTaint, Effect: "NoSchedule"},
				{Key: noSinkTaint, Effect: "NoSchedule"},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sinks := map[pcmAddress]string{}
			if c.sink {
				sinks[pcmAddress{Card: c.output.Card, PCM: c.output.PCM}] = "alsa_output.test"
			}

			devices := sliceDevices(named(c.output), nil, outputGraph(sinks))
			if got := devices[0].Taints; !reflect.DeepEqual(got, c.taints) {
				t.Fatalf("taints = %+v, want %+v", got, c.taints)
			}
		})
	}
}

// The node name and the no-sink taint come from one fact, so a device
// that publishes the name of its node must never also say it has none.
// Those two would state opposite facts: the name says a claim would
// play, and the taint says a claim would reach nothing.
func TestSliceDevicesNeverPublishesANodeNameAndTheNoSinkTaint(t *testing.T) {
	outputs := named(
		alsaEndpoint{Card: 0, PCM: 0},
		alsaEndpoint{Card: 0, PCM: 3, HDMI: true, Monitor: true},
		alsaEndpoint{Card: 0, PCM: 8, HDMI: true},
		alsaEndpoint{Card: 0, PCM: 9, HDMI: true},
	)
	// Every declared node is in the graph, which is what PipeWire holds
	// once it has loaded the operator's drop-in, whether a monitor is on
	// the port or not.
	sinks := map[pcmAddress]string{}
	for _, output := range outputs {
		sinks[pcmAddress{Card: output.Card, PCM: output.PCM}] = sinkNodeName(output.Card, output.PCM)
	}

	for _, device := range sliceDevices(outputs, nil, outputGraph(sinks)) {
		name, named := device.Attributes[nodeNameAttribute]
		if !named {
			t.Errorf("%s published no node name for a node in the graph", device.Name)
			continue
		}
		for _, taint := range device.Taints {
			if taint.Key == noSinkTaint {
				t.Errorf("%s published the node name %q and the no-sink taint",
					device.Name, *name.String)
			}
		}
	}
}

func TestSliceDevicesOmitsANodeNameTooLongToPublish(t *testing.T) {
	// A truncated name would not name the sink, and the delivery reads the
	// current name from PipeWire rather than from the slice.
	long := ""
	for range maxAttributeLength + 1 {
		long += "x"
	}
	devices := sliceDevices(
		named(alsaEndpoint{Card: 0, PCM: 0}),
		nil,
		outputGraph(map[pcmAddress]string{{Card: 0, PCM: 0}: long}),
	)
	if _, published := devices[0].Attributes[nodeNameAttribute]; published {
		t.Fatal("a name past the attribute limit published anyway")
	}
	if len(devices[0].Taints) != 0 {
		t.Fatalf("an output with a sink was tainted: %+v", devices[0].Taints)
	}
}

// One paired speaker, connected, with WirePlumber's node for it in
// the graph.
func testSpeakers() map[string]speaker {
	return map[string]speaker{
		testSpeakerAddress: {Name: "Kitchen Speaker", Connected: true},
	}
}

// A mixed slice: the card's two outputs and one paired speaker,
// which is what a machine with a radio publishes.
func TestSliceDevicesPublishesSpeakersBesideTheCardsOutputs(t *testing.T) {
	outputs := named(alsaEndpoint{Card: 0, PCM: 0}, hdmiOutput(t, 0, 3))
	sinks := map[pcmAddress]string{
		{Card: 0, PCM: 0}: sinkNodeName(0, 0),
		{Card: 0, PCM: 3}: sinkNodeName(0, 3),
	}
	graph := outputGraph(sinks)
	graph.Speakers[testSpeakerAddress] = bluezSink{Node: testSpeakerNode, Codec: "sbc", Codecs: twoCodecs()}

	devices := sliceDevices(outputs, testSpeakers(), graph)
	if len(devices) != 3 {
		t.Fatalf("devices = %d, want three", len(devices))
	}
	// The list is sorted by name, so the speaker's dashed MAC sorts
	// ahead of the card's outputs.
	names := []string{testSpeakerName, testAnalogName, testSinkName}
	for i, want := range names {
		if devices[i].Name != want {
			t.Fatalf("devices[%d] = %q, want %q", i, devices[i].Name, want)
		}
	}

	// The card's outputs publish exactly what they published without a
	// radio on the machine.
	if got := sliceDevices(outputs, nil, outputGraph(sinks)); !sameDevices(got, devices[1:]) {
		t.Errorf("the card's outputs changed beside a speaker:\n%+v\n%+v", got, devices[1:])
	}

	speaker := devices[0]
	attributes := map[string]string{
		sinkAttribute:     testSpeakerName,
		"address":         "A0:AB:51:33:B7:12",
		"connectionType":  "bluetooth",
		"name":            "Kitchen Speaker",
		"codec":           "sbc",
		"codecs":          "sbc aptx",
		nodeNameAttribute: testSpeakerNode,
	}
	for name, want := range attributes {
		if got := stringAttribute(t, speaker, name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if connected := speaker.Attributes["connected"]; connected.Bool == nil || !*connected.Bool {
		t.Errorf("connected = %+v, want true", connected)
	}
	if len(speaker.Taints) != 0 {
		t.Errorf("a connected speaker with a node is tainted: %+v", speaker.Taints)
	}
}

// A paired speaker that is switched off publishes with both taints,
// so a consumer's claim on it parks instead of failing prepare, and
// the claim allocates when somebody turns the speaker on.
func TestSliceDevicesTaintsASpeakerThatCannotPlay(t *testing.T) {
	cases := []struct {
		name    string
		speaker speaker
		sink    bool
		taints  []DeviceTaint
	}{
		{
			name:    "connected, with a node",
			speaker: speaker{Name: "Kitchen Speaker", Connected: true},
			sink:    true,
		},
		{
			name:    "paired and switched off",
			speaker: speaker{Name: "Kitchen Speaker"},
			taints: []DeviceTaint{
				{Key: disconnectedTaint, Effect: "NoExecute"},
				{Key: noSinkTaint, Effect: "NoSchedule"},
			},
		},
		{
			// bluetoothd reports the connection before WirePlumber has
			// built the node, and the taints report the stricter fact.
			name:    "connected, with no node yet",
			speaker: speaker{Name: "Kitchen Speaker", Connected: true},
			taints: []DeviceTaint{
				{Key: disconnectedTaint, Effect: "NoExecute"},
				{Key: noSinkTaint, Effect: "NoSchedule"},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sinks := map[string]bluezSink{}
			if c.sink {
				sinks[testSpeakerAddress] = bluezSink{
					Node:   testSpeakerNode,
					Codec:  "sbc",
					Codecs: twoCodecs(),
				}
			}

			devices := speakerDevices(map[string]speaker{testSpeakerAddress: c.speaker}, sinks)
			if got := devices[0].Taints; !reflect.DeepEqual(got, c.taints) {
				t.Fatalf("taints = %+v, want %+v", got, c.taints)
			}
			// The sink name, the codec, and the codec set are what the
			// graph holds, so a speaker with no node publishes none of
			// them.
			for _, name := range []string{nodeNameAttribute, "codec", "codecs"} {
				if _, published := devices[0].Attributes[name]; published != c.sink {
					t.Errorf("%s published = %v, want %v", name, published, c.sink)
				}
			}
		})
	}
}

// A device that answers no codec choice publishes no list, and the
// speaker is claimable all the same. The negotiated codec is a
// separate fact and still publishes.
func TestSliceDevicesPublishesNoCodecListWithoutOne(t *testing.T) {
	sinks := map[string]bluezSink{
		testSpeakerAddress: {Node: testSpeakerNode, Codec: "sbc"},
	}

	devices := speakerDevices(testSpeakers(), sinks)
	if _, published := devices[0].Attributes["codecs"]; published {
		t.Errorf("codecs = %+v, want none", devices[0].Attributes["codecs"])
	}
	if got := stringAttribute(t, devices[0], "codec"); got != "sbc" {
		t.Errorf("codec = %q, want sbc", got)
	}
}

// Unpairing is the one thing that removes a speaker from the slice.
// Any other removal would strand a claim that holds it: the
// allocation still names the device, and the kubelet retries its
// prepare against a device in no slice, with no bound.
func TestSliceDevicesDropsAnUnpairedSpeaker(t *testing.T) {
	outputs := named(alsaEndpoint{Card: 0, PCM: 0})
	graph := outputGraph(map[pcmAddress]string{{Card: 0, PCM: 0}: sinkNodeName(0, 0)})

	devices := sliceDevices(outputs, nil, graph)
	if len(devices) != 1 || devices[0].Name != testAnalogName {
		t.Fatalf("devices = %+v, want the card's output alone", devices)
	}
}

func TestSameDevicesIgnoresTheServersTimestamp(t *testing.T) {
	// The API server fills TimeAdded in on every taint it stores. A
	// comparison that read it would call every pass a change, and every
	// slice write wakes every DRA-pending pod in the cluster.
	published := []SliceDevice{{
		Name:   testSinkName,
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute", TimeAdded: "2026-08-16T12:00:00Z"}},
	}}
	current := []SliceDevice{{
		Name:   testSinkName,
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute"}},
	}}
	if !sameDevices(published, current) {
		t.Fatal("a stored timestamp counted as a change")
	}
}

func TestSameDevicesSeesRealChanges(t *testing.T) {
	tainted := []SliceDevice{{
		Name:   testSinkName,
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute", TimeAdded: "2026-08-16T12:00:00Z"}},
	}}
	clear := []SliceDevice{{Name: testSinkName}}
	if sameDevices(tainted, clear) {
		t.Fatal("clearing a taint did not count as a change")
	}
	renamed := []SliceDevice{{Name: "liken-1-pci-0000-00-1f-3-hdmi-3"}}
	if sameDevices(clear, renamed) {
		t.Fatal("a different output did not count as a change")
	}
	relabeled := []SliceDevice{{
		Name:       testSinkName,
		Attributes: map[string]DeviceAttribute{nodeNameAttribute: AttrString("alsa_output.test")},
	}}
	if sameDevices(clear, relabeled) {
		t.Fatal("a new attribute did not count as a change")
	}
}

// stringAttribute reads one string attribute, and reports a device
// that does not publish it as a failure rather than a panic.
func stringAttribute(t *testing.T, device SliceDevice, name string) string {
	t.Helper()
	attribute, published := device.Attributes[name]
	if !published || attribute.String == nil {
		t.Fatalf("%s publishes no string attribute %q", device.Name, name)
	}
	return *attribute.String
}

// intAttribute reads one number attribute.
func intAttribute(t *testing.T, device SliceDevice, name string) int64 {
	t.Helper()
	attribute, published := device.Attributes[name]
	if !published || attribute.Int == nil {
		t.Fatalf("%s publishes no number attribute %q", device.Name, name)
	}
	return *attribute.Int
}
