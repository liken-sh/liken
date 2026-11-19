package main

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"
)

// pw-dump prints a property that looks like a number without quotes,
// so the fixture has card and device values in both forms.
func TestParseGraph(t *testing.T) {
	graph, err := parseGraph(fixture(t, "pw-dump.json"))
	if err != nil {
		t.Fatalf("parsing pw-dump.json: %v", err)
	}
	sinks := sinkNames(graph)
	if len(sinks) != 2 {
		t.Fatalf("sinks = %v, want two", sinks)
	}
	if len(graph.Speakers) != 0 {
		t.Errorf("a graph with no radio has the speakers %v, want none", graph.Speakers)
	}

	// The HDMI sink gives its card and device as numbers. Two sinks
	// claim that PCM device, and the name that sorts first wins.
	hdmi := sinks[pcmAddress{Card: 0, PCM: 3}]
	if want := "alsa_output.pci-0000_00_1f.3.hdmi-stereo"; hdmi != want {
		t.Errorf("the HDMI sink = %q, want %q", hdmi, want)
	}

	// The analog sink gives its card and device as strings, under the
	// keys that the ALSA plugin writes.
	analog := sinks[pcmAddress{Card: 0, PCM: 0}]
	if want := "alsa_output.pci-0000_00_1f.3.analog-stereo"; analog != want {
		t.Errorf("the analog sink = %q, want %q", analog, want)
	}
}

// pw-dump prints the cookie in the info block of the Core object. A
// document with no Core object has cookie zero.
func TestParseGraphReadsThePipeWireCookie(t *testing.T) {
	cases := []struct {
		fixture string
		cookie  uint32
	}{
		{"pw-dump-bluez.json", 1067734355},
		{"pw-dump.json", 0},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			graph, err := parseGraph(fixture(t, c.fixture))
			if err != nil {
				t.Fatalf("parsing %s: %v", c.fixture, err)
			}
			if graph.Cookie != c.cookie {
				t.Errorf("cookie = %d, want %d", graph.Cookie, c.cookie)
			}
		})
	}
}

// A declared node has no alsa.card or alsa.device, so the operator's
// own liken.audio.card and liken.audio.pcm properties map it to its PCM
// device.
func TestParseGraphMapsTheNodesTheOperatorDeclares(t *testing.T) {
	graph, err := parseGraph(fixture(t, "pw-dump-declared.json"))
	if err != nil {
		t.Fatalf("parsing pw-dump-declared.json: %v", err)
	}
	sinks := sinkNames(graph)
	if len(sinks) != 2 {
		t.Fatalf("sinks = %v, want two", sinks)
	}
	if got := sinks[pcmAddress{Card: 0, PCM: 3}]; got != sinkNodeName(0, 3) {
		t.Errorf("the HDMI sink = %q, want %q", got, sinkNodeName(0, 3))
	}
	if got := sinks[pcmAddress{Card: 0, PCM: 0}]; got != sinkNodeName(0, 0) {
		t.Errorf("the analog sink = %q, want %q", got, sinkNodeName(0, 0))
	}
}

// The capture node of a PCM device has the same card and PCM
// properties as its playback node, so only the direction separates
// them, and sinkNames must not return the capture node.
func TestParseGraphMapsACaptureNode(t *testing.T) {
	graph, err := parseGraph(fixture(t, "pw-dump-declared.json"))
	if err != nil {
		t.Fatalf("parsing pw-dump-declared.json: %v", err)
	}
	analog := pcmAddress{Card: 0, PCM: 0}
	source := graph.Nodes[nodeAddress{pcmAddress: analog, Direction: directionSource}]
	if source.Name != "liken.audio.card0-pcm0c" {
		t.Errorf("the capture node = %q, want liken.audio.card0-pcm0c", source.Name)
	}
	if source.ID != 50 {
		t.Errorf("the capture node's id = %d, want 50", source.ID)
	}
	sink := graph.Nodes[nodeAddress{pcmAddress: analog, Direction: directionSink}]
	if sink.Name != sinkNodeName(0, 0) {
		t.Errorf("the playback node on the same PCM device = %q, want %q", sink.Name, sinkNodeName(0, 0))
	}
	if got, has := sinkNames(graph)[analog]; got == source.Name || !has {
		t.Errorf("the playback index has %q, present %t, for the analog PCM, want the playback node and not the capture node %q", got, has, source.Name)
	}
}

