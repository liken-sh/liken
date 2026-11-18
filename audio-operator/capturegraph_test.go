package main

import (
	"os"
	"strings"
	"testing"
)

// drillStream is the node.name that the capture container gives a
// tap's stream. The fixtures contain it where a real dump has the name
// that pw-record was started with.
const drillStream = "audio-capture-725682cad0fd5870"

// readGraphFixture reads a graph from testdata/capture. A fixture has
// PWTARGETID in place of the link's output node ID and PWSTREAMNAME in
// place of the tap's stream name. The fake pw-dump in
// testdata/capture/bin replaces them for each request, so one graph
// serves a tap on any endpoint in it. This function replaces them with
// the DAC, node 46, and drillStream.
func readGraphFixture(t *testing.T, name string) []byte {
	t.Helper()
	document, err := os.ReadFile("testdata/capture/" + name)
	if err != nil {
		t.Fatalf("reading the graph fixture: %v", err)
	}
	document = []byte(strings.ReplaceAll(string(document), "PWTARGETID", "46"))
	return []byte(strings.ReplaceAll(string(document), "PWSTREAMNAME", drillStream))
}

func TestARunningNodeReportsTheFormatItNegotiated(t *testing.T) {
	format, err := resolveNode(readGraphFixture(t, "graph.json"),
		"liken-1-usb-0573-1573-a34004801402-usb-audio", directionSink)
	if err != nil {
		t.Fatalf("resolving the DAC: %v", err)
	}
	if format.NodeID != 46 {
		t.Errorf("the node is %d, want 46", format.NodeID)
	}
	if format.Rate != 48000 || format.Channels != 2 {
		t.Errorf("the format is %d Hz %d channels, want 48000 Hz 2 channels",
			format.Rate, format.Channels)
	}
}

func TestASuspendedNodeTakesItsChannelsFromTheHardware(t *testing.T) {
	// A suspended 5.1 sink must not be recorded as stereo, so the channel
	// count comes from EnumFormat and the rate from the settings metadata.
	format, err := resolveNode(readGraphFixture(t, "graph.json"),
		"pci-0000-00-1f-3-hdmi-0", directionSink)
	if err != nil {
		t.Fatalf("resolving the HDMI sink: %v", err)
	}
	if format.Channels != 6 {
		t.Errorf("the channel count is %d, want 6", format.Channels)
	}
	if format.Rate != 48000 {
		t.Errorf("the rate is %d, want the graph's 48000", format.Rate)
	}
}

func TestASuspendedSourceResolvesToItsOwnNode(t *testing.T) {
	format, err := resolveNode(readGraphFixture(t, "graph.json"),
		"liken-1-usb-0573-1573-a34004801402-usb-audio-capture", directionSource)
	if err != nil {
		t.Fatalf("resolving the microphone: %v", err)
	}
	if format.NodeID != 47 || format.Channels != 1 {
		t.Errorf("the microphone resolved to node %d with %d channels, want node 47 with 1 channel",
			format.NodeID, format.Channels)
	}
}

func TestANodeOfTheOtherDirectionIsNotTheOneAsked(t *testing.T) {
	// The DAC's playback node and capture node have different names
	// here, but a request for a Source with the Sink's name must find
	// nothing, and must not return the Sink.
	_, err := resolveNode(readGraphFixture(t, "graph.json"),
		"liken-1-usb-0573-1573-a34004801402-usb-audio", directionSource)
	if err == nil {
		t.Fatal("resolveNode returned the sink for a source request, want an error")
	}
	if !strings.Contains(err.Error(), "liken-1-usb-0573-1573-a34004801402-usb-audio") {
		t.Errorf("the error is %v, want it to name the requested node", err)
	}
}

func TestANameTheGraphDoesNotHoldIsARefusal(t *testing.T) {
	_, err := resolveNode(readGraphFixture(t, "graph.json"), "kitchen", directionSink)
	if err == nil {
		t.Fatal("a name that the graph does not contain resolved, want an error")
	}
}

func TestAGraphWithNoSettingsTakesPipeWiresOwnRate(t *testing.T) {
	format, err := resolveNode(readGraphFixture(t, "graph-no-settings.json"),
		"pci-0000-00-1f-3-hdmi-0", directionSink)
	if err != nil {
		t.Fatalf("resolving the sink: %v", err)
	}
	if format.Rate != defaultCaptureRate {
		t.Errorf("the rate is %d, want %d", format.Rate, defaultCaptureRate)
	}
	if format.Channels != 6 {
		t.Errorf("the channel count is %d, want 6", format.Channels)
	}
}

