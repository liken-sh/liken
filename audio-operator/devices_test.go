package main

import (
	"reflect"
	"testing"
)

func hdmiOutput(t *testing.T, card, pcm int) alsaEndpoint {
	t.Helper()
	block, err := parseELD(fixture(t, "eld-hdmi-lg-ultrawide.bin"))
	if err != nil {
		t.Fatalf("parsing the ELD fixture: %v", err)
	}
	return named(alsaEndpoint{Card: card, PCM: pcm, HDMI: true, Monitor: true, ELD: block})[0]
}

// labNames holds the names that nameEndpoints gives the PCM devices of
// the lab card, so a test can write an endpoint literal without reading
// a machine fixture.
var labNames = map[pcmAddress]string{
	{Card: 0, PCM: 0}: testAnalogName,
	{Card: 0, PCM: 3}: testSinkName,
	{Card: 0, PCM: 8}: "liken-1-pci-0000-00-1f-3-hdmi-1",
	{Card: 0, PCM: 9}: "liken-1-pci-0000-00-1f-3-hdmi-2",
	{Card: 1, PCM: 0}: "liken-1-usb-0573-1573-a34004801402-usb-audio",
}

// named sets DeviceName on each endpoint from labNames. An endpoint with
// no name is not published.
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
		t.Fatalf("sliceDevices returned %d devices, want 2", len(devices))
	}
	if devices[0].Name != testAnalogName || devices[1].Name != testSinkName {
		t.Fatalf("names = %q, %q, want %q, %q", devices[0].Name, devices[1].Name, testAnalogName, testSinkName)
	}

	analog := devices[0]
	if got := *analog.Attributes["connectionType"].String; got != "analog" {
		t.Errorf("the analog output's connectionType = %q, want analog", got)
	}
	if got := stringAttribute(t, analog, sinkAttribute); got != testAnalogName {
		t.Errorf("sink = %q, want %q", got, testAnalogName)
	}
	if got := intAttribute(t, analog, "card"); got != 0 {
		t.Errorf("card = %d, want 0", got)
	}
	if got := intAttribute(t, analog, "pcm"); got != 0 {
		t.Errorf("pcm = %d, want 0", got)
	}
	if _, published := analog.Attributes[PairingAttribute]; published {
		t.Error("the analog output published a pairing attribute, want none")
	}
	if len(analog.Taints) != 0 {
		t.Errorf("the analog output has the taints %+v, want none", analog.Taints)
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
			t.Errorf("the HDMI output published no %s, want %q", name, want)
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
		t.Errorf("the HDMI output has the taints %+v, want none", hdmi.Taints)
	}
}

// A DeviceClass selects playback or capture by which of sink and source
// is present, so a device must not have both.
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
		t.Fatalf("names = %q, %q, want %q, %q", sink.Name, source.Name, labNames[address], labNames[address]+captureSuffix)
	}

	if got := stringAttribute(t, source, sourceAttribute); got != source.Name {
		t.Errorf("source = %q, want %q", got, source.Name)
	}
	if got := stringAttribute(t, source, nodeNameAttribute); got != sourceNodeName(1, 0) {
		t.Errorf("nodeName = %q, want %q", got, sourceNodeName(1, 0))
	}
	if got := stringAttribute(t, source, "connectionType"); got != "usb" {
		t.Errorf("connectionType = %q, want usb", got)
	}
	if len(source.Taints) != 0 {
		t.Errorf("a source with a node has the taints %+v, want none", source.Taints)
	}
	if _, published := source.Attributes[sinkAttribute]; published {
		t.Errorf("the source published %s, want only %s", sinkAttribute, sourceAttribute)
	}
	if _, published := sink.Attributes[sourceAttribute]; published {
		t.Errorf("the sink published %s, want only %s", sourceAttribute, sinkAttribute)
	}
}

