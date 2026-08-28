package main

// The resting layer: the settings a spec declares, and the write each
// one lands in.
//
// One rule governs every field. The operator writes a declared field
// where the endpoint diverges from it, and it writes nothing at all
// for a field the spec leaves out. So an empty spec costs the
// hardware nothing, a declaration stands through a restart of the
// operator or a reconnect of the speaker, and a value a person set by
// hand on an undeclared field stays where they put it. The one
// exception is the unity default: a sink node PipeWire has just built
// is set to unity when its spec declares no level, because this pod
// stores no volumes and unity is the one level the operator can
// defend with no declaration to read.

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
// node: whether PipeWire built it since the last pass, and the level
// this operator last wrote to it, if any. A suspended node reports no
// level, so the last write is the only thing a declaration on it can
// be judged against.
type nodeMemory struct {
	New     bool
	Written *levelWrite
}

// plannedWrites is the resting layer's whole decision: what the
// declaration and the endpoint disagree on, and what the declaration
// states that the endpoint cannot take.
//
// Three rules. A declared field is written where the endpoint
// diverges from it. A field the spec leaves out is written nowhere,
// apart from the unity default a new sink node takes. A value the
// hardware refuses is reported and never written, so a typo in a
// control name costs one log line and no register.
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
		// A suspended node reports no levels at all, so a declared
		// level on one is judged against the level this operator last
		// wrote to it: it is written when the node is new, when nothing
		// was written yet, and when the declaration changed, and not
		// on every pass, because a write that repeated would raise its
		// own event and answer it forever.
		switch {
		case known:
			if (want.Volume != nil && *want.Volume != volume) || (want.Mute != nil && *want.Mute != mute) {
				writes.Level = &want
			}
		case node.New || node.Written == nil || !node.Written.same(want):
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

// plannedCodec answers which codec the speaker must switch to, and
// nothing at all while a claim holds it.
//
// The claim holds the switch back because a codec switch destroys the
// speaker's node and builds another, so it always interrupts what
// plays. A claim's own codec parameter is what wins while the claim
// lasts, and the resting codec is applied when the claim ends.
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
			continue
		}
		fmt.Printf("%s: %s is %s\n", facts.Name, write.Element.Name, write.Value)
	}
	if writes.Codec != "" {
		if _, err := e.switchCodec(ctx, facts.Speaker.Address, writes.Codec, facts.Speaker.Sink); err != nil {
			failures = append(failures, err)
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
