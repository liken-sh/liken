package main

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

// layoutEndpoints returns the HDMI and analog playback endpoints of one
// card, with the device names that nameEndpoints gives them. A non-nil
// monitor is the ELD of a monitor connected to the HDMI output.
func layoutEndpoints(t *testing.T, monitor *eld) (hdmi, analog alsaEndpoint) {
	t.Helper()
	hdmi = alsaEndpoint{Card: 0, PCM: 3, HDMI: true, Identity: cardIdentity{Bus: pciBus},
		DeviceName: "node-1-pci-0000-00-1f-3-hdmi-0"}
	if monitor != nil {
		hdmi.Monitor, hdmi.ELD = true, *monitor
	}
	analog = alsaEndpoint{Card: 0, PCM: 0, Identity: cardIdentity{Bus: pciBus},
		DeviceName: "node-1-pci-0000-00-1f-3-analog"}
	return hdmi, analog
}

func TestPlanLayoutsFindsEachLayoutDifference(t *testing.T) {
	receiver := eldWith(t, 8, fiveOne|rearCenterPair)
	television := eldWith(t, 8, frontPair)
	quad := []string{"FL", "FR", "RL", "RR"}
	fromELD := func(positions []string) channelLayout {
		return channelLayout{Source: layoutFromELD, Positions: positions}
	}

	cases := []struct {
		name     string
		monitor  *eld
		declared channelLayout
		spec     []string
		want     []channelLayout
	}{
		{"the same receiver", &receiver, fromELD(surround71Layout), nil, nil},
		{"a receiver that turned off", nil, fromELD(surround71Layout), nil, nil},
		{"a television that turned off before the pod started", nil, channelLayout{Source: layoutNone}, nil, nil},
		{"a receiver that turned on after the pod started", &receiver, channelLayout{Source: layoutNone}, nil,
			[]channelLayout{fromELD(surround71Layout)}},
		{"a television in place of the receiver", &television, fromELD(surround71Layout), nil,
			[]channelLayout{fromELD(stereoLayout)}},
		{"a spec that arrived", &receiver, fromELD(surround71Layout), quad,
			[]channelLayout{{Source: layoutFromSpec, Positions: quad}}},
		{"a spec that was removed", &receiver, channelLayout{Source: layoutFromSpec, Positions: quad}, nil,
			[]channelLayout{fromELD(surround71Layout)}},
		{"a spec that states the ELD's layout", &receiver, fromELD(surround71Layout), surround71Layout,
			[]channelLayout{{Source: layoutFromSpec, Positions: surround71Layout}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hdmi, _ := layoutEndpoints(t, c.monitor)
			declared := map[nodeAddress]channelLayout{hdmi.graphAddress(): c.declared}
			specs := map[nodeAddress][]string{}
			if c.spec != nil {
				specs[hdmi.graphAddress()] = c.spec
			}
			changes := planLayouts(declared, []alsaEndpoint{hdmi}, specs)
			if len(changes) != len(c.want) {
				t.Fatalf("changes = %+v, want %+v", changes, c.want)
			}
			for i, change := range changes {
				if !change.To.equal(c.want[i]) || !change.From.equal(c.declared) {
					t.Errorf("change %d = %+v, want from %+v to %+v", i, change, c.declared, c.want[i])
				}
			}
		})
	}
}

func TestPlanLayoutsSkipsAnEndpointWithNoDeclaredNode(t *testing.T) {
	receiver := eldWith(t, 8, fiveOne|rearCenterPair)
	hdmi, _ := layoutEndpoints(t, &receiver)
	if changes := planLayouts(map[nodeAddress]channelLayout{}, []alsaEndpoint{hdmi}, nil); len(changes) != 0 {
		t.Errorf("planLayouts returned %+v for an endpoint that PipeWire has no node for, want no change", changes)
	}
}

