package main

// These tests cover one pass of the controller: the resources it
// creates, the status it writes and the status it does not write
// again, the declaration it carries to the hardware, and what it
// reports for an endpoint the machine no longer publishes.

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// testEndpointControl builds a controller over the API server fixture,
// with no card behind it: the fixture machines hold ordinary files
// where a control device would be, so the endpoints publish without
// their capabilities and the pass is the same pass.
func testEndpointControl(t *testing.T, api *endpointAPI, record *writeRecord) *endpointControl {
	t.Helper()
	control := recordingControl(record)
	control.client = testClient(t, api.handler(t))
	control.machine = "liken-1"
	control.claims = &preparedClaims{}
	control.now = func() time.Time { return factsTime }
	control.openCard = func(int) (*mixer, error) {
		return nil, errors.New("this test has no control device")
	}
	return control
}

// labEndpoints is the analog jack of the lab card and its capture
// side, which is one PCM device in both directions.
func labEndpoints() []alsaEndpoint {
	return named(
		alsaEndpoint{Card: 0, PCM: 0},
		alsaEndpoint{Card: 0, PCM: 0, Capture: true},
	)
}

// labGraph holds a node for each of those endpoints and one for the
// speaker.
func labGraph() pwGraph {
	address := pcmAddress{Card: 0, PCM: 0}
	return pwGraph{
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

// Every endpoint the machine publishes gets its own resource, a
// playback one as a Sink and a capture one as a Source, and the
// operator creates each one before it writes any status.
func TestPassCreatesAResourceForEveryEndpoint(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	control.claims.prepared("claim-1",
		EndpointClaim{Namespace: "media", Name: "kitchen"}, []string{testSpeakerName})

	if err := control.pass(context.Background(), labEndpoints(), testSpeakers(), labGraph()); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{testAnalogName, testSpeakerName} {
		if _, created := api.sinks[name]; !created {
			t.Errorf("no Sink for %s; the fixture holds %v", name, api.sinks)
		}
	}
	if _, created := api.sources[testSourceName]; !created {
		t.Errorf("no Source for %s; the fixture holds %v", testSourceName, api.sources)
	}

	analog := api.sinks[testAnalogName].Status
	if analog.Node != "liken-1" || analog.NodeName != sinkNodeName(0, 0) {
		t.Errorf("the analog sink's status = %+v", analog)
	}
	if analog.Claim != nil {
		t.Errorf("the analog sink reports the claim %+v, and the claim holds the speaker", analog.Claim)
	}
	// The claim that holds the speaker is what answers which workload
	// has it now.
	speaker := api.sinks[testSpeakerName].Status
	if speaker.Claim == nil || speaker.Claim.Name != "kitchen" || speaker.Claim.Namespace != "media" {
		t.Errorf("the speaker's claim = %+v", speaker.Claim)
	}
	if speaker.Bluetooth == nil || speaker.Bluetooth.Pairing != testSpeakerName {
		t.Errorf("the speaker's bluetooth block = %+v", speaker.Bluetooth)
	}
}

// A pass that finds the published status already correct writes
// nothing, so a machine at rest costs the API server one read for
// each endpoint and no write.
func TestPassWritesTheStatusOnce(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	ctx := context.Background()

	if err := control.pass(ctx, labEndpoints(), testSpeakers(), labGraph()); err != nil {
		t.Fatal(err)
	}
	writes := 0
	for _, request := range api.requests {
		if request[:3] == "PUT" {
			writes++
		}
	}
	if writes != 3 {
		t.Fatalf("the first pass wrote %d statuses, want one for each endpoint: %v", writes, api.requests)
	}

	api.requests = nil
	if err := control.pass(ctx, labEndpoints(), testSpeakers(), labGraph()); err != nil {
		t.Fatal(err)
	}
	for _, request := range api.requests {
		if request[:3] == "PUT" {
			t.Errorf("a second pass wrote a status again: %v", api.requests)
			break
		}
	}
}

// The unity default is for a node PipeWire has just built. A level a
// person set on a node that stands reaches the status and nothing
// else.
func TestUnityIsWrittenOnceForEachNode(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := testEndpointControl(t, api, record)
	ctx := context.Background()

	graph := labGraph()
	quiet := graph.Nodes[nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink}]
	quiet.Volumes = []float64{0.4, 0.4}
	graph.Nodes[nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink}] = quiet

	if err := control.pass(ctx, labEndpoints(), nil, graph); err != nil {
		t.Fatal(err)
	}
	if record.node == nil || record.level.Volume == nil || *record.level.Volume != unityPercent {
		t.Fatalf("a new node was left at 40 percent: %+v", record)
	}

	// The same node, still at the level the graph reports, is left
	// alone: the operator wrote it once and a person may have moved it
	// since.
	record.node, record.level = nil, levelWrite{}
	if err := control.pass(ctx, labEndpoints(), nil, graph); err != nil {
		t.Fatal(err)
	}
	if record.node != nil {
		t.Errorf("the second pass wrote the level again: %+v", record)
	}

	// A node PipeWire built again carries a new object id, and it is
	// born at the configuration's level, so it takes the write.
	rebuilt := quiet
	rebuilt.ID = 77
	graph.Nodes[nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink}] = rebuilt
	if err := control.pass(ctx, labEndpoints(), nil, graph); err != nil {
		t.Fatal(err)
	}
	if record.node == nil || record.level.Volume == nil || *record.level.Volume != unityPercent {
		t.Errorf("a node that was built again was left at 40 percent: %+v", record)
	}
}

