package main

// The writes that make an endpoint match the level, mute, ALSA
// controls, and Bluetooth codec that its Sink or Source spec declares.
//
// The operator writes no field that the spec leaves out, so an empty
// spec never touches the hardware, and a value that a person set by
// hand stays. The exception is a sink node that PipeWire has just
// built, when the spec declares no level or mute: the operator sets it
// to unity (100%). It stores no earlier volume to restore, so unity is
// the only level it can justify.
//
// Controls and the codec are written on every pass where the endpoint
// differs from the spec. The declared level and mute are written only
// when the declaration changed or the node is new, as when a speaker
// reconnects. Other writers change the level on purpose, such as a volume
// request in status.session.volumeAsk (asks.go) and the buttons on a
// speaker, and writing the declared level on every pass would undo their
// change.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/liken-sh/liken/kubernetes/events"
)

// levelWrite is a volume in percent of unity, a mute, or both. Both go
// to PipeWire in one Props pod, which PipeWire applies as a unit. A nil
// field is left out of the pod, so the node keeps its current value.
type levelWrite struct {
	Volume *int
	Mute   *bool
}

func (l levelWrite) same(other levelWrite) bool {
	return optionalEqual(l.Volume, other.Volume) && optionalEqual(l.Mute, other.Mute)
}

