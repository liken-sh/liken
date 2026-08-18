package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The document in testdata is the shape pw-dump prints: an array of
// objects, each with its interface type and its properties. pw-dump
// leaves a property whose value reads as a number unquoted, even
// though every value in PipeWire's property list is a string, so the
// fixture holds both forms.
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

// The nodes this operator declares have no alsa.card and no
// alsa.device, because those two come from the udev device that
// WirePlumber's monitor builds and this graph has no monitor in it.
// The operator's own two properties map a declared node back to its
// PCM device.
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

// The declared outputs and a graph that holds a sink for each one.
func testOutputs() []alsaOutput {
	return []alsaOutput{{Card: 0, PCM: 0}, {Card: 0, PCM: 3, HDMI: true, Monitor: true}}
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
	read := func(context.Context) (map[pcmAddress]string, error) {
		reads++
		return testSinks(), nil
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
	read := func(context.Context) (map[pcmAddress]string, error) {
		return map[pcmAddress]string{{Card: 0, PCM: 0}: sinkNodeName(0, 0)}, nil
	}

	start := time.Now()
	waitForNodes(context.Background(), read, testOutputs(), 50*time.Millisecond)

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("waited %s past a %s timeout", elapsed, 50*time.Millisecond)
	}
}

// A graph that never reads is the same bounded wait, and not a hang.
func TestWaitForNodesGivesUpWhenTheGraphNeverReads(t *testing.T) {
	read := func(context.Context) (map[pcmAddress]string, error) {
		return nil, errors.New("running pw-dump: no such file or directory")
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
	read := func(context.Context) (map[pcmAddress]string, error) {
		return nil, errors.New("running pw-dump: no such file or directory")
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
			want:  []string{"card0-pcm0", "card0-pcm3"},
		},
		{
			name:  "one of them does",
			sinks: map[pcmAddress]string{{Card: 0, PCM: 3}: sinkNodeName(0, 3)},
			want:  []string{"card0-pcm0"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := missingNodes(testOutputs(), c.sinks)
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
