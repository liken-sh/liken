package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// The document in testdata is the shape pw-dump prints: an array of
// objects, each with its interface type and its properties. pw-dump
// leaves a property whose value reads as a number unquoted, even
// though every value in PipeWire's property list is a string, so the
// fixture holds both forms.
func TestParseGraph(t *testing.T) {
	graph, err := parseGraph(fixture(t, "pw-dump.json"))
	if err != nil {
		t.Fatal(err)
	}
	sinks := sinkNames(graph)
	if len(sinks) != 2 {
		t.Fatalf("sinks = %v, want two", sinks)
	}
	if len(graph.Speakers) != 0 {
		t.Errorf("a graph with no radio in it holds %v", graph.Speakers)
	}

	// The HDMI sink names its card and device as numbers, and two
	// sinks name that PCM device. The first name in alphabetical order
	// is the one that publishes, so a profile change that leaves the
	// pair for a moment does not write the slice twice.
	hdmi := sinks[pcmAddress{Card: 0, PCM: 3}]
	if hdmi != "alsa_output.pci-0000_00_1f.3.hdmi-stereo" {
		t.Errorf("the HDMI sink = %q", hdmi)
	}

	// The analog sink names its card and device as strings, under the
	// keys the ALSA plugin writes.
	analog := sinks[pcmAddress{Card: 0, PCM: 0}]
	if analog != "alsa_output.pci-0000_00_1f.3.analog-stereo" {
		t.Errorf("the analog sink = %q", analog)
	}
}

// The nodes this operator declares have no alsa.card and no
// alsa.device, because those two come from the udev device that
// WirePlumber's monitor builds and this graph has no monitor in it.
// The operator's own two properties map a declared node back to its
// PCM device.
func TestParseGraphMapsTheNodesTheOperatorDeclares(t *testing.T) {
	graph, err := parseGraph(fixture(t, "pw-dump-declared.json"))
	if err != nil {
		t.Fatal(err)
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

// A capture PCM declares its own node under the
// same two properties as the playback one, so the direction is what
// separates them, and that a source never enters the playback index
// the delivery reads.
func TestParseGraphMapsACaptureNode(t *testing.T) {
	graph, err := parseGraph(fixture(t, "pw-dump-declared.json"))
	if err != nil {
		t.Fatal(err)
	}
	analog := pcmAddress{Card: 0, PCM: 0}
	source := graph.Nodes[nodeAddress{pcmAddress: analog, Direction: directionSource}]
	if source.Name != "liken.audio.card0-pcm0c" {
		t.Errorf("the capture node = %q", source.Name)
	}
	if source.ID != 50 {
		t.Errorf("the capture node's id = %d, want 50", source.ID)
	}
	sink := graph.Nodes[nodeAddress{pcmAddress: analog, Direction: directionSink}]
	if sink.Name != sinkNodeName(0, 0) {
		t.Errorf("the playback node on the same PCM device = %q", sink.Name)
	}
	if got, has := sinkNames(graph)[analog]; got == source.Name || !has {
		t.Errorf("the playback index holds %q for the analog PCM", got)
	}
}

// The three facts the endpoint's status reads off a
// node, and the two shapes a declared node prints them in: a running
// node carries its levels and its negotiated format, and a suspended
// one carries an empty list for each.
func TestParseGraphReadsTheFactsAStatusReports(t *testing.T) {
	graph, err := parseGraph(fixture(t, "pw-dump-declared.json"))
	if err != nil {
		t.Fatal(err)
	}
	running := graph.Nodes[nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 3}, Direction: directionSink}]
	if !running.Mute {
		t.Error("the muted node reports itself unmuted")
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
		t.Errorf("a suspended node reports the levels %v", suspended.Volumes)
	}
	if !reflect.DeepEqual(suspended.Format, pwFormat{}) {
		t.Errorf("a suspended node reports the format %+v", suspended.Format)
	}
	if suspended.Mute {
		t.Error("a suspended node's mute was read from the ALSA Props block")
	}
}

func TestParseGraphReportsBrokenOutput(t *testing.T) {
	if _, err := parseGraph([]byte("this is not JSON")); err == nil {
		t.Fatal("a document that is not JSON did not report an error")
	}
}

// The peer this file's Bluetooth fixtures name, in the three
// forms the operator uses: the key it maps on, the device name it
// publishes, and the node name WirePlumber builds.
const (
	testSpeakerAddress = "a0:ab:51:33:b7:12"
	testSpeakerName    = "a0-ab-51-33-b7-12"
	testSpeakerNode    = "bluez_output.A0_AB_51_33_B7_12.1"
)

// outputGraph is a graph with the card's sinks in it and no
// Bluetooth speaker, which is what a machine with no radio holds.
func outputGraph(sinks map[pcmAddress]string) pwGraph {
	nodes := map[nodeAddress]pwNode{}
	for address, name := range sinks {
		nodes[nodeAddress{pcmAddress: address, Direction: directionSink}] = pwNode{Name: name}
	}
	return pwGraph{Nodes: nodes, Speakers: map[string]bluezSink{}}
}

