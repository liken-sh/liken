package main

// Choosing the A2DP codec that a Bluetooth speaker plays.
//
// A speaker and this image's bluez5 plugin support several codecs in
// common. WirePlumber picks one when the speaker connects, but
// the right codec depends on what the listener prefers when the radio
// is busy: aptX keeps its bitrate and the sound drops out, and SBC
// lowers its bitpool and keeps playing at a lower quality. So the
// operator publishes the codecs that a speaker offers in the codecs
// attribute of its ResourceSlice device, a claim selects one in its
// opaque parameters, and a Sink's spec.codec sets one while no claim
// holds the speaker.
//
// A switch silences the speaker while the transport renegotiates.
// PipeWire destroys the sink node and creates a new one with the same
// name and a new id. WirePlumber links a playing stream to the new
// node, so a consumer that is already playing keeps playing.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// codecProperty is the bluez5 device property that lists the offered
// codecs in PropInfo and sets the codec in Props. PropInfo labels it
// "Air codec".
const codecProperty = "bluetoothAudioCodec"

const codecParameter = "codec"

// codecSwitchTimeout is how long a switch waits for the rebuilt sink
// node to report the new codec, while bluetoothd closes the transport,
// the speaker renegotiates, and PipeWire creates the node. A prepare
// that times out fails, and the kubelet retries it.
// plans/completed/05-choosing-the-codec.md has the times measured on a
// speaker.
const codecSwitchTimeout = 10 * time.Second

// bluezCodec is one codec that a speaker offers. pw-cli accepts only
// the integer ID, and the codecs attribute and a claim's codec
// parameter use the name.
type bluezCodec struct {
	ID   int
	Name string
}

// pwPropInfo is one PropInfo entry of a PipeWire object, as pw-dump
// prints it. Type and Labels stay raw JSON because their shape depends
// on the property: a choice prints an object of alternatives, and other
// properties print a bare value. codecChoices decodes them for the
// codec property.
type pwPropInfo struct {
	ID     string          `json:"id"`
	Type   json.RawMessage `json:"type"`
	Labels json.RawMessage `json:"labels"`
}

// codecOptions returns the codecs in the bluetoothAudioCodec entry of a
// device's PropInfo, or nil when the device has none.
func codecOptions(params []pwPropInfo) []bluezCodec {
	for _, info := range params {
		if info.ID != codecProperty {
			continue
		}
		return codecChoices(info)
	}
	return nil
}

// codecChoices returns the alternatives of the codec property's
// PropInfo entry, which are the codecs that both the speaker and this
// image support. The codec that plays comes first, because PropInfo
// lists the current value as both default and alt1. pw-dump prints the
// alternatives as a JSON object, whose key order has no meaning, so
// codecChoices reads alt1, alt2, and so on until a key is missing. It
// leaves out an ID with no label, which has no name to publish, and an
// ID that repeats.
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

// codecLabels maps each codec ID to its label. pw-dump prints the
// labels of a SPA choice as one flat array: ID, label, ID, label. A pair
// that does not decode is skipped, so one bad label drops one codec and
// not the whole list. When an ID repeats, the first label is kept.
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

// codecName converts a PropInfo label to the spelling of the sink
// node's api.bluez5.codec property, which the codec attribute
// publishes: aptX becomes aptx, and SBC-XQ becomes sbc_xq. So the codecs
// attribute and the codec attribute use the same names, with no table
// of names to keep in step with PipeWire.
func codecName(label string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(label)), "-", "_")
}

// codecList returns the value of the codecs attribute. A DRA device
// attribute cannot be a list, so codecList joins the names with
// spaces, as lpcmBitDepths does, and a CEL selector tests the value
// with .contains(). It leaves out the first name that would make the
// value longer than maxAttributeLength and every name after it, because
// a cut name could match a different codec in .contains().
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

// codecNames returns the names of the codecs that the speaker offers,
// in PropInfo's order, so the playing codec comes first.
func (s bluezSink) codecNames() []string {
	names := make([]string, 0, len(s.Codecs))
	for _, codec := range s.Codecs {
		names = append(names, codec.Name)
	}
	return names
}

func findCodec(codecs []bluezCodec, name string) (bluezCodec, bool) {
	for _, codec := range codecs {
		if codec.Name == name {
			return codec, true
		}
	}
	return bluezCodec{}, false
}

// codecProps returns the SPA pod that sets the codec. pw-cli parses the
// pod against the property's enum type, so the value must be the
// integer ID. A label fails that parse with an error that names no
// property.
func codecProps(id int) string {
	return fmt.Sprintf("{ %s: %d }", codecProperty, id)
}

func setDeviceCodec(ctx context.Context, device, codec int) error {
	return setParam(ctx, device, "Props", codecProps(codec))
}

// codecSwitch writes a codec on a bluez5 device and waits for the
// rebuilt sink node to report it. A prepare uses it for a claim's codec,
// and the reconciler uses it for Sink.spec.codec. Its operations are
// fields so that a test can run a switch with no PipeWire.
//
// changes returns a channel that closes when the graph feed receives
// the next graph from pw-dump -m, or when the monitor ends. await reads
// the graph again only when that channel closes, so it reads once for
// each batch of changes and never on a timer.
type codecSwitch struct {
	write   func(ctx context.Context, device, codec int) error
	read    func(context.Context) (pwGraph, error)
	changes func() <-chan struct{}
	timeout time.Duration
}

// speakerCodecSwitch returns the switch that applies Sink.spec.codec.
// The reconciler applies spec.codec only while no claim holds the
// speaker, because a switch replaces the node under the claim's stream,
// and a claim's codec parameter takes precedence while the claim exists.
func speakerCodecSwitch(feed *graphFeed) codecSwitch {
	return codecSwitch{
		write:   setDeviceCodec,
		read:    feed.read,
		changes: feed.changes,
		timeout: codecSwitchTimeout,
	}
}

