package main

// These tests run whole passes against a declared spec.volume and
// spec.mute.

import (
	"context"
	"reflect"
	"testing"
)

// A declaration that a person writes reaches the node on the next
// pass, whether or not the endpoint is claimed.
func TestPassCarriesADeclaredLevel(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := testEndpointControl(t, api, record)
	ctx := context.Background()
	if err := control.pass(ctx, labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}

	api.sinks[testAnalogName].Spec = SinkSpec{Volume: declaredLevel(25), Mute: pointerTo(true)}
	if err := control.pass(ctx, labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	if record.node == nil || record.level.Volume == nil || *record.level.Volume != 25 || record.level.Mute == nil || !*record.level.Mute {
		t.Fatalf("the node received %+v, want volume 25 and mute on", record)
	}
}

// suspendedGraph is the lab graph with the analog jack's node idle. An
// idle node reports no volumes.
func suspendedGraph() pwGraph {
	graph := labGraph()
	address := nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink}
	idle := graph.Nodes[address]
	idle.Volumes = nil
	graph.Nodes[address] = idle
	return graph
}

// An operator that starts while the node is idle cannot read the
// node's level to compare with the declaration, which the earlier
// process may already have written. So the start writes nothing, and a
// later change to the declaration is written.
func TestAStartWritesNothingToASuspendedNodeUntilTheDeclarationChanges(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := testEndpointControl(t, api, record)
	api.sinks[testAnalogName] = &Sink{
		Metadata: EndpointMeta{Name: testAnalogName},
		Spec:     SinkSpec{Volume: declaredLevel(40)},
	}
	ranBefore(t, api, suspendedGraph())
	ctx := context.Background()

	for range 2 {
		if err := control.pass(ctx, labEndpoints(), nil, suspendedGraph(), nil); err != nil {
			t.Fatalf("running the pass: %v", err)
		}
	}
	if record.node != nil {
		t.Fatalf("a start wrote %+v to a suspended node, want no write", record.level)
	}

	api.sinks[testAnalogName].Spec.Volume = declaredLevel(25)
	if err := control.pass(ctx, labEndpoints(), nil, suspendedGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	if record.node == nil || record.level.Volume == nil || *record.level.Volume != 25 {
		t.Fatalf("the suspended node received %+v, want volume 25", record)
	}
}

// A level that changes at the device after the declaration was applied
// is reported in status.observed and not written over. A button on the
// speaker or a VolumeAsk changes the level this way, and writing the
// declaration back would undo the change.
func TestADeviceThatDriftsIsFollowedAndNotUndone(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := testEndpointControl(t, api, record)
	ctx := context.Background()
	if err := control.pass(ctx, labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	api.sinks[testAnalogName].Spec.Volume = declaredLevel(40)
	if err := control.pass(ctx, labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	written := len(record.levels)

	if err := control.pass(ctx, labEndpoints(), nil, turnedDownGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	address := nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink}
	drifted := turnedDownGraph()
	louder := drifted.Nodes[address]
	louder.Volumes = []float64{0.7, 0.7}
	drifted.Nodes[address] = louder
	if err := control.pass(ctx, labEndpoints(), nil, drifted, nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}

	if got := record.levels[written:]; len(got) != 0 {
		t.Errorf("the passes wrote %+v over the device's own level, want no write", got)
	}
	if observed := api.sinks[testAnalogName].Status.Observed; observed == nil || *observed.Volume != 70 {
		t.Errorf("observed = %+v, want the device's 70 percent", observed)
	}
}

func TestAChangedDeclarationIsWritten(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := testEndpointControl(t, api, record)
	api.sinks[testAnalogName] = &Sink{
		Metadata: EndpointMeta{Name: testAnalogName},
		Spec:     SinkSpec{Volume: declaredLevel(40)},
	}
	ctx := context.Background()
	if err := control.pass(ctx, labEndpoints(), nil, turnedDownGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}

	api.sinks[testAnalogName].Spec.Volume = declaredLevel(25)
	if err := control.pass(ctx, labEndpoints(), nil, turnedDownGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	if len(record.levels) != 1 || *record.levels[0].Volume != 25 {
		t.Fatalf("the node received %+v, want one write of volume 25", record.levels)
	}
}

// A speaker that reconnects gets a new PipeWire node, and the operator
// writes the declared level to it, whatever level the speaker reports.
func TestAReconnectTakesTheDeclaredLevel(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := testEndpointControl(t, api, record)
	api.sinks[testSpeakerName] = &Sink{
		Metadata: EndpointMeta{Name: testSpeakerName},
		Spec:     SinkSpec{Volume: declaredLevel(40)},
	}
	ctx := context.Background()
	graph := turnedDownGraph()
	ranBefore(t, api, graph)
	if err := control.pass(ctx, labEndpoints(), testSpeakers(), graph, nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	if len(record.levels) != 0 {
		t.Fatalf("a start wrote %+v to the speaker, want no write", record.levels)
	}

	speaker := graph.Speakers[testSpeakerAddress]
	speaker.NodeID = 91
	graph.Speakers[testSpeakerAddress] = speaker
	if err := control.pass(ctx, labEndpoints(), testSpeakers(), graph, nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	if record.route == nil || len(record.levels) != 1 || *record.levels[0].Volume != 40 {
		t.Fatalf("the reconnected speaker received %+v on the route %+v, want one write of volume 40", record.levels, record.route)
	}
}

// A running node whose level matches the declaration counts as
// applied, so the operator writes nothing when the node goes idle and
// stops reporting its level.
func TestARunningNodeThatMatchesTakesNoWriteWhenItGoesIdle(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := testEndpointControl(t, api, record)
	api.sinks[testAnalogName] = &Sink{
		Metadata: EndpointMeta{Name: testAnalogName},
		Spec:     SinkSpec{Volume: declaredLevel(40)},
	}
	ctx := context.Background()

	for _, graph := range []pwGraph{turnedDownGraph(), suspendedGraph()} {
		if err := control.pass(ctx, labEndpoints(), nil, graph, nil); err != nil {
			t.Fatalf("running the pass: %v", err)
		}
	}
	if record.node != nil {
		t.Errorf("a node that matched the declaration was written %+v when it went idle, want no write", record.level)
	}
}

// mutedGraph is the lab graph after a declared mute was applied to the
// analog jack's node.
func mutedGraph() pwGraph {
	graph := labGraph()
	address := nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink}
	muted := graph.Nodes[address]
	muted.Mute = true
	graph.Nodes[address] = muted
	return graph
}

// After a reboot, PipeWire creates every node again unmuted at its
// configured level. It can give a node the same object id as before
// the reboot, but it reports a new cookie, so the operator writes the
// declared mute to the new node.
func TestARebootedMachineTakesTheDeclaredMute(t *testing.T) {
	api := newEndpointAPI()
	api.sinks[testAnalogName] = &Sink{
		Metadata: EndpointMeta{Name: testAnalogName},
		Spec:     SinkSpec{Mute: pointerTo(true)},
	}
	ranBefore(t, api, mutedGraph())

	record := &writeRecord{}
	after := testEndpointControl(t, api, record)
	booted := labGraph()
	booted.Cookie = labCookie + 1
	if err := after.pass(context.Background(), labEndpoints(), nil, booted, nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	if record.node == nil || record.level.Mute == nil || !*record.level.Mute {
		t.Errorf("the node PipeWire built at the boot took %+v, want the declared mute", record.level)
	}
}

// status.applied records the node that the declaration was applied
// to, and the declaration, so that an operator that starts later can
// tell that node from one that PipeWire created since.
func TestThePassPublishesTheNodeItApplied(t *testing.T) {
	api := newEndpointAPI()
	api.sinks[testAnalogName] = &Sink{
		Metadata: EndpointMeta{Name: testAnalogName},
		Spec:     SinkSpec{Volume: declaredLevel(40), Mute: pointerTo(true)},
	}
	control := testEndpointControl(t, api, &writeRecord{})
	if err := control.pass(context.Background(), labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}

	want := EndpointApplied{PipeWireCookie: labCookie, Node: 42, Volume: pointerTo(40), Mute: pointerTo(true)}
	if got := api.sinks[testAnalogName].Status.Applied; got == nil || !reflect.DeepEqual(*got, want) {
		t.Errorf("status.applied = %+v, want %+v", got, want)
	}
}

// A declaration that a person changes while the operator is down
// differs from the one in status.applied, so the start writes it to the
// running node.
func TestAStartWritesADeclarationChangedWhileTheOperatorWasDown(t *testing.T) {
	api := newEndpointAPI()
	api.sinks[testAnalogName] = &Sink{
		Metadata: EndpointMeta{Name: testAnalogName},
		Spec:     SinkSpec{Volume: declaredLevel(40)},
	}
	ranBefore(t, api, turnedDownGraph())

	api.sinks[testAnalogName].Spec.Mute = pointerTo(true)
	record := &writeRecord{}
	control := testEndpointControl(t, api, record)
	if err := control.pass(context.Background(), labEndpoints(), nil, turnedDownGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	if len(record.levels) != 1 || record.levels[0].Mute == nil || !*record.levels[0].Mute {
		t.Errorf("the start wrote %+v, want the changed declaration once", record.levels)
	}
}

// When PipeWire restarts inside its container while the operator runs,
// for example to apply a new layout, it can create the node again
// under the same id. Its new cookie marks the node as new, so the
// operator writes the declaration to it.
func TestAPipeWireRestartedInPlaceTakesTheDeclaredMute(t *testing.T) {
	api := newEndpointAPI()
	api.sinks[testAnalogName] = &Sink{
		Metadata: EndpointMeta{Name: testAnalogName},
		Spec:     SinkSpec{Mute: pointerTo(true)},
	}
	record := &writeRecord{}
	control := testEndpointControl(t, api, record)
	ctx := context.Background()
	if err := control.pass(ctx, labEndpoints(), nil, mutedGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	written := len(record.levels)

	restarted := labGraph()
	restarted.Cookie = labCookie + 1
	if err := control.pass(ctx, labEndpoints(), nil, restarted, nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	if got := record.levels[written:]; len(got) != 1 || got[0].Mute == nil || !*got[0].Mute {
		t.Errorf("the node PipeWire built again took %+v, want the declared mute", got)
	}
}
