package main

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/kubernetes/events"
)

// graphWithoutAnalogSink is the lab graph with no PipeWire node for the
// analog sink.
func graphWithoutAnalogSink() pwGraph {
	graph := labGraph()
	delete(graph.Nodes, nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink})
	return graph
}

// Each condition transition posts one Event with the condition's
// reason, and a pass that changes nothing posts nothing. A Sink that is
// Connected and has no PipeWire node posts a Warning.
func TestEachConditionTransitionPostsOneEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := newEndpointAPI()
		control, recorded := eventedControl(t, api.handler(t))
		ctx := context.Background()

		for _, graph := range []pwGraph{labGraph(), labGraph(), graphWithoutAnalogSink()} {
			if err := control.pass(ctx, labEndpoints(), nil, graph, nil); err != nil {
				t.Fatalf("running the pass: %v", err)
			}
		}
		synctest.Wait()

		want := []eventLine{
			{events.TypeNormal, "NoJackSensing", 1},
			{events.TypeNormal, "NodePresent", 1},
			{events.TypeWarning, "NoNode", 1},
		}
		if got := eventLines(recorded.About(SinkKind, testAnalogName)); !slices.Equal(got, want) {
			t.Errorf("the Sink's events = %+v, want %+v", got, want)
		}
	})
}

// An endpoint that leaves the machine posts Normal Events only,
// because a person unplugs a USB card on purpose.
func TestAnEndpointThatLeftPostsNormalEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := newEndpointAPI()
		control, recorded := eventedControl(t, api.handler(t))
		ctx := context.Background()

		if err := control.pass(ctx, labEndpoints(), nil, labGraph(), nil); err != nil {
			t.Fatalf("running the pass: %v", err)
		}
		if err := control.pass(ctx, labEndpoints()[:1], nil, labGraph(), nil); err != nil {
			t.Fatalf("running the pass: %v", err)
		}
		synctest.Wait()

		want := []eventLine{
			{events.TypeNormal, "NoJackSensing", 1},
			{events.TypeNormal, "NodePresent", 1},
			{events.TypeNormal, "EndpointAbsent", 1},
			{events.TypeNormal, "NoNode", 1},
		}
		if got := eventLines(recorded.About(SourceKind, testSourceName)); !slices.Equal(got, want) {
			t.Errorf("the Source's events = %+v, want %+v", got, want)
		}
	})
}

// refuseStatus wraps api and fails every status write with 503 while
// refusing is true, as an API server that is restarting does.
func refuseStatus(api http.Handler, refusing *atomic.Bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if refusing.Load() && strings.HasSuffix(r.URL.Path, "/status") {
			http.Error(w, "the API server is restarting", http.StatusServiceUnavailable)
			return
		}
		api.ServeHTTP(w, r)
	})
}

// A status write that the API server refuses posts no Events, and the
// next pass whose write succeeds posts the transitions.
func TestARefusedStatusWritePostsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := newEndpointAPI()
		var refusing atomic.Bool
		refusing.Store(true)
		control, recorded := eventedControl(t, refuseStatus(api.handler(t), &refusing))
		ctx := context.Background()

		if err := control.pass(ctx, labEndpoints()[:1], nil, labGraph(), nil); err == nil {
			t.Fatal("the pass returned no error, want the refused status write")
		}
		synctest.Wait()
		if posted := recorded.About(SinkKind, testAnalogName); len(posted) != 0 {
			t.Fatalf("a refused write posted %+v, want no Event", eventLines(posted))
		}

		refusing.Store(false)
		if err := control.pass(ctx, labEndpoints()[:1], nil, labGraph(), nil); err != nil {
			t.Fatalf("running the pass: %v", err)
		}
		synctest.Wait()
		if got := eventLines(recorded.About(SinkKind, testAnalogName)); len(got) != 2 {
			t.Errorf("the landed write posted %+v, want the two new conditions", got)
		}
	})
}

