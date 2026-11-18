package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// testEndpointControl builds a controller over the endpointAPI
// fixture. Its openCard always fails, so endpoints publish without
// capabilities or controls.
func testEndpointControl(t *testing.T, api *endpointAPI, record *writeRecord) *endpointControl {
	t.Helper()
	return controlOver(t, api.handler(t), record)
}

func controlOver(t *testing.T, api http.Handler, record *writeRecord) *endpointControl {
	t.Helper()
	control := recordingControl(record)
	control.client = testClient(t, api)
	control.machine = "liken-1"
	control.claims = &preparedClaims{}
	control.now = func() time.Time { return factsTime }
	control.openCard = func(int) (*mixer, error) {
		return nil, errors.New("this test has no control device")
	}
	return control
}

// ranBefore runs a pass of an earlier operator process over graph, so
// the fixture holds the status.applied that the process wrote. That
// process's writes to PipeWire go to a writeRecord that the test
// discards, and graph does not change.
func ranBefore(t *testing.T, api *endpointAPI, graph pwGraph) {
	t.Helper()
	before := testEndpointControl(t, api, &writeRecord{})
	if err := before.pass(context.Background(), labEndpoints(), testSpeakers(), graph, nil); err != nil {
		t.Fatalf("running the pass of the earlier operator: %v", err)
	}
}

// labEndpoints is the analog jack of the lab card as a playback
// endpoint and a capture endpoint. They are one PCM device in two
// directions.
func labEndpoints() []alsaEndpoint {
	return named(
		alsaEndpoint{Card: 0, PCM: 0},
		alsaEndpoint{Card: 0, PCM: 0, Capture: true},
	)
}

const labCookie = 1067734355

// labGraph has a PipeWire node for each endpoint of labEndpoints and a
// sink for the Bluetooth test speaker.
func labGraph() pwGraph {
	address := pcmAddress{Card: 0, PCM: 0}
	return pwGraph{
		Cookie: labCookie,
		Nodes: map[nodeAddress]pwNode{
			{pcmAddress: address, Direction: directionSink}: {
				ID: 42, Name: sinkNodeName(0, 0), Volumes: []float64{1, 1},
			},
			{pcmAddress: address, Direction: directionSource}: {
				ID: 43, Name: sourceNodeName(0, 0), Volumes: []float64{1},
			},
		},
		Speakers: map[string]bluezSink{
			testSpeakerAddress: {Node: testSpeakerNode, NodeID: 63, Codec: "sbc", Volumes: []float64{1, 1}},
		},
	}
}

// A pass creates a Sink for each playback endpoint and a Source for
// each capture endpoint, before it writes any status.
func TestPassCreatesAResourceForEveryEndpoint(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	control.claims.record("claim-1",
		EndpointClaim{Namespace: "media", Name: "kitchen"}, []string{testSpeakerName})

	if err := control.pass(context.Background(), labEndpoints(), testSpeakers(), labGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}

	for _, name := range []string{testAnalogName, testSpeakerName} {
		if _, created := api.sinks[name]; !created {
			t.Errorf("the fixture holds the Sinks %v, want one named %s", api.sinks, name)
		}
	}
	if _, created := api.sources[testSourceName]; !created {
		t.Errorf("the fixture holds the Sources %v, want one named %s", api.sources, testSourceName)
	}

	analog := api.sinks[testAnalogName].Status
	if analog.Node != "liken-1" || analog.NodeName != sinkNodeName(0, 0) {
		t.Errorf("the analog sink's status is %+v, want node liken-1 and node name %s", analog, sinkNodeName(0, 0))
	}
	if len(analog.Claims) != 0 {
		t.Errorf("the analog sink reports the claims %+v, want none, because the claim holds the speaker", analog.Claims)
	}
	// The speaker's status.claims lists the claim that holds it, so a
	// reader can find the workload that plays through the speaker.
	speaker := api.sinks[testSpeakerName].Status
	if want := []EndpointClaim{{Namespace: "media", Name: "kitchen"}}; !reflect.DeepEqual(speaker.Claims, want) {
		t.Errorf("the speaker's claims = %+v, want %+v", speaker.Claims, want)
	}
	if speaker.Bluetooth == nil || speaker.Bluetooth.Peripheral != testSpeakerName {
		t.Errorf("the speaker's bluetooth block is %+v, want the peripheral %s", speaker.Bluetooth, testSpeakerName)
	}
}

