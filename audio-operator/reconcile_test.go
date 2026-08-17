package main

// These tests cover the reconcile pass's decisions: what it publishes,
// what it refuses to publish, and when it gives up and lets the
// kubelet restart the pod.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// failingSinks stands in for a PipeWire that does not answer.
func failingSinks(context.Context) (map[pcmAddress]string, error) {
	return nil, errors.New("running pw-dump: no such file or directory")
}

// testReconciler builds a reconciler over a fake API server and a
// fake set of delivered nodes.
func testReconciler(t *testing.T, api *slicePublishFixture, sinks func(context.Context) (map[pcmAddress]string, error), nodes ...string) *reconciler {
	t.Helper()
	sndDir = deliveredNodes(t, nodes...)
	specDirectory(t)
	return &reconciler{
		client:   testClient(t, api.handler(t)),
		nodeName: "liken-1",
		owner:    testOwner(),
		sinks:    sinks,
	}
}

// A single failed graph read leaves the published slice alone. A
// slice that tainted every output would evict every consumer for a
// pw-dump that timed out once.
func TestReconcileSkipsTheWriteWhenPipeWireDoesNotAnswer(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	operator := testReconciler(t, api, failingSinks, "pcmC0D0p", "pcmC0D3p")

	if err := operator.reconcile(context.Background()); err != nil {
		t.Fatalf("one failed graph read gave up: %v", err)
	}
	if len(api.requests) != 0 {
		t.Errorf("a failed graph read reached the API server: %v", api.requests)
	}
}

// A run of failed graph reads is a different state from one. The
// published slice still says every output has a sink, every prepare
// call fails, and nothing bounds it, so the operator gives up and the
// kubelet's restart is the repair.
func TestReconcileGivesUpAfterARunOfFailedGraphReads(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	operator := testReconciler(t, api, failingSinks, "pcmC0D0p")

	for pass := 1; pass < maxSinkFailures; pass++ {
		if err := operator.reconcile(context.Background()); err != nil {
			t.Fatalf("pass %d of %d gave up early: %v", pass, maxSinkFailures, err)
		}
	}
	if err := operator.reconcile(context.Background()); err == nil {
		t.Fatalf("%d failed graph reads in a row did not make the operator give up", maxSinkFailures)
	}
}

// One good read clears the count, so a pw-dump that fails now and
// then never adds up to a restart.
func TestReconcileForgetsFailuresAfterAGoodRead(t *testing.T) {
	api := &slicePublishFixture{}
	sinks := failingSinks
	operator := testReconciler(t, api, func(ctx context.Context) (map[pcmAddress]string, error) {
		return sinks(ctx)
	}, "pcmC0D0p")

	for pass := 1; pass < maxSinkFailures; pass++ {
		if err := operator.reconcile(context.Background()); err != nil {
			t.Fatalf("pass %d gave up early: %v", pass, err)
		}
	}
	sinks = func(context.Context) (map[pcmAddress]string, error) {
		return map[pcmAddress]string{{Card: 0, PCM: 0}: "alsa_output.test"}, nil
	}
	if err := operator.reconcile(context.Background()); err != nil {
		t.Fatalf("a good read gave up: %v", err)
	}
	sinks = failingSinks
	for pass := 1; pass < maxSinkFailures; pass++ {
		if err := operator.reconcile(context.Background()); err != nil {
			t.Fatalf("the count survived a good read: %v", err)
		}
	}
}

// An empty enumeration is never a fact about a machine this operator
// runs on, so the pass publishes nothing and does not read the graph.
func TestReconcileSkipsTheWriteWhenTheCardHasNoOutput(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	read := false
	operator := testReconciler(t, api, func(context.Context) (map[pcmAddress]string, error) {
		read = true
		return map[pcmAddress]string{}, nil
	}, "controlC0", "pcmC0D0c")

	if err := operator.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(api.requests) != 0 {
		t.Errorf("an empty enumeration reached the API server: %v", api.requests)
	}
	if read {
		t.Error("an empty enumeration read PipeWire's graph anyway")
	}
}

