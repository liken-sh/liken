package main

// These tests cover the volume asks the media operator writes into a
// Sink's status.session: each new ask applies once, an ask found in the
// first pass applies never, a burst applies its newest ask, a pass
// does not undo an ask, and the loop applies an ask without the
// settle window.

import (
	"context"
	"testing"
	"time"
)

// ask writes a volume ask into a Sink the fixture holds, as the media
// operator's apply does.
func ask(api *endpointAPI, name string, level int, at string) {
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

// startedControl is a controller whose first pass over the lab graph
// has run, so every Sink is read and every ask so far is recorded.
func startedControl(t *testing.T, api *endpointAPI, record *writeRecord, graph pwGraph) *endpointControl {
	t.Helper()
	control := testEndpointControl(t, api, record)
	if err := control.pass(context.Background(), labEndpoints(), testSpeakers(), graph, nil); err != nil {
		t.Fatal(err)
	}
	return control
}

func TestAnAskAppliesOnce(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := startedControl(t, api, record, labGraph())
	ctx := context.Background()

	ask(api, testAnalogName, 30, firstAsk)
	control.applyAsks(ctx)
	control.applyAsks(ctx)

	if len(record.levels) != 1 || *record.levels[0].Volume != 30 || *record.levels[0].Mute {
		t.Errorf("the ask wrote %+v, want 30 percent and no mute once", record.levels)
	}
}

// The speaker's level lives on its Route, and the ask takes the same
// write a declared level takes there.
func TestAnAskOnASpeakerLandsOnItsRoute(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := startedControl(t, api, record, turnedDownGraph())

	ask(api, testSpeakerName, 55, firstAsk)
	control.applyAsks(context.Background())

	if record.route == nil || len(record.levels) != 1 || *record.levels[0].Volume != 55 {
		t.Errorf("the ask wrote %+v on the route %+v", record.levels, record.route)
	}
}

// An ask already on the Sink when the operator starts was applied by
// the last operator, or is older than the device's own level.
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
		t.Errorf("an ask from before the start wrote %+v", record.levels)
	}
}

// Asks that arrive before the loop applies one are one write, of the
// newest.
func TestABurstOfAsksAppliesTheNewest(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := startedControl(t, api, record, labGraph())

	ask(api, testAnalogName, 30, firstAsk)
	ask(api, testAnalogName, 25, secondAsk)
	control.applyAsks(context.Background())

	if len(record.levels) != 1 || *record.levels[0].Volume != 25 {
		t.Errorf("the burst wrote %+v, want 25 percent once", record.levels)
	}
}

// The pass after an ask follows the level the ask set, and the
// declared level stays where it was.
func TestAPassDoesNotUndoAnAsk(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	api.sinks[testAnalogName] = &Sink{
		Metadata: EndpointMeta{Name: testAnalogName, ResourceVersion: api.nextVersion()},
		Spec:     SinkSpec{Volume: declaredLevel(40)},
	}
	control := startedControl(t, api, record, turnedDownGraph())
	ctx := context.Background()

	ask(api, testAnalogName, 70, firstAsk)
	control.applyAsks(ctx)
	asked := turnedDownGraph()
	address := nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink}
	node := asked.Nodes[address]
	node.Volumes = []float64{0.7, 0.7}
	asked.Nodes[address] = node
	if err := control.pass(ctx, labEndpoints(), testSpeakers(), asked, nil); err != nil {
		t.Fatal(err)
	}

	if len(record.levels) != 1 || *record.levels[0].Volume != 70 {
		t.Errorf("the ask and the pass wrote %+v, want the ask's 70 percent alone", record.levels)
	}
}