// A pass over unchanged state writes no status. The controller has no
// watch stores here, so each pass reads every resource from the API
// server.
func TestPassWritesTheStatusOnce(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	ctx := context.Background()

	if err := control.pass(ctx, labEndpoints(), testSpeakers(), labGraph(), nil); err != nil {
		t.Fatalf("running the first pass: %v", err)
	}
	if writes := statusWrites(api); writes != 3 {
		t.Fatalf("the first pass wrote %d statuses, want one for each endpoint: %v", writes, api.requests)
	}

	api.requests = nil
	if err := control.pass(ctx, labEndpoints(), testSpeakers(), labGraph(), nil); err != nil {
		t.Fatalf("running the second pass: %v", err)
	}
	if writes := statusWrites(api); writes != 0 {
		t.Errorf("a second pass wrote %d statuses, want none: %v", writes, api.requests)
	}
}

// turnedDownGraph is labGraph after a person set the analog sink to 40
// percent and the speaker to 30 percent. The speaker has absolute
// volume, so its level is on its Route.
func turnedDownGraph() pwGraph {
	graph := labGraph()
	address := nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink}
	analog := graph.Nodes[address]
	analog.Volumes = []float64{0.4, 0.4}
	graph.Nodes[address] = analog
	speaker := graph.Speakers[testSpeakerAddress]
	speaker.Device = 64
	speaker.Route = &pwRoute{Device: 1, Volumes: []float64{0.3, 0.3}, AbsoluteVolume: true}
	graph.Speakers[testSpeakerAddress] = speaker
	return graph
}

// An operator that restarts while PipeWire keeps running finds the
// nodes that status.applied records, at levels a person chose. It
// writes no level to them, so a speaker that plays under a claim keeps
// its level.
func TestAStartWritesNothingToNodesThatAlreadyExist(t *testing.T) {
	api := newEndpointAPI()
	ranBefore(t, api, turnedDownGraph())
	record := &writeRecord{}
	control := testEndpointControl(t, api, record)
	control.claims.record("claim-1",
		EndpointClaim{Namespace: "media", Name: "film"}, []string{testSpeakerName})
	ctx := context.Background()

	for range 2 {
		if err := control.pass(ctx, labEndpoints(), testSpeakers(), turnedDownGraph(), nil); err != nil {
			t.Fatalf("running a pass after the start: %v", err)
		}
	}
	if record.node != nil || record.route != nil {
		t.Errorf("a start wrote the level %+v to a node that already existed, want no write", record)
	}
}

// The operator writes the unity level once to a node that PipeWire
// builds after the operator started. A level that a person sets on the
// node afterwards is reported in status and not written back.
func TestUnityIsWrittenOnceForANodeBuiltAfterTheStart(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := testEndpointControl(t, api, record)
	ctx := context.Background()

	graph := turnedDownGraph()
	if err := control.pass(ctx, labEndpoints(), nil, graph, nil); err != nil {
		t.Fatalf("running the first pass: %v", err)
	}

	// A rebuilt node has a new PipeWire object id, so the operator
	// treats it as new and writes the unity level to it.
	address := nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink}
	rebuilt := graph.Nodes[address]
	rebuilt.ID = 77
	graph.Nodes[address] = rebuilt
	if err := control.pass(ctx, labEndpoints(), nil, graph, nil); err != nil {
		t.Fatalf("running the pass over the rebuilt node: %v", err)
	}
	if record.node == nil || record.node.ID != 77 || record.level.Volume == nil || *record.level.Volume != unityPercent {
		t.Fatalf("the write to a node built after the start is %+v, want the unity level on node 77", record)
	}

	record.node, record.level = nil, levelWrite{}
	if err := control.pass(ctx, labEndpoints(), nil, graph, nil); err != nil {
		t.Fatalf("running the pass after the unity write: %v", err)
	}
	if record.node != nil {
		t.Errorf("the next pass wrote the level %+v again, want no write", record)
	}
}

