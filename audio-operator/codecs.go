package main

// Choosing the A2DP codec a speaker plays.
//
// A speaker and this image agree on a set of codecs, and the
// transport plays exactly one. WirePlumber picks at connect time,
// but which loss a person prefers when the air gets busy is a
// preference, not a graph fact: aptX holds its bitrate and gaps, SBC
// shrinks its bitpool and keeps the stream whole at a lower
// quality. So the offered set publishes as an attribute, and a
// claim states a choice. This file holds both halves.
//
// A switch costs about a second of silence while the transport
// renegotiates. The sink node is destroyed and built again under
// the same name with a new id, and WirePlumber re-links a playing
// stream to the new node on its own, so a consumer already playing
// keeps playing.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// codecProperty is the property the bluez5 device answers with the
// codec choice, in PropInfo and in Props.
//
// The name is the bluez5 plugin's own; its PropInfo entry labels
// the property "Air codec".
const codecProperty = "bluetoothAudioCodec"

// codecParameter is the one key this driver reads out of a claim's
// opaque parameters.
const codecParameter = "codec"

// codecSwitchTimeout bounds the wait for the transport to come back
// with the requested codec.
//
// In that window bluetoothd tears the transport down, the
// endpoint renegotiates, and the new node appears in the graph.
// The lab speaker takes one to four seconds. A timeout fails the
// prepare, and the kubelet's retry starts a fresh wait.
const codecSwitchTimeout = 10 * time.Second

// codecSwitchInterval is how often that wait reads the graph again.
//
// This wait polls where the rest of the operator is event-driven,
// because the graph arrives by a pw-dump exec and an exec offers
// nothing to subscribe to. A quarter second adds little to the
// second the renegotiation already takes.
const codecSwitchInterval = 250 * time.Millisecond

// bluezCodec is one codec the device offers: the integer id a write
// names it by, and the name this operator publishes it as.
//
// The id and the name are both kept because they speak to
// different parties: a write names the codec by integer id, and
// pw-cli refuses the string label, while the attribute and the
// claim speak the published name.
type bluezCodec struct {
	ID   int
	Name string
}

// pwPropInfo describes one property an object accepts: its name, the
// values it takes, and a display label for each one.
//
// type and labels stay raw because their shape depends on the
// property: a choice prints an object of alternatives, a plain
// property prints a bare value, and only the codec reader knows
// which one it is looking at.
type pwPropInfo struct {
	ID     string          `json:"id"`
	Type   json.RawMessage `json:"type"`
	Labels json.RawMessage `json:"labels"`
}

// codecOptions reads the codecs a bluez5 device offers out of its
// PropInfo list, in the order the device enumerated them.
//
// The alternatives are the codecs the speaker and this image both
// support, and the first is the one playing now, because PropInfo
// names the current value both as the default and as alt1. An
// unlabeled id has no name to publish and a repeated id adds
// nothing, so both are dropped.
func codecOptions(params []pwPropInfo) []bluezCodec {
	for _, info := range params {
		if info.ID != codecProperty {
			continue
		}
		return codecChoices(info)
	}
	return nil
}

// codecChoices reads one PropInfo entry.
//
// The alternatives are read by name, alt1 upward, because pw-dump
// prints them as one JSON object, and an object's printed order
// proves nothing.
func codecChoices(info pwPropInfo) []bluezCodec {
	var alternatives map[string]json.RawMessage
	if err := json.Unmarshal(info.Type, &alternatives); err != nil {
		return nil
	}
	labels := codecLabels(info.Labels)
	var codecs []bluezCodec
	named := map[int]bool{}
	for alternative := 1; ; alternative++ {
		raw, offered := alternatives[fmt.Sprintf("alt%d", alternative)]
		if !offered {
			return codecs
		}
		var id int
		if err := json.Unmarshal(raw, &id); err != nil {
			continue
		}
		label, hasLabel := labels[id]
		if !hasLabel || named[id] {
			continue
		}
		named[id] = true
		codecs = append(codecs, bluezCodec{ID: id, Name: codecName(label)})
	}
}

// codecLabels reads the display label of each codec id.
//
// The labels array alternates id, label, the JSON print of a SPA
// choice's label table. A malformed pair is skipped rather than
// fatal, because one bad label should cost one codec, not the
// list.
func codecLabels(raw json.RawMessage) map[int]string {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	labels := map[int]string{}
	for i := 0; i+1 < len(items); i += 2 {
		var id int
		var label string
		if json.Unmarshal(items[i], &id) != nil {
			continue
		}
		if json.Unmarshal(items[i+1], &label) != nil {
			continue
		}
		if _, taken := labels[id]; taken {
			continue
		}
		labels[id] = label
	}
	return labels
}

// codecName turns a PropInfo label into the name this operator
// publishes.
//
// The transform is a lowercase and a dash-to-underscore, not a
// table, because that lands on the spelling api.bluez5.codec
// prints: aptX becomes aptx, SBC-XQ becomes sbc_xq. The list and
// the negotiated codec speak one language, and no table here can
// drift from PipeWire's.
func codecName(label string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(label)), "-", "_")
}