// A spec that sets a codec the speaker does not offer posts one
// SpecRefused Warning, however many passes refuse it.
func TestARefusedSpecPostsOneWarning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := newEndpointAPI()
		codec := "ldac"
		api.sinks[testSpeakerName] = &Sink{
			Metadata: EndpointMeta{Name: testSpeakerName, UID: "uid-speaker", ResourceVersion: "1"},
			Spec:     SinkSpec{Codec: &codec},
		}
		control, recorded := eventedControl(t, api.handler(t))
		ctx := context.Background()

		for range 3 {
			if err := control.pass(ctx, nil, testSpeakers(), labGraph(), nil); err != nil {
				t.Fatalf("running the pass: %v", err)
			}
		}
		synctest.Wait()

		var refused []events.Event
		for _, event := range recorded.About(SinkKind, testSpeakerName) {
			if event.Reason == reasonSpecRefused {
				refused = append(refused, event)
			}
		}
		if len(refused) != 1 || refused[0].Type != events.TypeWarning || refused[0].Count != 1 ||
			refused[0].InvolvedObject.UID != "uid-speaker" {
			t.Errorf("SpecRefused events = %+v, want one Warning on the speaker", refused)
		}
	})
}

func reasonsAmong(posted []events.Event, reasons ...string) []eventLine {
	var lines []eventLine
	for _, line := range eventLines(posted) {
		if slices.Contains(reasons, line.Reason) {
			lines = append(lines, line)
		}
	}
	return lines
}

// The first failed PipeWire graph read posts PipeWireLost on each Sink
// of the machine, the failed reads after it post nothing, and the
// first successful read posts PipeWireRecovered.
func TestAPipeWireThatStopsRespondingPostsItsFirstFailureAndItsRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sinks := failingGraph
		operator := testReconciler(t, &slicePublishFixture{}, func(ctx context.Context) (pwGraph, error) {
			return sinks(ctx)
		}, "pcmC0D0p")
		outputs, err := readEndpoints()
		if err != nil {
			t.Fatalf("reading the endpoints: %v", err)
		}
		named, _ := nameEndpoints(operator.nodeName, outputs)
		operator.endpoints = &endpointInventory{}
		operator.endpoints.publish(named)
		control, recorded := eventedControl(t, newEndpointAPI().handler(t))
		operator.control = control
		sink := named[0].Name()

		for range maxFailedGraphReads - 1 {
			if err := operator.reconcile(context.Background()); err != nil {
				t.Fatalf("reconciling: %v", err)
			}
		}
		sinks = staticGraph(outputGraph(map[pcmAddress]string{{Card: 0, PCM: 0}: "alsa_output.test"}))
		if err := operator.reconcile(context.Background()); err != nil {
			t.Fatalf("reconciling: %v", err)
		}
		synctest.Wait()

		want := []eventLine{
			{events.TypeWarning, reasonPipeWireLost, 1},
			{events.TypeNormal, reasonPipeWireRecovered, 1},
		}
		got := reasonsAmong(recorded.About(SinkKind, sink), reasonPipeWireLost, reasonPipeWireRecovered)
		if !slices.Equal(got, want) {
			t.Errorf("the Sink's events = %+v, want %+v", got, want)
		}
	})
}

// When bluetoothd stops responding, the operator posts
// BluetoothUnavailable once on each speaker it last reported, and
// BluetoothAvailable when bluetoothd responds again.
func TestABluetoothdThatStopsRespondingPostsItsFirstFailureAndItsRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		control, recorded := eventedControl(t, newEndpointAPI().handler(t))
		var failure error
		operator := &reconciler{
			control: control,
			speakers: func() (map[string]speaker, error) {
				if failure != nil {
					return nil, failure
				}
				return testSpeakers(), nil
			},
		}

		operator.pairedSpeakers()
		failure = errors.New("org.freedesktop.DBus.Error.NoReply")
		operator.pairedSpeakers()
		operator.pairedSpeakers()
		failure = nil
		operator.pairedSpeakers()
		synctest.Wait()

		want := []eventLine{
			{events.TypeWarning, reasonBluetoothUnavailable, 1},
			{events.TypeNormal, reasonBluetoothAvailable, 1},
		}
		if got := eventLines(recorded.About(SinkKind, testSpeakerName)); !slices.Equal(got, want) {
			t.Errorf("the speaker's events = %+v, want %+v", got, want)
		}
	})
}