// An idle node announces no change, so the level an ask set is what
// status.observed reports for it until the node runs.
func TestAnIdleNodeReportsTheLevelAnAskSet(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := startedControl(t, api, record, suspendedGraph())
	ctx := context.Background()

	ask(api, testAnalogName, 30, firstAsk)
	control.applyAsks(ctx)
	if err := control.pass(ctx, labEndpoints(), testSpeakers(), suspendedGraph(), nil); err != nil {
		t.Fatal(err)
	}

	observed := api.sinks[testAnalogName].Status.Observed
	if observed == nil || observed.Volume == nil || *observed.Volume != 30 {
		t.Errorf("observed = %+v, want the ask's 30 percent", observed)
	}
	if len(record.levels) != 1 {
		t.Errorf("the pass after the ask wrote %+v again", record.levels[1:])
	}
}

// staleGraph is the lab graph with the analog jack's node suspended
// at the level it last ran at, which it keeps printing after a write.
func staleGraph(level float64) pwGraph {
	graph := labGraph()
	address := nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink}
	node := graph.Nodes[address]
	node.Volumes, node.Suspended = []float64{level, level}, true
	graph.Nodes[address] = node
	return graph
}

// runningAt is the lab graph with the analog jack's node running at a
// level.
func runningAt(level float64) pwGraph {
	graph := labGraph()
	address := nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink}
	node := graph.Nodes[address]
	node.Volumes = []float64{level, level}
	graph.Nodes[address] = node
	return graph
}

// A suspended node that keeps printing the level it last ran at
// reports the level an ask set, through the passes that follow. Once
// the node runs, it reports its own level, and when it suspends again
// it reports the level it last ran at, not the older ask.
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

	ask(api, testAnalogName, 70, firstAsk)
	control.applyAsks(ctx)
	if err := control.pass(ctx, labEndpoints(), testSpeakers(), staleGraph(1), nil); err != nil {
		t.Fatal(err)
	}
	if got := observed(); got != 70 {
		t.Errorf("after the ask, the suspended node reports %d percent, want 70", got)
	}

	if err := control.pass(ctx, labEndpoints(), testSpeakers(), runningAt(0.4), nil); err != nil {
		t.Fatal(err)
	}
	if got := observed(); got != 40 {
		t.Errorf("the running node reports %d percent, want its own 40", got)
	}

	if err := control.pass(ctx, labEndpoints(), testSpeakers(), staleGraph(0.4), nil); err != nil {
		t.Fatal(err)
	}
	if got := observed(); got != 40 {
		t.Errorf("suspended again, the node reports %d percent, want the 40 it ran at", got)
	}
}

// The loop applies an ask as it arrives. The settle window holds back
// the pass, and an ask that waited for it would apply 1.5 s late.
// The level goes into status.observed at once, well inside the window,
// and the loop then wakes a pass.
func TestTheLoopAppliesAnAskWithoutTheSettleWindow(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := startedControl(t, api, record, labGraph())
	written := make(chan levelWrite, 1)
	control.setLevel = func(_ context.Context, _ pwNode, level levelWrite) error {
		written <- level
		return nil
	}
	asks := make(chan struct{}, 1)
	poked := make(chan struct{}, 1)
	operator := &reconciler{control: control, asks: asks, poke: func() { poked <- struct{}{} }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, operator, make(chan struct{})) }()

	asked := time.Now()
	ask(api, testAnalogName, 30, firstAsk)
	asks <- struct{}{}

	select {
	case level := <-written:
		if *level.Volume != 30 {
			t.Errorf("the loop wrote %+v, want 30 percent", level)
		}
	case <-time.After(settleWindow):
		t.Fatal("the ask did not apply within the settle window")
	}
	select {
	case <-poked:
	case <-time.After(settleWindow):
		t.Error("the loop woke no pass to report the level")
	}
	// The ask's level is reported before the loop wakes the pass, with
	// no pass run at all.
	if elapsed := time.Since(asked); elapsed > settleWindow/3 {
		t.Errorf("the level was reported %v after the ask", elapsed)
	}
	api.mutex.Lock()
	observed := api.sinks[testAnalogName].Status.Observed
	api.mutex.Unlock()
	if observed == nil || observed.Volume == nil || *observed.Volume != 30 || observed.Mute == nil || *observed.Mute {
		t.Errorf("observed = %+v, want the ask's 30 percent, not muted", observed)
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("the loop ended with %v", err)
	}
}
