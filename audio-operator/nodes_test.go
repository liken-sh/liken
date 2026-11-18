package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// declaredObjects parses the context.objects array of a generated
// drop-in, so a test checks objects and not text.
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

// Each playback PCM device gets a sink node with the ALSA path it
// opens, and with the card and PCM properties that parseGraph maps back
// to an endpoint.
func TestNodeConfigDeclaresOneSinkForEachPlaybackPCM(t *testing.T) {
	outputs := []alsaEndpoint{
		{Card: 0, PCM: 3, HDMI: true},
		{Card: 0, PCM: 0},
	}
	objects := declaredObjects(t, nodeConfig(outputs, nil))
	if len(objects) != 2 {
		t.Fatalf("declared %d objects, want two", len(objects))
	}

	// Objects come out sorted by card and PCM number, whatever order the
	// enumeration returned the endpoints in.
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

// PCM device 0 of this card plays and records, as a USB card's does, so
// its sink and its source have one ALSA address and need two names.
func TestNodeConfigDeclaresASourceForEachCapturePCM(t *testing.T) {
	objects := declaredObjects(t, nodeConfig([]alsaEndpoint{
		{Card: 1, PCM: 0, Capture: true},
		{Card: 1, PCM: 0},
	}, nil))
	if len(objects) != 2 {
		t.Fatalf("declared %d objects, want two", len(objects))
	}

	// Playback sorts before capture.
	sink, source := objects[0], objects[1]
	if got := sink.Args["factory.name"]; got != "api.alsa.pcm.sink" {
		t.Errorf("the playback object's factory = %q, want api.alsa.pcm.sink", got)
	}
	args := map[string]string{
		"factory.name":      "api.alsa.pcm.source",
		"media.class":       "Audio/Source",
		"api.alsa.path":     "hw:1,0",
		"api.alsa.pcm.card": "1",
		"node.name":         sourceNodeName(1, 0),
		nodeCardProperty:    "1",
		nodePCMProperty:     "0",
	}
	for key, want := range args {
		if got := source.Args[key]; got != want {
			t.Errorf("the capture object's %s = %q, want %q", key, got, want)
		}
	}
	if !slices.Contains(source.Flags, nofail) {
		t.Errorf("the capture object's flags = %v, want %s among them", source.Flags, nofail)
	}
	if sink.Args["node.name"] == source.Args["node.name"] {
		t.Errorf("both directions of one PCM device declare the node name %q, want a name for each direction", sink.Args["node.name"])
	}
}

// A monitor changes only a sink's layout, and the layout reaches
// nodeConfig only through its layouts argument.
func TestNodeConfigDeclaresAnHDMIOutputWithNoMonitor(t *testing.T) {
	connected := nodeConfig([]alsaEndpoint{{Card: 0, PCM: 3, HDMI: true, Monitor: true}}, nil)
	unplugged := nodeConfig([]alsaEndpoint{{Card: 0, PCM: 3, HDMI: true}}, nil)
	if connected != unplugged {
		t.Errorf("a monitor changed the declaration:\n%s\n%s", connected, unplugged)
	}
}

func TestNodeConfigDoesNotMoveWithTheEnumerationOrder(t *testing.T) {
	one := nodeConfig([]alsaEndpoint{{Card: 0, PCM: 0}, {Card: 0, PCM: 3}, {Card: 1, PCM: 0}}, nil)
	other := nodeConfig([]alsaEndpoint{{Card: 1, PCM: 0}, {Card: 0, PCM: 3}, {Card: 0, PCM: 0}}, nil)
	if one != other {
		t.Errorf("the enumeration order changed the declaration:\n%s\n%s", one, other)
	}
}

// A card with no PCM device declares no node, and the file still
// parses.
func TestNodeConfigDeclaresNothingForACardWithNoOutputs(t *testing.T) {
	if objects := declaredObjects(t, nodeConfig(nil, nil)); len(objects) != 0 {
		t.Errorf("declared %+v for a card with no outputs, want no object", objects)
	}
}

// Two endpoints must never get the same node name. With a shared name,
// one PCM device would have no node and the other would have two.
func TestNodeConfigNamesEveryOutputOnce(t *testing.T) {
	outputs := []alsaEndpoint{{Card: 0, PCM: 0}, {Card: 0, PCM: 3}, {Card: 1, PCM: 0}, {Card: 1, PCM: 3}}
	seen := map[string]bool{}
	for _, object := range declaredObjects(t, nodeConfig(outputs, nil)) {
		name := object.Args["node.name"]
		if seen[name] {
			t.Errorf("two outputs declare the node name %q", name)
		}
		seen[name] = true
	}
	if len(seen) != len(outputs) {
		t.Errorf("%d outputs declared %d names, want one name for each output", len(outputs), len(seen))
	}
}

func TestWriteNodeConfigLandsWherePipeWireReadsIt(t *testing.T) {
	pipewireConfigDir = filepath.Join(t.TempDir(), "pipewire.conf.d")
	t.Cleanup(func() { pipewireConfigDir = "/etc/pipewire/pipewire.conf.d" })

	document, err := writeNodeConfig([]alsaEndpoint{{Card: 0, PCM: 3, HDMI: true}}, nil)
	if err != nil {
		t.Fatalf("writing the declaration: %v", err)
	}
	written, err := os.ReadFile(filepath.Join(pipewireConfigDir, dropInName))
	if err != nil {
		t.Fatalf("reading the declaration back: %v", err)
	}
	if string(written) != document {
		t.Errorf("the file holds something other than the document it returned:\n%s", written)
	}
	if objects := declaredObjects(t, string(written)); len(objects) != 1 {
		t.Errorf("the written file declares %d objects, want one", len(objects))
	}
}

// Each kind of layout reaches the node through its own properties, and
// api.alsa.path is the same for every kind.
func TestNodeConfigDeclaresEachKindOfLayout(t *testing.T) {
	absent := "<absent>"
	cases := []struct {
		name     string
		layout   channelLayout
		channels string
		position string
		chmap    string
		source   string
	}{
		{"a 7.1 layout from the ELD", channelLayout{Source: layoutFromELD, Positions: surround71Layout},
			"8", "[ FL FR RL RR FC LFE SL SR ]", absent, "ELD"},
		{"a stereo layout from the ELD", channelLayout{Source: layoutFromELD, Positions: stereoLayout},
			"2", "[ FL FR ]", absent, "ELD"},
		{"a layout from spec", channelLayout{Source: layoutFromSpec, Positions: surround51Layout},
			"6", "[ FL FR RL RR FC LFE ]", absent, "Spec"},
		{"the channel map", channelLayout{Source: layoutFromChannelMap},
			absent, absent, "true", "ChannelMap"},
		{"no layout", channelLayout{Source: layoutNone}, absent, absent, absent, absent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			output := alsaEndpoint{Card: 0, PCM: 3}
			objects := declaredObjects(t, nodeConfig([]alsaEndpoint{output},
				map[nodeAddress]channelLayout{output.graphAddress(): c.layout}))
			args := objects[0].Args
			read := func(key string) string {
				if value, ok := args[key]; ok {
					return value
				}
				return absent
			}
			want := map[string]string{
				"audio.channels":     c.channels,
				"audio.position":     c.position,
				"api.alsa.use-chmap": c.chmap,
				layoutSourceProperty: c.source,
				"api.alsa.path":      "hw:0,3",
			}
			for key, value := range want {
				if got := read(key); got != value {
					t.Errorf("%s = %q, want %q", key, got, value)
				}
			}
		})
	}
}

// A source can have the card and PCM numbers of a sink, as on a USB
// card, and a layout keyed by the sink's address must not apply to it.
func TestNodeConfigGivesASourceNoLayout(t *testing.T) {
	sink := alsaEndpoint{Card: 1, PCM: 0}
	source := alsaEndpoint{Card: 1, PCM: 0, Capture: true}
	objects := declaredObjects(t, nodeConfig([]alsaEndpoint{sink, source},
		map[nodeAddress]channelLayout{sink.graphAddress(): {Source: layoutFromChannelMap}}))
	if _, ok := objects[1].Args["api.alsa.use-chmap"]; ok {
		t.Errorf("the source was declared with the sink's layout: %v, want no api.alsa.use-chmap", objects[1].Args)
	}
}
