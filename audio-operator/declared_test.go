package main

import (
	"reflect"
	"testing"
)

// The reconcile pass parses the drop-in that nodeConfig wrote, so
// each node and layout must parse back to the value that was written.
func TestTheDropInReadsBackAsItWasWritten(t *testing.T) {
	hdmi := alsaEndpoint{Card: 0, PCM: 3}
	analog := alsaEndpoint{Card: 0, PCM: 0}
	usb := alsaEndpoint{Card: 1, PCM: 0}
	microphone := alsaEndpoint{Card: 1, PCM: 0, Capture: true}
	layouts := map[nodeAddress]channelLayout{
		hdmi.graphAddress(): {Source: layoutFromELD, Positions: surround71Layout,
			Encoded: []string{"AC3", "EAC3", "TrueHD"}},
		analog.graphAddress(): {Source: layoutFromSpec, Positions: []string{"FL", "FR", "RL", "RR"}},
		usb.graphAddress():    {Source: layoutFromChannelMap},
	}
	nodes, err := parseDeclaration(nodeConfig([]alsaEndpoint{hdmi, analog, usb, microphone}, layouts))
	if err != nil {
		t.Fatalf("parsing the declaration: %v", err)
	}
	want := []declaredNode{
		{Address: analog.graphAddress(), Layout: layouts[analog.graphAddress()]},
		{Address: hdmi.graphAddress(), Layout: layouts[hdmi.graphAddress()]},
		{Address: usb.graphAddress(), Layout: layouts[usb.graphAddress()]},
		{Address: microphone.graphAddress()},
	}
	if !reflect.DeepEqual(nodes, want) {
		t.Errorf("parseDeclaration returned\n%+v\nwant\n%+v", nodes, want)
	}
}

func TestASinkWithNoLayoutReadsBackAsNone(t *testing.T) {
	nodes, err := parseDeclaration(nodeConfig([]alsaEndpoint{{Card: 0, PCM: 0}}, nil))
	if err != nil {
		t.Fatalf("parsing the declaration: %v", err)
	}
	if got := nodes[0].Layout; !got.equal(channelLayout{Source: layoutNone}) || got.Source != layoutNone {
		t.Errorf("layout = %+v, want None", got)
	}
}

func TestParseDeclarationRefusesWhatItCannotRead(t *testing.T) {
	cases := []struct {
		name     string
		document string
	}{
		{"no context.objects", "# nothing here\n"},
		{"a body that is not JSON", configPrefix + "[ { factory = adapter } ]"},
		{"a node with no card number", configPrefix + `[{"factory": "adapter", "args": {"node.name": "x"}}]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := parseDeclaration(c.document); err == nil {
				t.Errorf("parseDeclaration(%q) returned no error, want a refusal", c.document)
			}
		})
	}
}

// A layout change rewrites the drop-in from the parsed nodes, so the
// rewrite must reproduce the original drop-in.
func TestADropInRebuildsFromItsOwnNodes(t *testing.T) {
	outputs := []alsaEndpoint{{Card: 0, PCM: 0}, {Card: 0, PCM: 3}, {Card: 1, PCM: 0}, {Card: 1, PCM: 0, Capture: true}}
	layouts := map[nodeAddress]channelLayout{outputs[1].graphAddress(): {Source: layoutFromELD, Positions: stereoLayout}}
	document := nodeConfig(outputs, layouts)
	nodes, err := parseDeclaration(document)
	if err != nil {
		t.Fatalf("parsing the declaration: %v", err)
	}
	if again := nodeConfig(declaredEndpoints(nodes), declaredLayouts(nodes)); again != document {
		t.Errorf("the rebuilt declaration is\n%s\nwant\n%s", again, document)
	}
}

func TestSamePCMDevicesIgnoresLayoutsAndOrder(t *testing.T) {
	hdmi := alsaEndpoint{Card: 0, PCM: 3, HDMI: true, Monitor: true}
	analog := alsaEndpoint{Card: 0, PCM: 0}
	nodes, err := parseDeclaration(nodeConfig([]alsaEndpoint{analog, hdmi},
		map[nodeAddress]channelLayout{hdmi.graphAddress(): {Source: layoutFromELD, Positions: stereoLayout}}))
	if err != nil {
		t.Fatalf("parsing the declaration: %v", err)
	}
	cases := []struct {
		name    string
		outputs []alsaEndpoint
		want    bool
	}{
		{"the same devices in another order", []alsaEndpoint{hdmi, analog}, true},
		{"a device that left", []alsaEndpoint{analog}, false},
		{"a device that arrived", []alsaEndpoint{analog, hdmi, {Card: 1, PCM: 0}}, false},
		{"a capture device in place of a playback one", []alsaEndpoint{analog, {Card: 0, PCM: 3, Capture: true}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := samePCMDevices(nodes, c.outputs); got != c.want {
				t.Errorf("samePCMDevices = %v, want %v", got, c.want)
			}
		})
	}
}