// AllocatedConfig is one entry of status.allocation.devices.config, in
// which the scheduler combines the config of the claim and of its
// DeviceClass. The scheduler copies an opaque entry unchanged and does
// not validate it, so codecParameters validates it. An entry with no
// requests applies to every request of the claim. Source is
// configFromClaim or configFromClass.
type AllocatedConfig struct {
	Source   string              `json:"source"`
	Requests []string            `json:"requests"`
	Opaque   *OpaqueDeviceConfig `json:"opaque"`
}

// The values of AllocatedConfig.Source. A codec from the claim takes
// precedence over one from the DeviceClass, because the class sets the
// cluster's default and the claim is the workload's own choice. The
// precedence reads Source and not the order of the list, because the
// allocator sets that order and no API guarantees it.
const (
	configFromClaim = "FromClaim"
	configFromClass = "FromClass"
)

// OpaqueDeviceConfig holds the parameters for one driver. A claim that
// pairs a screen with its speakers has entries for each driver, and
// each driver reads only the entries with its own Driver name.
type OpaqueDeviceConfig struct {
	Driver     string          `json:"driver"`
	Parameters json.RawMessage `json:"parameters"`
}

type codecChoice struct {
	Codec     string
	FromClaim bool
}

// codecSelection holds the codecs that a claim's config entries select,
// by request name, and the codec of an entry that names no requests.
type codecSelection struct {
	requests    map[string]codecChoice
	allRequests codecChoice
}

// record stores the codec of one entry for one request, or for all
// requests when request is empty. A later entry replaces an earlier
// entry from the same source, but an entry from the DeviceClass never
// replaces one from the claim, whatever the order.
func (s *codecSelection) record(request string, choice codecChoice) {
	current := s.allRequests
	if request != "" {
		current = s.requests[request]
	}
	if current.Codec != "" && current.FromClaim && !choice.FromClaim {
		return
	}
	if request == "" {
		s.allRequests = choice
		return
	}
	if s.requests == nil {
		s.requests = map[string]codecChoice{}
	}
	s.requests[request] = choice
}

// forRequest returns the codec for the request of one allocation
// result, or "" when no entry selects one. An entry from the claim
// takes precedence over one from the DeviceClass, even when the
// DeviceClass entry names the request and the claim's entry does not.
// Between entries from the same source, an entry that names the request
// takes precedence over one for all requests.
func (s codecSelection) forRequest(request string) string {
	named := s.requests[request]
	if named.Codec == "" {
		return s.allRequests.Codec
	}
	if s.allRequests.Codec != "" && s.allRequests.FromClaim && !named.FromClaim {
		return s.allRequests.Codec
	}
	return named.Codec
}

// claimCodecs reads the codec from each of this driver's opaque config
// entries in an allocation. An entry for another driver is skipped,
// because that driver validates it. Parameters that codecParameters
// refuses fail the read, from the DeviceClass as well as from the claim.
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
			selection.record("", choice)
			continue
		}
		for _, request := range entry.Requests {
			selection.record(request, choice)
		}
	}
	return selection, nil
}

// codecParameters returns the codec from the parameters of one opaque
// entry, or "" when there are none. A key other than codec is an
// error, because a typo that the driver ignored would play the wrong
// codec with no message.
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

// selectCodec switches the speaker to the claim's codec and returns the
// rebuilt sink. The caller must use the returned sink, because the
// switch destroys the node of the sink passed in.
func (p *draPlugin) selectCodec(ctx context.Context, address, codec string, sink bluezSink) (bluezSink, error) {
	return p.codecSwitch().choose(ctx, address, codec, sink)
}

func (p *draPlugin) codecSwitch() codecSwitch {
	return codecSwitch{
		write:   p.setCodec,
		read:    p.graph,
		changes: p.changes,
		timeout: codecSwitchTimeout,
	}
}

// choose switches the speaker to codec and returns the rebuilt sink, or
// the current sink when it already plays codec. A codec that the
// speaker does not offer fails before any write, with the offered list
// in the error.
func (c codecSwitch) choose(ctx context.Context, address, codec string, sink bluezSink) (bluezSink, error) {
	chosen, offered := findCodec(sink.Codecs, codec)
	if !offered {
		return bluezSink{}, fmt.Errorf("speaker %s does not offer the codec %q, and offers %s",
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

// awaitCodec waits for the codec switch that a prepare started.
func (p *draPlugin) awaitCodec(ctx context.Context, address, codec string) (bluezSink, error) {
	return p.codecSwitch().await(ctx, address, codec)
}

// await waits until the speaker's sink reports codec, or the timeout
// passes. It finds the sink by the speaker's Bluetooth address and not
// by node id, because the switch destroys the node and its id. A
// timeout returns an error and not the sink that plays, because that
// sink plays another codec. A failed read waits for the next change.
//
// await calls changes before each read, so a graph that arrives between
// the read and the select closes a channel that await already has, and
// await reads again instead of missing the change.
func (c codecSwitch) await(ctx context.Context, address, codec string) (bluezSink, error) {
	deadline := time.NewTimer(c.timeout)
	defer deadline.Stop()
	for {
		changed := c.changes()
		graph, err := c.read(ctx)
		if err == nil {
			if sink, playing := graph.Speakers[address]; playing && sink.Codec == codec {
				return sink, nil
			}
		}
		select {
		case <-ctx.Done():
			return bluezSink{}, ctx.Err()
		case <-deadline.C:
			return bluezSink{}, fmt.Errorf("speaker %s did not report the codec %s within %s",
				speakerName(address), codec, c.timeout)
		case <-changed:
		}
	}
}
