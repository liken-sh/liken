package main

// These tests cover a declared level and mute through whole passes:
// the declaration reaches the endpoint when it changes and when the
// node appears, a start writes nothing, and a level that moves at the
// device is followed and not written back.

import (
	"context"
	"testing"
)

// A declaration a person writes reaches the endpoint on the pass that
// reads it, under a claim or not.
func TestPassCarriesADeclaredLevel(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := testEndpointControl(t, api, record)
	ctx := context.Background()
	if err := control.pass(ctx, labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatal(err)
	}

	api.sinks[testAnalogName].Spec = SinkSpec{Volume: declaredLevel(25), Mute: pointerTo(true)}
	if err := control.pass(ctx, labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatal(err)
	}
	if record.node == nil || record.level.Volume == nil || *record.level.Volume != 25 || record.level.Mute == nil || !*record.level.Mute {
		t.Fatalf("the declaration reached the node as %+v", record)
	}
}

// suspendedGraph is the lab graph with the analog jack's node idle:
// it stands and runs no stream, so it reports no levels.
func suspendedGraph() pwGraph {
	graph := labGraph()
	address := nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink}
	idle := graph.Nodes[address]
	idle.Volumes = nil
	graph.Nodes[address] = idle
	return graph
}

// An operator that starts again finds an idle node it cannot read,
// under a declaration it may have written before the restart. The
// start writes nothing to it, and a person's later change of the
// declaration still reaches it.
func TestAStartWritesNothingToASuspendedNodeUntilTheDeclarationChanges(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := testEndpointControl(t, api, record)
	api.sinks[testAnalogName] = &Sink{
		Metadata: EndpointMeta{Name: testAnalogName},
		Spec:     SinkSpec{Volume: declaredLevel(40)},
	}
	ctx := context.Background()

	for range 2 {
		if err := control.pass(ctx, labEndpoints(), nil, suspendedGraph(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if record.node != nil {
		t.Fatalf("a start wrote %+v to a suspended node", record.level)
	}

	api.sinks[testAnalogName].Spec.Volume = declaredLevel(25)
	if err := control.pass(ctx, labEndpoints(), nil, suspendedGraph(), nil); err != nil {
		t.Fatal(err)
	}
	if record.node == nil || record.level.Volume == nil || *record.level.Volume != 25 {
		t.Fatalf("a changed declaration reached the suspended node as %+v", record)
	}
}

// A level that moves at the device after the declaration was applied
// is followed: the pass reports it and writes nothing. A speaker's own
// button and a volume ask both move the level this way, and an
// operator that wrote the declaration back would undo each of them.
func TestADeviceThatDriftsIsFollowedAndNotUndone(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := testEndpointControl(t, api, record)
	ctx := context.Background()
	if err := control.pass(ctx, labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatal(err)
	}
	api.sinks[testAnalogName].Spec.Volume = declaredLevel(40)
	if err := control.pass(ctx, labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatal(err)
	}
	written := len(record.levels)

	if err := control.pass(ctx, labEndpoints(), nil, turnedDownGraph(), nil); err != nil {
		t.Fatal(err)
	}
	address := nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink}
	drifted := turnedDownGraph()
	louder := drifted.Nodes[address]
	louder.Volumes = []float64{0.7, 0.7}
	drifted.Nodes[address] = louder
	if err := control.pass(ctx, labEndpoints(), nil, drifted, nil); err != nil {
		t.Fatal(err)
	}

	if got := record.levels[written:]; len(got) != 0 {
		t.Errorf("the passes wrote %+v over the device's own level", got)
	}
	if observed := api.sinks[testAnalogName].Status.Observed; observed == nil || *observed.Volume != 70 {
		t.Errorf("observed = %+v, want the device's 70 percent", observed)
	}
}

// A changed declaration is written to a node that runs at another
// level.
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
		t.Fatal(err)
	}

	api.sinks[testAnalogName].Spec.Volume = declaredLevel(25)
	if err := control.pass(ctx, labEndpoints(), nil, turnedDownGraph(), nil); err != nil {
		t.Fatal(err)
	}
	if len(record.levels) != 1 || *record.levels[0].Volume != 25 {
		t.Fatalf("the changed declaration reached the node as %+v", record.levels)
	}
}

// A speaker that reconnects is a node PipeWire built again, and it
// takes the declared level, whatever level the speaker comes back at.
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
	if err := control.pass(ctx, labEndpoints(), testSpeakers(), graph, nil); err != nil {
		t.Fatal(err)
	}
	if len(record.levels) != 0 {
		t.Fatalf("a start wrote %+v to the speaker", record.levels)
	}

	speaker := graph.Speakers[testSpeakerAddress]
	speaker.NodeID = 91
	graph.Speakers[testSpeakerAddress] = speaker
	if err := control.pass(ctx, labEndpoints(), testSpeakers(), graph, nil); err != nil {
		t.Fatal(err)
	}
	if record.route == nil || len(record.levels) != 1 || *record.levels[0].Volume != 40 {
		t.Fatalf("the reconnected speaker took %+v on the route %+v", record.levels, record.route)
	}
}

// A running node that matches the declaration is judged to hold it,
// so the node takes no write when it goes idle and stops reporting
// its level.
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
			t.Fatal(err)
		}
	}
	if record.node != nil {
		t.Errorf("a node that matched the declaration was written %+v when it went idle", record.level)
	}
}