func TestSliceDevicesTaintsASourceWithNoNode(t *testing.T) {
	outputs := named(alsaEndpoint{Card: 1, PCM: 0, Capture: true, Identity: cardIdentity{Bus: "usb"}})

	// The playback node of the same PCM device must not count as a node
	// for the source.
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
		t.Error("a source with no node published a node name, want none")
	}
}

func TestSliceDevicesPublishesNoOutputAttribute(t *testing.T) {
	outputs := named(alsaEndpoint{Card: 0, PCM: 0}, hdmiOutput(t, 0, 3))
	graph := outputGraph(map[pcmAddress]string{
		{Card: 0, PCM: 0}: sinkNodeName(0, 0),
		{Card: 0, PCM: 3}: sinkNodeName(0, 3),
	})
	graph.Speakers[testSpeakerAddress] = bluezSink{Node: testSpeakerNode, Codec: "sbc"}

	for _, device := range sliceDevices(outputs, testSpeakers(), graph) {
		if _, published := device.Attributes["output"]; published {
			t.Errorf("%s publishes the output attribute, want none", device.Name)
		}
	}
}

func TestMonitorAttributesOmitTheLPCMFactsWithoutAnLPCMDescriptor(t *testing.T) {
	block, err := parseELD(withByte(fixture(t, "eld-hdmi-lg-ultrawide.bin"), 32, 0x11))
	if err != nil {
		t.Fatalf("parsing the ELD fixture: %v", err)
	}

	attributes := map[string]DeviceAttribute{}
	addMonitorAttributes(attributes, block)

	for _, name := range []string{"lpcmChannels", "lpcmMaxRateHz", "lpcmBitDepths"} {
		if _, published := attributes[name]; published {
			t.Errorf("a block with no LPCM descriptor published %s, want none", name)
		}
	}
	if got := *attributes["monitorName"].String; got != "LG ULTRAWIDE" {
		t.Errorf("monitorName = %q, want %q", got, "LG ULTRAWIDE")
	}
}

func TestSliceDevicesTaintsAnOutputThatCannotPlay(t *testing.T) {
	// Each case lists the taints in the order that sliceDevices adds
	// them.
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

func TestSliceDevicesNeverPublishesANodeNameAndTheNoSinkTaint(t *testing.T) {
	outputs := named(
		alsaEndpoint{Card: 0, PCM: 0},
		alsaEndpoint{Card: 0, PCM: 3, HDMI: true, Monitor: true},
		alsaEndpoint{Card: 0, PCM: 8, HDMI: true},
		alsaEndpoint{Card: 0, PCM: 9, HDMI: true},
	)
	// PipeWire creates a node for every PCM device that the operator's
	// drop-in declares, including an HDMI output with no monitor.
	sinks := map[pcmAddress]string{}
	for _, output := range outputs {
		sinks[pcmAddress{Card: output.Card, PCM: output.PCM}] = sinkNodeName(output.Card, output.PCM)
	}

	for _, device := range sliceDevices(outputs, nil, outputGraph(sinks)) {
		name, named := device.Attributes[nodeNameAttribute]
		if !named {
			t.Errorf("%s published no node name for a node in the graph, want one", device.Name)
			continue
		}
		for _, taint := range device.Taints {
			if taint.Key == noSinkTaint {
				t.Errorf("%s published the node name %q and the no-sink taint, want only the name",
					device.Name, *name.String)
			}
		}
	}
}

func TestSliceDevicesOmitsANodeNameTooLongToPublish(t *testing.T) {
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
		t.Fatal("a name past the attribute limit was published, want no nodeName attribute")
	}
	if len(devices[0].Taints) != 0 {
		t.Fatalf("an output with a sink has the taints %+v, want none", devices[0].Taints)
	}
}

