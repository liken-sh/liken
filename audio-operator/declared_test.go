package main

import (
	"reflect"
	"testing"
)

// The reconcile pass reads the running declaration back, so every
// node and every layout the generator writes must read back as it
// was given.
func TestTheDeclarationReadsBackAsItWasWritten(t *testing.T) {
	hdmi := alsaEndpoint{Card: 0, PCM: 3}
	analog := alsaEndpoint{Card: 0, PCM: 0}
	usb := alsaEndpoint{Card: 1, PCM: 0}
	microphone := alsaEndpoint{Card: 1, PCM: 0, Capture: true}
	layouts := map[nodeAddress]channelLayout{
		hdmi.graphAddress():   {Source: layoutFromELD, Positions: surround71Layout},
		analog.graphAddress(): {Source: layoutFromSpec, Positions: []string{"FL", "FR", "RL", "RR"}},
		usb.graphAddress():    {Source: layoutFromChannelMap},
	}
	nodes, err := parseDeclaration(nodeConfig([]alsaEndpoint{hdmi, analog, usb, microphone}, layouts))
	if err != nil {
		t.Fatal(err)
	}
	want := []declaredNode{
		{Address: analog.graphAddress(), Layout: layouts[analog.graphAddress()]},
		{Address: hdmi.graphAddress(), Layout: layouts[hdmi.graphAddress()]},
		{Address: usb.graphAddress(), Layout: layouts[usb.graphAddress()]},
		{Address: microphone.graphAddress()},
	}
	if !reflect.DeepEqual(nodes, want) {
		t.Errorf("read back\n%+v\nwant\n%+v", nodes, want)
	}
}

// A sink declared with no layout reads back as None, which is what a
// declaration written before layouts existed holds for every sink.
func TestASinkWithNoLayoutReadsBackAsNone(t *testing.T) {
	nodes, err := parseDeclaration(nodeConfig([]alsaEndpoint{{Card: 0, PCM: 0}}, nil))
	if err != nil {
		t.Fatal(err)
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
				t.Errorf("parsed %q", c.document)
			}
		})
	}
}

// The rewrite for a layout change writes the same nodes again, so a
// declaration rebuilt from what it holds generates the same document.
func TestADeclarationRebuildsFromItsOwnNodes(t *testing.T) {
	outputs := []alsaEndpoint{{Card: 0, PCM: 0}, {Card: 0, PCM: 3}, {Card: 1, PCM: 0}, {Card: 1, PCM: 0, Capture: true}}
	layouts := map[nodeAddress]channelLayout{outputs[1].graphAddress(): {Source: layoutFromELD, Positions: stereoLayout}}
	document := nodeConfig(outputs, layouts)
	nodes, err := parseDeclaration(document)
	if err != nil {
		t.Fatal(err)
	}
	if again := nodeConfig(declaredEndpoints(nodes), declaredLayouts(nodes)); again != document {
		t.Errorf("the rebuilt declaration differs:\n%s\n%s", document, again)
	}
}

func TestSamePCMDevicesIgnoresLayoutsAndOrder(t *testing.T) {
	hdmi := alsaEndpoint{Card: 0, PCM: 3, HDMI: true, Monitor: true}
	analog := alsaEndpoint{Card: 0, PCM: 0}
	nodes, err := parseDeclaration(nodeConfig([]alsaEndpoint{analog, hdmi},
		map[nodeAddress]channelLayout{hdmi.graphAddress(): {Source: layoutFromELD, Positions: stereoLayout}}))
	if err != nil {
		t.Fatal(err)
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
				t.Errorf("same = %v, want %v", got, c.want)
			}
		})
	}
}
