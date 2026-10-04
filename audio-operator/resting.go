package main

// The resting layer: the settings a spec declares, and the write each
// one lands in.
//
// The operator writes nothing at all for a field the spec leaves out.
// So an empty spec costs the hardware nothing, and a value a person
// set by hand on an undeclared field stays where they put it. The one
// exception is the unity default: a sink node PipeWire has just built
// is set to unity when its spec declares no level, because this pod
// stores no volumes and unity is the one level the operator can
// defend with no declaration to read.
//
// A declared control and a declared codec are written wherever the
// endpoint diverges from them. A declared level and mute are not. The
// operator writes them when the declaration changes and when the
// node appears, such as a speaker that reconnects, and follows the
// device at every other time. The level has other writers: the media
// operator's volume asks (asks.go) and a speaker's own buttons. An
// operator that wrote the declaration back on each divergence would
// undo each of them on its next pass.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// levelWrite is one write of a level: the volume as a percent of
// unity, the mute, or both, which go in one write because PipeWire
// applies one Props pod at once. A field the spec leaves out is nil
// and stays out of the pod, so a declared mute on a node whose level
// is unknown never writes a gain of zero beside it.
type levelWrite struct {
	Volume *int
	Mute   *bool
}

// same reports whether two level writes state the same thing.
func (l levelWrite) same(other levelWrite) bool {
	return optionalEqual(l.Volume, other.Volume) && optionalEqual(l.Mute, other.Mute)
}

