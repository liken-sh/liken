package main

// These tests cover two decisions: what one output publishes as a
// device, and when the operator writes the slice at all. The second
// set runs against a small API server that holds one ResourceSlice
// and records the requests it received.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func testClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	// The credentials are empty, so the client sends no bearer token
	// and reads no file from disk.
	return NewClient(server.URL, server.Client(), "")
}

func testOwner() OwnerReference {
	return OwnerReference{APIVersion: "v1", Kind: "Node", Name: "liken-1", UID: "abc-123"}
}

func hdmiOutput(t *testing.T, card, pcm int) alsaOutput {
	t.Helper()
	block, err := parseELD(fixture(t, "eld-hdmi-lg-ultrawide.bin"))
	if err != nil {
		t.Fatal(err)
	}
	return alsaOutput{Card: card, PCM: pcm, HDMI: true, Monitor: true, ELD: block}
}

func TestSliceDevicesPublishesEachOutput(t *testing.T) {
	outputs := []alsaOutput{
		{Card: 0, PCM: 0},
		hdmiOutput(t, 0, 3),
	}
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
	if devices[0].Name != "card0-pcm0" || devices[1].Name != "card0-pcm3" {
		t.Fatalf("names = %q, %q", devices[0].Name, devices[1].Name)
	}

	analog := devices[0]
	if got := *analog.Attributes["connectionType"].String; got != "analog" {
		t.Errorf("the analog output's connection type = %q", got)
	}
	// A selector cannot read a device's name, so the name is an
	// attribute as well, and its two halves are numbers.
	if got := stringAttribute(t, analog, "output"); got != "card0-pcm0" {
		t.Errorf("output = %q", got)
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
		"output":         "card0-pcm3",
		"connectionType": "hdmi",
		"manufacturer":   "GSM",
		"product":        "5b09",
		"monitorName":    "LG ULTRAWIDE",
		"speakers":       "FL/FR",
		"lpcmBitDepths":  "16 20 24",
		"sinkName":       "alsa_output.pci-0000_00_1f.3.hdmi-stereo",
		PairingAttribute: "gsm-5b09-lg-ultrawide",
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
	if len(hdmi.Taints) != 0 {
		t.Errorf("the HDMI output has taints: %+v", hdmi.Taints)
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
		output alsaOutput
		sink   bool
		taints []DeviceTaint
	}{
		{
			name:   "an HDMI output with a monitor and a sink",
			output: alsaOutput{Card: 0, PCM: 3, HDMI: true, Monitor: true},
			sink:   true,
		},
		{
			name:   "the analog jack with a sink",
			output: alsaOutput{Card: 0, PCM: 0},
			sink:   true,
		},
		{
			name:   "an unplugged monitor whose sink node is still there",
			output: alsaOutput{Card: 0, PCM: 3, HDMI: true},
			sink:   true,
			taints: []DeviceTaint{
				{Key: disconnectedTaint, Effect: "NoExecute"},
				{Key: noMonitorTaint, Effect: "NoSchedule"},
			},
		},
		{
			name:   "an output with a monitor and no sink node",
			output: alsaOutput{Card: 0, PCM: 3, HDMI: true, Monitor: true},
			taints: []DeviceTaint{
				{Key: disconnectedTaint, Effect: "NoExecute"},
				{Key: noSinkTaint, Effect: "NoSchedule"},
			},
		},
		{
			name:   "the analog jack with no sink node",
			output: alsaOutput{Card: 0, PCM: 0},
			taints: []DeviceTaint{
				{Key: disconnectedTaint, Effect: "NoExecute"},
				{Key: noSinkTaint, Effect: "NoSchedule"},
			},
		},
		{
			name:   "an unplugged monitor with no sink node either",
			output: alsaOutput{Card: 0, PCM: 3, HDMI: true},
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

			devices := sliceDevices([]alsaOutput{c.output}, nil, outputGraph(sinks))
			if got := devices[0].Taints; !reflect.DeepEqual(got, c.taints) {
				t.Fatalf("taints = %+v, want %+v", got, c.taints)
			}
		})
	}
}

