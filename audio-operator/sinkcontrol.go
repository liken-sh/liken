package main

// The controller for the Sink and Source resources.
//
// A pass visits every endpoint this machine publishes. When the
// endpoint has no resource, the pass creates one with an empty spec,
// because the operator declares nothing itself and the resource exists
// for a person to declare on. Then the pass writes the spec to the
// endpoint (resting.go): the declared level when the declaration
// changed or PipeWire built a new node, and each declared control and
// codec that differs from what the endpoint reads. A field that the
// spec leaves out is not written. Last, the pass writes the status, and
// only when it differs from the published status, so an endpoint that
// does not change costs no write.
//
// Between passes, the loop applies the volume requests that
// media-operator writes into a Sink's status.session.volumeAsk
// (asks.go).
//
// After the endpoints, the pass sweeps (sweep.go). The sweep marks a
// resource Absent when its status.node names this machine and the
// machine does not publish its endpoint, such as the Sink of an
// unplugged USB card. It keeps the resource's spec.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// openCard, setLevel, setRoute, and switchCodec are fields so that a
// test can run a pass without a sound card or PipeWire.
type endpointControl struct {
	client  *apiclient.Client
	machine string

	// cache holds the Sink and Source watch stores. A pass reads a
	// resource from them, and from the API server when the store has no
	// current copy (objectcache.go).
	cache objectCache

	claims *preparedClaims
	now    func() time.Time

	openCard func(card int) (*mixer, error)

	// setLevel writes a level to a PipeWire node, and setRoute writes
	// it to the Route of a Bluetooth device.
	setLevel func(ctx context.Context, node pwNode, level levelWrite) error
	setRoute func(ctx context.Context, device int, route pwRoute, level levelWrite) error

	// switchCodec switches a Bluetooth speaker to the codec and returns
	// the sink that PipeWire publishes for it after the switch.
	switchCodec func(ctx context.Context, address, codec string, sink bluezSink) (bluezSink, error)

	// readings is the metrics registry. A nil registry records nothing,
	// so a test can leave it out.
	readings *metrics

	// recorder posts the Events of this machine's Sinks and Sources. A
	// nil recorder posts nothing.
	recorder *events.Recorder

	// nodes maps an endpoint's name to the PipeWire cookie and node id
	// that the operator last read for it, and the levels it last wrote.
	// When a node's id differs from the record, PipeWire built the node
	// after that read, and the pass writes the default unity level to
	// it. The map is a cache of status.applied, and the first pass after
	// a start reads status.applied instead (resting.go).
	nodes map[string]nodeRecord

	// loggedFailures maps a name to the failure text that report last logged
	// for it, so a failure that repeats logs once.
	loggedFailures map[string]string

	// asks maps a Sink's name to the time of the last volume request
	// that the operator read from it. latest maps a sink endpoint's name
	// to the facts that the last pass read, and a volume request between
	// passes is applied against those facts (asks.go).
	asks   map[string]string
	latest map[string]endpointFacts

	// swept is the endpoint names at the last sweep, and sweptAt is the
	// time of that sweep. sweepDue reports a sweep as due when the names
	// change, and otherwise once per backstopInterval (sweep.go).
	swept   []string
	sweptAt time.Time
}

func newEndpointControl(client *apiclient.Client, cached objectCache, machine string, claims *preparedClaims,
	feed *graphFeed, readings *metrics) *endpointControl {
	return &endpointControl{
		client:         client,
		cache:          cached,
		machine:        machine,
		claims:         claims,
		now:            time.Now,
		openCard:       openMixer,
		setLevel:       setNodeLevel,
		setRoute:       setRouteLevel,
		switchCodec:    speakerCodecSwitch(feed).choose,
		readings:       readings,
		nodes:          map[string]nodeRecord{},
		loggedFailures: map[string]string{},
		asks:           map[string]string{},
		latest:         map[string]endpointFacts{},
	}
}

// endpoint is the facts that one pass read about an endpoint, with the
// open control device that the pass writes the endpoint's controls
// through. card is nil for a Bluetooth speaker, which is on no card,
// and for a card that failed to open.
type endpoint struct {
	facts endpointFacts
	card  *mixer
}