// A running declared node prints its levels, mute, and negotiated
// format. A suspended one prints an empty list for the levels and the
// format.
func TestParseGraphReadsTheFactsAStatusReports(t *testing.T) {
	graph, err := parseGraph(fixture(t, "pw-dump-declared.json"))
	if err != nil {
		t.Fatalf("parsing pw-dump-declared.json: %v", err)
	}
	running := graph.Nodes[nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 3}, Direction: directionSink}]
	if !running.Mute {
		t.Error("the muted node reads as unmuted, want muted")
	}
	if want := []float64{0.5, 0.5}; !reflect.DeepEqual(running.Volumes, want) {
		t.Errorf("levels = %v, want %v", running.Volumes, want)
	}
	want := pwFormat{Rate: 48000, Channels: 2, Positions: []string{"FL", "FR"}}
	if !reflect.DeepEqual(running.Format, want) {
		t.Errorf("format = %+v, want %+v", running.Format, want)
	}

	suspended := graph.Nodes[nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink}]
	if len(suspended.Volumes) != 0 {
		t.Errorf("a suspended node reports the levels %v, want none", suspended.Volumes)
	}
	if !reflect.DeepEqual(suspended.Format, pwFormat{}) {
		t.Errorf("a suspended node reports the format %+v, want none", suspended.Format)
	}
	if suspended.Mute {
		t.Error("a suspended node reads as muted, want unmuted, because the ALSA Props block carries no mute")
	}
}

// A suspended node can still print the channelVolumes it last ran at,
// so the node's state, not the list, shows that the level is stale.
func TestParseGraphReadsWhetherANodeIsSuspended(t *testing.T) {
	node := func(id int, pcm, state string) string {
		return `{"id": ` + strconv.Itoa(id) + `, "type": "PipeWire:Interface:Node", "info": {"state": "` + state + `",
			"props": {"media.class": "Audio/Sink", "node.name": "liken.audio.card0-pcm` + pcm + `",
				"liken.audio.card": "0", "liken.audio.pcm": "` + pcm + `"},
			"params": {"Props": [{"mute": false, "channelVolumes": [1.0, 1.0]}]}}}`
	}
	graph, err := parseGraph([]byte("[" + node(40, "3", "suspended") + "," + node(41, "7", "running") + "]"))
	if err != nil {
		t.Fatalf("parsing the two-node document: %v", err)
	}
	suspended := graph.Nodes[nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 3}, Direction: directionSink}]
	if !suspended.Suspended || len(suspended.Volumes) != 2 {
		t.Errorf("the suspended node reads as %+v, want Suspended with two levels", suspended)
	}
	if running := graph.Nodes[nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 7}, Direction: directionSink}]; running.Suspended {
		t.Errorf("the running node reads as %+v, want it not suspended", running)
	}
}

func TestParseGraphReportsBrokenOutput(t *testing.T) {
	if _, err := parseGraph([]byte("this is not JSON")); err == nil {
		t.Fatal("parseGraph of a document that is not JSON returned no error, want one")
	}
}

func TestAFailedGraphReadIncludesTheStderrOfPWDump(t *testing.T) {
	stubProbeDump(t, "1")
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	_, err := readGraph(ctx)
	want := "running pw-dump: exit status 1: remote error: Connection refused"
	if err == nil || err.Error() != want {
		t.Errorf("readGraph returned %v, want %q", err, want)
	}
}

// The peer of the Bluetooth fixtures as the key of pwGraph.Speakers, as
// the device name that the operator publishes, and as the node name
// that WirePlumber builds.
const (
	testSpeakerAddress = "a0:ab:51:33:b7:12"
	testSpeakerName    = "a0-ab-51-33-b7-12"
	testSpeakerNode    = "bluez_output.A0_AB_51_33_B7_12.1"
)

// outputGraph is a graph with the card's sinks and no Bluetooth
// speaker.
func outputGraph(sinks map[pcmAddress]string) pwGraph {
	nodes := map[nodeAddress]pwNode{}
	for address, name := range sinks {
		nodes[nodeAddress{pcmAddress: address, Direction: directionSink}] = pwNode{Name: name}
	}
	return pwGraph{Nodes: nodes, Speakers: map[string]bluezSink{}}
}

