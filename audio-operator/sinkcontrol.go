package main

// The controller behind the two resources.
//
// One pass goes over every endpoint the machine publishes, and for
// each one it does three things in order. It makes the resource
// exist, created with an empty spec, because the operator declares
// nothing about how an endpoint should rest: the resource exists so a
// person can. It writes the resting layer: a declared level when the
// declaration changed or the node is new, every declared control and
// codec the endpoint has diverged from, and nothing at all for a field
// the spec leaves out (resting.go). And it writes its part of status,
// but only where this pass would say something the published status
// does not already say, so a settled endpoint costs no write.
//
// Between passes, the loop applies the volume asks the media operator
// writes into a Sink's status.session (asks.go).
//
// The pass ends with a sweep for the resources this machine holds
// whose endpoint it no longer publishes, such as a USB card that was
// unplugged. Those keep their spec and report the absence.

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
	"github.com/liken-sh/liken/kubernetes/informer"
)

// endpointControl reconciles the Sinks and the Sources of one
// machine.
//
// The four writes are fields so that a test drives the resting layer
// with no card and no PipeWire behind it, the way the DRA plugin's
// seams work. nodes is what the unity default reads: it records the
// PipeWire object id each endpoint's node had when this operator last
// looked, and a node with a different id is one PipeWire built since.
type endpointControl struct {
	client  *apiclient.Client
	machine string

	// cache is the two watches' stores and memos, which the pass reads
	// in place of the API server (objectcache.go).
	cache objectCache

	claims *preparedClaims
	now    func() time.Time

	// openCard opens one card's control device. A card that does not
	// open costs its endpoints their capabilities and their controls,
	// and the rest of the status still publishes.
	openCard func(card int) (*mixer, error)

	// setLevel writes a level on a node, and setRoute writes one on a
	// Bluetooth device's Route.
	setLevel func(ctx context.Context, node pwNode, level levelWrite) error
	setRoute func(ctx context.Context, device int, route pwRoute, level levelWrite) error

	// switchCodec makes a speaker play one codec and answers with the
	// sink that came back.
	switchCodec func(ctx context.Context, address, codec string, sink bluezSink) (bluezSink, error)

	// readings is the registry the metrics listener serves. A nil
	// registry takes every call here and drops it, which is what the
	// tests that build a controller by hand run with.
	readings *metrics

	// nodes is what this operator remembers about each endpoint's
	// node: the PipeWire object id it had when the operator last
	// looked, and the level last written to it. A node whose id is
	// not this one is a node PipeWire built since, and the unity
	// default writes to a new node alone.
	nodes map[string]nodeRecord

	// started is true once the first pass has run. The first pass
	// finds nodes PipeWire built before this operator started, with
	// levels a person may have chosen, so it records them as seen and
	// writes no unity to them. Only a node that appears after it is
	// new.
	started bool

	// refusals keeps the report of a declaration this operator cannot
	// write to one line for each run of passes that finds it.
	refusals map[string]string

	// asks is the time of the last volume ask this operator read on
	// each Sink, and latest is what the last pass read about each sink
	// endpoint it publishes. An ask applies to the endpoint as the last
	// pass read it (asks.go).
	asks   map[string]string
	latest map[string]endpointFacts

	// swept and sweptAt hold the endpoints of the last listing and
	// when it ran, which is what keeps the listing to the slower
	// cadence.
	swept   []string
	sweptAt time.Time
}

// newEndpointControl builds the controller. Every seam takes its real
// implementation here and a stand-in only in a test.
func newEndpointControl(client *apiclient.Client, cached objectCache, machine string, claims *preparedClaims,
	feed *graphFeed, readings *metrics) *endpointControl {
	return &endpointControl{
		client:      client,
		cache:       cached,
		machine:     machine,
		claims:      claims,
		now:         time.Now,
		openCard:    openMixer,
		setLevel:    setNodeLevel,
		setRoute:    setRouteLevel,
		switchCodec: speakerCodecSwitch(feed).choose,
		readings:    readings,
		nodes:       map[string]nodeRecord{},
		refusals:    map[string]string{},
		asks:        map[string]string{},
		latest:      map[string]endpointFacts{},
	}
}

// endpoint is one endpoint of one pass: the facts it read, and the
// open control device its own writes go through. The device is nil on
// a Bluetooth speaker, which is on no card, and on a card that did not
// open.
type endpoint struct {
	facts endpointFacts
	card  *mixer
}

// pass reconciles every endpoint this machine publishes, and then the
// resources this machine holds whose endpoint is gone.
//
// Each card is opened once and read once for all of its endpoints.
// One endpoint's failure never stops another's: the failures are
// collected and returned together, and the reconciler counts the
// pass as failed.
//
// layouts is the layout state of each declared sink, keyed by device
// name, which the reconciler composed this pass (layoutdrift.go).
func (e *endpointControl) pass(ctx context.Context, endpoints []alsaEndpoint,
	speakers map[string]speaker, graph pwGraph, layouts map[string]layoutState) error {
	// The control devices stay open for the length of the pass,
	// because the same descriptor reads a control's value and writes
	// the declaration back to it.
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
	e.started = true
	return errors.Join(failures...)
}