func optionalEqual[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// String names the write for a log line.
func (l levelWrite) String() string {
	var parts []string
	if l.Volume != nil {
		parts = append(parts, fmt.Sprintf("volume %d%%", *l.Volume))
	}
	if l.Mute != nil {
		parts = append(parts, fmt.Sprintf("mute %t", *l.Mute))
	}
	return strings.Join(parts, ", ")
}

// controlWrite is one write of one of the card's own controls.
type controlWrite struct {
	Element control
	Value   string
}

// endpointWrites is everything one pass must write to make one
// endpoint rest where its spec says. A pass that finds nothing
// diverged writes nothing.
type endpointWrites struct {
	Level    *levelWrite
	Controls []controlWrite
	Codec    string
}

// nodeMemory is what the controller remembers about an endpoint's
// node: whether PipeWire built it since the last pass, whether the
// first pass after the operator started found it, and the declaration
// the node was last judged against, if any. A declaration that differs
// from that judgment is a change of the spec.
type nodeMemory struct {
	New   bool
	Found bool
	Held  *levelWrite
}

// nodeRecord is one endpoint's node as the controller last saw it.
//
// written is the level this operator last wrote, from the spec or from
// a volume ask, which status.observed reports for an idle node. held is
// the declaration the node was last judged against: the declaration
// this operator wrote, or the one it adopted because the node already
// held it or because the first pass found the node. A declaration is
// written again only when it differs from held, so a level that moved
// at the device or by an ask stays where it moved.
type nodeRecord struct {
	id      int
	written *levelWrite
	held    *levelWrite
}

// actuate writes what the declaration and the endpoint disagree on.
func (e *endpointControl) actuate(ctx context.Context, spec declaration, reading endpoint) error {
	name := reading.facts.Name
	writes, refusals := plannedWrites(spec, reading.facts, e.remember(reading.facts))
	e.report(name, refusals)
	err := e.apply(ctx, reading, writes)
	record, seen := e.nodes[name]
	switch {
	case !seen:
		// The endpoint has no node, so there is nothing to record.
	case writes.Level != nil && err != nil:
		// The node is recorded as seen before the write, and a
		// level that did not land has to be tried again, so the
		// failure forgets it and the next pass reads it as a new
		// node.
		delete(e.nodes, name)
	case writes.Level != nil:
		record.written = writes.Level
		if spec.Volume != nil || spec.Mute != nil {
			record.held = writes.Level
		}
		e.nodes[name] = record
	case spec.Volume != nil || spec.Mute != nil:
		// The pass judged the declaration and planned no level write:
		// the node holds it, the first pass adopted it, or the
		// declaration is unchanged. Later passes judge against it.
		record.held = &levelWrite{Volume: spec.Volume, Mute: spec.Mute}
		e.nodes[name] = record
	default:
		// The spec declares no level, so a declaration a person writes
		// later is a change, even when it states the level held before.
		record.held = nil
		e.nodes[name] = record
	}
	return err
}

// remember reports whether PipeWire built this endpoint's node since
// the operator last looked, with the declaration the node that stands
// was last judged against, and records the node it sees now.
//
// This is what the unity default and a declaration read. A new node
// takes the declaration, or unity when there is none, once. A level a
// person or an ask set on a node that stands is left alone: it reaches
// status.observed and nothing else. A node the first pass finds is not
// new, because the memory starts empty when the operator starts, and a
// node that stood before the start can hold a level a person chose,
// under a claim that plays. The first pass reports it as found, so
// that a declaration on it is adopted and not written.
func (e *endpointControl) remember(facts endpointFacts) nodeMemory {
	if !facts.HasNode {
		delete(e.nodes, facts.Name)
		return nodeMemory{}
	}
	last, seen := e.nodes[facts.Name]
	if !seen || last.id != facts.Node.ID {
		e.nodes[facts.Name] = nodeRecord{id: facts.Node.ID}
		return nodeMemory{New: e.started, Found: !e.started}
	}
	return nodeMemory{Held: last.held}
}

// plannedWrites is the resting layer's whole decision: what the
// declaration and the endpoint disagree on, and what the declaration
// states that the endpoint cannot take.
//
// Four rules. A declared level is written when the node is new and
// when the declaration changed, unless the endpoint already holds it.
// A declared control or codec is written where the endpoint diverges
// from it. A field the spec leaves out is written nowhere, apart from
// the unity default a new sink node takes. A value the hardware
// refuses is reported and never written, so a typo in a control name
// costs one log line and no register.
func plannedWrites(spec declaration, facts endpointFacts, node nodeMemory) (endpointWrites, []string) {
	var writes endpointWrites
	var refusals []string

	volume, mute, known := facts.level()
	switch {
	case !facts.HasNode:
		// An endpoint with no node has nothing to write a level to.
		// The no-sink taint and the Ready condition are where that
		// shows, and the controls below still write, because they
		// are the card's and not the node's.
	case spec.Volume != nil || spec.Mute != nil:
		want := levelWrite{Volume: spec.Volume, Mute: spec.Mute}
		// The declaration is compared with the one the node was last
		// judged against, and not with the level the node reports, so a
		// level that moved at the device or by an ask stays where it
		// moved. A node with no judgment yet takes the declaration.
		//
		// The first pass after a start writes nothing. The node stood
		// before the start, and its level can be an ask or a press of a
		// speaker's button that the declaration does not know. A
		// suspended node also reports no level, and PipeWire 1.4.2
		// applies a Props write to one and announces no change, so the
		// operator cannot read whether it holds the declaration. The
		// pass records the declaration as the one the node holds.
		changed := node.New || (!node.Found && (node.Held == nil || !node.Held.same(want)))
		holds := known && (want.Volume == nil || *want.Volume == volume) && (want.Mute == nil || *want.Mute == mute)
		if changed && !holds {
			writes.Level = &want
		}
	case facts.Direction == directionSink && node.New && known && volume != unityPercent:
		unity := unityPercent
		writes.Level = &levelWrite{Volume: &unity}
	}

	for _, name := range slices.Sorted(maps.Keys(spec.Controls)) {
		value := spec.Controls[name]
		element, declared := findControl(facts.Controls, name)
		if !declared {
			refusals = append(refusals, fmt.Sprintf("the spec states the control %q, "+
				"and this endpoint lists none by that name", name))
			continue
		}
		if err := declarable(element.Capability, value); err != nil {
			refusals = append(refusals, fmt.Sprintf("the spec states %q for %q: %v", value, name, err))
			continue
		}
		if current, read := facts.Values[name]; read && current == value {
			continue
		}
		writes.Controls = append(writes.Controls, controlWrite{Element: element, Value: value})
	}

	if codec, refusal := plannedCodec(spec, facts); refusal != "" {
		refusals = append(refusals, refusal)
	} else {
		writes.Codec = codec
	}
	return writes, refusals
}

// plannedCodec returns the codec to apply, or no change while a claim
// allocates the speaker. Switching codecs replaces the speaker's node
// and interrupts playback. A claim's codec parameter takes precedence
// for the duration of the claim. The operator applies the codec from
// the Sink spec after the claim ends.
func plannedCodec(spec declaration, facts endpointFacts) (string, string) {
	if spec.Codec == nil || facts.Speaker == nil || !facts.Speaker.HasSink {
		return "", ""
	}
	codec := *spec.Codec
	if facts.Speaker.Sink.Codec == codec || facts.Claim != nil {
		return "", ""
	}
	if _, offered := findCodec(facts.Speaker.Sink.Codecs, codec); !offered {
		return "", fmt.Sprintf("the spec states the codec %q, and the speaker offers %s",
			codec, codecList(facts.Speaker.Sink.Codecs))
	}
	return codec, ""
}

// findControl picks one of an endpoint's controls by the kernel's own
// name for it.
func findControl(controls []control, name string) (control, bool) {
	for _, element := range controls {
		if element.Name == name {
			return element, true
		}
	}
	return control{}, false
}

// declarable reports whether a control takes the declared value, by
// encoding it the way the write would. One encoder answers for both,
// so a value this accepts is a value the card takes.
func declarable(capability controlCapability, value string) error {
	var scratch ctlElemValue
	return encodeControlValue(capability, value, scratch.Value[:])
}

// apply makes the writes one pass planned. Each one is reported on
// its own line, because a write to hardware is the one thing this
// operator does that a person cannot see in the resource.
func (e *endpointControl) apply(ctx context.Context, reading endpoint, writes endpointWrites) error {
	var failures []error
	facts := reading.facts
	if level := writes.Level; level != nil {
		if err := e.applyLevel(ctx, facts, *level); err != nil {
			failures = append(failures, err)
			e.readings.controlFailed(operationVolume)
		} else {
			fmt.Printf("%s: %s\n", facts.Name, level)
		}
	}
	// A control write goes through the same descriptor the value was
	// read through, so an endpoint whose card did not open has no
	// control to write and lists none for a spec to state.
	for _, write := range writes.Controls {
		if reading.card == nil {
			break
		}
		if err := reading.card.writeElement(write.Element, write.Value); err != nil {
			failures = append(failures, err)
			e.readings.controlFailed(operationControl)
			continue
		}
		fmt.Printf("%s: %s is %s\n", facts.Name, write.Element.Name, write.Value)
	}
	if writes.Codec != "" {
		if _, err := e.switchCodec(ctx, facts.Speaker.Address, writes.Codec, facts.Speaker.Sink); err != nil {
			failures = append(failures, err)
			e.readings.controlFailed(operationCodec)
		} else {
			fmt.Printf("%s: codec %s\n", facts.Name, writes.Codec)
		}
	}
	return errors.Join(failures...)
}

// applyLevel writes one level where the endpoint's level lives.
func (e *endpointControl) applyLevel(ctx context.Context, facts endpointFacts, level levelWrite) error {
	if device, route, absolute := facts.absoluteRoute(); absolute {
		return e.setRoute(ctx, device, route, level)
	}
	return e.setLevel(ctx, facts.Node, level)
}
