package main

import (
	"os"
	"strings"
	"testing"
)

// drillStream is the node.name the container gives one tap's own
// stream. The fixtures carry it where a real dump carries the name
// pw-record was started with.
const drillStream = "audio-capture-725682cad0fd5870"

// readGraphFixture reads one graph. The fixture carries PWTARGETID
// where the link's output node goes, so the fake pw-dump in
// testdata/capture/bin can point one graph at any endpoint in it; a
// reader here takes the DAC, which is node 46. PWSTREAMNAME is where
// the tap's own stream is named.
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
		"usb-0573-1573-a34004801402-usb-audio", directionSink)
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
	// A suspended 5.1 sink is never guessed as stereo: the channel
	// count comes from EnumFormat and the rate from the graph's own
	// settings metadata.
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
		"usb-0573-1573-a34004801402-usb-audio-capture", directionSource)
	if err != nil {
		t.Fatalf("resolving the microphone: %v", err)
	}
	if format.NodeID != 47 || format.Channels != 1 {
		t.Errorf("the microphone resolved to node %d with %d channels",
			format.NodeID, format.Channels)
	}
}

func TestANodeOfTheOtherDirectionIsNotTheOneAsked(t *testing.T) {
	// The DAC's playback node and its capture node carry different
	// names here, but a request for a Source by the Sink's name must
	// find nothing rather than the Sink.
	_, err := resolveNode(readGraphFixture(t, "graph.json"),
		"usb-0573-1573-a34004801402-usb-audio", directionSource)
	if err == nil {
		t.Fatal("a sink answered a source request")
	}
	if !strings.Contains(err.Error(), "usb-0573-1573-a34004801402-usb-audio") {
		t.Errorf("the refusal does not name what was asked for: %v", err)
	}
}

func TestANameTheGraphDoesNotHoldIsARefusal(t *testing.T) {
	_, err := resolveNode(readGraphFixture(t, "graph.json"), "kitchen", directionSink)
	if err == nil {
		t.Fatal("a name the graph does not hold resolved")
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
		t.Errorf("the link to the DAC read as %v", state)
	}
}

func TestAStreamLinkedElsewhereIsTheWrongTarget(t *testing.T) {
	// pw-record never refuses a bad target: with the property set and
	// an unknown name it links to the default sink's monitor. This is
	// the read that catches it.
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
		t.Errorf("a stream this container did not start read as %v", state)
	}
}

func TestAStreamWithNoLinkYetIsNeitherRightNorWrong(t *testing.T) {
	// PipeWire takes a moment to build the link, so "no link at all"
	// has to be its own state: a tap that answered wrong-target on the
	// first poll would refuse every slow node.
	document := readGraphFixture(t, "graph-no-settings.json")
	state, err := confirmLink(document, drillStream, 48)
	if err != nil {
		t.Fatalf("reading the graph: %v", err)
	}
	if state != linkNone {
		t.Errorf("a graph with no link read as %v", state)
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

// The graph the drill on liken-1 read while a tap ran: the DAC's sink
// at node 33, pw-record's own stream at 123, and the two links that
// carry its monitor ports. The stream carries application.name and no
// application.process.id, because the kernel cannot translate a peer's
// pid across PID namespaces on this socket.
func TestTheConfirmationReadsTheDrillsOwnDump(t *testing.T) {
	document := readGraphFixture(t, "graph-drill.json")

	state, err := confirmLink(document, drillStream, 33)
	if err != nil {
		t.Fatalf("reading the graph: %v", err)
	}
	if state != linkOnTarget {
		t.Errorf("the tap on the DAC read as %v, want linkOnTarget", state)
	}

	// The property the first build matched on is not in this dump at
	// all, which is why every tap ended wrong-target.
	if strings.Contains(string(document), "application.process.id") {
		t.Error("the drill's dump carries application.process.id, and it does not")
	}

	// A tap whose stream is not in the graph yet has made no link.
	if state, _ := confirmLink(document, "audio-capture-another", 33); state != linkNone {
		t.Errorf("another request's stream read as %v", state)
	}
	// The same stream against another sink is the wrong target.
	if state, _ := confirmLink(document, drillStream, 39); state != linkElsewhere {
		t.Errorf("a link to the DAC read as %v against the microphone", state)
	}
}

func TestTheDrillsGraphResolvesBothOfTheDACsNodes(t *testing.T) {
	document := readGraphFixture(t, "graph-drill.json")

	sink, err := resolveNode(document, "liken.audio.card1-pcm0", directionSink)
	if err != nil {
		t.Fatalf("resolving the DAC: %v", err)
	}
	if sink.NodeID != 33 || sink.Rate != 48000 || sink.Channels != 2 {
		t.Errorf("the DAC resolved to %+v", sink)
	}
	source, err := resolveNode(document, "liken.audio.card1-pcm0c", directionSource)
	if err != nil {
		t.Fatalf("resolving the microphone: %v", err)
	}
	if source.NodeID != 39 {
		t.Errorf("the microphone resolved to node %d", source.NodeID)
	}
	// The drill asked the sink's name as a source and got a 404, never
	// another endpoint's sound.
	if _, err := resolveNode(document, "liken.audio.card1-pcm0", directionSource); err == nil {
		t.Error("the DAC's sink answered a source request")
	}
}
