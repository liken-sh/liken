package main

import (
	"context"
	"testing"
	"time"
)

// writeVolumeAsk stores a volume request in a Sink of the fake API
// server, as the media operator does.
func writeVolumeAsk(api *endpointAPI, name string, level int, at string) {
	api.mutex.Lock()
	defer api.mutex.Unlock()
	sink := api.sinks[name]
	sink.Status.Session = &SinkSession{Player: "media/den", VolumeAsk: &VolumeAsk{Level: level, At: at}}
	sink.Metadata.ResourceVersion = api.nextVersion()
}

const (
	firstAsk  = "2026-10-04T12:15:25.164Z"
	secondAsk = "2026-10-04T12:15:25.264Z"
)

// startedControl returns a controller that has run its first pass over
// the graph, as an operator does when it restarts while PipeWire is
// running. Every Sink has been read, and every request so far is
// recorded.
func startedControl(t *testing.T, api *endpointAPI, record *writeRecord, graph pwGraph) *endpointControl {
	t.Helper()
	ranBefore(t, api, graph)
	control := testEndpointControl(t, api, record)
	if err := control.pass(context.Background(), labEndpoints(), testSpeakers(), graph, nil); err != nil {
		t.Fatalf("running the first pass: %v", err)
	}
	return control
}

func TestAnAskAppliesOnce(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := startedControl(t, api, record, labGraph())
	ctx := context.Background()

	writeVolumeAsk(api, testAnalogName, 30, firstAsk)
	control.applyAsks(ctx)
	control.applyAsks(ctx)

	if len(record.levels) != 1 || *record.levels[0].Volume != 30 || *record.levels[0].Mute {
		t.Errorf("the request wrote %+v, want 30 percent and no mute once", record.levels)
	}
}

// A request for a speaker is written to its Route, as a declared level
// is.
func TestAnAskOnASpeakerIsWrittenToItsRoute(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := startedControl(t, api, record, turnedDownGraph())

	writeVolumeAsk(api, testSpeakerName, 55, firstAsk)
	control.applyAsks(context.Background())

	if record.route == nil || len(record.levels) != 1 || *record.levels[0].Volume != 55 {
		t.Errorf("the request wrote %+v on the route %+v, want 55 percent once on the speaker's Route", record.levels, record.route)
	}
}

// A request already on the Sink when the operator starts is not applied.
func TestAnAskFoundInTheFirstPassIsNotApplied(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	api.sinks[testAnalogName] = &Sink{
		Metadata: EndpointMeta{Name: testAnalogName, ResourceVersion: api.nextVersion()},
		Status:   SinkStatus{EndpointStatus: EndpointStatus{Node: "liken-1"}, Session: denSession()},
	}
	control := startedControl(t, api, record, labGraph())

	control.applyAsks(context.Background())

	if len(record.levels) != 0 {
		t.Errorf("a request from before the start wrote %+v, want no write", record.levels)
	}
}

func TestABurstOfAsksAppliesTheNewest(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := startedControl(t, api, record, labGraph())

	writeVolumeAsk(api, testAnalogName, 30, firstAsk)
	writeVolumeAsk(api, testAnalogName, 25, secondAsk)
	control.applyAsks(context.Background())

	if len(record.levels) != 1 || *record.levels[0].Volume != 25 {
		t.Errorf("the burst wrote %+v, want 25 percent once", record.levels)
	}
}

// A pass after a request does not write the declared level back over the
// level that the request set.
func TestAPassDoesNotUndoAnAsk(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	api.sinks[testAnalogName] = &Sink{
		Metadata: EndpointMeta{Name: testAnalogName, ResourceVersion: api.nextVersion()},
		Spec:     SinkSpec{Volume: declaredLevel(40)},
	}
	control := startedControl(t, api, record, turnedDownGraph())
	ctx := context.Background()

	writeVolumeAsk(api, testAnalogName, 70, firstAsk)
	control.applyAsks(ctx)
	asked := turnedDownGraph()
	address := nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink}
	node := asked.Nodes[address]
	node.Volumes = []float64{0.7, 0.7}
	asked.Nodes[address] = node
	if err := control.pass(ctx, labEndpoints(), testSpeakers(), asked, nil); err != nil {
		t.Fatalf("running the pass after the request: %v", err)
	}

	if len(record.levels) != 1 || *record.levels[0].Volume != 70 {
		t.Errorf("the request and the pass wrote %+v, want the request's 70 percent alone", record.levels)
	}
}