// When the machine stops publishing an endpoint, its resource keeps
// its spec and reports the absence in its conditions. Deleting the
// resource would lose the level that a person declared for a card that
// is only unplugged for a while.
func TestPassReportsAnEndpointThatLeft(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	ctx := context.Background()

	if err := control.pass(ctx, labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass over every endpoint: %v", err)
	}
	if err := control.pass(ctx, labEndpoints()[:1], nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass without the capture endpoint: %v", err)
	}

	status := api.sources[testSourceName].Status
	if status.NodeName != "" {
		t.Errorf("an absent endpoint names the node %q, want none", status.NodeName)
	}
	for _, want := range []EndpointCondition{
		condition(ConnectedCondition, false, "EndpointAbsent",
			"This machine no longer publishes the endpoint", factsTime),
		condition(ReadyCondition, false, "NoNode",
			"PipeWire has no node for this endpoint", factsTime),
	} {
		if !holdsCondition(status.Conditions, want) {
			t.Errorf("conditions = %+v, want %+v among them", status.Conditions, want)
		}
	}
	if analog := api.sinks[testAnalogName].Status; analog.NodeName != sinkNodeName(0, 0) {
		t.Errorf("the status of the endpoint that stayed is %+v, want the node name %s", analog, sinkNodeName(0, 0))
	}
}

// A resource whose status.node names another machine is left alone.
// Sinks and Sources are cluster-scoped, and each machine sweeps only
// the ones that name it.
func TestPassLeavesAnotherMachinesEndpointAlone(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	elsewhere := &Sink{
		Metadata: EndpointMeta{Name: "stick-1-pci-0000-00-0e-0-hdmi-0"},
		Status:   sinkStatus(EndpointStatus{Node: "stick-1", NodeName: "liken.audio.card0-pcm3"}),
	}
	api.sinks[elsewhere.Metadata.Name] = elsewhere

	if err := control.pass(context.Background(), labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass: %v", err)
	}
	if got := api.sinks[elsewhere.Metadata.Name].Status.NodeName; got != "liken.audio.card0-pcm3" {
		t.Errorf("another machine's sink names the node %q after the pass, want liken.audio.card0-pcm3", got)
	}
}

// statusWrites counts the PUT and PATCH requests the fixture received.
func statusWrites(api *endpointAPI) int {
	api.mutex.Lock()
	defer api.mutex.Unlock()
	writes := 0
	for _, request := range api.requests {
		if strings.HasPrefix(request, "PUT ") || strings.HasPrefix(request, "PATCH ") {
			writes++
		}
	}
	return writes
}

// A later pass over unchanged hardware writes nothing, for a present
// endpoint and for one the sweep reports absent. A status field that
// changes on its own, such as a timestamp, would make every pass write,
// and every write notifies each watcher of the resource.
func TestALaterPassOverTheSameHardwareWritesNothing(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	ctx := context.Background()
	if err := control.pass(ctx, labEndpoints(), testSpeakers(), labGraph(), nil); err != nil {
		t.Fatalf("running the pass over every endpoint: %v", err)
	}
	if err := control.pass(ctx, labEndpoints()[:1], testSpeakers(), labGraph(), nil); err != nil {
		t.Fatalf("running the pass without the capture endpoint: %v", err)
	}
	written := statusWrites(api)

	// A clock past backstopInterval makes sweepDue sweep again.
	control.now = func() time.Time { return factsTime.Add(2 * time.Hour) }
	if err := control.pass(ctx, labEndpoints()[:1], testSpeakers(), labGraph(), nil); err != nil {
		t.Fatalf("running the pass two hours later: %v", err)
	}
	if got := statusWrites(api); got != written {
		t.Errorf("a later pass over the same hardware wrote %d statuses, want none", got-written)
	}
}