// pass reconciles every endpoint this machine publishes, and then
// sweeps when sweepDue reports that a sweep is due. A failure on one
// endpoint does not stop the others, and pass returns every failure
// joined into one error.
//
// layouts maps a declared sink's name to the layout state that the
// caller composed for this pass (layoutdrift.go).
func (e *endpointControl) pass(ctx context.Context, endpoints []alsaEndpoint,
	speakers map[string]speaker, graph pwGraph, layouts map[string]layoutState) error {
	// The control devices stay open until the pass ends, because the
	// descriptor that reads a control's value is also the one that
	// writes the declaration back to it.
	cards := e.openCards(endpoints)
	defer closeCards(cards)

	var failures []error
	present := map[string]bool{}
	e.latest = map[string]endpointFacts{}
	for _, reading := range e.read(cards, endpoints, speakers, graph, layouts) {
		present[reading.facts.Name] = true
		if err := e.reconcile(ctx, reading); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", reading.facts.Name, err))
		}
	}
	if err := e.sweep(present); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func (e *endpointControl) read(cards map[int]*mixer, endpoints []alsaEndpoint,
	speakers map[string]speaker, graph pwGraph, layouts map[string]layoutState) []endpoint {
	readings := make([]endpoint, 0, len(endpoints)+len(speakers))
	grouped := byCard(endpoints)
	for _, card := range slices.Sorted(maps.Keys(grouped)) {
		readings = append(readings, e.cardEndpoints(cards[card], grouped[card], graph, layouts)...)
	}
	for _, address := range slices.Sorted(maps.Keys(speakers)) {
		sink, hasSink := graph.Speakers[address]
		name := speakerName(address)
		readings = append(readings, endpoint{facts: endpointFacts{
			Name:      name,
			Direction: directionSink,
			Machine:   e.machine,
			Speaker: &speakerFacts{
				Address: address,
				Paired:  speakers[address],
				Sink:    sink,
				HasSink: hasSink,
			},
			Node:     sink.sinkNode(),
			HasNode:  hasSink,
			PipeWire: graph.Cookie,
			Claims:   e.claims.holders(name),
		}})
	}
	return readings
}

// byCard groups endpoints by card, because the controls and jacks are
// read once per card.
func byCard(endpoints []alsaEndpoint) map[int][]alsaEndpoint {
	cards := map[int][]alsaEndpoint{}
	for _, endpoint := range endpoints {
		cards[endpoint.Card] = append(cards[endpoint.Card], endpoint)
	}
	return cards
}

// openCards opens the control device of every card in endpoints, for
// one pass. Each pass opens the devices again, and does not keep them
// open for the life of the pod, because a card's set of controls
// changes when a monitor is plugged in and an open reads the current
// set. A card that fails to open maps to nil, and its endpoints publish
// without capabilities or controls.
func (e *endpointControl) openCards(endpoints []alsaEndpoint) map[int]*mixer {
	cards := map[int]*mixer{}
	for _, card := range slices.Sorted(maps.Keys(byCard(endpoints))) {
		device, err := e.openCard(card)
		if err != nil {
			e.report(fmt.Sprintf("card %d", card),
				[]string{fmt.Sprintf("opening the control device: %v", err)})
			cards[card] = nil
			continue
		}
		e.report(fmt.Sprintf("card %d", card), nil)
		cards[card] = device
	}
	return cards
}

func closeCards(cards map[int]*mixer) {
	for _, device := range cards {
		if device != nil {
			_ = device.Close()
		}
	}
}

func (e *endpointControl) cardEndpoints(device *mixer, endpoints []alsaEndpoint, graph pwGraph,
	layouts map[string]layoutState) []endpoint {
	var attached map[string][]control
	var jacks map[string]bool
	if device != nil {
		attached = attachControls(device.controls, endpointControlsOf(endpoints))
		sensed, err := device.jackStates()
		if err != nil {
			// With no jack states, the Connected condition reports True
			// with the reason NoJackSensing, as for a card that senses no
			// jack.
			fmt.Fprintf(os.Stderr, "reading the jacks of card %d: %v\n", device.card, err)
		}
		jacks = sensed
	}

	readings := make([]endpoint, 0, len(endpoints))
	for _, alsa := range endpoints {
		node, hasNode := graph.Nodes[alsa.graphAddress()]
		controls := attached[alsa.Name()]
		plugged, sensed := jackState(alsa.direction(), jacks)
		var layout *layoutState
		if state, ok := layouts[alsa.Name()]; ok {
			layout = &state
		}
		readings = append(readings, endpoint{
			card: device,
			facts: endpointFacts{
				Name:      alsa.Name(),
				Direction: alsa.direction(),
				Machine:   e.machine,
				Endpoint:  alsa,
				Node:      node,
				HasNode:   hasNode,
				PipeWire:  graph.Cookie,
				Controls:  controls,
				Values:    e.controlValues(device, alsa.Name(), controls),
				Plugged:   plugged,
				Sensed:    sensed,
				Claims:    e.claims.holders(alsa.Name()),
				Layout:    layout,
			},
		})
	}
	return readings
}

// controlValues reads every control of one endpoint by element and not
// by name, because a card with several HDMI outputs has one IEC958
// control for each output under the same name, and only the element's
// index tells them apart.
func (e *endpointControl) controlValues(device *mixer, name string, controls []control) map[string]string {
	if device == nil || len(controls) == 0 {
		return nil
	}
	values := make(map[string]string, len(controls))
	var failures []string
	for _, element := range controls {
		value, err := device.readElement(element)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		values[element.Name] = value
	}
	e.report(name+" controls", failures)
	return values
}