// read gathers what one pass read about every endpoint, from the
// card, from bluetoothd's paired set, and from the graph.
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
			Node:    sink.sinkNode(),
			HasNode: hasSink,
			Claim:   e.holder(name),
		}})
	}
	return readings
}

// byCard groups an inventory by the card each endpoint is on, because
// the controls and the jacks are read once per card.
func byCard(endpoints []alsaEndpoint) map[int][]alsaEndpoint {
	cards := map[int][]alsaEndpoint{}
	for _, endpoint := range endpoints {
		cards[endpoint.Card] = append(cards[endpoint.Card], endpoint)
	}
	return cards
}

// openCards opens the control device of every card the inventory
// holds, for the length of one pass.
//
// The card is enumerated on every pass rather than held open for the
// life of the pod, because a card's element set changes when a
// monitor arrives, and one open and a few ioctls read a state that
// cannot then go stale. A card that does not open costs its endpoints
// their capabilities and their controls, and the rest of their status
// still publishes.
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

// cardEndpoints reads one card: what it declares, what its jacks say,
// and the value of every control that belongs to each of its
// endpoints.
func (e *endpointControl) cardEndpoints(device *mixer, endpoints []alsaEndpoint, graph pwGraph,
	layouts map[string]layoutState) []endpoint {
	var attached map[string][]control
	var jacks map[string]bool
	if device != nil {
		attached = attachControls(device.controls, endpointControlsOf(endpoints))
		sensed, err := device.jackStates()
		if err != nil {
			// The card senses jacks and would not read them, so the
			// Connected condition falls back to the rule for a card
			// that senses none.
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
				Controls:  controls,
				Values:    e.controlValues(device, alsa.Name(), controls),
				Plugged:   plugged,
				Sensed:    sensed,
				Claim:     e.holder(alsa.Name()),
				Layout:    layout,
			},
		})
	}
	return readings
}

// controlValues reads every control that belongs to one endpoint.
//
// The read is by element and not by name, because a card with
// several HDMI slots declares one IEC958 control per slot under one
// name, and the index is what tells them apart.
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

// holder answers which claim holds one endpoint now.
func (e *endpointControl) holder(name string) *EndpointClaim {
	claim, held := e.claims.holder(name)
	if !held {
		return nil
	}
	return &claim
}

// reconcile makes one endpoint's resource exist, writes the resting
// declaration where the endpoint diverges from it, and writes the
// status last.
//
// This is the reconcile loop layer 2 reports on: the duration is
// observed and the error counted whether or not the pass changed
// anything, because a pass that changes nothing still counts as a run.
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
	actuated := e.actuate(ctx, sink.Spec.declaration(), reading)
	reading.facts.Written = e.nodes[reading.facts.Name].written
	e.latest[reading.facts.Name] = reading.facts
	e.recordEndpoint(reading)
	now := e.now()
	want := func(published EndpointStatus) (EndpointStatus, bool) {
		return reading.facts.status(published, now), true
	}
	err = e.settleSinkStatus(sink, want)
	if errors.Is(err, apiclient.ErrNotFound) {
		// The store held a copy of a resource somebody deleted since.
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
	actuated := e.actuate(ctx, source.Spec.declaration(), reading)
	reading.facts.Written = e.nodes[reading.facts.Name].written
	e.recordEndpoint(reading)
	now := e.now()
	want := func(published EndpointStatus) (EndpointStatus, bool) {
		return reading.facts.status(published, now), true
	}
	err = e.settleSourceStatus(source, want)
	if errors.Is(err, apiclient.ErrNotFound) {
		// The store held a copy of a resource somebody deleted since.
		if source, err = e.createSource(reading.facts.Name); err == nil {
			err = e.settleSourceStatus(source, want)
		}
	}
	return errors.Join(actuated, err)
}

// recordEndpoint puts what this pass read about one endpoint's
// presence on the hardware triple's gauges, from the same Connected
// and Ready facts sinkstatus.go composes into the resource's
// conditions, and whether a claim holds the endpoint now.
func (e *endpointControl) recordEndpoint(reading endpoint) {
	connected, _, _ := reading.facts.connected()
	ready, _, _ := reading.facts.ready()
	e.readings.endpoint(reading.facts.Name, connected, ready, reading.facts.Claim != nil)
}

// report prints one line for each run of passes that finds the same
// trouble with one endpoint, and clears the record when the trouble
// is gone.
func (e *endpointControl) report(name string, failures []string) {
	if len(failures) == 0 {
		delete(e.refusals, name)
		return
	}
	said := strings.Join(failures, "; ")
	if e.refusals[name] == said {
		return
	}
	e.refusals[name] = said
	fmt.Fprintf(os.Stderr, "%s: %s\n", name, said)
}

// sameStatus reports whether the published status already says what
// this pass would say. The comparison is of what goes on the wire, so
// a field the API server does not store cannot make every pass a
// write.
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