// sinkNames returns the name of the playback node of each PCM device.
func sinkNames(graph pwGraph) map[pcmAddress]string {
	names := map[pcmAddress]string{}
	for address, node := range graph.Nodes {
		if address.Direction == directionSink {
			names[address.pcmAddress] = node.Name
		}
	}
	return names
}

func speakerGraph(speakers map[string]bluezSink) pwGraph {
	return pwGraph{Nodes: map[nodeAddress]pwNode{}, Speakers: speakers}
}

// staticGraph returns a graph read that always returns graph, so a
// test can run a pass without PipeWire.
func staticGraph(graph pwGraph) func(context.Context) (pwGraph, error) {
	return func(context.Context) (pwGraph, error) { return graph, nil }
}

// The fixture is written by hand from the property names that the
// bluez5 SPA plugin of pipewire 1.4.2 sets and the node name that
// WirePlumber 0.5.8 builds from them. It is not a capture from a
// machine with a speaker.
func TestParseGraphReadsABluetoothSink(t *testing.T) {
	graph, err := parseGraph(fixture(t, "pw-dump-bluez.json"))
	if err != nil {
		t.Fatalf("parsing pw-dump-bluez.json: %v", err)
	}
	if len(graph.Speakers) != 1 {
		t.Fatalf("speakers = %v, want one", graph.Speakers)
	}
	// The key is the address in lowercase colon form, whatever case
	// BlueZ printed it in.
	sink := graph.Speakers[testSpeakerAddress]
	if sink.Node != testSpeakerNode {
		t.Errorf("node = %q, want %q", sink.Node, testSpeakerNode)
	}
	if sink.Codec != "sbc" {
		t.Errorf("codec = %q, want sbc", sink.Codec)
	}
	// A level write on the node names it by NodeID and reads the channel
	// count from Volumes.
	if sink.NodeID != 63 {
		t.Errorf("node id = %d, want 63", sink.NodeID)
	}
	if want := []float64{0.064, 0.064}; !reflect.DeepEqual(sink.Volumes, want) {
		t.Errorf("volumes = %v, want %v", sink.Volumes, want)
	}
	// The codecs come from the bluez5 Device object, not from the node,
	// because the write that switches the codec names the device.
	if sink.Device != 62 {
		t.Errorf("device id = %d, want 62", sink.Device)
	}
	codecs := []bluezCodec{
		{ID: 1, Name: "sbc"},
		{ID: 6, Name: "aptx"},
		{ID: 2, Name: "sbc_xq"},
		{ID: 9, Name: "aptx_ll"},
	}
	if !reflect.DeepEqual(sink.Codecs, codecs) {
		t.Errorf("codecs = %+v, want %+v", sink.Codecs, codecs)
	}
	// The speaker's own level is on the device's Output route. The
	// Input route is the headset microphone, which this pod does not
	// open. A volumeStep shows that the transport has a volume of its
	// own.
	route := sink.Route
	if route == nil {
		t.Fatal("the device's output route is nil, want the Output route")
	}
	if route.Index != 1 || route.Device != 0 {
		t.Errorf("route = index %d device %d, want index 1 device 0", route.Index, route.Device)
	}
	if !route.Mute {
		t.Error("the muted route reads as unmuted, want muted")
	}
	if want := []float64{0.25, 0.25}; !reflect.DeepEqual(route.Volumes, want) {
		t.Errorf("route levels = %v, want %v", route.Volumes, want)
	}
	if !route.AbsoluteVolume {
		t.Error("a route with a volumeStep reports no absolute volume, want AbsoluteVolume")
	}
	if want := (pwFormat{Rate: 44100, Channels: 2, Positions: []string{"FL", "FR"}}); !reflect.DeepEqual(sink.Format, want) {
		t.Errorf("format = %+v, want %+v", sink.Format, want)
	}

	// The card's sink is in the same document, and the Bluetooth node
	// has no ALSA address that could be mistaken for it.
	sinks := sinkNames(graph)
	if got := sinks[pcmAddress{Card: 0, PCM: 0}]; got != sinkNodeName(0, 0) {
		t.Errorf("the analog sink = %q, want %q", got, sinkNodeName(0, 0))
	}
	if len(sinks) != 1 {
		t.Errorf("outputs = %v, want one", sinks)
	}
}

// testSinks has a sink node for each output of testOutputs.
func testOutputs() []alsaEndpoint {
	return []alsaEndpoint{
		{Card: 0, PCM: 0, DeviceName: "liken-1-pci-0000-00-1f-3-alc236-analog"},
		{Card: 0, PCM: 3, HDMI: true, Monitor: true, DeviceName: testSinkName},
	}
}

