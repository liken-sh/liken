package main

// The controller behind the two resources.
//
// One pass goes over every endpoint the machine publishes, and for
// each one it does three things in order. It makes the resource
// exist, created with an empty spec, because the operator declares
// nothing about how an endpoint should rest: the resource exists so a
// person can. It writes the resting layer, which is every declared
// field the endpoint has diverged from, and nothing at all for a
// field the spec leaves out. And it writes the whole of status, but
// only where this pass would say something the published status does
// not already say, so a settled endpoint costs no write.
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
	client  *Client
	machine string
	claims  *preparedClaims
	now     func() time.Time

	// openCard opens one card's control device. A card that does not
	// open costs its endpoints their capabilities and their controls,
	// and the rest of the status still publishes.
	openCard func(card int) (*mixer, error)

	// setLevel writes a level on a node, and setRoute writes one on a
	// Bluetooth device's Route.
	setLevel func(ctx context.Context, node pwNode, volume int, mute bool) error
	setRoute func(ctx context.Context, device int, route pwRoute, volume int, mute bool) error

	// switchCodec makes a speaker play one codec and answers with the
	// sink that came back.
	switchCodec func(ctx context.Context, address, codec string, sink bluezSink) (bluezSink, error)

	// nodes is what this operator remembers about each endpoint's
	// node: the PipeWire object id it had when the operator last
	// looked, and the level last written to it. A node whose id is
	// not this one is a node PipeWire built since, and the unity
	// default writes to a new node alone.
	nodes map[string]nodeRecord

	// refusals keeps the report of a declaration this operator cannot
	// write to one line for each run of passes that finds it.
	refusals map[string]string

	// swept and sweptAt hold the endpoints of the last listing and
	// when it ran, which is what keeps the listing to the slower
	// cadence.
	swept   []string
	sweptAt time.Time
}

// newEndpointControl builds the controller. Every seam takes its real
// implementation here and a stand-in only in a test.
func newEndpointControl(client *Client, machine string, claims *preparedClaims,
	graph func(context.Context) (pwGraph, error)) *endpointControl {
	return &endpointControl{
		client:      client,
		machine:     machine,
		claims:      claims,
		now:         time.Now,
		openCard:    openMixer,
		setLevel:    setNodeLevel,
		setRoute:    setRouteLevel,
		switchCodec: speakerCodecSwitch(graph).choose,
		nodes:       map[string]nodeRecord{},
		refusals:    map[string]string{},
	}
}