// sinkNames reads back the playback node of each PCM device, which is
// what the tests below assert a parse produced.
func sinkNames(graph pwGraph) map[pcmAddress]string {
	names := map[pcmAddress]string{}
	for address, node := range graph.Nodes {
		if address.Direction == directionSink {
			names[address.pcmAddress] = node.Name
		}
	}
	return names
}

// speakerGraph is the other half: a graph with Bluetooth sinks in
// it and no card.
func speakerGraph(speakers map[string]bluezSink) pwGraph {
	return pwGraph{Nodes: map[nodeAddress]pwNode{}, Speakers: speakers}
}

// staticGraph reads back one fixed graph, so a test drives a pass
// with no PipeWire behind it.
func staticGraph(graph pwGraph) func(context.Context) (pwGraph, error) {
	return func(context.Context) (pwGraph, error) { return graph, nil }
}

// The fixture is assembled from the property names the bluez5 SPA
// plugin sets in pipewire 1.4.2 and the node name WirePlumber 0.5.8
// builds from them. It was not captured from a machine with a
// speaker on it; the drill supplies that capture.
func TestParseGraphReadsABluetoothSink(t *testing.T) {
	graph, err := parseGraph(fixture(t, "pw-dump-bluez.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Speakers) != 1 {
		t.Fatalf("speakers = %v, want one", graph.Speakers)
	}
	// The address is the key, in the lowercase colon form, whatever
	// case BlueZ printed it in.
	sink := graph.Speakers[testSpeakerAddress]
	if sink.Node != testSpeakerNode {
		t.Errorf("node = %q, want %q", sink.Node, testSpeakerNode)
	}
	if sink.Codec != "sbc" {
		t.Errorf("codec = %q, want sbc", sink.Codec)
	}
	// The node's own object id and its channel volumes, which a codec
	// switch needs: the switch destroys this node and builds another
	// one, born at PipeWire's own level rather than this one.
	if sink.NodeID != 63 {
		t.Errorf("node id = %d, want 63", sink.NodeID)
	}
	if want := []float64{0.064, 0.064}; !reflect.DeepEqual(sink.Volumes, want) {
		t.Errorf("volumes = %v, want %v", sink.Volumes, want)
	}
	// The codec set comes from the bluez5 Device object, not from the
	// node, and the write that switches the codec names that object.
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
	// The speaker's level lives on the device's
	// Route, that the Output route is the speaker's and the Input one
	// is the headset microphone this pod does not open, and that
	// volumeStep is the graph saying the transport reports a volume.
	route := sink.Route
	if route == nil {
		t.Fatal("the device published no output route")
	}
	if route.Index != 1 || route.Device != 0 {
		t.Errorf("route = index %d device %d, want index 1 device 0", route.Index, route.Device)
	}
	if !route.Mute {
		t.Error("the muted route reports itself unmuted")
	}
	if want := []float64{0.25, 0.25}; !reflect.DeepEqual(route.Volumes, want) {
		t.Errorf("route levels = %v, want %v", route.Volumes, want)
	}
	if !route.AbsoluteVolume {
		t.Error("a route with a volumeStep reports no absolute volume")
	}
	// The speaker's own node carries the format the transport
	// negotiated.
	if want := (pwFormat{Rate: 44100, Channels: 2, Positions: []string{"FL", "FR"}}); !reflect.DeepEqual(sink.Format, want) {
		t.Errorf("format = %+v, want %+v", sink.Format, want)
	}

	// The card's own sink is in the same document, and a Bluetooth
	// node has no ALSA address for it to be mistaken for.
	sinks := sinkNames(graph)
	if got := sinks[pcmAddress{Card: 0, PCM: 0}]; got != sinkNodeName(0, 0) {
		t.Errorf("the analog sink = %q, want %q", got, sinkNodeName(0, 0))
	}
	if len(sinks) != 1 {
		t.Errorf("outputs = %v, want one", sinks)
	}
}

// The declared outputs and a graph that holds a sink for each one.
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
// configuration, so the usual case is one graph read and no wait.
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
		t.Errorf("waited %s for nodes that were already there", elapsed)
	}
}

// An output whose node PipeWire could not create publishes with the
// no-sink taint. Waiting past the timeout would leave the card's
// working outputs unpublished over the one that is broken.
func TestWaitForNodesGivesUpAtTheTimeout(t *testing.T) {
	read := func(context.Context) (pwGraph, error) {
		return outputGraph(map[pcmAddress]string{{Card: 0, PCM: 0}: sinkNodeName(0, 0)}), nil
	}

	start := time.Now()
	waitForNodes(context.Background(), read, testOutputs(), 50*time.Millisecond)

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("waited %s past a %s timeout", elapsed, 50*time.Millisecond)
	}
}

// A graph that never reads is the same bounded wait, and not a hang.
func TestWaitForNodesGivesUpWhenTheGraphNeverReads(t *testing.T) {
	read := func(context.Context) (pwGraph, error) {
		return pwGraph{}, errors.New("running pw-dump: no such file or directory")
	}

	start := time.Now()
	waitForNodes(context.Background(), read, testOutputs(), 50*time.Millisecond)

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("waited %s past a %s timeout", elapsed, 50*time.Millisecond)
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
		t.Errorf("a cancelled context waited %s", elapsed)
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
