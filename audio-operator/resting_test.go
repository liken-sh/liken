package main

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func alsaSink(volume int, mute bool) endpointFacts {
	return endpointFacts{
		Name:      testAnalogName,
		Direction: directionSink,
		Endpoint:  alsaEndpoint{Card: 0, PCM: 0, DeviceName: testAnalogName},
		Node: pwNode{
			ID:      42,
			Name:    sinkNodeName(0, 0),
			Mute:    mute,
			Volumes: []float64{float64(volume) / 100, float64(volume) / 100},
		},
		HasNode: true,
	}
}

// sinkWithNoLevel is an ALSA sink whose node prints no volumes, as a
// node can while it runs no stream, so its level is unknown.
func sinkWithNoLevel() endpointFacts {
	facts := alsaSink(0, false)
	facts.Node.Volumes = nil
	return facts
}

// staleSink is an ALSA sink whose node is suspended and prints the
// unity level it had when it last ran.
func staleSink() endpointFacts {
	facts := alsaSink(100, false)
	facts.Node.Suspended = true
	return facts
}

func withControls(facts endpointFacts, controls []control, values map[string]string) endpointFacts {
	facts.Controls, facts.Values = controls, values
	return facts
}

// speakerSink is a Bluetooth speaker that reports absolute volume, so
// its level is read from and written to the device's Route.
func speakerSink(volume int, codec string) endpointFacts {
	level := float64(volume) / 100
	facts := endpointFacts{
		Name:      testSpeakerName,
		Direction: directionSink,
		Speaker: &speakerFacts{
			Address: testSpeakerAddress,
			Paired:  speaker{Name: "Kitchen Speaker", Connected: true},
			Sink: bluezSink{
				Node: testSpeakerNode, NodeID: 63, Device: 62, Codec: codec, Codecs: twoCodecs(),
				Volumes: []float64{1, 1},
				Route:   &pwRoute{Index: 1, Volumes: []float64{level, level}, AbsoluteVolume: true},
			},
			HasSink: true,
		},
		HasNode: true,
	}
	facts.Node = facts.Speaker.Sink.sinkNode()
	return facts
}