// The sink name and the no-sink taint come from one fact, so a device
// that publishes the name of its sink must never also say it has none.
// Those two would state opposite facts: the name says a claim would
// play, and the taint says a claim would reach nothing.
func TestSliceDevicesNeverPublishesASinkNameAndTheNoSinkTaint(t *testing.T) {
	outputs := []alsaOutput{
		{Card: 0, PCM: 0},
		{Card: 0, PCM: 3, HDMI: true, Monitor: true},
		{Card: 0, PCM: 8, HDMI: true},
		{Card: 0, PCM: 9, HDMI: true},
	}
	// Every declared node is in the graph, which is what PipeWire holds
	// once it has loaded the operator's drop-in, whether a monitor is on
	// the port or not.
	sinks := map[pcmAddress]string{}
	for _, output := range outputs {
		sinks[pcmAddress{Card: output.Card, PCM: output.PCM}] = sinkNodeName(output.Card, output.PCM)
	}

	for _, device := range sliceDevices(outputs, nil, outputGraph(sinks)) {
		name, named := device.Attributes["sinkName"]
		if !named {
			t.Errorf("%s published no sink name for a node in the graph", device.Name)
			continue
		}
		for _, taint := range device.Taints {
			if taint.Key == noSinkTaint {
				t.Errorf("%s published sinkName %q and the no-sink taint",
					device.Name, *name.String)
			}
		}
	}
}

func TestSliceDevicesOmitsASinkNameTooLongToPublish(t *testing.T) {
	// A truncated name would not name the sink, and the delivery reads the
	// current name from PipeWire rather than from the slice.
	long := ""
	for range maxAttributeLength + 1 {
		long += "x"
	}
	devices := sliceDevices(
		[]alsaOutput{{Card: 0, PCM: 0}},
		nil,
		outputGraph(map[pcmAddress]string{{Card: 0, PCM: 0}: long}),
	)
	if _, published := devices[0].Attributes["sinkName"]; published {
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
	outputs := []alsaOutput{{Card: 0, PCM: 0}, hdmiOutput(t, 0, 3)}
	graph := pwGraph{
		Outputs: map[pcmAddress]string{
			{Card: 0, PCM: 0}: sinkNodeName(0, 0),
			{Card: 0, PCM: 3}: sinkNodeName(0, 3),
		},
		Speakers: map[string]bluezSink{
			testSpeakerAddress: {Node: testSpeakerNode, Codec: "sbc"},
		},
	}

	devices := sliceDevices(outputs, testSpeakers(), graph)
	if len(devices) != 3 {
		t.Fatalf("devices = %d, want three", len(devices))
	}
	// The list is sorted by name, so the speaker's dashed MAC sorts
	// ahead of the card's outputs.
	names := []string{testSpeakerName, "card0-pcm0", "card0-pcm3"}
	for i, want := range names {
		if devices[i].Name != want {
			t.Fatalf("devices[%d] = %q, want %q", i, devices[i].Name, want)
		}
	}

	// The card's outputs publish exactly what they published without a
	// radio on the machine.
	if got := sliceDevices(outputs, nil, outputGraph(graph.Outputs)); !sameDevices(got, devices[1:]) {
		t.Errorf("the card's outputs changed beside a speaker:\n%+v\n%+v", got, devices[1:])
	}

	speaker := devices[0]
	attributes := map[string]string{
		"output":         testSpeakerName,
		"address":        "A0:AB:51:33:B7:12",
		"connectionType": "bluetooth",
		"name":           "Kitchen Speaker",
		"codec":          "sbc",
		"sinkName":       testSpeakerNode,
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
				sinks[testSpeakerAddress] = bluezSink{Node: testSpeakerNode, Codec: "sbc"}
			}

			devices := speakerDevices(map[string]speaker{testSpeakerAddress: c.speaker}, sinks)
			if got := devices[0].Taints; !reflect.DeepEqual(got, c.taints) {
				t.Fatalf("taints = %+v, want %+v", got, c.taints)
			}
			// The sink name and the codec are what the graph holds, so a
			// speaker with no node publishes neither.
			for _, name := range []string{"sinkName", "codec"} {
				if _, published := devices[0].Attributes[name]; published != c.sink {
					t.Errorf("%s published = %v, want %v", name, published, c.sink)
				}
			}
		})
	}
}