func testSpeakers() map[string]speaker {
	return map[string]speaker{
		testSpeakerAddress: {Name: "Kitchen Speaker", Connected: true},
	}
}

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
		t.Fatalf("sliceDevices returned %d devices, want 3", len(devices))
	}
	// The speaker's name is its MAC address with dashes, which sorts
	// before the card's names.
	names := []string{testSpeakerName, testAnalogName, testSinkName}
	for i, want := range names {
		if devices[i].Name != want {
			t.Fatalf("devices[%d] = %q, want %q", i, devices[i].Name, want)
		}
	}

	if got := sliceDevices(outputs, nil, outputGraph(sinks)); !sameDevices(got, devices[1:]) {
		t.Errorf("beside a speaker, the card's outputs are\n%+v\nwant the same as with no radio\n%+v", devices[1:], got)
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
		t.Errorf("a connected speaker with a node has the taints %+v, want none", speaker.Taints)
	}
}

// A paired speaker that is switched off has both taints, so a claim on
// it stays Pending instead of failing in prepare, and the scheduler
// allocates it when someone turns the speaker on.
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
			// bluetoothd reports the connection before WirePlumber creates
			// the node, and the taints depend on the node.
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
			for _, name := range []string{nodeNameAttribute, "codec", "codecs"} {
				if _, published := devices[0].Attributes[name]; published != c.sink {
					t.Errorf("%s published = %v, want %v", name, published, c.sink)
				}
			}
		})
	}
}

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

// Only unpairing removes a speaker from the slice. A speaker removed
// for any other reason would leave its claim with an allocation that
// names a device in no slice, and the kubelet would retry prepare for
// it without end.
func TestSliceDevicesDropsAnUnpairedSpeaker(t *testing.T) {
	outputs := named(alsaEndpoint{Card: 0, PCM: 0})
	graph := outputGraph(map[pcmAddress]string{{Card: 0, PCM: 0}: sinkNodeName(0, 0)})

	devices := sliceDevices(outputs, nil, graph)
	if len(devices) != 1 || devices[0].Name != testAnalogName {
		t.Fatalf("devices = %+v, want the card's output alone", devices)
	}
}

func TestSameDevicesIgnoresTheServersTimestamp(t *testing.T) {
	published := []SliceDevice{{
		Name:   testSinkName,
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute", TimeAdded: "2026-08-16T12:00:00Z"}},
	}}
	current := []SliceDevice{{
		Name:   testSinkName,
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute"}},
	}}
	if !sameDevices(published, current) {
		t.Fatal("sameDevices = false for a stored timestamp, want true")
	}
}

func TestSameDevicesSeesRealChanges(t *testing.T) {
	tainted := []SliceDevice{{
		Name:   testSinkName,
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute", TimeAdded: "2026-08-16T12:00:00Z"}},
	}}
	clear := []SliceDevice{{Name: testSinkName}}
	if sameDevices(tainted, clear) {
		t.Fatal("sameDevices = true after a taint was cleared, want false")
	}
	renamed := []SliceDevice{{Name: "liken-1-pci-0000-00-1f-3-hdmi-3"}}
	if sameDevices(clear, renamed) {
		t.Fatal("sameDevices = true for a different output, want false")
	}
	relabeled := []SliceDevice{{
		Name:       testSinkName,
		Attributes: map[string]DeviceAttribute{nodeNameAttribute: AttrString("alsa_output.test")},
	}}
	if sameDevices(clear, relabeled) {
		t.Fatal("sameDevices = true for a new attribute, want false")
	}
}

// stringAttribute fails the test when the device has no string
// attribute of that name, where a plain read would panic on nil.
func stringAttribute(t *testing.T, device SliceDevice, name string) string {
	t.Helper()
	attribute, published := device.Attributes[name]
	if !published || attribute.String == nil {
		t.Fatalf("%s publishes no string attribute %q", device.Name, name)
	}
	return *attribute.String
}

func intAttribute(t *testing.T, device SliceDevice, name string) int64 {
	t.Helper()
	attribute, published := device.Attributes[name]
	if !published || attribute.Int == nil {
		t.Fatalf("%s publishes no number attribute %q", device.Name, name)
	}
	return *attribute.Int
}