// codecList is the value of the codecs attribute: the names, space
// separated, in the device's own order.
//
// A list is one string because a device attribute holds no array,
// the convention lpcmBitDepths set, and a selector asks with
// .contains(). A name that would pass the attribute limit is
// dropped whole, because half a name would match the wrong
// codec.
func codecList(codecs []bluezCodec) string {
	list := ""
	for _, codec := range codecs {
		next := codec.Name
		if list != "" {
			next = list + " " + codec.Name
		}
		if len(next) > maxAttributeLength {
			return list
		}
		list = next
	}
	return list
}

// codecNames lists the codecs the speaker offers by name, in the
// device's own order with the one playing first. It is the list
// status.bluetooth.codecs carries, read from the graph value alone,
// so the reconciler answers it with no prepare in flight.
func (s bluezSink) codecNames() []string {
	names := make([]string, 0, len(s.Codecs))
	for _, codec := range s.Codecs {
		names = append(names, codec.Name)
	}
	return names
}

// findCodec resolves a published name back to the id a write names.
func findCodec(codecs []bluezCodec, name string) (bluezCodec, bool) {
	for _, codec := range codecs {
		if codec.Name == name {
			return codec, true
		}
	}
	return bluezCodec{}, false
}

// codecProps is the pod that sets the codec.
//
// The value is the integer id. pw-cli parses the pod against the
// property's enum type, and a string label fails that parse with
// an error that names no property.
func codecProps(id int) string {
	return fmt.Sprintf("{ %s: %d }", codecProperty, id)
}

// setDeviceCodec writes the codec on one bluez5 device.
func setDeviceCodec(ctx context.Context, device, codec int) error {
	return setParam(ctx, device, "Props", codecProps(codec))
}

// codecSwitch is one codec change: a write on the device, and a wait
// for the rebuilt node to report the codec. The prepare path and the
// resting declaration both switch a codec, and they share this one
// implementation. The write and the read are fields so that a test
// drives them with no PipeWire behind it.
type codecSwitch struct {
	write    func(ctx context.Context, device, codec int) error
	read     func(context.Context) (pwGraph, error)
	timeout  time.Duration
	interval time.Duration
}

// speakerCodecSwitch is the switch the reconciler makes for
// Sink.spec.codec. It makes it only while no claim holds the
// speaker, because a switch replaces the node and interrupts
// whatever plays, and a claim's own codec parameter wins while the
// claim lasts.
func speakerCodecSwitch(read func(context.Context) (pwGraph, error)) codecSwitch {
	return codecSwitch{
		write:    setDeviceCodec,
		read:     read,
		timeout:  codecSwitchTimeout,
		interval: codecSwitchInterval,
	}
}

// AllocatedConfig is one entry of the configuration the scheduler
// resolved for an allocation.
//
// An opaque block is DRA's channel for a driver's own parameters:
// the scheduler validates none of it and copies it into the
// allocation unread. An entry with no requests applies to every
// request in the claim, and the source says whether the claim's
// author wrote the entry or the DeviceClass carried it in.
type AllocatedConfig struct {
	Source   string              `json:"source"`
	Requests []string            `json:"requests"`
	Opaque   *OpaqueDeviceConfig `json:"opaque"`
}

// The two places a resolved config entry comes from: the claim its
// author wrote, and the DeviceClass the claim allocates through.
//
// The claim's own choice wins over the class's, because a class
// is the cluster's default and a claim is the workload's say. The
// precedence reads the source field rather than the list's order,
// because the order is the allocator's habit and no API promises
// it.
const (
	configFromClaim = "FromClaim"
	configFromClass = "FromClass"
)

// OpaqueDeviceConfig is one driver's own parameters inside a claim.
//
// The driver name decides whose parameters these are. A claim
// that pairs two drivers, a screen with its speakers, holds one
// block per driver, and each driver reads only its own.
type OpaqueDeviceConfig struct {
	Driver     string          `json:"driver"`
	Parameters json.RawMessage `json:"parameters"`
}

// codecChoice is one codec a config entry stated, and whether the
// claim stated it or the class did.
type codecChoice struct {
	Codec     string
	FromClaim bool
}

// codecSelection is the codec each request asks for. The requests map
// holds what a block that names its requests stated, and every holds
// what a block with no requests stated, which applies to every
// request in the claim.
type codecSelection struct {
	requests map[string]codecChoice
	every    codecChoice
}

// state records one entry's codec, and refuses to let a class's
// entry overwrite the claim's own.
//
// A later entry of the same source overwrites an earlier one, the
// plain reading of a list. An entry from the class never
// overwrites one from the claim, whatever the order.
func (s *codecSelection) state(request string, choice codecChoice) {
	current := s.every
	if request != "" {
		current = s.requests[request]
	}
	if current.Codec != "" && current.FromClaim && !choice.FromClaim {
		return
	}
	if request == "" {
		s.every = choice
		return
	}
	if s.requests == nil {
		s.requests = map[string]codecChoice{}
	}
	s.requests[request] = choice
}

