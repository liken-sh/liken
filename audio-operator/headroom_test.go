package main

import (
	"strings"
	"testing"
	"testing/synctest"
)

// The headroom comes from the Sink whatever the source of the
// positions, so a receiver that turns off keeps the headroom its Sink
// asks for, and a sink with no positions can still have one.
func TestASinksHeadroomReachesItsLayout(t *testing.T) {
	receiver := eldWith(t, 8, fiveOne|rearCenterPair)
	hdmi := alsaEndpoint{Card: 0, PCM: 3, HDMI: true, Monitor: true, ELD: receiver,
		Identity: cardIdentity{Bus: pciBus}}
	unplugged := alsaEndpoint{Card: 0, PCM: 3, HDMI: true, Identity: cardIdentity{Bus: pciBus}}
	analog := alsaEndpoint{Card: 0, PCM: 0, Identity: cardIdentity{Bus: pciBus}}
	usb := alsaEndpoint{Card: 1, PCM: 0, Identity: cardIdentity{Bus: usbBus}}
	quad := []string{"FL", "FR", "RL", "RR"}
	fromELD := channelLayout{Source: layoutFromELD, Positions: surround71Layout}

	cases := []struct {
		name     string
		output   alsaEndpoint
		spec     layoutSpec
		declared channelLayout
		want     channelLayout
	}{
		{"a receiver", hdmi, layoutSpec{Headroom: 4096}, channelLayout{},
			channelLayout{Source: layoutFromELD, Positions: surround71Layout, Headroom: 4096}},
		{"a receiver that turned off", unplugged, layoutSpec{Headroom: 4096}, fromELD,
			channelLayout{Source: layoutFromELD, Positions: surround71Layout, Headroom: 4096}},
		{"a headroom removed while the receiver is off", unplugged, layoutSpec{},
			channelLayout{Source: layoutFromELD, Positions: surround71Layout, Headroom: 4096}, fromELD},
		{"the analog jack", analog, layoutSpec{Headroom: 1024}, channelLayout{},
			channelLayout{Source: layoutNone, Headroom: 1024}},
		{"a USB card", usb, layoutSpec{Headroom: 1024}, channelLayout{},
			channelLayout{Source: layoutFromChannelMap, Headroom: 1024}},
		{"a spec layout with a headroom", analog, layoutSpec{Positions: quad, Headroom: 2048}, channelLayout{},
			channelLayout{Source: layoutFromSpec, Positions: quad, Headroom: 2048}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := selectLayout(c.output, c.spec, c.declared); !got.equal(c.want) {
				t.Errorf("selectLayout = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestNodeConfigDeclaresTheHeadroom(t *testing.T) {
	cases := []struct {
		name   string
		layout channelLayout
		want   string
	}{
		{"a headroom", channelLayout{Source: layoutFromELD, Positions: stereoLayout, Headroom: 4096}, "4096"},
		{"a headroom on a sink with no positions", channelLayout{Source: layoutNone, Headroom: 512}, "512"},
		{"no headroom", channelLayout{Source: layoutFromELD, Positions: stereoLayout}, "<absent>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			output := alsaEndpoint{Card: 0, PCM: 3}
			objects := declaredObjects(t, nodeConfig([]alsaEndpoint{output},
				map[nodeAddress]channelLayout{output.graphAddress(): c.layout}))
			got, ok := objects[0].Args[headroomProperty]
			if !ok {
				got = "<absent>"
			}
			if got != c.want {
				t.Errorf("%s = %q, want %q", headroomProperty, got, c.want)
			}
		})
	}
}

// The reconcile pass compares the headroom it reads back from the
// drop-in with the Sink's, so the value must survive the round trip,
// on a sink with no positions too.
func TestTheHeadroomReadsBackFromTheDropIn(t *testing.T) {
	hdmi := alsaEndpoint{Card: 0, PCM: 3}
	analog := alsaEndpoint{Card: 0, PCM: 0}
	layouts := map[nodeAddress]channelLayout{
		hdmi.graphAddress():   {Source: layoutFromELD, Positions: surround71Layout, Headroom: 4096},
		analog.graphAddress(): {Source: layoutNone, Headroom: 256},
	}
	nodes, err := parseDeclaration(nodeConfig([]alsaEndpoint{hdmi, analog}, layouts))
	if err != nil {
		t.Fatalf("parsing the declaration: %v", err)
	}
	for _, node := range nodes {
		if want := layouts[node.Address]; !node.Layout.equal(want) {
			t.Errorf("the sink at %+v reads back as %+v, want %+v", node.Address, node.Layout, want)
		}
	}
}

func TestPlanLayoutsFindsAHeadroomChange(t *testing.T) {
	receiver := eldWith(t, 8, fiveOne|rearCenterPair)
	withHeadroom := func(samples int) channelLayout {
		return channelLayout{Source: layoutFromELD, Positions: surround71Layout, Headroom: samples}
	}
	cases := []struct {
		name     string
		declared channelLayout
		headroom int
		want     *channelLayout
	}{
		{"a headroom that arrived", withHeadroom(0), 4096, &channelLayout{Source: layoutFromELD,
			Positions: surround71Layout, Headroom: 4096}},
		{"a headroom that changed", withHeadroom(1024), 4096, &channelLayout{Source: layoutFromELD,
			Positions: surround71Layout, Headroom: 4096}},
		{"a headroom that was removed", withHeadroom(4096), 0, &channelLayout{Source: layoutFromELD,
			Positions: surround71Layout}},
		{"the same headroom", withHeadroom(4096), 4096, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hdmi, _ := layoutEndpoints(t, &receiver)
			declared := map[nodeAddress]channelLayout{hdmi.graphAddress(): c.declared}
			specs := map[nodeAddress]layoutSpec{hdmi.graphAddress(): {Headroom: c.headroom}}
			changes := planLayouts(declared, []alsaEndpoint{hdmi}, specs)
			switch {
			case c.want == nil && len(changes) != 0:
				t.Errorf("changes = %+v, want none", changes)
			case c.want != nil && (len(changes) != 1 || !changes[0].To.equal(*c.want)):
				t.Errorf("changes = %+v, want one to %+v", changes, *c.want)
			}
		})
	}
}

// The declare container reads spec.headroom.samples with spec.layout,
// so PipeWire starts with the headroom that the Sink asks for.
func TestTheDeclareContainerReadsEachSinksHeadroom(t *testing.T) {
	endpoints := liken1Endpoints(t)
	hdmi := endpoints["liken-1-pci-0000-00-1f-3-hdmi-0"]
	server := &sinkServer{sinks: map[string]Sink{
		hdmi.Name(): {Metadata: EndpointMeta{Name: hdmi.Name()},
			Spec: SinkSpec{Headroom: &SinkHeadroom{Samples: 4096}}},
	}}
	var outputs []alsaEndpoint
	for _, endpoint := range endpoints {
		endpoint.DeviceName = ""
		outputs = append(outputs, endpoint)
	}

	specs := apiSpecs(testClient(t, server.handler()), "liken-1", outputs)
	if got := specs[hdmi.graphAddress()]; len(specs) != 1 || got.Headroom != 4096 {
		t.Errorf("specs = %+v, want the HDMI slot's alone with a headroom of 4096", specs)
	}
}

// A headroom on a Sink rewrites the declaration like a layout, and the
// LayoutChanged Event and the LayoutApplied message name it.
func TestASinksHeadroomRewritesTheDeclarationAndPostsLayoutChanged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		operator, endpoints := layoutReconciler(t)
		analog := endpoints[1]
		server := &sinkServer{sinks: map[string]Sink{
			analog.Name(): {Metadata: EndpointMeta{Name: analog.Name(), UID: "uid-1"},
				Spec: SinkSpec{Headroom: &SinkHeadroom{Samples: 4096}}},
		}}
		control, recorded := eventedControl(t, server.handler())
		operator.control = control

		states := operator.reconcileLayouts(endpoints, pwGraph{})
		state := states[analog.Name()]
		if state.Layout.Headroom != 4096 || !strings.Contains(state.Message, "with 4096 samples of headroom") {
			t.Errorf("the analog sink reports %+v, want a headroom of 4096 in its layout and its message", state)
		}
		synctest.Wait()
		posted := recorded.About(SinkKind, analog.Name())
		if len(posted) != 1 || posted[0].Reason != reasonLayoutChanged ||
			!strings.Contains(posted[0].Message, "from no positions to no positions, with 4096 samples of headroom") {
			t.Errorf("events = %+v, want one LayoutChanged that names the headroom", posted)
		}
	})
}
