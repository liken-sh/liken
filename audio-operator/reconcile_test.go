package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func failingGraph(context.Context) (pwGraph, error) {
	return pwGraph{}, errors.New("running pw-dump: no such file or directory")
}

// testReconciler builds a reconciler over a fake API server and the
// device files in /dev/snd that the test names.
func testReconciler(t *testing.T, api *slicePublishFixture, graph func(context.Context) (pwGraph, error), nodes ...string) *reconciler {
	t.Helper()
	sndDir = sndDirWith(t, nodes...)
	specDirectory(t)
	// declared starts as the drop-in that the init container writes for
	// these devices, so a pass finds no mismatch with the card.
	outputs, err := readEndpoints()
	if err != nil {
		t.Fatalf("reading the endpoints: %v", err)
	}
	return &reconciler{
		client:   testClient(t, api.handler(t)),
		nodeName: "liken-1",
		owner:    testOwner(),
		graph:    graph,
		declared: nodeConfig(outputs, nil),
	}
}

// A single failed graph read leaves the published slice alone. A
// slice that tainted every output would evict every pod that uses one
// for a pw-dump that timed out once.
func TestReconcileSkipsTheWriteWhenPipeWireDoesNotRespond(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	operator := testReconciler(t, api, failingGraph, "pcmC0D0p", "pcmC0D3p")

	if err := operator.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile after one failed graph read = %v, want nil", err)
	}
	if len(api.requests) != 0 {
		t.Errorf("a failed graph read sent %v to the API server, want no request", api.requests)
	}
}

// While PipeWire does not respond, the published slice lists every
// output as playable, and every prepare call fails. So after
// maxFailedGraphReads failed reads in a row, reconcile returns an
// error, and the operator exits for the kubelet to restart it.
func TestReconcileReturnsAnErrorAfterARunOfFailedGraphReads(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	operator := testReconciler(t, api, failingGraph, "pcmC0D0p")

	for pass := 1; pass < maxFailedGraphReads; pass++ {
		if err := operator.reconcile(context.Background()); err != nil {
			t.Fatalf("pass %d of %d returned %v, want nil", pass, maxFailedGraphReads, err)
		}
	}
	if err := operator.reconcile(context.Background()); err == nil {
		t.Fatalf("reconcile after %d failed graph reads in a row returned nil, want an error", maxFailedGraphReads)
	}
}

// One good read clears the count, so a pw-dump that fails now and
// then never adds up to a restart.
func TestReconcileClearsTheFailureCountAfterAGoodRead(t *testing.T) {
	api := &slicePublishFixture{}
	sinks := failingGraph
	operator := testReconciler(t, api, func(ctx context.Context) (pwGraph, error) {
		return sinks(ctx)
	}, "pcmC0D0p")

	for pass := 1; pass < maxFailedGraphReads; pass++ {
		if err := operator.reconcile(context.Background()); err != nil {
			t.Fatalf("pass %d returned %v, want nil", pass, err)
		}
	}
	sinks = staticGraph(outputGraph(map[pcmAddress]string{{Card: 0, PCM: 0}: "alsa_output.test"}))
	if err := operator.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile with a good read = %v, want nil", err)
	}
	sinks = failingGraph
	for pass := 1; pass < maxFailedGraphReads; pass++ {
		if err := operator.reconcile(context.Background()); err != nil {
			t.Fatalf("a failed read after a good one returned %v, want nil, because the good read clears the count", err)
		}
	}
}

// A card with no PCM device is never a real state of a machine where
// this operator runs, so the pass publishes nothing and does not read
// the graph.
func TestReconcileSkipsTheWriteWhenTheCardHasNoPCMDevice(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	read := false
	operator := testReconciler(t, api, func(context.Context) (pwGraph, error) {
		read = true
		return outputGraph(map[pcmAddress]string{}), nil
	}, "controlC0", "timer", "seq")

	if err := operator.reconcile(context.Background()); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	if len(api.requests) != 0 {
		t.Errorf("an empty enumeration sent %v to the API server, want no request", api.requests)
	}
	if read {
		t.Error("an empty enumeration read PipeWire's graph, want no read")
	}
}

func TestReconcilePublishesWhatItReads(t *testing.T) {
	api := &slicePublishFixture{}
	operator := testReconciler(t, api, staticGraph(outputGraph(
		map[pcmAddress]string{{Card: 0, PCM: 0}: "alsa_output.analog"})), "pcmC0D0p", "pcmC0D3p")

	if err := operator.reconcile(context.Background()); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	if api.created == nil {
		t.Fatal("the first pass created no slice, want one")
	}
	devices := api.created.Spec.Devices
	if len(devices) != 2 {
		t.Fatalf("devices = %+v, want two", devices)
	}
	if got := stringAttribute(t, devices[0], sinkAttribute); got != testAnalogName {
		t.Errorf("sink = %q, want %q", got, testAnalogName)
	}
	if len(devices[0].Taints) != 0 {
		t.Errorf("the output with a sink has the taints %+v, want none", devices[0].Taints)
	}
	noSink := []DeviceTaint{
		{Key: disconnectedTaint, Effect: "NoExecute"},
		{Key: noSinkTaint, Effect: "NoSchedule"},
	}
	if got := devices[1].Taints; !reflect.DeepEqual(got, noSink) {
		t.Errorf("the output with no sink node has %+v, want %+v", got, noSink)
	}
}