func TestReconcilePublishesWhatItReads(t *testing.T) {
	api := &slicePublishFixture{}
	operator := testReconciler(t, api, func(context.Context) (map[pcmAddress]string, error) {
		return map[pcmAddress]string{{Card: 0, PCM: 0}: "alsa_output.analog"}, nil
	}, "pcmC0D0p", "pcmC0D3p")

	if err := operator.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if api.created == nil {
		t.Fatal("the first pass published nothing")
	}
	devices := api.created.Spec.Devices
	if len(devices) != 2 {
		t.Fatalf("devices = %+v, want two", devices)
	}
	if got := stringAttribute(t, devices[0], "output"); got != "card0-pcm0" {
		t.Errorf("output = %q", got)
	}
	if len(devices[0].Taints) != 0 {
		t.Errorf("the output with a sink is tainted: %+v", devices[0].Taints)
	}
	if len(devices[1].Taints) != 2 {
		t.Errorf("the output with no sink carries %+v, want both taints", devices[1].Taints)
	}
}

// A sound server that died takes every output with it, and the slice
// has to say so before the process ends. Otherwise the consumers of
// that card hold a dead socket until somebody notices.
func TestTaintEverythingTaintsEveryOutput(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	operator := testReconciler(t, api, failingSinks, "pcmC0D0p", "pcmC0D3p")

	operator.taintEverything()

	if api.updated == nil {
		t.Fatal("the outputs were not tainted on the way out")
	}
	for _, device := range api.updated.Spec.Devices {
		if len(device.Taints) != 2 {
			t.Errorf("%s carries %+v, want both taints", device.Name, device.Taints)
		}
	}
}

// The loop ends the process when a daemon dies, and it says so in the
// slice on the way out.
func TestRunTaintsAndStopsWhenADaemonDies(t *testing.T) {
	api := &slicePublishFixture{existing: publishedSlice(testDevices(), 3)}
	operator := testReconciler(t, api, failingSinks, "pcmC0D0p", "pcmC0D3p")

	died := make(chan error, 1)
	died <- errors.New("pipewire exited: signal: killed")

	err := run(context.Background(), operator, died, make(chan struct{}))
	if err == nil {
		t.Fatal("a daemon that died did not stop the operator")
	}
	if api.updated == nil {
		t.Fatal("a daemon that died left the slice saying the outputs play")
	}
	for _, device := range api.updated.Spec.Devices {
		if len(device.Taints) != 2 {
			t.Errorf("%s carries %+v, want both taints", device.Name, device.Taints)
		}
	}
}

// The event sources are the jack nodes and the backstop tick. A
// closed channel while the context is live leaves the operator with
// no way to notice a monitor again, so it stops instead.
func TestRunStopsWhenTheEventSourcesClose(t *testing.T) {
	api := &slicePublishFixture{}
	operator := testReconciler(t, api, failingSinks, "pcmC0D0p")

	settled := make(chan struct{})
	close(settled)

	err := run(context.Background(), operator, make(chan error), settled)
	if err == nil {
		t.Fatal("the operator ran on after its event sources closed")
	}
	if !strings.Contains(err.Error(), "event sources closed") {
		t.Errorf("error = %v", err)
	}
}

// The same closed channel during shutdown is the ordinary end of the
// process, and not a failure to report.
func TestRunEndsQuietlyWhenTheContextIsDone(t *testing.T) {
	api := &slicePublishFixture{}
	operator := testReconciler(t, api, failingSinks, "pcmC0D0p")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	settled := make(chan struct{})
	close(settled)

	if err := run(ctx, operator, make(chan error), settled); err != nil {
		t.Fatalf("a shutdown reported a failure: %v", err)
	}
	if len(api.requests) != 0 {
		t.Errorf("a shutdown wrote to the API server: %v", api.requests)
	}
}
