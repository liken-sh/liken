package main

import "testing"

// The document in testdata is the shape pw-dump prints: an array of
// objects, each with its interface type and its properties. pw-dump
// leaves a property whose value reads as a number unquoted, even
// though every value in PipeWire's property list is a string, so the
// fixture carries both forms.
func TestParseSinks(t *testing.T) {
	sinks, err := parseSinks(fixture(t, "pw-dump.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sinks) != 2 {
		t.Fatalf("sinks = %v, want two", sinks)
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

// The nodes this operator declares carry no alsa.card and no
// alsa.device, because those two come from the udev device that
// WirePlumber's monitor builds and this graph has no monitor in it.
// The operator's own two properties are what map a declared node back
// to its PCM device.
func TestParseSinksMapsTheNodesTheOperatorDeclares(t *testing.T) {
	sinks, err := parseSinks(fixture(t, "pw-dump-declared.json"))
	if err != nil {
		t.Fatal(err)
	}
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

func TestParseSinksReportsBrokenOutput(t *testing.T) {
	if _, err := parseSinks([]byte("this is not JSON")); err == nil {
		t.Fatal("a document that is not JSON did not report an error")
	}
}