// Unpairing is the one thing that removes a speaker from the slice.
// Any other removal would strand a claim that holds it: the
// allocation still names the device, and the kubelet retries its
// prepare against a device in no slice, with no bound.
func TestSliceDevicesDropsAnUnpairedSpeaker(t *testing.T) {
	outputs := []alsaOutput{{Card: 0, PCM: 0}}
	graph := outputGraph(map[pcmAddress]string{{Card: 0, PCM: 0}: sinkNodeName(0, 0)})

	devices := sliceDevices(outputs, nil, graph)
	if len(devices) != 1 || devices[0].Name != "card0-pcm0" {
		t.Fatalf("devices = %+v, want the card's output alone", devices)
	}
}

func TestSameDevicesIgnoresTheServersTimestamp(t *testing.T) {
	// The API server fills TimeAdded in on every taint it stores. A
	// comparison that read it would call every pass a change, and every
	// slice write wakes every DRA-pending pod in the cluster.
	published := []SliceDevice{{
		Name:   "card0-pcm3",
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute", TimeAdded: "2026-08-16T12:00:00Z"}},
	}}
	current := []SliceDevice{{
		Name:   "card0-pcm3",
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute"}},
	}}
	if !sameDevices(published, current) {
		t.Fatal("a stored timestamp counted as a change")
	}
}

func TestSameDevicesSeesRealChanges(t *testing.T) {
	tainted := []SliceDevice{{
		Name:   "card0-pcm3",
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute", TimeAdded: "2026-08-16T12:00:00Z"}},
	}}
	clear := []SliceDevice{{Name: "card0-pcm3"}}
	if sameDevices(tainted, clear) {
		t.Fatal("clearing a taint did not count as a change")
	}
	renamed := []SliceDevice{{Name: "card0-pcm7"}}
	if sameDevices(clear, renamed) {
		t.Fatal("a different output did not count as a change")
	}
	relabeled := []SliceDevice{{
		Name:       "card0-pcm3",
		Attributes: map[string]DeviceAttribute{"sinkName": AttrString("alsa_output.test")},
	}}
	if sameDevices(clear, relabeled) {
		t.Fatal("a new attribute did not count as a change")
	}
}

// slicePublishFixture is a small API server that holds at most one
// ResourceSlice. It records the requests it received.
type slicePublishFixture struct {
	existing *ResourceSlice
	requests []string
	created  *ResourceSlice
	updated  *ResourceSlice
}

func (f *slicePublishFixture) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		switch r.Method {
		case http.MethodGet:
			if f.existing == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(f.existing)
		case http.MethodPost:
			f.created = &ResourceSlice{}
			_ = json.NewDecoder(r.Body).Decode(f.created)
			_ = json.NewEncoder(w).Encode(f.created)
		case http.MethodPut:
			f.updated = &ResourceSlice{}
			_ = json.NewDecoder(r.Body).Decode(f.updated)
			_ = json.NewEncoder(w).Encode(f.updated)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
}

func testDevices() []SliceDevice {
	return []SliceDevice{{
		Name:       "card0-pcm3",
		Attributes: map[string]DeviceAttribute{"connectionType": AttrString("hdmi")},
	}}
}

func TestEnsureCreatesTheSliceOnFirstPublish(t *testing.T) {
	api := &slicePublishFixture{}
	client := testClient(t, api.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), testDevices()); err != nil {
		t.Fatal(err)
	}
	if api.created == nil {
		t.Fatal("no slice was created")
	}
	slice := api.created
	if slice.Metadata.Name != "liken-1-audio.liken.sh" {
		t.Errorf("name = %q", slice.Metadata.Name)
	}
	if slice.Spec.Driver != "audio.liken.sh" || slice.Spec.NodeName != "liken-1" {
		t.Errorf("spec = %+v", slice.Spec)
	}
	if slice.Spec.Pool.Name != "liken-1" || slice.Spec.Pool.Generation != 1 || slice.Spec.Pool.ResourceSliceCount != 1 {
		t.Errorf("pool = %+v", slice.Spec.Pool)
	}
	// The Node owns the slice, not the pod, so the slice outlives an
	// operator restart and leaves with the machine.
	if len(slice.Metadata.OwnerReferences) != 1 || slice.Metadata.OwnerReferences[0].UID != "abc-123" {
		t.Errorf("ownerReferences = %+v", slice.Metadata.OwnerReferences)
	}
}

func TestEnsureLeavesAnUnchangedSliceAlone(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	client := testClient(t, api.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), testDevices()); err != nil {
		t.Fatal(err)
	}
	if api.created != nil || api.updated != nil {
		t.Errorf("an unchanged inventory must not write: %v", api.requests)
	}
}