// nodeRecord is one endpoint's node as the controller last saw it.
type nodeRecord struct {
	id      int
	written *levelWrite
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
func (e *endpointControl) pass(ctx context.Context, endpoints []alsaEndpoint,
	speakers map[string]speaker, graph pwGraph) error {
	// The control devices stay open for the length of the pass,
	// because the same descriptor reads a control's value and writes
	// the declaration back to it.
	cards := e.openCards(endpoints)
	defer closeCards(cards)

	var failures []error
	present := map[string]bool{}
	for _, reading := range e.read(cards, endpoints, speakers, graph) {
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

// read gathers what one pass read about every endpoint, from the
// card, from bluetoothd's paired set, and from the graph.
func (e *endpointControl) read(cards map[int]*mixer, endpoints []alsaEndpoint,
	speakers map[string]speaker, graph pwGraph) []endpoint {
	readings := make([]endpoint, 0, len(endpoints)+len(speakers))
	grouped := byCard(endpoints)
	for _, card := range slices.Sorted(maps.Keys(grouped)) {
		readings = append(readings, e.cardEndpoints(cards[card], grouped[card], graph)...)
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
func (e *endpointControl) cardEndpoints(device *mixer, endpoints []alsaEndpoint, graph pwGraph) []endpoint {
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
func (e *endpointControl) reconcile(ctx context.Context, reading endpoint) error {
	if reading.facts.Direction == directionSource {
		return e.reconcileSource(ctx, reading)
	}
	return e.reconcileSink(ctx, reading)
}

func (e *endpointControl) reconcileSink(ctx context.Context, reading endpoint) error {
	sink, err := getSink(e.client, reading.facts.Name)
	if errors.Is(err, ErrNotFound) {
		sink, err = createSink(e.client, reading.facts.Name)
	}
	if err != nil {
		return err
	}
	actuated := e.actuate(ctx, sink.Spec.declaration(), reading)
	status := reading.facts.status(sink.Status, e.now())
	if sameStatus(sink.Status, status) {
		return actuated
	}
	_, err = writeSinkStatus(e.client, sink, status)
	return errors.Join(actuated, err)
}

func (e *endpointControl) reconcileSource(ctx context.Context, reading endpoint) error {
	source, err := getSource(e.client, reading.facts.Name)
	if errors.Is(err, ErrNotFound) {
		source, err = createSource(e.client, reading.facts.Name)
	}
	if err != nil {
		return err
	}
	actuated := e.actuate(ctx, source.Spec.declaration(), reading)
	status := reading.facts.status(source.Status, e.now())
	if sameStatus(source.Status, status) {
		return actuated
	}
	_, err = writeSourceStatus(e.client, source, status)
	return errors.Join(actuated, err)
}

// actuate writes what the declaration and the endpoint disagree on.
func (e *endpointControl) actuate(ctx context.Context, spec declaration, reading endpoint) error {
	writes, refusals := plannedWrites(spec, reading.facts, e.remember(reading.facts))
	e.report(reading.facts.Name, refusals)
	err := e.apply(ctx, reading, writes)
	if writes.Level != nil {
		if err != nil {
			// The node is recorded as seen before the write, and a
			// level that did not land has to be tried again, so the
			// failure forgets it and the next pass reads it as a new
			// node.
			delete(e.nodes, reading.facts.Name)
		} else {
			e.nodes[reading.facts.Name] = nodeRecord{id: reading.facts.Node.ID, written: writes.Level}
		}
	}
	return err
}

// remember reports whether PipeWire built this endpoint's node since
// the operator last looked, with the level last written to the node
// that stands, and records the node it sees now.
//
// This is what the unity default and a declaration on a suspended
// node read. A new node is written to unity once. A level a person
// set by hand on a node that stands is left alone: it reaches
// status.observed and nothing else, because the spec declares no
// level and the operator invents none.
func (e *endpointControl) remember(facts endpointFacts) nodeMemory {
	if !facts.HasNode {
		delete(e.nodes, facts.Name)
		return nodeMemory{}
	}
	last, seen := e.nodes[facts.Name]
	if !seen || last.id != facts.Node.ID {
		e.nodes[facts.Name] = nodeRecord{id: facts.Node.ID}
		return nodeMemory{New: true}
	}
	return nodeMemory{Written: last.written}
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

// sweep reports the endpoints this machine held a resource for and no
// longer publishes.
//
// The resource is never deleted, because it holds the declaration a
// person wrote. A USB card that is unplugged for an hour must come
// back to the level it rested at, and a speaker that moved to another
// machine keeps its Sink. The conditions are what report the absence.
func (e *endpointControl) sweep(present map[string]bool) error {
	if !e.sweepDue(present) {
		return nil
	}
	var failures []error
	sinks, err := listSinks(e.client)
	if err != nil {
		failures = append(failures, err)
	}
	for _, sink := range sinks {
		if sink.Status.Node != e.machine || present[sink.Metadata.Name] {
			continue
		}
		status := absentStatus(sink.Status, e.now())
		if sameStatus(sink.Status, status) {
			continue
		}
		if _, err := writeSinkStatus(e.client, &sink, status); err != nil {
			failures = append(failures, err)
		}
	}
	sources, err := listSources(e.client)
	if err != nil {
		failures = append(failures, err)
	}
	for _, source := range sources {
		if source.Status.Node != e.machine || present[source.Metadata.Name] {
			continue
		}
		status := absentStatus(source.Status, e.now())
		if sameStatus(source.Status, status) {
			continue
		}
		if _, err := writeSourceStatus(e.client, &source, status); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// sweepDue answers whether this pass lists the resources. It does
// when the endpoints this machine publishes are not the endpoints of
// the last listing, so a card that arrives or leaves is answered on
// the pass that finds it, and otherwise once per backstop interval.
func (e *endpointControl) sweepDue(present map[string]bool) bool {
	names := slices.Sorted(maps.Keys(present))
	if !slices.Equal(names, e.swept) {
		e.swept, e.sweptAt = names, e.now()
		return true
	}
	if e.now().Before(e.sweptAt.Add(backstopInterval)) {
		return false
	}
	e.sweptAt = e.now()
	return true
}

// absentStatus is what a resource reports once its machine no longer
// publishes the endpoint. The identity it read stays, because it says
// which piece of hardware this resource is for.
func absentStatus(published EndpointStatus, now time.Time) EndpointStatus {
	status := published
	status.NodeName = ""
	status.Format = nil
	status.Claim = nil
	status.Conditions = setCondition(status.Conditions, condition(ConnectedCondition, false,
		"EndpointAbsent", "this machine no longer publishes the endpoint", now))
	status.Conditions = setCondition(status.Conditions, condition(ReadyCondition, false,
		"NoNode", "PipeWire holds no node for this endpoint", now))
	return status
}