// PipeWire creates its nodes from the drop-in that the init container
// writes, so a PCM device that appears later gets no node from this
// PipeWire. Only a replacement pod declares the new set, so the
// operator keeps running and publishes the new output with the no-sink
// taint.
func TestReconcilePublishesWhenTheCardsPCMDevicesChange(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	operator := testReconciler(t, api, staticGraph(outputGraph(
		map[pcmAddress]string{{Card: 0, PCM: 0}: sinkNodeName(0, 0)})), "pcmC0D0p")

	sndDir = sndDirWith(t, "pcmC0D0p", "pcmC0D3p")
	if err := operator.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile after a new PCM device = %v, want nil", err)
	}
	if api.updated == nil {
		t.Fatal("a new PCM device left the slice unchanged, want an updated slice")
	}
	devices := api.updated.Spec.Devices
	if len(devices) != 2 {
		t.Fatalf("devices = %+v, want two", devices)
	}
	noSink := []DeviceTaint{
		{Key: disconnectedTaint, Effect: "NoExecute"},
		{Key: noSinkTaint, Effect: "NoSchedule"},
	}
	if got := devices[1].Taints; !reflect.DeepEqual(got, noSink) {
		t.Errorf("the undeclared output has %+v, want %+v", got, noSink)
	}
}

// A pass on a machine with a radio publishes the card's outputs and
// the paired speakers in one slice.
func TestReconcilePublishesTheSpeakersBluetoothdReports(t *testing.T) {
	api := &slicePublishFixture{}
	operator := testReconciler(t, api, staticGraph(pwGraph{
		Nodes: map[nodeAddress]pwNode{
			{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink}: {Name: sinkNodeName(0, 0)},
		},
		Speakers: map[string]bluezSink{
			testSpeakerAddress: {Node: testSpeakerNode, Codec: "sbc"},
		},
	}), "pcmC0D0p")
	operator.speakers = func() (map[string]speaker, error) { return testSpeakers(), nil }

	if err := operator.reconcile(context.Background()); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	devices := api.created.Spec.Devices
	if len(devices) != 2 {
		t.Fatalf("devices = %+v, want the output and the speaker", devices)
	}
	if devices[0].Name != testSpeakerName || devices[1].Name != testAnalogName {
		t.Fatalf("names = %q, %q, want %q, %q", devices[0].Name, devices[1].Name, testSpeakerName, testAnalogName)
	}
	if len(devices[0].Taints) != 0 {
		t.Errorf("a connected speaker with a node has the taints %+v, want none", devices[0].Taints)
	}
}

// When bluetoothd stops responding, the speakers are still paired, so
// they stay in the slice with taints that keep pods off them. A speaker
// removed from the slice would leave a claim that names it with no
// device.
func TestReconcileKeepsTheSpeakersWhenBluetoothdStopsResponding(t *testing.T) {
	api := &slicePublishFixture{}
	graph := pwGraph{
		Nodes: map[nodeAddress]pwNode{
			{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink}: {Name: sinkNodeName(0, 0)},
		},
		Speakers: map[string]bluezSink{
			testSpeakerAddress: {Node: testSpeakerNode, Codec: "sbc"},
		},
	}
	responding := true
	operator := testReconciler(t, api, func(context.Context) (pwGraph, error) {
		if responding {
			return graph, nil
		}
		return pwGraph{Nodes: graph.Nodes, Speakers: map[string]bluezSink{}}, nil
	}, "pcmC0D0p")
	operator.speakers = func() (map[string]speaker, error) {
		if responding {
			return testSpeakers(), nil
		}
		return nil, ErrNoAdapter
	}

	if err := operator.reconcile(context.Background()); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	api.existing = api.created

	responding = false
	if err := operator.reconcile(context.Background()); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	devices := api.updated.Spec.Devices
	if len(devices) != 2 {
		t.Fatalf("devices = %+v, want the output and the speaker", devices)
	}
	tainted := []DeviceTaint{
		{Key: disconnectedTaint, Effect: "NoExecute"},
		{Key: noSinkTaint, Effect: "NoSchedule"},
	}
	if got := devices[0].Taints; !reflect.DeepEqual(got, tainted) {
		t.Errorf("the speaker has %+v, want %+v", got, tainted)
	}
	if len(devices[1].Taints) != 0 {
		t.Errorf("the card's output has the taints %+v after the bus was lost, want none", devices[1].Taints)
	}
}