func testSinks() map[pcmAddress]string {
	return map[pcmAddress]string{
		{Card: 0, PCM: 0}: sinkNodeName(0, 0),
		{Card: 0, PCM: 3}: sinkNodeName(0, 3),
	}
}

// PipeWire creates the declared nodes while it loads its
// configuration, so the first graph read normally finds them all.
func TestWaitForNodesReturnsAsSoonAsEveryOutputHasASink(t *testing.T) {
	reads := 0
	read := func(context.Context) (pwGraph, error) {
		reads++
		return outputGraph(testSinks()), nil
	}

	start := time.Now()
	waitForNodes(context.Background(), read, testOutputs(), time.Minute)

	if reads != 1 {
		t.Errorf("read the graph %d times, want one", reads)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("waited %s for nodes that were already there, want no wait", elapsed)
	}
}

// An output whose node PipeWire could not create publishes with the
// no-sink taint, so waitForNodes stops at the timeout. A longer wait
// would keep the card's working outputs unpublished.
func TestWaitForNodesGivesUpAtTheTimeout(t *testing.T) {
	read := func(context.Context) (pwGraph, error) {
		return outputGraph(map[pcmAddress]string{{Card: 0, PCM: 0}: sinkNodeName(0, 0)}), nil
	}

	start := time.Now()
	waitForNodes(context.Background(), read, testOutputs(), 50*time.Millisecond)

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("waited %s with a %s timeout, want under 5s", elapsed, 50*time.Millisecond)
	}
}

// A graph read that keeps failing ends at the timeout too, and does
// not hang.
func TestWaitForNodesGivesUpWhenTheGraphNeverReads(t *testing.T) {
	read := func(context.Context) (pwGraph, error) {
		return pwGraph{}, errors.New("running pw-dump: no such file or directory")
	}

	start := time.Now()
	waitForNodes(context.Background(), read, testOutputs(), 50*time.Millisecond)

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("waited %s with a %s timeout, want under 5s", elapsed, 50*time.Millisecond)
	}
}

func TestWaitForNodesStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	read := func(context.Context) (pwGraph, error) {
		return pwGraph{}, errors.New("running pw-dump: no such file or directory")
	}

	start := time.Now()
	waitForNodes(ctx, read, testOutputs(), time.Hour)

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("a cancelled context waited %s, want under 5s", elapsed)
	}
}

func TestMissingNodesNamesTheOutputsWithNoSink(t *testing.T) {
	cases := []struct {
		name  string
		sinks map[pcmAddress]string
		want  []string
	}{
		{name: "every output has one", sinks: testSinks()},
		{
			name:  "none of them do",
			sinks: map[pcmAddress]string{},
			want:  []string{"liken-1-pci-0000-00-1f-3-alc236-analog", testSinkName},
		},
		{
			name:  "one of them does",
			sinks: map[pcmAddress]string{{Card: 0, PCM: 3}: sinkNodeName(0, 3)},
			want:  []string{"liken-1-pci-0000-00-1f-3-alc236-analog"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := missingNodes(testOutputs(), outputGraph(c.sinks))
			if len(got) != len(c.want) {
				t.Fatalf("missing = %v, want %v", got, c.want)
			}
			for i, name := range c.want {
				if got[i] != name {
					t.Errorf("missing[%d] = %q, want %q", i, got[i], name)
				}
			}
		})
	}
}

// A link has the ids of the two nodes it joins, and the graph records
// both, so a stream into a sink and a tap of its monitor both mark the
// sink.
func TestParseGraphReadsWhichNodesALinkTouches(t *testing.T) {
	graph, err := parseGraph([]byte(`[
		{"id": 60, "type": "PipeWire:Interface:Link",
		 "info": {"output-node-id": 51, "output-port-id": 50, "input-node-id": 59, "input-port-id": 61, "state": "active", "props": {}}},
		{"id": 61, "type": "PipeWire:Interface:Link", "info": null}
	]`))
	if err != nil {
		t.Fatalf("parsing the two-link document: %v", err)
	}
	if len(graph.Linked) != 2 || !graph.Linked[51] || !graph.Linked[59] {
		t.Errorf("linked = %v, want 51 and 59", graph.Linked)
	}
}