func TestPlannedWrites(t *testing.T) {
	master := control{Name: "Master Playback Volume", Interface: ctlElemIfaceMixer,
		Capability: controlCapability{Type: capabilityInteger,
			Min: pointerTo(int64(0)), Max: pointerTo(int64(87)), Channels: 2}}
	autoMute := control{Name: "Auto-Mute Mode", Interface: ctlElemIfaceMixer,
		Capability: controlCapability{Type: capabilityEnumerated,
			Values: []string{"Disabled", "Enabled"}, Channels: 1}}
	cardControls := []control{master, autoMute}
	cardValues := map[string]string{"Master Playback Volume": "64", "Auto-Mute Mode": "Enabled"}

	cases := []struct {
		name       string
		spec       declaration
		facts      endpointFacts
		newNode    bool
		reconciled *levelWrite
		level      *levelWrite
		controls   []controlWrite
		codec      string
		refusals   int
	}{
		{
			name:  "an empty spec on an existing node writes nothing",
			facts: alsaSink(50, false),
		},
		{
			name:    "a sink whose node is new goes to unity",
			facts:   alsaSink(40, false),
			newNode: true,
			level:   &levelWrite{Volume: pointerTo(100)},
		},
		{
			name:    "a new node already at unity is left alone",
			facts:   alsaSink(100, false),
			newNode: true,
		},
		{
			// Only a sink gets the unity default.
			name: "a source whose node is new is left alone",
			facts: func() endpointFacts {
				facts := alsaSink(40, false)
				facts.Direction = directionSource
				return facts
			}(),
			newNode: true,
		},
		{
			name:  "a new declaration the endpoint does not match",
			spec:  declaration{Volume: pointerTo(50)},
			facts: alsaSink(100, false),
			level: &levelWrite{Volume: pointerTo(50)},
		},
		{
			name:       "a changed declaration the endpoint does not match",
			spec:       declaration{Volume: pointerTo(50)},
			facts:      alsaSink(100, false),
			reconciled: &levelWrite{Volume: pointerTo(70)},
			level:      &levelWrite{Volume: pointerTo(50)},
		},
		{
			// The level changed at the device or through a volume request
			// after the last comparison, so the operator leaves it.
			name:       "an endpoint that drifted from the declaration it was judged against",
			spec:       declaration{Volume: pointerTo(50)},
			facts:      alsaSink(30, false),
			reconciled: &levelWrite{Volume: pointerTo(50)},
		},
		{
			name:       "a declaration on a node that is new",
			spec:       declaration{Volume: pointerTo(50)},
			facts:      alsaSink(30, false),
			newNode:    true,
			reconciled: &levelWrite{Volume: pointerTo(50)},
			level:      &levelWrite{Volume: pointerTo(50)},
		},
		{
			name:  "a declared level the endpoint already has",
			spec:  declaration{Volume: pointerTo(50)},
			facts: alsaSink(50, false),
		},
		{
			// The write has only the mute, so the level is not written.
			name:  "a declared mute leaves the level the endpoint has",
			spec:  declaration{Mute: pointerTo(true)},
			facts: alsaSink(50, false),
			level: &levelWrite{Mute: pointerTo(true)},
		},
		{
			name:  "a declaration on an endpoint with no node writes nothing",
			spec:  declaration{Volume: pointerTo(50)},
			facts: endpointFacts{Name: testAnalogName, Direction: directionSink},
		},
		{
			name:     "a declared control the card has at another value",
			spec:     declaration{Controls: map[string]string{"Master Playback Volume": "40"}},
			facts:    withControls(alsaSink(50, false), cardControls, cardValues),
			controls: []controlWrite{{Element: master, Value: "40"}},
		},
		{
			name:  "a declared control the card already has",
			spec:  declaration{Controls: map[string]string{"Master Playback Volume": "64"}},
			facts: withControls(alsaSink(50, false), cardControls, cardValues),
		},
		{
			name:     "a control the endpoint does not list",
			spec:     declaration{Controls: map[string]string{"Headphone Playback Volume": "40"}},
			facts:    withControls(alsaSink(50, false), cardControls, cardValues),
			refusals: 1,
		},
		{
			name:     "a value outside an integer control's range",
			spec:     declaration{Controls: map[string]string{"Master Playback Volume": "900"}},
			facts:    withControls(alsaSink(50, false), cardControls, cardValues),
			refusals: 1,
		},
		{
			name:     "a value an enumerated control does not declare",
			spec:     declaration{Controls: map[string]string{"Auto-Mute Mode": "Sometimes"}},
			facts:    withControls(alsaSink(50, false), cardControls, cardValues),
			refusals: 1,
		},
		{
			name:  "a declared codec the speaker does not play",
			spec:  declaration{Codec: pointerTo("aptx")},
			facts: speakerSink(50, "sbc"),
			codec: "aptx",
		},
		{
			name:  "a declared codec the speaker already plays",
			spec:  declaration{Codec: pointerTo("sbc")},
			facts: speakerSink(50, "sbc"),
		},
		{
			// Switching codecs replaces the node and interrupts playback,
			// so the declared codec waits until the claim ends.
			name: "a declared codec while a claim holds the speaker",
			spec: declaration{Codec: pointerTo("aptx")},
			facts: func() endpointFacts {
				facts := speakerSink(50, "sbc")
				facts.Claims = []EndpointClaim{{Namespace: "media", Name: "kitchen"}}
				return facts
			}(),
		},
		{
			name:     "a codec the speaker does not offer",
			spec:     declaration{Codec: pointerTo("ldac")},
			facts:    speakerSink(50, "sbc"),
			refusals: 1,
		},
		{
			name:  "a declared level on a speaker",
			spec:  declaration{Volume: pointerTo(25)},
			facts: speakerSink(50, "sbc"),
			level: &levelWrite{Volume: pointerTo(25)},
		},
		{
			name:  "a declaration on a node with no level",
			spec:  declaration{Mute: pointerTo(true)},
			facts: sinkWithNoLevel(),
			level: &levelWrite{Mute: pointerTo(true)},
		},
		{
			name:       "the same declaration again on a node with no level",
			spec:       declaration{Mute: pointerTo(true)},
			facts:      sinkWithNoLevel(),
			reconciled: &levelWrite{Mute: pointerTo(true)},
		},
		{
			name:       "a changed declaration on a node with no level",
			spec:       declaration{Volume: pointerTo(40), Mute: pointerTo(true)},
			facts:      sinkWithNoLevel(),
			reconciled: &levelWrite{Mute: pointerTo(true)},
			level:      &levelWrite{Volume: pointerTo(40), Mute: pointerTo(true)},
		},
		{
			// The node prints no level, so after an operator restart the
			// spec is compared with the declaration in status.applied.
			name:       "an unchanged declaration on a node with no level after an operator restart",
			spec:       declaration{Volume: pointerTo(40)},
			facts:      sinkWithNoLevel(),
			reconciled: &levelWrite{Volume: pointerTo(40)},
		},
		{
			// The level can have come from a volume request or a button
			// on the device, so the operator leaves it.
			name:       "an unchanged declaration on a running node that moved away from it",
			spec:       declaration{Volume: pointerTo(40)},
			facts:      alsaSink(50, false),
			reconciled: &levelWrite{Volume: pointerTo(40)},
		},
		{
			// A suspended node prints the volume it had when it last ran,
			// so a printed 100% does not show that the declared 100% is
			// set.
			name:       "a changed declaration on a suspended node that prints it",
			spec:       declaration{Volume: pointerTo(100)},
			facts:      staleSink(),
			reconciled: &levelWrite{Volume: pointerTo(70)},
			level:      &levelWrite{Volume: pointerTo(100)},
		},
		{
			name:  "no declaration on a node with no level",
			facts: sinkWithNoLevel(),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			writes, refusals := plannedWrites(c.spec, c.facts, nodeHistory{New: c.newNode, Reconciled: c.reconciled})
			if !sameLevel(writes.Level, c.level) {
				t.Errorf("level = %+v, want %+v", writes.Level, c.level)
			}
			if !slices.EqualFunc(writes.Controls, c.controls, sameControlWrite) {
				t.Errorf("controls = %+v, want %+v", writes.Controls, c.controls)
			}
			if writes.Codec != c.codec {
				t.Errorf("codec = %q, want %q", writes.Codec, c.codec)
			}
			if len(refusals) != c.refusals {
				t.Errorf("refusals = %v, want %d", refusals, c.refusals)
			}
		})
	}
}