// reconcile creates the endpoint's resource when it is missing, writes
// the spec to the endpoint where they differ, and writes the status. It
// records the duration and the error in the reconcile metrics on every
// call, also when nothing changed.
func (e *endpointControl) reconcile(ctx context.Context, reading endpoint) error {
	kind, do := SinkKind, e.reconcileSink
	if reading.facts.Direction == directionSource {
		kind, do = SourceKind, e.reconcileSource
	}
	start := time.Now()
	err := do(ctx, reading)
	e.readings.reconciled(kind, time.Since(start), err)
	return err
}

func (e *endpointControl) reconcileSink(ctx context.Context, reading endpoint) error {
	sink, err := informer.ReadOne[Sink](e.client, e.cache.sinks, reading.facts.Name, sinkPath(reading.facts.Name))
	if errors.Is(err, apiclient.ErrNotFound) {
		sink, err = e.createSink(reading.facts.Name)
	}
	if err != nil {
		return err
	}
	e.noteAsk(sink)
	actuated := e.actuate(ctx, endpointReference(SinkKind, sink.Metadata.Name, sink.Metadata.UID),
		sink.Spec.declaration(), sink.Status.Applied, reading)
	reading.facts.Written = e.nodes[reading.facts.Name].written
	reading.facts.Applied = e.applied(reading.facts)
	e.latest[reading.facts.Name] = reading.facts
	e.recordEndpoint(reading)
	now := e.now()
	want := func(published EndpointStatus) (EndpointStatus, bool) {
		return reading.facts.status(published, now), true
	}
	err = e.settleSinkStatus(sink, want)
	if errors.Is(err, apiclient.ErrNotFound) {
		// The store held a Sink that was deleted after the read.
		if sink, err = e.createSink(reading.facts.Name); err == nil {
			err = e.settleSinkStatus(sink, want)
		}
	}
	return errors.Join(actuated, err)
}

func (e *endpointControl) reconcileSource(ctx context.Context, reading endpoint) error {
	source, err := informer.ReadOne[Source](e.client, e.cache.sources, reading.facts.Name, sourcePath(reading.facts.Name))
	if errors.Is(err, apiclient.ErrNotFound) {
		source, err = e.createSource(reading.facts.Name)
	}
	if err != nil {
		return err
	}
	actuated := e.actuate(ctx, endpointReference(SourceKind, source.Metadata.Name, source.Metadata.UID),
		source.Spec.declaration(), source.Status.Applied, reading)
	reading.facts.Written = e.nodes[reading.facts.Name].written
	reading.facts.Applied = e.applied(reading.facts)
	e.recordEndpoint(reading)
	now := e.now()
	want := func(published EndpointStatus) (EndpointStatus, bool) {
		return reading.facts.status(published, now), true
	}
	err = e.settleSourceStatus(source, want)
	if errors.Is(err, apiclient.ErrNotFound) {
		// The store held a Source that was deleted after the read.
		if source, err = e.createSource(reading.facts.Name); err == nil {
			err = e.settleSourceStatus(source, want)
		}
	}
	return errors.Join(actuated, err)
}

// recordEndpoint sets the audio_endpoint_connected, _ready, and
// _claimed gauges for one endpoint. The connected and ready values come
// from the same methods that build the resource's conditions
// (sinkstatus.go).
func (e *endpointControl) recordEndpoint(reading endpoint) {
	connected, _, _ := reading.facts.connected()
	ready, _, _ := reading.facts.ready()
	e.readings.endpoint(reading.facts.Name, connected, ready, len(reading.facts.Claims) > 0)
}

// report logs the failures for name when they differ from the failures
// it last logged for name, so a failure that persists logs once and not
// on every pass. An empty failures clears the record. report returns
// the text it logged, or "" when it logged nothing.
func (e *endpointControl) report(name string, failures []string) string {
	if len(failures) == 0 {
		delete(e.loggedFailures, name)
		return ""
	}
	said := strings.Join(failures, "; ")
	if e.loggedFailures[name] == said {
		return ""
	}
	e.loggedFailures[name] = said
	fmt.Fprintf(os.Stderr, "%s: %s\n", name, said)
	return said
}

// sameStatus reports whether two statuses are equal. It compares the
// JSON encodings, so two statuses that encode to the same object are
// equal, such as one with a nil list and one with an empty list under
// omitempty.
func sameStatus(published, current EndpointStatus) bool {
	was, err := json.Marshal(published)
	if err != nil {
		return false
	}
	is, err := json.Marshal(current)
	if err != nil {
		return false
	}
	return string(was) == string(is)
}