// layoutReconciler returns a reconciler whose drop-in lists the analog
// jack and an HDMI output with no monitor connected. The drop-in is in a
// temporary directory.
func layoutReconciler(t *testing.T) (*reconciler, []alsaEndpoint) {
	t.Helper()
	pipewireConfigDir = filepath.Join(t.TempDir(), "pipewire.conf.d")
	t.Cleanup(func() { pipewireConfigDir = "/etc/pipewire/pipewire.conf.d" })
	hdmi, analog := layoutEndpoints(t, nil)
	document, err := writeNodeConfig([]alsaEndpoint{analog, hdmi}, selectLayouts([]alsaEndpoint{analog, hdmi}, nil, nil))
	if err != nil {
		t.Fatalf("writing the declaration: %v", err)
	}
	return &reconciler{nodeName: "node-1", declared: document}, []alsaEndpoint{hdmi, analog}
}

// playingGraph returns a graph in which a stream is linked to the HDMI
// sink.
func playingGraph(hdmi alsaEndpoint) pwGraph {
	return pwGraph{
		Nodes:  map[nodeAddress]pwNode{hdmi.graphAddress(): {ID: 42, Name: sinkNodeName(0, 3)}},
		Linked: map[int]bool{42: true, 77: true},
	}
}

func TestAReceiverThatTurnsOnWaitsForTheFilmToEnd(t *testing.T) {
	operator, endpoints := layoutReconciler(t)
	declared := operator.declared
	receiver := eldWith(t, 8, fiveOne|rearCenterPair)
	hdmi, _ := layoutEndpoints(t, &receiver)
	endpoints[0] = hdmi

	states := operator.reconcileLayouts(endpoints, playingGraph(hdmi))
	if operator.declared != declared {
		t.Errorf("the declaration changed while a stream played, want no change until the stream ends")
	}
	written, err := readNodeConfig()
	if err != nil {
		t.Fatalf("reading the drop-in: %v", err)
	}
	if written != declared {
		t.Errorf("the drop-in changed while a stream played, want no change until the stream ends")
	}
	state := states[hdmi.Name()]
	if state.Applied || state.Reason != layoutReasonAwaitingIdle {
		t.Errorf("the HDMI sink reports %+v, want AwaitingIdle", state)
	}
	if !state.Layout.equal(channelLayout{Source: layoutNone}) {
		t.Errorf("the HDMI sink reports %s, want the declared layout", state.Layout)
	}
	if analog := states[endpoints[1].Name()]; !analog.Applied {
		t.Errorf("the analog sink, whose layout did not change, reports %+v, want Applied", analog)
	}
}

func TestAReceiverThatTurnsOnRewritesTheDeclarationWhenIdle(t *testing.T) {
	operator, endpoints := layoutReconciler(t)
	receiver := eldWith(t, 8, fiveOne|rearCenterPair)
	hdmi, _ := layoutEndpoints(t, &receiver)
	endpoints[0] = hdmi
	idle := playingGraph(hdmi)
	idle.Linked = map[int]bool{}

	states := operator.reconcileLayouts(endpoints, idle)
	written, err := readNodeConfig()
	if err != nil {
		t.Fatalf("reading the drop-in: %v", err)
	}
	if written != operator.declared {
		t.Errorf("the reconciler records a declaration other than the drop-in it wrote, want the same text")
	}
	nodes, err := parseDeclaration(written)
	if err != nil {
		t.Fatalf("parsing the drop-in: %v", err)
	}
	want := channelLayout{Source: layoutFromELD, Positions: surround71Layout}
	if got := declaredLayouts(nodes)[hdmi.graphAddress()]; !got.equal(want) {
		t.Errorf("the drop-in declares %s, want %s", got, want)
	}
	if len(nodes) != 2 {
		t.Errorf("the rewrite declares %d nodes, want the same two", len(nodes))
	}
	if state := states[hdmi.Name()]; !state.Applied || !state.Layout.equal(want) {
		t.Errorf("the HDMI sink reports %+v, want Applied with %s", state, want)
	}
	if !operator.awaitingRestart() {
		t.Errorf("awaitingRestart() = false after the write, want true")
	}
}