func optionalEqual[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

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

type controlWrite struct {
	Element control
	Value   string
}

// endpointWrites is empty when the endpoint already matches its spec.
type endpointWrites struct {
	Level    *levelWrite
	Controls []controlWrite
	Codec    string
}

// nodeHistory is what earlier passes recorded about an endpoint's node.
// New is true when PipeWire built the node after the last pass read it.
// Reconciled is the declared level and mute that the node was last
// compared against, or nil when no level or mute was declared then.
type nodeHistory struct {
	New        bool
	Reconciled *levelWrite
}

// nodeRecord is what endpointControl keeps for one endpoint's node
// between passes.
//
// written is the last level the operator wrote to the node, from the
// spec or from a volume request. status.observed reports it when the node
// prints no level or is suspended, because the level that a suspended
// node prints can be stale.
//
// reconciled is the declared level and mute that the node was last
// compared against, whether or not that pass wrote them. The operator
// writes the declared level again only when the spec differs from
// reconciled.
//
// cookie and id identify the node. PipeWire reuses small ids after it
// restarts, so an id alone can match a node of an earlier PipeWire
// process. cookie is a random number that the PipeWire daemon picks
// when it starts.
type nodeRecord struct {
	cookie     uint32
	id         int
	written    *levelWrite
	reconciled *levelWrite
}

func (r nodeRecord) matches(facts endpointFacts) bool {
	return r.cookie == facts.PipeWire && r.id == facts.Node.ID
}

// actuate writes what the endpoint needs to match its spec, and updates
// the record of its node. It skips each declared value that the
// endpoint cannot accept, such as an unknown control name, a value out of
// range, or a codec the speaker does not offer. When the refused values
// differ from the ones of the last pass, it posts a SpecRefused Warning
// on the Sink or Source.
func (e *endpointControl) actuate(ctx context.Context, object events.ObjectReference, spec declaration,
	applied *EndpointApplied, reading endpoint) error {
	name := reading.facts.Name
	writes, refusals := plannedWrites(spec, reading.facts, e.recordNode(reading.facts, applied))
	if message := e.report(name, refusals); message != "" {
		e.recorder.Warning(object, reasonSpecRefused, message)
	}
	err := e.apply(ctx, reading, writes)
	record, seen := e.nodes[name]
	switch {
	case !seen:
		// The endpoint has no node.
	case writes.Level != nil && err != nil:
		// Without a record, the next pass treats the node as new and
		// writes the level again.
		delete(e.nodes, name)
	case writes.Level != nil:
		record.written = writes.Level
		if spec.Volume != nil || spec.Mute != nil {
			record.reconciled = writes.Level
		}
		e.nodes[name] = record
	case spec.Volume != nil || spec.Mute != nil:
		// The node already has the declared level, or the declaration is
		// unchanged. Later passes compare the spec against it.
		record.reconciled = &levelWrite{Volume: spec.Volume, Mute: spec.Mute}
		e.nodes[name] = record
	default:
		// The spec declares no level or mute. With reconciled cleared, a
		// declaration added later counts as a change, even when it
		// matches the level the node had before.
		record.reconciled = nil
		e.nodes[name] = record
	}
	// The level that a running node prints is newer than any earlier
	// write, so written is cleared. Otherwise status.observed would
	// report the old write after the node suspends, in place of the
	// level the node last ran at.
	if _, _, known := reading.facts.level(); known && !reading.facts.suspended() && writes.Level == nil {
		if record, seen := e.nodes[name]; seen {
			record.written = nil
			e.nodes[name] = record
		}
	}
	return err
}

// recordNode stores the endpoint's current node and returns its
// nodeHistory for plannedWrites.
//
// The records are in memory, so the first pass after an operator restart
// finds none and reads them from status.applied. A node whose cookie and
// id match status.applied kept running through the restart, and its level
// can have changed through a volume request or a button on a speaker. So
// that node is written again only when the spec changed since
// status.applied. Any other node is new, as on a machine that just
// booted, where PipeWire builds every node unmuted at its configured
// level. The operator writes the declared level, or unity, to a new
// node.
func (e *endpointControl) recordNode(facts endpointFacts, applied *EndpointApplied) nodeHistory {
	if !facts.HasNode {
		delete(e.nodes, facts.Name)
		return nodeHistory{}
	}
	last, seen := e.nodes[facts.Name]
	if seen && last.matches(facts) {
		return nodeHistory{Reconciled: last.reconciled}
	}
	if !seen && applied != nil {
		previous := nodeRecord{cookie: applied.PipeWireCookie, id: applied.Node}
		if previous.matches(facts) {
			if applied.Volume != nil || applied.Mute != nil {
				previous.reconciled = &levelWrite{Volume: applied.Volume, Mute: applied.Mute}
			}
			e.nodes[facts.Name] = previous
			return nodeHistory{Reconciled: previous.reconciled}
		}
	}
	e.nodes[facts.Name] = nodeRecord{cookie: facts.PipeWire, id: facts.Node.ID}
	return nodeHistory{New: true}
}

// applied returns the value of status.applied for the endpoint, or nil
// when the endpoint has no node. recordNode reads it back after an
// operator restart.
func (e *endpointControl) applied(facts endpointFacts) *EndpointApplied {
	record, seen := e.nodes[facts.Name]
	if !facts.HasNode || !seen {
		return nil
	}
	applied := &EndpointApplied{PipeWireCookie: record.cookie, Node: record.id}
	if record.reconciled != nil {
		applied.Volume, applied.Mute = record.reconciled.Volume, record.reconciled.Mute
	}
	return applied
}

// plannedWrites returns the writes that make the endpoint match the
// spec, and a message for each declared value that the endpoint cannot
// accept. A declared level is written when the node is new or the
// declaration changed, unless the node already has that level. A
// refused value is never written, so a mistyped control name makes no
// write to the card.
func plannedWrites(spec declaration, facts endpointFacts, node nodeHistory) (endpointWrites, []string) {
	var writes endpointWrites
	var refusals []string

	volume, mute, known := facts.level()
	switch {
	case !facts.HasNode:
		// The no-sink taint and the Ready condition report the missing
		// node. The controls below are still written, because they belong
		// to the card and not to the node.
	case spec.Volume != nil || spec.Mute != nil:
		want := levelWrite{Volume: spec.Volume, Mute: spec.Mute}
		// The declaration is compared with the last one, not with the
		// node's level, so a level that changed at the device or through
		// a volume request stays.
		changed := node.New || node.Reconciled == nil || !node.Reconciled.same(want)
		// A suspended node can print the level it last ran at, which does
		// not show that the declared level is set.
		alreadySet := known && !facts.suspended() && (want.Volume == nil || *want.Volume == volume) && (want.Mute == nil || *want.Mute == mute)
		if changed && !alreadySet {
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
			refusals = append(refusals, fmt.Sprintf("spec.controls names the control %q, "+
				"and this endpoint lists no control by that name", name))
			continue
		}
		if err := declarable(element.Capability, value); err != nil {
			refusals = append(refusals, fmt.Sprintf("spec.controls sets %q to %q: %v", name, value, err))
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

// plannedCodec returns the codec to switch the speaker to, or a refusal
// message when the speaker does not offer the declared codec. It
// returns neither while a claim has the speaker, because a switch
// replaces the speaker's node and interrupts playback, and a codec
// parameter on the claim takes precedence. The spec's codec is written
// on a pass after the claim ends.
func plannedCodec(spec declaration, facts endpointFacts) (string, string) {
	if spec.Codec == nil || facts.Speaker == nil || !facts.Speaker.HasSink {
		return "", ""
	}
	codec := *spec.Codec
	if facts.Speaker.Sink.Codec == codec || len(facts.Claims) > 0 {
		return "", ""
	}
	if _, offered := findCodec(facts.Speaker.Sink.Codecs, codec); !offered {
		return "", fmt.Sprintf("spec.codec is %q, and the speaker offers %s",
			codec, codecList(facts.Speaker.Sink.Codecs))
	}
	return codec, ""
}

func findControl(controls []control, name string) (control, bool) {
	for _, element := range controls {
		if element.Name == name {
			return element, true
		}
	}
	return control{}, false
}

// declarable uses the encoder of the real write, so the check and the
// write cannot disagree about which values a control accepts.
func declarable(capability controlCapability, value string) error {
	var scratch ctlElemValue
	return encodeControlValue(capability, value, scratch.Value[:])
}

// apply logs each write, because a write to the hardware does not show
// in any resource. A failed write does not stop the others.
func (e *endpointControl) apply(ctx context.Context, reading endpoint, writes endpointWrites) error {
	var failures []error
	facts := reading.facts
	if level := writes.Level; level != nil {
		if err := e.applyLevel(ctx, facts, *level); err != nil {
			failures = append(failures, err)
			e.readings.controlFailed(operationVolume)
		} else {
			fmt.Printf("%s: set %s\n", facts.Name, level)
		}
	}
	// A card that did not open lists no controls, so no control write is
	// planned when reading.card is nil.
	for _, write := range writes.Controls {
		if reading.card == nil {
			break
		}
		if err := reading.card.writeElement(write.Element, write.Value); err != nil {
			failures = append(failures, err)
			e.readings.controlFailed(operationControl)
			continue
		}
		fmt.Printf("%s: set the control %q to %s\n", facts.Name, write.Element.Name, write.Value)
	}
	if writes.Codec != "" {
		if _, err := e.switchCodec(ctx, facts.Speaker.Address, writes.Codec, facts.Speaker.Sink); err != nil {
			failures = append(failures, err)
			e.readings.controlFailed(operationCodec)
		} else {
			fmt.Printf("%s: switched the codec to %s\n", facts.Name, writes.Codec)
		}
	}
	return errors.Join(failures...)
}

func (e *endpointControl) applyLevel(ctx context.Context, facts endpointFacts, level levelWrite) error {
	if device, route, absolute := facts.absoluteRoute(); absolute {
		return e.setRoute(ctx, device, route, level)
	}
	return e.setLevel(ctx, facts.Node, level)
}
