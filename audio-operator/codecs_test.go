package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
)

// The entry the lab's speaker answers, captured verbatim from
// pw-dump on liken-1. aptX is the current codec, and the
// alternatives enumerate the set the speaker and this image share.
const labCodecEntry = `{
  "id": "bluetoothAudioCodec",
  "description": "Air codec",
  "type": { "default": 6, "alt1": 6, "alt2": 1, "alt3": 2, "alt4": 9 },
  "labels": [ 6, "aptX", 1, "SBC", 2, "SBC-XQ", 9, "aptX-LL" ]
}`

// propInfo reads one PropInfo entry the way parseGraph reads it.
func propInfo(t *testing.T, entry string) pwPropInfo {
	t.Helper()
	var info pwPropInfo
	if err := json.Unmarshal([]byte(entry), &info); err != nil {
		t.Fatal(err)
	}
	return info
}

func TestCodecOptionsReadsThePropInfoEntry(t *testing.T) {
	cases := []struct {
		name  string
		entry string
		want  []bluezCodec
	}{
		{
			name:  "the entry the lab speaker answers",
			entry: labCodecEntry,
			want: []bluezCodec{
				{ID: 6, Name: "aptx"},
				{ID: 1, Name: "sbc"},
				{ID: 2, Name: "sbc_xq"},
				{ID: 9, Name: "aptx_ll"},
			},
		},
		{
			name: "an id with no label",
			entry: `{"id": "bluetoothAudioCodec",
			         "type": {"default": 1, "alt1": 1, "alt2": 6},
			         "labels": [1, "SBC"]}`,
			want: []bluezCodec{{ID: 1, Name: "sbc"}},
		},
		{
			name: "an id the alternatives repeat",
			entry: `{"id": "bluetoothAudioCodec",
			         "type": {"default": 1, "alt1": 1, "alt2": 1, "alt3": 6},
			         "labels": [1, "SBC", 6, "aptX"]}`,
			want: []bluezCodec{{ID: 1, Name: "sbc"}, {ID: 6, Name: "aptx"}},
		},
		{
			name: "one alternative, which is the current codec",
			entry: `{"id": "bluetoothAudioCodec",
			         "type": {"default": 1, "alt1": 1},
			         "labels": [1, "SBC"]}`,
			want: []bluezCodec{{ID: 1, Name: "sbc"}},
		},
		{
			name:  "a property whose type is not a choice",
			entry: `{"id": "bluetoothAudioCodec", "type": false}`,
		},
		{
			name: "a choice with no labels",
			entry: `{"id": "bluetoothAudioCodec",
			         "type": {"default": 1, "alt1": 1}}`,
		},
		{
			name: "another property of the same device",
			entry: `{"id": "volume", "description": "Volume",
			         "type": {"default": 1.0, "min": 0.0, "max": 10.0}}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := codecOptions([]pwPropInfo{propInfo(t, c.entry)})
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("codecs = %+v, want %+v", got, c.want)
			}
		})
	}
}

// The published vocabulary is the one api.bluez5.codec prints, so a
// reader finds the negotiated codec in the list.
func TestCodecNameFollowsTheNodesSpelling(t *testing.T) {
	cases := []struct {
		label string
		want  string
	}{
		{label: "SBC", want: "sbc"},
		{label: "aptX", want: "aptx"},
		{label: "SBC-XQ", want: "sbc_xq"},
		{label: "aptX-LL", want: "aptx_ll"},
		{label: "aptX HD", want: "aptx hd"},
		{label: " LDAC ", want: "ldac"},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			if got := codecName(c.label); got != c.want {
				t.Errorf("codecName(%q) = %q, want %q", c.label, got, c.want)
			}
		})
	}
}

// twoCodecs is the set the fixtures publish: the negotiated codec
// first, in the order PropInfo enumerated them.
func twoCodecs() []bluezCodec {
	return []bluezCodec{{ID: 1, Name: "sbc"}, {ID: 6, Name: "aptx"}}
}

// manyCodecs is a set whose joined names pass the API's limit on the
// length of a string attribute.
func manyCodecs() []bluezCodec {
	codecs := make([]bluezCodec, 0, 8)
	for i := range 8 {
		codecs = append(codecs, bluezCodec{ID: i, Name: fmt.Sprintf("codec_number_%d", i)})
	}
	return codecs
}

func TestCodecListJoinsWholeNames(t *testing.T) {
	cases := []struct {
		name   string
		codecs []bluezCodec
		want   string
	}{
		{name: "no codec at all"},
		{name: "one codec", codecs: []bluezCodec{{ID: 1, Name: "sbc"}}, want: "sbc"},
		{name: "the set the lab answers", codecs: twoCodecs(), want: "sbc aptx"},
		{
			// A truncated list would name a codec that does not exist,
			// and a selector that reads it with .contains() would match
			// it.
			name:   "more names than the attribute holds",
			codecs: manyCodecs(),
			want:   "codec_number_0 codec_number_1 codec_number_2 codec_number_3",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := codecList(c.codecs)
			if got != c.want {
				t.Errorf("codecs = %q, want %q", got, c.want)
			}
			if len(got) > maxAttributeLength {
				t.Errorf("the value is %d characters, past the %d limit", len(got), maxAttributeLength)
			}
		})
	}
}

// The pod is the SPA object literal pw-cli parses. The id is the
// integer PropInfo gave: a string label fails the parse.
func TestCodecPropsWritesTheIntegerID(t *testing.T) {
	if got := codecProps(6); got != "{ bluetoothAudioCodec: 6 }" {
		t.Errorf("props = %q", got)
	}
}

// claimWith builds what the API server holds for a claim on one
// device, with the given entries in the configuration the scheduler
// resolved for the allocation. A claim's own spec.devices.config is
// not what the driver reads: the scheduler copies each block here,
// beside the DeviceClass's own, and marks where it came from.
func claimWith(device, config string) string {
	return fmt.Sprintf(`{
	  "metadata": {"name": "kitchen", "namespace": "media", "uid": "claim-1"},
	  "status": {"allocation": {"devices": {
	    "results": [
	      {"request": "speaker", "driver": "audio.liken.sh", "pool": "liken-1", "device": %q}
	    ],
	    "config": [%s]
	  }}}
	}`, device, config)
}

// configEntry is one resolved config block of this driver's own,
// from the given source, for the given requests.
func configEntry(source, requests, parameters string) string {
	return fmt.Sprintf(`{"source": %q, "requests": [%s], "opaque": {"driver": %q, "parameters": %s}}`,
		source, requests, DriverName, parameters)
}

// opaqueCodec is what the claim's author wrote, applying to every
// request in the claim.
func opaqueCodec(parameters string) string {
	return configEntry(configFromClaim, "", parameters)
}

// classCodec is the same block written in the DeviceClass, which is
// cluster policy rather than one workload's choice.
func classCodec(parameters string) string {
	return configEntry(configFromClass, "", parameters)
}

// claimConfig reads the resolved config array out of one claim
// document.
func claimConfig(t *testing.T, document string) []AllocatedConfig {
	t.Helper()
	claim := &ResourceClaim{}
	if err := json.Unmarshal([]byte(document), claim); err != nil {
		t.Fatal(err)
	}
	if claim.Status.Allocation == nil {
		t.Fatal("the claim document holds no allocation")
	}
	return claim.Status.Allocation.Devices.Config
}

func TestClaimCodecsReadsTheOpaqueBlock(t *testing.T) {
	cases := []struct {
		name    string
		config  string
		request string
		want    string
	}{
		{name: "a claim with no config block", request: "speaker"},
		{
			name:    "a codec for every request",
			config:  opaqueCodec(`{"codec": "sbc"}`),
			request: "speaker",
			want:    "sbc",
		},
		{
			name: "another driver's block",
			config: `{"source": "FromClaim", "opaque": {"driver": "display.liken.sh",
			          "parameters": {"mode": "1080p"}}}`,
			request: "speaker",
			want:    "",
		},
		{
			name:    "a block that names the requests it applies to",
			config:  configEntry(configFromClaim, `"speaker"`, `{"codec": "aptx"}`),
			request: "speaker",
			want:    "aptx",
		},
		{
			name:    "a block that names another request",
			config:  configEntry(configFromClaim, `"screen"`, `{"codec": "aptx"}`),
			request: "speaker",
			want:    "",
		},
		{
			name: "a request's own block over the claim's",
			config: opaqueCodec(`{"codec": "sbc"}`) + "," +
				configEntry(configFromClaim, `"speaker"`, `{"codec": "aptx"}`),
			request: "speaker",
			want:    "aptx",
		},
		{
			// The class is cluster policy, and it answers for a claim
			// that states nothing of its own.
			name:    "the class's codec, with none in the claim",
			config:  classCodec(`{"codec": "sbc"}`),
			request: "speaker",
			want:    "sbc",
		},
		{
			name:    "the claim's codec over the class's",
			config:  classCodec(`{"codec": "sbc"}`) + "," + opaqueCodec(`{"codec": "aptx"}`),
			request: "speaker",
			want:    "aptx",
		},
		{
			// The allocator lists the class's config first, and the
			// precedence reads the source rather than the order, so the
			// answer does not move when the order does.
			name:    "the claim's codec, listed before the class's",
			config:  opaqueCodec(`{"codec": "aptx"}`) + "," + classCodec(`{"codec": "sbc"}`),
			request: "speaker",
			want:    "aptx",
		},
		{
			name: "the claim's every-request block over the class's named one",
			config: configEntry(configFromClass, `"speaker"`, `{"codec": "sbc"}`) + "," +
				opaqueCodec(`{"codec": "aptx"}`),
			request: "speaker",
			want:    "aptx",
		},
		{
			name: "the class's named block, with the claim naming no request",
			config: configEntry(configFromClass, `"speaker"`, `{"codec": "sbc"}`) + "," +
				classCodec(`{"codec": "aptx"}`),
			request: "speaker",
			want:    "sbc",
		},
		{
			name:    "a block with empty parameters",
			config:  opaqueCodec(`{}`),
			request: "speaker",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			selection, err := claimCodecs(claimConfig(t, claimWith(testSpeakerName, c.config)))
			if err != nil {
				t.Fatal(err)
			}
			if got := selection.forRequest(c.request); got != c.want {
				t.Errorf("codec = %q, want %q", got, c.want)
			}
		})
	}
}

// A typo in the parameters is a codec nobody asked for, played with
// nothing said anywhere, so the parse refuses what it does not know.
// The source does not soften it: a typo in cluster policy is as
// wrong as one in a claim.
func TestClaimCodecsRefusesParametersItCannotRead(t *testing.T) {
	cases := []struct {
		name   string
		config string
		says   string
	}{
		{
			name:   "a key this driver does not read",
			config: opaqueCodec(`{"codecs": "sbc"}`),
			says:   `"codecs"`,
		},
		{
			name:   "a key the class does not read either",
			config: classCodec(`{"codecs": "sbc"}`),
			says:   `"codecs"`,
		},
		{
			name:   "a codec that is not a string",
			config: opaqueCodec(`{"codec": 6}`),
			says:   "not a string",
		},
		{
			name:   "parameters that are not an object",
			config: opaqueCodec(`["sbc"]`),
			says:   "parameters",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := claimCodecs(claimConfig(t, claimWith(testSpeakerName, c.config)))
			if err == nil {
				t.Fatal("the parse accepted parameters it cannot read")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("error = %q, want it to say %q", err, c.says)
			}
		})
	}
}

// codecWrite is one pw-cli set-param on the bluez5 device.
type codecWrite struct {
	Device int
	Codec  int
}

// volumeWrite is one pw-cli set-param on a sink node.
type volumeWrite struct {
	Node    int
	Volumes []float64
}

// fakePipeWire stands in for the graph read and the two writes a
// codec switch makes. A codec write changes what a later read
// reports, after settleReads reads, which is the renegotiation seen
// from here: the sink node is destroyed and built again with a new
// id under the same name.
type fakePipeWire struct {
	mu           sync.Mutex
	sink         bluezSink
	outputs      map[pcmAddress]string
	newNodeID    int
	settleReads  int
	stuck        bool
	pending      int
	switching    bool
	next         string
	reads        int
	codecWrites  []codecWrite
	volumeWrites []volumeWrite
}

// currentSink is the sink a connected speaker holds before any
// switch: SBC on node 63, at a level a person turned down.
func currentSink() bluezSink {
	return bluezSink{
		Node:    testSpeakerNode,
		NodeID:  63,
		Codec:   "sbc",
		Codecs:  twoCodecs(),
		Device:  62,
		Volumes: []float64{0.3, 0.3},
	}
}

func (f *fakePipeWire) read(context.Context) (pwGraph, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	f.settle()
	graph := pwGraph{Outputs: map[pcmAddress]string{}, Speakers: map[string]bluezSink{}}
	for output, node := range f.outputs {
		graph.Outputs[output] = node
	}
	if f.sink.Node != "" {
		graph.Speakers[testSpeakerAddress] = f.sink
	}
	return graph, nil
}

// settle applies a written codec once the renegotiation has taken
// as many reads as this fake was told it takes.
func (f *fakePipeWire) settle() {
	if !f.switching {
		return
	}
	if f.pending > 0 {
		f.pending--
		return
	}
	f.switching = false
	f.sink.Codec = f.next
	f.sink.NodeID = f.newNodeID
}

func (f *fakePipeWire) writeCodec(_ context.Context, device, codec int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.codecWrites = append(f.codecWrites, codecWrite{Device: device, Codec: codec})
	f.next = codecNameByID(f.sink.Codecs, codec)
	f.pending = f.settleReads
	f.switching = !f.stuck
	return nil
}

func (f *fakePipeWire) writeVolumes(_ context.Context, node int, volumes []float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.volumeWrites = append(f.volumeWrites, volumeWrite{Node: node, Volumes: volumes})
	return nil
}

func codecNameByID(codecs []bluezCodec, id int) string {
	for _, codec := range codecs {
		if codec.ID == id {
			return codec.Name
		}
	}
	return ""
}

// codecPlugin builds a DRA plugin over an API server that holds one
// claim, and over the fake PipeWire.
func codecPlugin(t *testing.T, claim string, pipewire *fakePipeWire) *draPlugin {
	t.Helper()
	plugin := testPlugin(t, claim, pwGraph{})
	plugin.graph = pipewire.read
	plugin.setCodec = pipewire.writeCodec
	plugin.setVolumes = pipewire.writeVolumes
	plugin.codecTimeout = 200 * time.Millisecond
	plugin.codecInterval = time.Millisecond
	return plugin
}

// deliveredNode is the node name the claim's CDI file gives the
// consumer.
func deliveredNode(t *testing.T, dir string) string {
	t.Helper()
	spec := readSpec(t, dir+"/audio.liken.sh-claim-1.json")
	for _, variable := range spec.Devices[0].ContainerEdits.Env {
		if strings.HasPrefix(variable, nodeVariable+"=") {
			return strings.TrimPrefix(variable, nodeVariable+"=")
		}
	}
	return ""
}

// The whole switch: the driver writes the codec by its integer id,
// waits for the rebuilt node to report it, sets the new node to
// unity, and only then delivers the node name.
func TestPrepareSwitchesTheCodecTheClaimStates(t *testing.T) {
	dir := specDirectory(t)
	pipewire := &fakePipeWire{sink: currentSink(), newNodeID: 99, settleReads: 2}
	plugin := codecPlugin(t, claimWith(testSpeakerName, opaqueCodec(`{"codec": "aptx"}`)), pipewire)

	entry := prepare(t, plugin, "claim-1")
	if entry.Error != "" {
		t.Fatalf("prepare failed: %s", entry.Error)
	}

	writes := []codecWrite{{Device: 62, Codec: 6}}
	if !reflect.DeepEqual(pipewire.codecWrites, writes) {
		t.Errorf("codec writes = %+v, want %+v", pipewire.codecWrites, writes)
	}
	// The unity write lands on the rebuilt node, not on the one the
	// switch destroyed.
	unity := []volumeWrite{{Node: 99, Volumes: []float64{1, 1}}}
	if !reflect.DeepEqual(pipewire.volumeWrites, unity) {
		t.Errorf("volume writes = %+v, want %+v", pipewire.volumeWrites, unity)
	}
	if got := deliveredNode(t, dir); got != testSpeakerNode {
		t.Errorf("delivered node = %q, want %q", got, testSpeakerNode)
	}
}

// Every prepare of a speaker delivers its sink at unity, whether the
// claim states a codec, states the one already playing, or states
// none at all. Any level a prepare finds is a leftover from an
// earlier tenant, never the arriving consumer's choice.
func TestPrepareDeliversASpeakerAtUnity(t *testing.T) {
	cases := []struct {
		name   string
		config string
		codecs int
	}{
		{
			name:   "a claim that states no codec",
			config: "",
		},
		{
			name:   "a claim that states the codec already playing",
			config: opaqueCodec(`{"codec": "sbc"}`),
		},
		{
			name:   "a claim that states the other codec",
			config: opaqueCodec(`{"codec": "aptx"}`),
			codecs: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := specDirectory(t)
			pipewire := &fakePipeWire{sink: currentSink(), newNodeID: 99}
			plugin := codecPlugin(t, claimWith(testSpeakerName, c.config), pipewire)

			entry := prepare(t, plugin, "claim-1")
			if entry.Error != "" {
				t.Fatalf("prepare failed: %s", entry.Error)
			}
			if len(pipewire.codecWrites) != c.codecs {
				t.Errorf("codec writes = %+v, want %d", pipewire.codecWrites, c.codecs)
			}
			if len(pipewire.volumeWrites) != 1 {
				t.Fatalf("volume writes = %+v, want one", pipewire.volumeWrites)
			}
			if levels := pipewire.volumeWrites[0].Volumes; !reflect.DeepEqual(levels, []float64{1, 1}) {
				t.Errorf("levels = %v, want unity on both channels", levels)
			}
			if got := deliveredNode(t, dir); got != testSpeakerNode {
				t.Errorf("delivered node = %q, want %q", got, testSpeakerNode)
			}
		})
	}
}

// The channel count comes from the node, and a node that reported
// none takes the stereo assumption. Object 0 is not a node, so a
// graph read that gave no id is written to at all.
func TestPrepareWritesUnityOnEveryChannelTheNodeHas(t *testing.T) {
	sixChannels := currentSink()
	sixChannels.Volumes = []float64{0.3, 0.3, 0.3, 0.3, 0.3, 0.3}
	noVolumes := currentSink()
	noVolumes.Volumes = nil
	noID := currentSink()
	noID.NodeID = 0
	cases := []struct {
		name string
		sink bluezSink
		want []volumeWrite
	}{
		{
			name: "a stereo node",
			sink: currentSink(),
			want: []volumeWrite{{Node: 63, Volumes: []float64{1, 1}}},
		},
		{
			name: "a node with six channels",
			sink: sixChannels,
			want: []volumeWrite{{Node: 63, Volumes: []float64{1, 1, 1, 1, 1, 1}}},
		},
		{
			name: "a node that reported no channels",
			sink: noVolumes,
			want: []volumeWrite{{Node: 63, Volumes: []float64{1, 1}}},
		},
		{
			name: "a node the graph gave no id",
			sink: noID,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			specDirectory(t)
			pipewire := &fakePipeWire{sink: c.sink}
			plugin := codecPlugin(t, claimWith(testSpeakerName, ""), pipewire)

			entry := prepare(t, plugin, "claim-1")
			if entry.Error != "" {
				t.Fatalf("prepare failed: %s", entry.Error)
			}
			if !reflect.DeepEqual(pipewire.volumeWrites, c.want) {
				t.Errorf("volume writes = %+v, want %+v", pipewire.volumeWrites, c.want)
			}
		})
	}
}

// The card's own outputs take no volume write. Their level lives on
// a route, which this plan does not touch.
func TestPrepareWritesNoVolumeOnTheCardsOutputs(t *testing.T) {
	specDirectory(t)
	pipewire := &fakePipeWire{outputs: map[pcmAddress]string{
		{Card: 0, PCM: 3}: "alsa_output.pci-0000_00_1f.3.hdmi-stereo",
	}}
	plugin := codecPlugin(t, allocatedClaim, pipewire)

	entry := prepare(t, plugin, "claim-1")
	if entry.Error != "" {
		t.Fatalf("prepare failed: %s", entry.Error)
	}
	if len(pipewire.volumeWrites) != 0 {
		t.Errorf("the driver wrote %+v on an ALSA output", pipewire.volumeWrites)
	}
}

// A unity write that fails fails the prepare. The pod waits, the
// kubelet retries, and the retry converges; a delivery that went
// ahead would play at a level nobody chose.
func TestPrepareFailsWhenTheUnityWriteFails(t *testing.T) {
	dir := specDirectory(t)
	pipewire := &fakePipeWire{sink: currentSink()}
	plugin := codecPlugin(t, claimWith(testSpeakerName, ""), pipewire)
	plugin.setVolumes = func(context.Context, int, []float64) error {
		return errors.New("running pw-cli set-param: no such file or directory")
	}

	entry := prepare(t, plugin, "claim-1")
	if entry.Error == "" {
		t.Fatalf("prepare delivered a sink it could not set: %+v", entry.Devices)
	}
	if !strings.Contains(entry.Error, "unity") {
		t.Errorf("error = %q, want it to say what it could not set", entry.Error)
	}
	if left := specFiles(t, dir); len(left) != 0 {
		t.Errorf("a refused claim left %v behind", left)
	}
}

func TestPrepareRefusesACodecItCannotDeliver(t *testing.T) {
	cases := []struct {
		name   string
		device string
		config string
		stuck  bool
		says   string
	}{
		{
			name:   "a codec the speaker does not offer",
			device: testSpeakerName,
			config: opaqueCodec(`{"codec": "ldac"}`),
			says:   "sbc aptx",
		},
		{
			name:   "a codec on the card's own output",
			device: "card0-pcm3",
			config: opaqueCodec(`{"codec": "sbc"}`),
			says:   "not a Bluetooth speaker",
		},
		{
			name:   "a parameter this driver does not read",
			device: testSpeakerName,
			config: opaqueCodec(`{"bitpool": "53"}`),
			says:   `"bitpool"`,
		},
		{
			// PipeWire took the write and the transport never came
			// back with the codec, so no node ever reports it.
			name:   "a switch the graph never reports",
			device: testSpeakerName,
			config: opaqueCodec(`{"codec": "aptx"}`),
			stuck:  true,
			says:   "did not report",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := specDirectory(t)
			pipewire := &fakePipeWire{sink: currentSink(), newNodeID: 99, stuck: c.stuck}
			plugin := codecPlugin(t, claimWith(c.device, c.config), pipewire)

			entry := prepare(t, plugin, "claim-1")
			if entry.Error == "" {
				t.Fatalf("prepare accepted a codec it cannot deliver: %+v", entry.Devices)
			}
			if !strings.Contains(entry.Error, c.says) {
				t.Errorf("error = %q, want it to say %q", entry.Error, c.says)
			}
			// A refusal delivers no node name, so no consumer plays
			// through a codec nobody chose.
			if left := specFiles(t, dir); len(left) != 0 {
				t.Errorf("a refused claim left %v behind", left)
			}
		})
	}
}

// A claim with no config block behaves as it did before a codec
// could be stated: WirePlumber's pick stands, and the driver writes
// nothing.
func TestPrepareLeavesTheCodecAloneWithNoConfigBlock(t *testing.T) {
	dir := specDirectory(t)
	pipewire := &fakePipeWire{sink: currentSink(), newNodeID: 99}
	plugin := codecPlugin(t, claimWith(testSpeakerName, ""), pipewire)

	entry := prepare(t, plugin, "claim-1")
	if entry.Error != "" {
		t.Fatalf("prepare failed: %s", entry.Error)
	}
	if len(pipewire.codecWrites) != 0 {
		t.Errorf("the driver wrote %+v for a claim that stated no codec", pipewire.codecWrites)
	}
	if got := deliveredNode(t, dir); got != testSpeakerNode {
		t.Errorf("delivered node = %q, want %q", got, testSpeakerNode)
	}
}

// The unprepare path never renegotiates. A speaker allocates to one
// claim at a time, so no other consumer holds the old codec, and a
// switch on teardown would sound for nobody.
func TestUnprepareLeavesTheCodecWhereTheClaimPutIt(t *testing.T) {
	specDirectory(t)
	pipewire := &fakePipeWire{sink: currentSink(), newNodeID: 99}
	plugin := codecPlugin(t, claimWith(testSpeakerName, opaqueCodec(`{"codec": "aptx"}`)), pipewire)

	if entry := prepare(t, plugin, "claim-1"); entry.Error != "" {
		t.Fatalf("prepare failed: %s", entry.Error)
	}
	writes := len(pipewire.codecWrites)

	resp, err := plugin.NodeUnprepareResources(context.Background(), &drav1.NodeUnprepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "kitchen", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if message := resp.Claims["claim-1"].Error; message != "" {
		t.Fatalf("unprepare failed: %s", message)
	}
	if len(pipewire.codecWrites) != writes {
		t.Errorf("unprepare wrote %+v", pipewire.codecWrites[writes:])
	}
}