// A declared level reaches the endpoint on the pass that reads it,
// under a claim or not.
func TestPassCarriesADeclaredLevel(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := testEndpointControl(t, api, record)
	api.sinks[testAnalogName] = &Sink{
		Metadata: EndpointMeta{Name: testAnalogName},
		Spec:     SinkSpec{Volume: pointerTo(25), Mute: pointerTo(true)},
	}

	if err := control.pass(context.Background(), labEndpoints(), nil, labGraph()); err != nil {
		t.Fatal(err)
	}
	if record.node == nil || record.level.Volume == nil || *record.level.Volume != 25 || record.level.Mute == nil || !*record.level.Mute {
		t.Fatalf("the declaration reached the node as %+v", record)
	}
}

// An endpoint the machine no longer publishes keeps its resource and
// its declaration, and the conditions report the absence. Deleting it
// would lose the level a person declared for a card that is unplugged
// for an hour.
func TestPassReportsAnEndpointThatLeft(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	ctx := context.Background()

	if err := control.pass(ctx, labEndpoints(), nil, labGraph()); err != nil {
		t.Fatal(err)
	}
	if err := control.pass(ctx, labEndpoints()[:1], nil, labGraph()); err != nil {
		t.Fatal(err)
	}

	status := api.sources[testSourceName].Status
	if status.NodeName != "" {
		t.Errorf("an absent endpoint still names the node %q", status.NodeName)
	}
	for _, want := range []EndpointCondition{
		condition(ConnectedCondition, false, "EndpointAbsent",
			"this machine no longer publishes the endpoint", factsTime),
		condition(ReadyCondition, false, "NoNode",
			"PipeWire holds no node for this endpoint", factsTime),
	} {
		if !holdsCondition(status.Conditions, want) {
			t.Errorf("conditions = %+v, want %+v among them", status.Conditions, want)
		}
	}
	// The endpoint that is still there keeps its own conditions.
	if analog := api.sinks[testAnalogName].Status; analog.NodeName != sinkNodeName(0, 0) {
		t.Errorf("the endpoint that stayed lost its node: %+v", analog)
	}
}

// An endpoint another machine publishes is left alone, because the
// resource is cluster-scoped and every machine sweeps its own.
func TestPassLeavesAnotherMachinesEndpointAlone(t *testing.T) {
	api := newEndpointAPI()
	control := testEndpointControl(t, api, &writeRecord{})
	elsewhere := &Sink{
		Metadata: EndpointMeta{Name: "stick-1-pci-0000-00-0e-0-hdmi-0"},
		Status:   EndpointStatus{Node: "stick-1", NodeName: "liken.audio.card0-pcm3"},
	}
	api.sinks[elsewhere.Metadata.Name] = elsewhere

	if err := control.pass(context.Background(), labEndpoints(), nil, labGraph()); err != nil {
		t.Fatal(err)
	}
	if got := api.sinks[elsewhere.Metadata.Name].Status.NodeName; got != "liken.audio.card0-pcm3" {
		t.Errorf("another machine's sink was swept: %q", got)
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

// The live check. It runs only where a card answers, and it reads the
// card without writing to it.
//
// The control device a pass opens is the one its writes go through,
// so the device has to outlive the read that gathered the facts. A
// device closed at the end of the read would fail every control write
// the same pass planned.
func TestTheCardStaysOpenForTheLengthOfAPass(t *testing.T) {
	sndDir = "/dev/snd"
	t.Cleanup(func() { sndDir = "/dev/snd" })
	if _, err := os.Stat(sndDir + "/controlC0"); err != nil {
		t.Skip("this machine has no card 0 to read")
	}
	control := newEndpointControl(nil, "liken-1", nil, nil)
	endpoints := []alsaEndpoint{{Card: 0, PCM: 0, DeviceName: "the local card"}}

	cards := control.openCards(endpoints)
	defer closeCards(cards)
	device := cards[0]
	if device == nil {
		t.Skip("card 0 does not open for this user")
	}

	readings := control.read(cards, endpoints, nil, pwGraph{})
	if len(readings) != 1 || readings[0].card != device {
		t.Fatalf("the reading carries %+v, want the open device", readings[0].card)
	}
	if len(device.controls) == 0 {
		t.Skip("card 0 declares no writable control")
	}
	if _, err := device.readElement(device.controls[0]); err != nil {
		t.Errorf("the card was closed before the pass could write through it: %v", err)
	}
}

// A level write that did not land is tried again on the next pass. A
// node recorded as written while the write failed would keep its
// level until PipeWire built it again.
func TestAFailedUnityWriteIsTriedAgain(t *testing.T) {
	api := newEndpointAPI()
	record := &writeRecord{}
	control := testEndpointControl(t, api, record)
	control.setLevel = func(context.Context, pwNode, levelWrite) error {
		return errors.New("pw-cli set-param: no such object")
	}

	graph := labGraph()
	address := nodeAddress{pcmAddress: pcmAddress{Card: 0, PCM: 0}, Direction: directionSink}
	quiet := graph.Nodes[address]
	quiet.Volumes = []float64{0.4, 0.4}
	graph.Nodes[address] = quiet

	// The first pass reports the failed write, which is what the
	// reconcile loop logs and carries on from.
	ctx := context.Background()
	if err := control.pass(ctx, labEndpoints(), nil, graph); err == nil {
		t.Fatal("a failed write reported nothing")
	}

	control.setLevel = func(_ context.Context, node pwNode, level levelWrite) error {
		record.node, record.level = &node, level
		return nil
	}
	if err := control.pass(ctx, labEndpoints(), nil, graph); err != nil {
		t.Fatal(err)
	}
	if record.node == nil || record.level.Volume == nil || *record.level.Volume != unityPercent {
		t.Fatalf("the write was not tried again: %+v", record)
	}
}