func sameLevel(got, want *levelWrite) bool {
	if got == nil || want == nil {
		return got == want
	}
	return got.same(*want)
}

func sameControlWrite(got, want controlWrite) bool {
	return got.Element.NumID == want.Element.NumID &&
		got.Element.Name == want.Element.Name &&
		got.Element.Index == want.Element.Index &&
		got.Value == want.Value
}

// writeRecord records the calls to the fake setLevel, setRoute, and
// switchCodec of recordingControl.
type writeRecord struct {
	node   *pwNode
	route  *pwRoute
	device int
	level  levelWrite
	codec  string
	levels []levelWrite
}

func recordingControl(record *writeRecord) *endpointControl {
	return &endpointControl{
		nodes:          map[string]nodeRecord{},
		loggedFailures: map[string]string{},
		asks:           map[string]string{},
		latest:         map[string]endpointFacts{},
		setLevel: func(_ context.Context, node pwNode, level levelWrite) error {
			record.node, record.level = &node, level
			record.levels = append(record.levels, level)
			return nil
		},
		setRoute: func(_ context.Context, device int, route pwRoute, level levelWrite) error {
			record.route, record.device, record.level = &route, device, level
			record.levels = append(record.levels, level)
			return nil
		},
		switchCodec: func(_ context.Context, _, codec string, sink bluezSink) (bluezSink, error) {
			record.codec = codec
			return sink, nil
		},
	}
}

// A level for a speaker that reports absolute volume is written to the
// device's Route, which reaches the speaker as AVRCP absolute volume.
// Every other endpoint gets the level on its node.
func TestALevelWriteGoesToTheRouteOnlyForASpeakerWithAbsoluteVolume(t *testing.T) {
	cases := []struct {
		name  string
		facts endpointFacts
		route bool
	}{
		{name: "an endpoint of the card", facts: alsaSink(100, false)},
		{name: "a speaker that reports its own volume", facts: speakerSink(50, "sbc"), route: true},
		{
			name: "a speaker that reports none",
			facts: func() endpointFacts {
				facts := speakerSink(50, "sbc")
				facts.Speaker.Sink.Route.AbsoluteVolume = false
				return facts
			}(),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			record := &writeRecord{}
			control := recordingControl(record)
			err := control.apply(context.Background(), endpoint{facts: c.facts},
				endpointWrites{Level: &levelWrite{Volume: pointerTo(25)}})
			if err != nil {
				t.Fatalf("applying the level write: %v", err)
			}
			if (record.route != nil) != c.route {
				t.Errorf("route write = %+v, node write = %+v, want a route write: %t", record.route, record.node, c.route)
			}
			if record.level.Volume == nil || *record.level.Volume != 25 {
				t.Errorf("level = %s, want volume 25", record.level)
			}
		})
	}
}

func TestAnEmptySpecWritesNothing(t *testing.T) {
	record := &writeRecord{}
	control := recordingControl(record)
	writes, refusals := plannedWrites(declaration{}, alsaSink(50, false), nodeHistory{})
	if err := control.apply(context.Background(), endpoint{facts: alsaSink(50, false)}, writes); err != nil {
		t.Fatalf("applying the planned writes: %v", err)
	}
	if record.node != nil || record.route != nil || record.codec != "" {
		t.Errorf("an empty spec wrote %+v, want no write", record)
	}
	if len(refusals) != 0 {
		t.Errorf("refusals = %v, want none", refusals)
	}
}

// apply returns the error of a failed level write. actuate then deletes
// the node's record, so the next pass writes the level again.
func TestAFailedLevelWriteIsReported(t *testing.T) {
	control := recordingControl(&writeRecord{})
	control.setLevel = func(context.Context, pwNode, levelWrite) error {
		return errors.New("pw-cli set-param: no such object")
	}

	err := control.apply(context.Background(), endpoint{facts: alsaSink(100, false)},
		endpointWrites{Level: &levelWrite{Volume: pointerTo(25)}})
	if err == nil {
		t.Fatal("apply with a failed level write returned nil, want the error")
	}
}