// When PipeWire dies, no output plays, so the slice must taint every
// output before the process exits. Without the taints, the pods that
// use the card keep a PipeWire socket that does not respond.
func TestTaintEverythingTaintsEveryOutput(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	operator := testReconciler(t, api, failingGraph, "pcmC0D0p", "pcmC0D3p")

	operator.taintEverything()

	if api.updated == nil {
		t.Fatal("taintEverything wrote no slice, want every output tainted")
	}
	for _, device := range api.updated.Spec.Devices {
		if len(device.Taints) != 2 {
			t.Errorf("%s has %+v, want the disconnected and no-sink taints",
				device.Name, device.Taints)
		}
	}
}

// wakesFor returns a channel with one wake buffered for each pass
// that the test wants run to start.
func wakesFor(passes int) <-chan struct{} {
	settled := make(chan struct{}, passes)
	for range passes {
		settled <- struct{}{}
	}
	return settled
}

// run taints every output and returns an error after
// maxFailedGraphReads failed graph reads in a row.
func TestRunTaintsAndStopsWhenPipeWireStopsResponding(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	operator := testReconciler(t, api, failingGraph, "pcmC0D0p", "pcmC0D3p")

	err := run(context.Background(), operator, wakesFor(maxFailedGraphReads))
	if err == nil {
		t.Fatal("run with a PipeWire that stopped responding returned nil, want an error")
	}
	if api.updated == nil {
		t.Fatal("run with a PipeWire that stopped responding wrote no slice, want every output tainted")
	}
	for _, device := range api.updated.Spec.Devices {
		if len(device.Taints) != 2 {
			t.Errorf("%s has %+v, want the disconnected and no-sink taints",
				device.Name, device.Taints)
		}
	}
}

// The previous operator's slice lists every output as playable, so
// when PipeWire never responds at startup, awaitPipeWire taints every
// output before the operator exits.
func TestAwaitPipeWireTaintsEveryOutputWhenItNeverResponds(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	operator := testReconciler(t, api, failingGraph, "pcmC0D0p", "pcmC0D3p")

	err := operator.awaitPipeWire(context.Background(), 50*time.Millisecond)
	if err == nil {
		t.Fatal("awaitPipeWire with a PipeWire that never responded returned nil, want an error")
	}
	if api.updated == nil {
		t.Fatal("awaitPipeWire with a PipeWire that never responded wrote no slice, want every output tainted")
	}
	for _, device := range api.updated.Spec.Devices {
		if len(device.Taints) != 2 {
			t.Errorf("%s has %+v, want the disconnected and no-sink taints",
				device.Name, device.Taints)
		}
	}
}

// When PipeWire responds, awaitPipeWire writes nothing, and the first
// reconcile pass writes the slice.
func TestAwaitPipeWireWritesNothingWhenPipeWireResponds(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	operator := testReconciler(t, api, staticGraph(outputGraph(
		map[pcmAddress]string{{Card: 0, PCM: 0}: sinkNodeName(0, 0)})), "pcmC0D0p")

	if err := operator.awaitPipeWire(context.Background(), time.Minute); err != nil {
		t.Fatalf("awaitPipeWire with a PipeWire that responded = %v, want nil", err)
	}
	if len(api.requests) != 0 {
		t.Errorf("the startup wait sent %v to the API server, want no request", api.requests)
	}
}

// settled closes when the bluetoothd or card event channel closes.
// While ctx is live, no pass would run again, so run returns an error
// and the operator exits.
func TestRunStopsWhenTheEventSourcesClose(t *testing.T) {
	api := &slicePublishFixture{}
	operator := testReconciler(t, api, failingGraph, "pcmC0D0p")

	settled := make(chan struct{})
	close(settled)

	err := run(context.Background(), operator, settled)
	if err == nil {
		t.Fatal("run after the event sources closed returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "event sources closed") {
		t.Errorf("run = %v, want the error that names the closed event sources", err)
	}
}

// When ctx has ended, a closed settled is a shutdown, so run returns
// nil and writes nothing.
func TestRunEndsQuietlyWhenTheContextIsDone(t *testing.T) {
	api := &slicePublishFixture{}
	operator := testReconciler(t, api, failingGraph, "pcmC0D0p")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	settled := make(chan struct{})
	close(settled)

	if err := run(ctx, operator, settled); err != nil {
		t.Fatalf("run during a shutdown = %v, want nil", err)
	}
	if len(api.requests) != 0 {
		t.Errorf("a shutdown sent %v to the API server, want no request", api.requests)
	}
}