// Until PipeWire restarts after a write, the drop-in on disk is newer
// than the running PipeWire, and every sink reports Restarting.
func TestEverySinkReportsTheRestart(t *testing.T) {
	operator, endpoints := layoutReconciler(t)
	operator.declarationStale = func() bool { return true }

	for name, state := range operator.reconcileLayouts(endpoints, pwGraph{}) {
		if state.Applied || state.Reason != layoutReasonRestarting {
			t.Errorf("%s reports %+v, want Restarting", name, state)
		}
	}
}

// A Sink that the pass fails to read must not be treated as a Sink whose
// spec.layout was removed, because that would revert its layout. The
// pass writes nothing and reports the layouts in the drop-in.
func TestASinkThatCannotBeReadChangesNoLayout(t *testing.T) {
	operator, endpoints := layoutReconciler(t)
	declared := operator.declared
	receiver := eldWith(t, 8, fiveOne|rearCenterPair)
	endpoints[0], _ = layoutEndpoints(t, &receiver)
	operator.control = &endpointControl{
		client:  testClient(t, failingAPI()),
		machine: "node-1",
	}

	states := operator.reconcileLayouts(endpoints, pwGraph{})
	if operator.declared != declared {
		t.Errorf("the declaration changed while the Sinks could not be read, want no change")
	}
	if state := states[endpoints[0].Name()]; !state.Applied || state.Layout.source() != layoutNone {
		t.Errorf("the HDMI sink reports %+v, want its declared layout", state)
	}
}

// A drop-in that does not parse leaves nothing to compare the
// layouts against, so the pass returns no layout state.
func TestADeclarationThatDoesNotParseReportsNoLayout(t *testing.T) {
	operator, endpoints := layoutReconciler(t)
	operator.declared = "# no objects\n"
	if states := operator.reconcileLayouts(endpoints, pwGraph{}); states != nil {
		t.Errorf("reconcileLayouts returned %+v, want nil", states)
	}
}

func TestGraphReadFailuresCountOnlyAfterTheLayoutRestartGrace(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	operator := testReconciler(t, api, failingGraph, "pcmC0D0p")
	operator.restartRequested = time.Now()

	for pass := 0; pass < maxFailedGraphReads+1; pass++ {
		if err := operator.reconcile(context.Background()); err != nil {
			t.Fatalf("pass %d during the restart stopped the operator: %v", pass, err)
		}
	}
	if operator.failedGraphReads != 0 {
		t.Errorf("the restart counted %d failures, want 0", operator.failedGraphReads)
	}
	operator.restartRequested = time.Now().Add(-layoutRestartGrace - time.Second)
	if err := operator.reconcile(context.Background()); err != nil {
		t.Fatalf("reconciling after the grace: %v", err)
	}
	if operator.failedGraphReads != 1 {
		t.Errorf("after the grace, the failure count is %d, want 1", operator.failedGraphReads)
	}
}

func TestPlayingReadsLinksOnPublishedNodesAlone(t *testing.T) {
	hdmi, _ := layoutEndpoints(t, nil)
	cases := []struct {
		name  string
		graph pwGraph
		want  bool
	}{
		{"a stream on a card's sink", playingGraph(hdmi), true},
		{"a stream on a speaker", pwGraph{
			Speakers: map[string]bluezSink{"7c:66:ef:01:23:45": {NodeID: 90}},
			Linked:   map[int]bool{90: true},
		}, true},
		{"a link between two nodes nobody publishes", pwGraph{
			Nodes:  map[nodeAddress]pwNode{hdmi.graphAddress(): {ID: 42}},
			Linked: map[int]bool{10: true, 11: true},
		}, false},
		{"no link", pwGraph{Nodes: map[nodeAddress]pwNode{hdmi.graphAddress(): {ID: 42}}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.graph.playing(); got != c.want {
				t.Errorf("playing() = %v, want %v", got, c.want)
			}
		})
	}
}

// failingAPI returns a handler that responds 403 Forbidden to every
// request, as the API server does for a ServiceAccount that has lost its
// permission to read Sinks.
func failingAPI() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "sinks.audio.liken.sh is forbidden", http.StatusForbidden)
	})
}
