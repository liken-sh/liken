package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// declaredObjects reads the context.objects array back out of a
// generated drop-in. The generator writes the strict JSON subset of
// SPA-JSON on purpose, so a test can parse what PipeWire will parse
// instead of matching text.
func declaredObjects(t *testing.T, document string) []staticNode {
	t.Helper()
	start := strings.Index(document, configPrefix)
	if start < 0 {
		t.Fatalf("the document declares no %s:\n%s", configPrefix, document)
	}
	var objects []staticNode
	body := document[start+len(configPrefix):]
	if err := json.Unmarshal([]byte(body), &objects); err != nil {
		t.Fatalf("the declaration is not valid JSON: %v\n%s", err, body)
	}
	return objects
}

// Every playback PCM device on the card gets one sink node, and each
// one holds the ALSA device it opens and the two properties that map
// it back to the output the operator publishes.
func TestNodeConfigDeclaresOneSinkForEachPlaybackPCM(t *testing.T) {
	outputs := []alsaOutput{
		{Card: 0, PCM: 3, HDMI: true},
		{Card: 0, PCM: 0},
	}
	objects := declaredObjects(t, nodeConfig(outputs))
	if len(objects) != 2 {
		t.Fatalf("declared %d objects, want two", len(objects))
	}

	// The card's outputs are declared in ALSA address order, whatever
	// order the enumeration returned them in.
	cases := []struct {
		card, pcm int
		path      string
	}{
		{card: 0, pcm: 0, path: "hw:0,0"},
		{card: 0, pcm: 3, path: "hw:0,3"},
	}
	for i, want := range cases {
		object := objects[i]
		if object.Factory != "adapter" {
			t.Errorf("object %d factory = %q, want adapter", i, object.Factory)
		}
		// One output that cannot be created must not stop PipeWire from
		// starting, because every other output on the card would lose
		// its sink with it.
		if !slices.Contains(object.Flags, nofail) {
			t.Errorf("object %d flags = %v, want %s among them", i, object.Flags, nofail)
		}
		args := map[string]string{
			"factory.name":      "api.alsa.pcm.sink",
			"media.class":       "Audio/Sink",
			"api.alsa.path":     want.path,
			"api.alsa.pcm.card": "0",
			"node.name":         sinkNodeName(want.card, want.pcm),
			nodeCardProperty:    "0",
			nodePCMProperty:     strings.TrimPrefix(want.path, "hw:0,"),
		}
		for key, value := range args {
			if got := object.Args[key]; got != value {
				t.Errorf("object %d %s = %q, want %q", i, key, got, value)
			}
		}
	}
}

// An HDMI output with no monitor on it is declared like any other. The
// PCM device is what the card has, and the operator must not build a
// graph that changes when somebody moves a cable, because PipeWire
// reads these declarations once.
func TestNodeConfigDeclaresAnHDMIOutputWithNoMonitor(t *testing.T) {
	connected := nodeConfig([]alsaOutput{{Card: 0, PCM: 3, HDMI: true, Monitor: true}})
	unplugged := nodeConfig([]alsaOutput{{Card: 0, PCM: 3, HDMI: true}})
	if connected != unplugged {
		t.Errorf("a monitor changed the declaration:\n%s\n%s", connected, unplugged)
	}
}

// The reconcile pass compares a freshly generated document against the
// one PipeWire started with, and restarts the pod when the two differ.
// A generator whose output moved with the enumeration order would
// restart the pod forever.
func TestNodeConfigDoesNotMoveWithTheEnumerationOrder(t *testing.T) {
	one := nodeConfig([]alsaOutput{{Card: 0, PCM: 0}, {Card: 0, PCM: 3}, {Card: 1, PCM: 0}})
	other := nodeConfig([]alsaOutput{{Card: 1, PCM: 0}, {Card: 0, PCM: 3}, {Card: 0, PCM: 0}})
	if one != other {
		t.Errorf("the enumeration order changed the declaration:\n%s\n%s", one, other)
	}
}

// A card with no playback PCM device declares no node, and the file is
// still a document PipeWire can read.
func TestNodeConfigDeclaresNothingForACardWithNoOutputs(t *testing.T) {
	if objects := declaredObjects(t, nodeConfig(nil)); len(objects) != 0 {
		t.Errorf("declared %+v for a card with no outputs", objects)
	}
}

// Two outputs must never claim one node name. A name that collided
// would give one PCM device no sink and the other one two.
func TestNodeConfigNamesEveryOutputOnce(t *testing.T) {
	outputs := []alsaOutput{{Card: 0, PCM: 0}, {Card: 0, PCM: 3}, {Card: 1, PCM: 0}, {Card: 1, PCM: 3}}
	seen := map[string]bool{}
	for _, object := range declaredObjects(t, nodeConfig(outputs)) {
		name := object.Args["node.name"]
		if seen[name] {
			t.Errorf("two outputs declare the node name %q", name)
		}
		seen[name] = true
	}
	if len(seen) != len(outputs) {
		t.Errorf("%d outputs declared %d names", len(outputs), len(seen))
	}
}

func TestWriteNodeConfigLandsWherePipeWireReadsIt(t *testing.T) {
	pipewireConfigDir = filepath.Join(t.TempDir(), "pipewire.conf.d")
	t.Cleanup(func() { pipewireConfigDir = "/etc/pipewire/pipewire.conf.d" })

	document, err := writeNodeConfig([]alsaOutput{{Card: 0, PCM: 3, HDMI: true}})
	if err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(filepath.Join(pipewireConfigDir, dropInName))
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != document {
		t.Errorf("the file holds something other than the document it returned:\n%s", written)
	}
	if objects := declaredObjects(t, string(written)); len(objects) != 1 {
		t.Errorf("the written file declares %d objects, want one", len(objects))
	}
}