// A resource whose name no machine publishes is reported absent once,
// by the machine that its status.node names. An example is the Sink of
// a USB card, whose name has no machine in it. The resource keeps its
// spec, because the spec is a person's declaration.
func TestAResourceNoMachinePublishesIsReportedAbsentOnce(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	const shared = "usb-0573-1573-a34004801402-usb-audio"
	api.sinks[shared] = &Sink{
		Metadata: EndpointMeta{Name: shared},
		Spec:     SinkSpec{Volume: declaredLevel(40)},
		Status: sinkStatus(EndpointStatus{Node: "liken-1", NodeName: "liken.audio.card1-pcm0", Conditions: []EndpointCondition{
			condition(ConnectedCondition, true, "CardPresent", "The card is on the USB bus", factsTime),
		}}),
	}
	ctx := context.Background()
	if err := control.pass(ctx, labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatalf("running the first pass: %v", err)
	}
	old := api.sinks[shared]
	if !holdsCondition(old.Status.Conditions, condition(ConnectedCondition, false, "EndpointAbsent",
		"This machine no longer publishes the endpoint", factsTime)) {
		t.Errorf("the old Sink's conditions = %+v, want it reported absent", old.Status.Conditions)
	}
	if old.Spec.Volume == nil || *old.Spec.Volume.Level != 40 {
		t.Errorf("the old Sink's spec = %+v, want the declaration kept", old.Spec)
	}
	written := statusWrites(api)

	control.now = func() time.Time { return factsTime.Add(2 * time.Hour) }
	if err := control.pass(ctx, labEndpoints(), nil, labGraph(), nil); err != nil {
		t.Fatalf("running the pass two hours later: %v", err)
	}
	if got := statusWrites(api); got != written {
		t.Errorf("the sweep wrote the absent Sink %d more times, want none", got-written)
	}
}

func holdsCondition(conditions []EndpointCondition, want EndpointCondition) bool {
	for _, condition := range conditions {
		if condition == want {
			return true
		}
	}
	return false
}

// This test reads a real card, so it runs only on a machine where card
// 0 opens, and it writes nothing to the card.
//
// A pass writes controls through the device it opened for the read, so
// the device must stay open after read returns. A device closed at the
// end of the read would fail every control write that the same pass
// planned.
func TestTheCardStaysOpenForTheLengthOfAPass(t *testing.T) {
	sndDir = "/dev/snd"
	t.Cleanup(func() { sndDir = "/dev/snd" })
	if _, err := os.Stat(sndDir + "/controlC0"); err != nil {
		t.Skip("this machine has no card 0 to read")
	}
	control := newEndpointControl(nil, objectCache{}, "liken-1", nil, nil, nil)
	endpoints := []alsaEndpoint{{Card: 0, PCM: 0, DeviceName: "the local card"}}

	cards := control.openCards(endpoints)
	defer closeCards(cards)
	device := cards[0]
	if device == nil {
		t.Skip("card 0 does not open for this user")
	}

	readings := control.read(cards, endpoints, nil, pwGraph{}, nil)
	if len(readings) != 1 || readings[0].card != device {
		t.Fatalf("the reading carries %+v, want the open device", readings[0].card)
	}
	if len(device.controls) == 0 {
		t.Skip("card 0 declares no writable control")
	}
	if _, err := device.readElement(device.controls[0]); err != nil {
		t.Errorf("reading a control after the read of the pass: %v, want the card still open", err)
	}
}

// A level write that fails is tried again on the next pass. If the
// operator recorded the node as written despite the failure, the node
// would keep its old level until PipeWire rebuilt it.
func TestAFailedUnityWriteIsTriedAgain(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := testEndpointControl(t, api, record)
	control.setLevel = func(context.Context, pwNode, levelWrite) error {
		return errors.New("pw-cli set-param: no such object")
	}

	// The first pass finds no analog node, so the node that appears
	// for the second pass counts as built after the start.
	graph := turnedDownGraph()
	address := nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink}
	quiet := graph.Nodes[address]
	delete(graph.Nodes, address)
	ctx := context.Background()
	if err := control.pass(ctx, labEndpoints(), nil, graph, nil); err != nil {
		t.Fatalf("running the pass with no analog node: %v", err)
	}
	graph.Nodes[address] = quiet

	if err := control.pass(ctx, labEndpoints(), nil, graph, nil); err == nil {
		t.Fatal("the pass returned no error after a level write failed, want the write's error")
	}

	control.setLevel = func(_ context.Context, node pwNode, level levelWrite) error {
		record.node, record.level = &node, level
		return nil
	}
	if err := control.pass(ctx, labEndpoints(), nil, graph, nil); err != nil {
		t.Fatalf("running the pass after the level write recovers: %v", err)
	}
	if record.node == nil || record.level.Volume == nil || *record.level.Volume != unityPercent {
		t.Fatalf("the write after the next pass is %+v, want the unity level written again", record)
	}
}