func TestAStreamLinkedToTheTargetIsConfirmed(t *testing.T) {
	document := readGraphFixture(t, "graph.json")
	state, err := confirmLink(document, drillStream, 46)
	if err != nil {
		t.Fatalf("reading the graph: %v", err)
	}
	if state != linkOnTarget {
		t.Errorf("the link to the DAC read as %v, want linkOnTarget", state)
	}
}

func TestAStreamLinkedElsewhereIsTheWrongTarget(t *testing.T) {
	// pw-record links a stream whose stream.capture.sink is an unknown
	// name to the default sink's monitor, and confirmLink must report
	// that link as linkElsewhere.
	document := readGraphFixture(t, "graph-wrong-target.json")
	state, err := confirmLink(document, drillStream, 46)
	if err != nil {
		t.Fatalf("reading the graph: %v", err)
	}
	if state != linkElsewhere {
		t.Errorf("a link to another sink read as %v, want linkElsewhere", state)
	}
}

func TestAGraphWithNoStreamOfOursConfirmsNothing(t *testing.T) {
	document := readGraphFixture(t, "graph.json")
	state, err := confirmLink(document, "audio-capture-somebody-else", 46)
	if err != nil {
		t.Fatalf("reading the graph: %v", err)
	}
	if state != linkNone {
		t.Errorf("a stream this container did not start read as %v, want linkNone", state)
	}
}

func TestAStreamWithNoLinkYetIsNeitherRightNorWrong(t *testing.T) {
	// PipeWire takes a moment to build the link, so "no link yet" must
	// be its own state. A tap that treated it as the wrong target on the
	// first poll would refuse every slow node.
	document := readGraphFixture(t, "graph-no-settings.json")
	state, err := confirmLink(document, drillStream, 48)
	if err != nil {
		t.Fatalf("reading the graph: %v", err)
	}
	if state != linkNone {
		t.Errorf("a graph with no link read as %v, want linkNone", state)
	}
}

func TestOutputThatIsNotAGraphIsAnError(t *testing.T) {
	if _, err := resolveNode([]byte("not json"), "kitchen", directionSink); err == nil {
		t.Error("output that is not a graph resolved")
	}
	if _, err := confirmLink([]byte("not json"), drillStream, 2); err == nil {
		t.Error("output that is not a graph confirmed")
	}
}

// graph-drill.json is a pw-dump taken on liken-1 while a tap ran. It has
// the DAC's sink at node 33, pw-record's stream at node 123, and the
// links from the sink's monitor ports to the stream. The stream has
// application.name and no application.process.id, because the kernel
// cannot translate a peer's pid across PID namespaces on this socket.
func TestTheConfirmationReadsTheDrillsOwnDump(t *testing.T) {
	document := readGraphFixture(t, "graph-drill.json")

	state, err := confirmLink(document, drillStream, 33)
	if err != nil {
		t.Fatalf("reading the graph: %v", err)
	}
	if state != linkOnTarget {
		t.Errorf("the tap on the DAC read as %v, want linkOnTarget", state)
	}

	// The dump has no application.process.id, so a match on that
	// property would find no stream, and every tap would wait for a link
	// until its deadline.
	if strings.Contains(string(document), "application.process.id") {
		t.Error("the fixture contains application.process.id, want the drill's dump, which does not")
	}

	if state, _ := confirmLink(document, "audio-capture-another", 33); state != linkNone {
		t.Errorf("another request's stream read as %v, want linkNone", state)
	}
	if state, _ := confirmLink(document, drillStream, 39); state != linkElsewhere {
		t.Errorf("a link to the DAC read as %v against the microphone, want linkElsewhere", state)
	}
}

func TestTheDrillsGraphResolvesBothOfTheDACsNodes(t *testing.T) {
	document := readGraphFixture(t, "graph-drill.json")

	sink, err := resolveNode(document, "liken.audio.card1-pcm0", directionSink)
	if err != nil {
		t.Fatalf("resolving the DAC: %v", err)
	}
	if sink.NodeID != 33 || sink.Rate != 48000 || sink.Channels != 2 {
		t.Errorf("the DAC resolved to %+v, want node 33 at 48000 Hz with 2 channels", sink)
	}
	source, err := resolveNode(document, "liken.audio.card1-pcm0c", directionSource)
	if err != nil {
		t.Fatalf("resolving the microphone: %v", err)
	}
	if source.NodeID != 39 {
		t.Errorf("the microphone resolved to node %d, want 39", source.NodeID)
	}
	// A request for the sink's name as a source must fail, so that it
	// gets a 404 and not another endpoint's sound.
	if _, err := resolveNode(document, "liken.audio.card1-pcm0", directionSource); err == nil {
		t.Error("resolveNode returned the DAC's sink for a source request, want an error")
	}
}