func TestEnsureReplacesAChangedSliceAndBumpsTheGeneration(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	client := testClient(t, api.handler(t))

	changed := testDevices()
	changed[0].Taints = []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute"}}
	if err := EnsureResourceSlice(client, "liken-1", testOwner(), changed); err != nil {
		t.Fatal(err)
	}
	if api.updated == nil {
		t.Fatal("a changed inventory did not write")
	}
	if got := api.updated.Spec.Pool.Generation; got != 4 {
		t.Errorf("generation = %d, want 4", got)
	}
	if len(api.updated.Spec.Devices[0].Taints) != 1 {
		t.Errorf("devices = %+v", api.updated.Spec.Devices)
	}
}

// An empty list is never a real state of a machine this operator runs
// on, because the pod holds an exclusive claim on a card and a card
// has playback PCM devices. Publishing one would retract devices that
// prepared claims still name.
func TestEnsureRefusesToPublishNothing(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	client := testClient(t, api.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), nil); err != ErrNoDevices {
		t.Fatalf("error = %v, want %v", err, ErrNoDevices)
	}
	if len(api.requests) != 0 {
		t.Errorf("an empty inventory reached the API server: %v", api.requests)
	}
}

func publishedSlice(devices []SliceDevice, generation int64) *ResourceSlice {
	return &ResourceSlice{
		Metadata: ResourceSliceMeta{Name: "liken-1-audio.liken.sh", ResourceVersion: "7"},
		Spec: ResourceSliceSpec{
			Driver:   DriverName,
			NodeName: "liken-1",
			Pool:     ResourcePool{Name: "liken-1", Generation: generation, ResourceSliceCount: 1},
			Devices:  devices,
		},
	}
}

// The next three tests read the line the publisher prints for each
// outcome. A slice that nobody rewrites and a slice that a stopped
// operator left behind hold the same resourceVersion and the same pool
// generation, so the log is the only place the two come apart.

func TestEnsureLogsTheSliceItCreated(t *testing.T) {
	capture := captureSliceLog(t)
	api := &slicePublishFixture{}
	client := testClient(t, api.handler(t))

	// PipeWire holds no node for the output, so the device publishes
	// tainted.
	if err := EnsureResourceSlice(client, "liken-1", testOwner(),
		sliceDevices([]alsaOutput{{Card: 0, PCM: 3}}, nil, pwGraph{})); err != nil {
		t.Fatal(err)
	}
	want := "slice: created generation 1, 1 device, 1 tainted: card0-pcm3 has " +
		disconnectedTaint + ", " + noSinkTaint
	if got := capture.only(t); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

func TestEnsureLogsTheSliceItWrote(t *testing.T) {
	capture := captureSliceLog(t)
	outputs := []alsaOutput{{Card: 0, PCM: 3}}
	playing := sliceDevices(outputs, nil, outputGraph(map[pcmAddress]string{{Card: 0, PCM: 3}: "alsa_output.test"}))
	api := &slicePublishFixture{existing: publishedSlice(playing, 3)}
	client := testClient(t, api.handler(t))

	// PipeWire lost the node. The device count does not move, so the
	// taints are the whole event, and they evict the pod that held the
	// claim.
	if err := EnsureResourceSlice(client, "liken-1", testOwner(), sliceDevices(outputs, nil, pwGraph{})); err != nil {
		t.Fatal(err)
	}
	want := "slice: wrote generation 4, 1 device, 1 tainted: card0-pcm3 gained " +
		disconnectedTaint + ", " + noSinkTaint
	if got := capture.only(t); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

func TestEnsureLogsThatNothingMoved(t *testing.T) {
	capture := captureSliceLog(t)
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	client := testClient(t, api.handler(t))

	if err := EnsureResourceSlice(client, "liken-1", testOwner(), testDevices()); err != nil {
		t.Fatal(err)
	}
	if api.updated != nil {
		t.Fatalf("an unchanged inventory wrote to the API: %v", api.requests)
	}
	want := "slice: unchanged at generation 3, 1 device, 0 tainted (1 pass)"
	if got := capture.only(t); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

func TestSliceName(t *testing.T) {
	// The driver name is the suffix, so liken's slice and this
	// operator's slice can both exist for one node.
	if got := sliceName("liken-1"); got != "liken-1-audio.liken.sh" {
		t.Fatalf("sliceName = %q", got)
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
