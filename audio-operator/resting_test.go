package main

// These tests cover the resting layer's whole decision: for each
// declared field and each value the endpoint reads now, which write
// the pass makes, or none at all.

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// alsaSink is an endpoint of the card with one node in the graph, at
// the level and the mute the caller states.
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

// withControls gives an endpoint the card's controls and the value
// each one reads now.
func withControls(facts endpointFacts, controls []control, values map[string]string) endpointFacts {
	facts.Controls, facts.Values = controls, values
	return facts
}

// speakerSink is a speaker whose transport reports a volume of its
// own, so its level lives on the device's Route.
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
	// The range is the card's own, and it is what a declared value is
	// judged against.
	master := control{Name: "Master Playback Volume", Interface: ctlElemIfaceMixer,
		Capability: controlCapability{Type: capabilityInteger,
			Min: pointerTo(int64(0)), Max: pointerTo(int64(87)), Channels: 2}}
	autoMute := control{Name: "Auto-Mute Mode", Interface: ctlElemIfaceMixer,
		Capability: controlCapability{Type: capabilityEnumerated,
			Values: []string{"Disabled", "Enabled"}, Channels: 1}}
	cardControls := []control{master, autoMute}
	cardValues := map[string]string{"Master Playback Volume": "64", "Auto-Mute Mode": "Enabled"}

	cases := []struct {
		name     string
		spec     declaration
		facts    endpointFacts
		newNode  bool
		level    *levelWrite
		controls []controlWrite
		codec    string
		refusals int
	}{
		{
			name:  "an empty spec on a node that stands writes nothing",
			facts: alsaSink(50, false),
		},
		{
			name:    "a sink whose node is new goes to unity",
			facts:   alsaSink(40, false),
			newNode: true,
			level:   &levelWrite{Volume: 100},
		},
		{
			name:    "a new node already at unity is left alone",
			facts:   alsaSink(100, false),
			newNode: true,
		},
		{
			// The unity default is the sink's. A microphone that no
			// declaration names is left where the card left it.
			name: "a source whose node is new is left alone",
			facts: func() endpointFacts {
				facts := alsaSink(40, false)
				facts.Direction = directionSource
				return facts
			}(),
			newNode: true,
		},
		{
			name:  "a declared level the endpoint does not hold",
			spec:  declaration{Volume: pointerTo(50)},
			facts: alsaSink(100, false),
			level: &levelWrite{Volume: 50},
		},
		{
			name:  "a declared level the endpoint already holds",
			spec:  declaration{Volume: pointerTo(50)},
			facts: alsaSink(50, false),
		},
		{
			// The two go in one write, so a reader never sees the level
			// move without the mute.
			name:  "a declared mute keeps the level the endpoint holds",
			spec:  declaration{Mute: pointerTo(true)},
			facts: alsaSink(50, false),
			level: &levelWrite{Volume: 50, Mute: true},
		},
		{
			name:  "a declaration on an endpoint with no node writes nothing",
			spec:  declaration{Volume: pointerTo(50)},
			facts: endpointFacts{Name: testAnalogName, Direction: directionSink},
		},
		{
			name:     "a declared control the card does not hold at that value",
			spec:     declaration{Controls: map[string]string{"Master Playback Volume": "40"}},
			facts:    withControls(alsaSink(50, false), cardControls, cardValues),
			controls: []controlWrite{{Element: master, Value: "40"}},
		},
		{
			name:  "a declared control the card already holds",
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
			// A switch destroys the node and interrupts what plays, so
			// the declaration waits for the claim to end.
			name: "a declared codec while a claim holds the speaker",
			spec: declaration{Codec: pointerTo("aptx")},
			facts: func() endpointFacts {
				facts := speakerSink(50, "sbc")
				facts.Claim = &EndpointClaim{Namespace: "media", Name: "kitchen"}
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
			level: &levelWrite{Volume: 25},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			writes, refusals := plannedWrites(c.spec, c.facts, c.newNode)
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
	return *got == *want
}

func sameControlWrite(got, want controlWrite) bool {
	return got.Element.NumID == want.Element.NumID &&
		got.Element.Name == want.Element.Name &&
		got.Element.Index == want.Element.Index &&
		got.Value == want.Value
}

// writeRecord holds what one pass wrote, so a test reads which of the
// two level writes the endpoint took.
type writeRecord struct {
	node   *pwNode
	route  *pwRoute
	device int
	volume int
	mute   bool
	codec  string
}

func recordingControl(record *writeRecord) *endpointControl {
	return &endpointControl{
		nodes:    map[string]int{},
		refusals: map[string]string{},
		setLevel: func(_ context.Context, node pwNode, volume int, mute bool) error {
			record.node, record.volume, record.mute = &node, volume, mute
			return nil
		},
		setRoute: func(_ context.Context, device int, route pwRoute, volume int, mute bool) error {
			record.route, record.device, record.volume, record.mute = &route, device, volume, mute
			return nil
		},
		switchCodec: func(_ context.Context, _, codec string, sink bluezSink) (bluezSink, error) {
			record.codec = codec
			return sink, nil
		},
	}
}

// A speaker whose transport reports a volume of its own takes the
// write on the device's Route, which is what reaches the speaker as
// AVRCP absolute volume. Every other endpoint takes the node's gain.
func TestLevelWritesLandWhereTheLevelLives(t *testing.T) {
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
				endpointWrites{Level: &levelWrite{Volume: 25}})
			if err != nil {
				t.Fatal(err)
			}
			if (record.route != nil) != c.route {
				t.Errorf("route write = %+v, node write = %+v", record.route, record.node)
			}
			if record.volume != 25 {
				t.Errorf("volume = %d, want 25", record.volume)
			}
		})
	}
}

// A pass that plans no write reaches no hardware at all.
func TestAnEmptySpecWritesNothing(t *testing.T) {
	record := &writeRecord{}
	control := recordingControl(record)
	writes, refusals := plannedWrites(declaration{}, alsaSink(50, false), false)
	if err := control.apply(context.Background(), endpoint{facts: alsaSink(50, false)}, writes); err != nil {
		t.Fatal(err)
	}
	if record.node != nil || record.route != nil || record.codec != "" {
		t.Errorf("an empty spec wrote %+v", record)
	}
	if len(refusals) != 0 {
		t.Errorf("refusals = %v", refusals)
	}
}

// A failed write fails the endpoint's own reconcile and nothing else.
// The next event or the backstop tick tries again.
func TestAFailedLevelWriteIsReported(t *testing.T) {
	control := recordingControl(&writeRecord{})
	control.setLevel = func(context.Context, pwNode, int, bool) error {
		return errors.New("pw-cli set-param: no such object")
	}

	err := control.apply(context.Background(), endpoint{facts: alsaSink(100, false)},
		endpointWrites{Level: &levelWrite{Volume: 25}})
	if err == nil {
		t.Fatal("a failed write reported nothing")
	}
}