// forRequest answers what one allocation result must play.
//
// Two rules, applied in this order: the claim's own choice beats
// the class's, and within one source a block that names the
// request beats a block that names none. So a claim's
// every-request block still beats a class block that names this
// request.
func (s codecSelection) forRequest(request string) string {
	named := s.requests[request]
	if named.Codec == "" {
		return s.every.Codec
	}
	if s.every.Codec != "" && s.every.FromClaim && !named.FromClaim {
		return s.every.Codec
	}
	return named.Codec
}

// claimCodecs reads this driver's own config blocks out of the
// configuration the scheduler resolved for an allocation.
//
// A block of another driver is not this driver's to judge, so it
// is skipped. A parameter this driver does not read fails
// whichever source wrote it: a typo in cluster policy plays the
// wrong codec as surely as a typo in a claim.
func claimCodecs(config []AllocatedConfig) (codecSelection, error) {
	selection := codecSelection{}
	for _, entry := range config {
		if entry.Opaque == nil || entry.Opaque.Driver != DriverName {
			continue
		}
		codec, err := codecParameters(entry.Opaque.Parameters)
		if err != nil {
			return codecSelection{}, err
		}
		if codec == "" {
			continue
		}
		choice := codecChoice{Codec: codec, FromClaim: entry.Source == configFromClaim}
		if len(entry.Requests) == 0 {
			selection.state("", choice)
			continue
		}
		for _, request := range entry.Requests {
			selection.state(request, choice)
		}
	}
	return selection, nil
}

// codecParameters reads one opaque block's parameters.
//
// An unknown key fails instead of being ignored, because a
// silently dropped typo would play the wrong codec with nothing
// said anywhere.
func codecParameters(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var parameters map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parameters); err != nil {
		return "", fmt.Errorf("the claim's %s parameters are not an object: %w", DriverName, err)
	}
	codec := ""
	for key, value := range parameters {
		if key != codecParameter {
			return "", fmt.Errorf("the claim's %s parameters name %q, and this driver reads only %q",
				DriverName, key, codecParameter)
		}
		if err := json.Unmarshal(value, &codec); err != nil {
			return "", fmt.Errorf("the claim's %s parameter is not a string: %s", codecParameter, value)
		}
	}
	return codec, nil
}

// selectCodec makes one speaker play the codec the claim states, and
// answers with the sink the consumer must be given.
//
// Write the codec, then wait for the node that reports it: the
// write is what destroys the old node, so nothing read after it
// means anything until the new node stands. The delivery's unity
// write follows in prepareClaim, on whichever node this returns.
func (p *draPlugin) selectCodec(ctx context.Context, address, codec string, sink bluezSink) (bluezSink, error) {
	return p.codecSwitch().choose(ctx, address, codec, sink)
}

// codecSwitch is the switch a prepare makes, built from the plugin's
// own seams, which are the production values outside a test.
func (p *draPlugin) codecSwitch() codecSwitch {
	return codecSwitch{
		write:    p.setCodec,
		read:     p.graph,
		timeout:  p.codecTimeout,
		interval: p.codecInterval,
	}
}

// choose makes the speaker play one codec and answers with the node
// that came back. A codec the speaker does not offer fails before any
// write, and the message names the offered list.
func (c codecSwitch) choose(ctx context.Context, address, codec string, sink bluezSink) (bluezSink, error) {
	chosen, offered := findCodec(sink.Codecs, codec)
	if !offered {
		return bluezSink{}, fmt.Errorf("speaker %s does not offer the codec %q; it offers %s",
			speakerName(address), codec, codecList(sink.Codecs))
	}
	if sink.Codec == codec {
		return sink, nil
	}

	if err := c.write(ctx, sink.Device, chosen.ID); err != nil {
		return bluezSink{}, fmt.Errorf("switching speaker %s to %s: %w", speakerName(address), codec, err)
	}
	return c.await(ctx, address, codec)
}

// awaitCodec reads the graph until the speaker's node reports the
// requested codec.
//
// The wait keys on the speaker's address and never an object id,
// because the switch destroys the node that held the id, and the
// address is the one name that survives the rebuild. A timeout
// fails the prepare rather than falling back to what plays,
// because what plays is the wrong codec by definition.
func (p *draPlugin) awaitCodec(ctx context.Context, address, codec string) (bluezSink, error) {
	return p.codecSwitch().await(ctx, address, codec)
}

// await reads the graph until the speaker's node reports the codec,
// or until the timeout passes.
func (c codecSwitch) await(ctx context.Context, address, codec string) (bluezSink, error) {
	deadline := time.Now().Add(c.timeout)
	for {
		graph, err := c.read(ctx)
		if err == nil {
			if sink, playing := graph.Speakers[address]; playing && sink.Codec == codec {
				return sink, nil
			}
		}
		if !time.Now().Before(deadline) {
			return bluezSink{}, fmt.Errorf("speaker %s did not report the codec %s within %s",
				speakerName(address), codec, c.timeout)
		}
		select {
		case <-ctx.Done():
			return bluezSink{}, ctx.Err()
		case <-time.After(c.interval):
		}
	}
}