// An idle node announces no change in the graph, so status.observed
// reports the level that the request set until the node runs.
func TestAnIdleNodeReportsTheLevelAnAskSet(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := startedControl(t, api, record, suspendedGraph())
	ctx := context.Background()

	writeVolumeAsk(api, testAnalogName, 30, firstAsk)
	control.applyAsks(ctx)
	if err := control.pass(ctx, labEndpoints(), testSpeakers(), suspendedGraph(), nil); err != nil {
		t.Fatalf("running the pass after the request: %v", err)
	}

	observed := api.sinks[testAnalogName].Status.Observed
	if observed == nil || observed.Volume == nil || *observed.Volume != 30 {
		t.Errorf("status.observed is %+v, want the request's 30 percent", observed)
	}
	if len(record.levels) != 1 {
		t.Errorf("the pass after the request wrote %+v again, want no second write", record.levels[1:])
	}
}

// staleGraph is the lab graph with the analog jack's node suspended.
// The node prints the level it last ran at, even after a write.
func staleGraph(level float64) pwGraph {
	graph := labGraph()
	address := nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink}
	node := graph.Nodes[address]
	node.Volumes, node.Suspended = []float64{level, level}, true
	graph.Nodes[address] = node
	return graph
}

func runningAt(level float64) pwGraph {
	graph := labGraph()
	address := nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink}
	node := graph.Nodes[address]
	node.Volumes = []float64{level, level}
	graph.Nodes[address] = node
	return graph
}

// A suspended node keeps printing the level it last ran at, so
// status.observed reports the level that the request set through the
// passes that follow. When the node runs, status.observed reports the
// node's own level. When the node suspends again, it reports the level it
// last ran at and not the older request.
func TestASuspendedNodeThatPrintsAStaleLevelReportsTheAsk(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := startedControl(t, api, record, staleGraph(1))
	ctx := context.Background()
	observed := func() int {
		t.Helper()
		volume := api.sinks[testAnalogName].Status.Observed.Volume
		if volume == nil {
			t.Fatal("observed reports no volume")
		}
		return *volume
	}

	writeVolumeAsk(api, testAnalogName, 70, firstAsk)
	control.applyAsks(ctx)
	if err := control.pass(ctx, labEndpoints(), testSpeakers(), staleGraph(1), nil); err != nil {
		t.Fatalf("running the pass after the request: %v", err)
	}
	if got := observed(); got != 70 {
		t.Errorf("after the request, the suspended node reports %d percent, want 70", got)
	}

	if err := control.pass(ctx, labEndpoints(), testSpeakers(), runningAt(0.4), nil); err != nil {
		t.Fatalf("running the pass with the node running: %v", err)
	}
	if got := observed(); got != 40 {
		t.Errorf("the running node reports %d percent, want its own 40", got)
	}

	if err := control.pass(ctx, labEndpoints(), testSpeakers(), staleGraph(0.4), nil); err != nil {
		t.Fatalf("running the pass with the node suspended again: %v", err)
	}
	if got := observed(); got != 40 {
		t.Errorf("suspended again, the node reports %d percent, want the 40 it ran at", got)
	}
}

// The loop applies a request when it arrives and does not wait for
// settleWindow. It writes the level into status.observed well inside
// the window and then requests a pass.
func TestTheLoopAppliesAnAskWithoutTheSettleWindow(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := startedControl(t, api, record, labGraph())
	written := make(chan levelWrite, 1)
	control.setLevel = func(_ context.Context, _ pwNode, level levelWrite) error {
		written <- level
		return nil
	}
	askWakes := make(chan struct{}, 1)
	passRequested := make(chan struct{}, 1)
	operator := &reconciler{control: control, volumeAsks: askWakes, requestPass: func() { passRequested <- struct{}{} }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, operator, make(chan struct{})) }()

	asked := time.Now()
	writeVolumeAsk(api, testAnalogName, 30, firstAsk)
	askWakes <- struct{}{}

	select {
	case level := <-written:
		if *level.Volume != 30 {
			t.Errorf("the loop wrote %+v, want 30 percent", level)
		}
	case <-time.After(settleWindow):
		t.Fatal("the request did not apply within the settle window")
	}
	select {
	case <-passRequested:
	case <-time.After(settleWindow):
		t.Error("the loop requested no pass to report the level")
	}
	// The level is in status.observed before the loop requests a pass,
	// and no pass has run.
	if elapsed := time.Since(asked); elapsed > settleWindow/3 {
		t.Errorf("the level was reported %v after the request, want under %v", elapsed, settleWindow/3)
	}
	api.mutex.Lock()
	observed := api.sinks[testAnalogName].Status.Observed
	api.mutex.Unlock()
	if observed == nil || observed.Volume == nil || *observed.Volume != 30 || observed.Mute == nil || *observed.Mute {
		t.Errorf("status.observed is %+v, want the request's 30 percent, not muted", observed)
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("the loop returned %v, want nil", err)
	}
}
