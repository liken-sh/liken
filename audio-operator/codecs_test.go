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
	"testing/synctest"
	"time"

	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
)

// labCodecEntry is the PropInfo entry that pw-dump printed for a
// speaker on liken-1. aptX plays, and the alternatives are the codecs
// that the speaker and this image both support.
const labCodecEntry = `{
  "id": "bluetoothAudioCodec",
  "description": "Air codec",
  "type": { "default": 6, "alt1": 6, "alt2": 1, "alt3": 2, "alt4": 9 },
  "labels": [ 6, "aptX", 1, "SBC", 2, "SBC-XQ", 9, "aptX-LL" ]
}`

// propInfo decodes one PropInfo entry.
func propInfo(t *testing.T, entry string) pwPropInfo {
	t.Helper()
	var info pwPropInfo
	if err := json.Unmarshal([]byte(entry), &info); err != nil {
		t.Fatalf("parsing the PropInfo entry: %v", err)
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
			name:  "the entry the lab speaker reports",
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

// codecName spells a codec the way api.bluez5.codec does, so the codec
// attribute names one of the codecs in the codecs attribute.
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

// twoCodecs returns the codecs that the fixtures' speaker offers, with
// the playing codec first, as PropInfo lists them.
func twoCodecs() []bluezCodec {
	return []bluezCodec{{ID: 1, Name: "sbc"}, {ID: 6, Name: "aptx"}}
}

// manyCodecs returns codecs whose joined names are longer than
// maxAttributeLength.
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
		{name: "the set the lab speaker offers", codecs: twoCodecs(), want: "sbc aptx"},
		{
			// A name cut at the limit would be a codec that does not
			// exist, and a selector with .contains() could match it.
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
				t.Errorf("the value is %d characters, want at most %d", len(got), maxAttributeLength)
			}
		})
	}
}

func TestCodecPropsWritesTheIntegerID(t *testing.T) {
	want := "{ bluetoothAudioCodec: 6 }"
	if got := codecProps(6); got != want {
		t.Errorf("codecProps(6) = %q, want %q", got, want)
	}
}

// claimWith returns an allocated claim for one device, whose
// status.allocation.devices.config has the given entries. The driver
// reads the config there and not in spec.devices.config, because the
// scheduler copies the entries of the claim and of the DeviceClass
// there and records the source of each.
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

// configEntry returns one of this driver's entries of the allocation's
// config.
func configEntry(source, requests, parameters string) string {
	return fmt.Sprintf(`{"source": %q, "requests": [%s], "opaque": {"driver": %q, "parameters": %s}}`,
		source, requests, DriverName, parameters)
}

// claimEntry returns an entry from the claim for all of its requests.
func claimEntry(parameters string) string {
	return configEntry(configFromClaim, "", parameters)
}

// classEntry returns an entry from the DeviceClass for all requests.
func classEntry(parameters string) string {
	return configEntry(configFromClass, "", parameters)
}

// claimConfig decodes the claim and returns its allocation's config.
func claimConfig(t *testing.T, document string) []AllocatedConfig {
	t.Helper()
	claim := &ResourceClaim{}
	if err := json.Unmarshal([]byte(document), claim); err != nil {
		t.Fatalf("parsing the claim document: %v", err)
	}
	if claim.Status.Allocation == nil {
		t.Fatal("the claim document has no allocation")
	}
	return claim.Status.Allocation.Devices.Config
}

func TestClaimCodecsReadsTheOpaqueEntries(t *testing.T) {
	cases := []struct {
		name    string
		config  string
		request string
		want    string
	}{
		{name: "a claim with no config entry", request: "speaker"},
		{
			name:    "a codec for every request",
			config:  claimEntry(`{"codec": "sbc"}`),
			request: "speaker",
			want:    "sbc",
		},
		{
			name: "another driver's entry",
			config: `{"source": "FromClaim", "opaque": {"driver": "display.liken.sh",
			          "parameters": {"mode": "1080p"}}}`,
			request: "speaker",
			want:    "",
		},
		{
			name:    "an entry that names the request",
			config:  configEntry(configFromClaim, `"speaker"`, `{"codec": "aptx"}`),
			request: "speaker",
			want:    "aptx",
		},
		{
			name:    "an entry that names another request",
			config:  configEntry(configFromClaim, `"screen"`, `{"codec": "aptx"}`),
			request: "speaker",
			want:    "",
		},
		{
			name: "an entry for the request over one for all requests",
			config: claimEntry(`{"codec": "sbc"}`) + "," +
				configEntry(configFromClaim, `"speaker"`, `{"codec": "aptx"}`),
			request: "speaker",
			want:    "aptx",
		},
		{
			name:    "the class's codec, with none in the claim",
			config:  classEntry(`{"codec": "sbc"}`),
			request: "speaker",
			want:    "sbc",
		},
		{
			name:    "the claim's codec over the class's",
			config:  classEntry(`{"codec": "sbc"}`) + "," + claimEntry(`{"codec": "aptx"}`),
			request: "speaker",
			want:    "aptx",
		},
		{
			// The allocator lists the class's entries first, but
			// forRequest reads the source and not the order.
			name:    "the claim's codec, listed before the class's",
			config:  claimEntry(`{"codec": "aptx"}`) + "," + classEntry(`{"codec": "sbc"}`),
			request: "speaker",
			want:    "aptx",
		},
		{
			name: "the claim's entry for all requests over the class's entry for the request",
			config: configEntry(configFromClass, `"speaker"`, `{"codec": "sbc"}`) + "," +
				claimEntry(`{"codec": "aptx"}`),
			request: "speaker",
			want:    "aptx",
		},
		{
			name: "the class's entry for the request over its entry for all requests",
			config: configEntry(configFromClass, `"speaker"`, `{"codec": "sbc"}`) + "," +
				classEntry(`{"codec": "aptx"}`),
			request: "speaker",
			want:    "sbc",
		},
		{
			name:    "an entry with empty parameters",
			config:  claimEntry(`{}`),
			request: "speaker",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			selection, err := claimCodecs(claimConfig(t, claimWith(testSpeakerName, c.config)))
			if err != nil {
				t.Fatalf("reading the claim's codecs: %v", err)
			}
			if got := selection.forRequest(c.request); got != c.want {
				t.Errorf("codec = %q, want %q", got, c.want)
			}
		})
	}
}

// The DeviceClass's parameters are refused like the claim's.
func TestClaimCodecsRefusesParametersItCannotRead(t *testing.T) {
	cases := []struct {
		name        string
		config      string
		wantInError string
	}{
		{
			name:        "a key this driver does not read",
			config:      claimEntry(`{"codecs": "sbc"}`),
			wantInError: `"codecs"`,
		},
		{
			name:        "the same key in the DeviceClass",
			config:      classEntry(`{"codecs": "sbc"}`),
			wantInError: `"codecs"`,
		},
		{
			name:        "a codec that is not a string",
			config:      claimEntry(`{"codec": 6}`),
			wantInError: "not a string",
		},
		{
			name:        "parameters that are not an object",
			config:      claimEntry(`["sbc"]`),
			wantInError: "parameters",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := claimCodecs(claimConfig(t, claimWith(testSpeakerName, c.config)))
			if err == nil {
				t.Fatal("claimCodecs returned no error, want a refusal of the parameters")
			}
			if !strings.Contains(err.Error(), c.wantInError) {
				t.Errorf("error = %q, want it to contain %q", err, c.wantInError)
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

// fakePipeWire is a fake of the graph read and of the codec and volume
// writes. After a codec write and settleReads more reads, a read reports
// the new codec on a node with newNodeID and the same name, as PipeWire
// rebuilds the sink node. When stuck is true, no read reports the new
// codec.
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

// currentSink returns the sink of a connected speaker before any
// switch: SBC on node 63, below unity volume.
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
	graph := pwGraph{Nodes: map[nodeAddress]pwNode{}, Speakers: map[string]bluezSink{}}
	for output, node := range f.outputs {
		graph.Nodes[nodeAddress{pcmAddress: output, Direction: directionSink}] = pwNode{Name: node}
	}
	if f.sink.Node != "" {
		graph.Speakers[testSpeakerAddress] = f.sink
	}
	return graph, nil
}

// settle applies the written codec once settleReads reads have passed
// since the write.
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

// changes returns a closed channel while a switch is in progress, so
// await reads again at once. Otherwise it returns nil, which never
// fires, so a stuck switch waits until its timeout.
func (f *fakePipeWire) changes() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.switching {
		return nil
	}
	moved := make(chan struct{})
	close(moved)
	return moved
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

// codecPlugin builds a DRA plugin whose API server has the claim and
// whose PipeWire is the fake.
func codecPlugin(t *testing.T, claim string, pipewire *fakePipeWire) *draPlugin {
	t.Helper()
	plugin := testPlugin(t, claim, pwGraph{})
	plugin.graph = pipewire.read
	plugin.setCodec = pipewire.writeCodec
	plugin.setVolumes = pipewire.writeVolumes
	plugin.changes = pipewire.changes
	return plugin
}

// nodeInSpec returns the node name that the claim's CDI spec gives the
// consumer.
func nodeInSpec(t *testing.T, dir string) string {
	t.Helper()
	spec := readSpec(t, dir+"/audio.liken.sh-claim-1.json")
	for _, variable := range spec.Devices[0].ContainerEdits.Env {
		if strings.HasPrefix(variable, nodeVariable+"=") {
			return strings.TrimPrefix(variable, nodeVariable+"=")
		}
	}
	return ""
}

// The driver writes the codec by its integer ID, waits for the rebuilt
// node to report it, writes unity volume on the new node, and then
// returns the node name.
func TestPrepareSwitchesToTheCodecTheClaimSelects(t *testing.T) {
	dir := specDirectory(t)
	pipewire := &fakePipeWire{sink: currentSink(), newNodeID: 99, settleReads: 2}
	plugin := codecPlugin(t, claimWith(testSpeakerName, claimEntry(`{"codec": "aptx"}`)), pipewire)

	entry := prepare(t, plugin, "claim-1")
	if entry.Error != "" {
		t.Fatalf("preparing the claim: %s", entry.Error)
	}

	writes := []codecWrite{{Device: 62, Codec: 6}}
	if !reflect.DeepEqual(pipewire.codecWrites, writes) {
		t.Errorf("codec writes = %+v, want %+v", pipewire.codecWrites, writes)
	}
	// The unity write is on the rebuilt node, not on the node that the
	// switch destroyed.
	unity := []volumeWrite{{Node: 99, Volumes: []float64{1, 1}}}
	if !reflect.DeepEqual(pipewire.volumeWrites, unity) {
		t.Errorf("volume writes = %+v, want %+v", pipewire.volumeWrites, unity)
	}
	if got := nodeInSpec(t, dir); got != testSpeakerNode {
		t.Errorf("the prepared node is %q, want %q", got, testSpeakerNode)
	}
}

// A prepare of a speaker that no other claim holds sets its sink to
// unity, whether the claim selects a codec, the codec already playing,
// or no codec. A level that the prepare finds was set for an earlier
// claim, not for this one.
func TestPrepareDeliversASpeakerAtUnity(t *testing.T) {
	cases := []struct {
		name   string
		config string
		codecs int
	}{
		{
			name:   "a claim that selects no codec",
			config: "",
		},
		{
			name:   "a claim that selects the codec already playing",
			config: claimEntry(`{"codec": "sbc"}`),
		},
		{
			name:   "a claim that selects the other codec",
			config: claimEntry(`{"codec": "aptx"}`),
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
				t.Fatalf("preparing the claim: %s", entry.Error)
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
			if got := nodeInSpec(t, dir); got != testSpeakerNode {
				t.Errorf("the prepared node is %q, want %q", got, testSpeakerNode)
			}
		})
	}
}

// The unity write has one value for each channel of the node, and two
// for a node that reported no channels. Object 0 is not a node, so a
// sink with no node id gets no write.
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
				t.Fatalf("preparing the claim: %s", entry.Error)
			}
			if !reflect.DeepEqual(pipewire.volumeWrites, c.want) {
				t.Errorf("volume writes = %+v, want %+v", pipewire.volumeWrites, c.want)
			}
		})
	}
}

// The level of a sound card's output is set on its route, which a
// prepare does not change.
func TestPrepareWritesNoVolumeOnTheCardsOutputs(t *testing.T) {
	specDirectory(t)
	pipewire := &fakePipeWire{outputs: map[pcmAddress]string{
		{Card: 0, PCM: 3}: "alsa_output.pci-0000_00_1f.3.hdmi-stereo",
	}}
	plugin := codecPlugin(t, allocatedClaim, pipewire)

	entry := prepare(t, plugin, "claim-1")
	if entry.Error != "" {
		t.Fatalf("preparing the claim: %s", entry.Error)
	}
	if len(pipewire.volumeWrites) != 0 {
		t.Errorf("volume writes = %+v on an ALSA output, want none", pipewire.volumeWrites)
	}
}

// The kubelet retries a failed prepare. A prepare that succeeded after a
// failed unity write would play at the level set for an earlier claim.
func TestPrepareFailsWhenTheUnityWriteFails(t *testing.T) {
	dir := specDirectory(t)
	pipewire := &fakePipeWire{sink: currentSink()}
	plugin := codecPlugin(t, claimWith(testSpeakerName, ""), pipewire)
	plugin.setVolumes = func(context.Context, int, []float64) error {
		return errors.New("running pw-cli set-param: no such file or directory")
	}

	entry := prepare(t, plugin, "claim-1")
	if entry.Error == "" {
		t.Fatalf("prepare returned %+v, want a refusal because the unity write returned an error", entry.Devices)
	}
	if !strings.Contains(entry.Error, "unity") {
		t.Errorf("error = %q, want it to name the unity write", entry.Error)
	}
	if left := specFiles(t, dir); len(left) != 0 {
		t.Errorf("a refused claim left the spec files %v, want none", left)
	}
}

func TestPrepareRefusesACodecItCannotDeliver(t *testing.T) {
	cases := []struct {
		name        string
		device      string
		config      string
		stuck       bool
		wantInError string
		waits       time.Duration
	}{
		{
			name:        "a codec the speaker does not offer",
			device:      testSpeakerName,
			config:      claimEntry(`{"codec": "ldac"}`),
			wantInError: "sbc aptx",
		},
		{
			name:        "a codec on the card's own output",
			device:      testSinkName,
			config:      claimEntry(`{"codec": "sbc"}`),
			wantInError: "not a Bluetooth speaker",
		},
		{
			name:        "a parameter this driver does not read",
			device:      testSpeakerName,
			config:      claimEntry(`{"bitpool": "53"}`),
			wantInError: `"bitpool"`,
		},
		{
			// PipeWire accepts the write, and no node reports the
			// codec before the timeout.
			name:        "a switch the graph never reports",
			device:      testSpeakerName,
			config:      claimEntry(`{"codec": "aptx"}`),
			stuck:       true,
			wantInError: "did not report",
			waits:       codecSwitchTimeout,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				dir := specDirectory(t)
				pipewire := &fakePipeWire{sink: currentSink(), newNodeID: 99, stuck: c.stuck}
				plugin := codecPlugin(t, claimWith(c.device, c.config), pipewire)

				started := time.Now()
				entry := prepare(t, plugin, "claim-1")
				if waited := time.Since(started); waited != c.waits {
					t.Errorf("prepare returned after %s, want %s", waited, c.waits)
				}
				if entry.Error == "" {
					t.Fatalf("prepare returned %+v, want a refusal", entry.Devices)
				}
				if !strings.Contains(entry.Error, c.wantInError) {
					t.Errorf("error = %q, want it to contain %q", entry.Error, c.wantInError)
				}
				// A refused prepare leaves no CDI spec, so no consumer
				// plays a codec that the claim did not select.
				if left := specFiles(t, dir); len(left) != 0 {
					t.Errorf("a refused claim left the spec files %v, want none", left)
				}
			})
		})
	}
}

// A claim with no config entry keeps the codec that WirePlumber chose.
func TestPrepareLeavesTheCodecAloneWithNoConfigEntry(t *testing.T) {
	dir := specDirectory(t)
	pipewire := &fakePipeWire{sink: currentSink(), newNodeID: 99}
	plugin := codecPlugin(t, claimWith(testSpeakerName, ""), pipewire)

	entry := prepare(t, plugin, "claim-1")
	if entry.Error != "" {
		t.Fatalf("preparing the claim: %s", entry.Error)
	}
	if len(pipewire.codecWrites) != 0 {
		t.Errorf("codec writes = %+v for a claim that selects no codec, want none", pipewire.codecWrites)
	}
	if got := nodeInSpec(t, dir); got != testSpeakerNode {
		t.Errorf("the prepared node is %q, want %q", got, testSpeakerNode)
	}
}

// A switch after the claim ends would rebuild the sink for no consumer.
func TestUnprepareWritesNoCodec(t *testing.T) {
	specDirectory(t)
	pipewire := &fakePipeWire{sink: currentSink(), newNodeID: 99}
	plugin := codecPlugin(t, claimWith(testSpeakerName, claimEntry(`{"codec": "aptx"}`)), pipewire)

	if entry := prepare(t, plugin, "claim-1"); entry.Error != "" {
		t.Fatalf("preparing the claim: %s", entry.Error)
	}
	writes := len(pipewire.codecWrites)

	resp, err := plugin.NodeUnprepareResources(context.Background(), &drav1.NodeUnprepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "kitchen", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatalf("unpreparing the claim: %v", err)
	}
	if message := resp.Claims["claim-1"].Error; message != "" {
		t.Fatalf("unpreparing the claim: %s", message)
	}
	if len(pipewire.codecWrites) != writes {
		t.Errorf("unprepare wrote the codecs %+v, want none", pipewire.codecWrites[writes:])
	}
}

// The reconciler's switch for Sink.spec.codec uses codecSwitchTimeout,
// as a prepare does, and reads the graph through the graph feed.
func TestSpeakerCodecSwitchUsesTheSwitchTimeout(t *testing.T) {
	sink := currentSink()
	sink.Codec = "aptx"
	read := staticGraph(speakerGraph(map[string]bluezSink{testSpeakerAddress: sink}))

	change := speakerCodecSwitch(&graphFeed{poll: read})
	if change.timeout != codecSwitchTimeout {
		t.Errorf("timeout = %s, want %s", change.timeout, codecSwitchTimeout)
	}

	// The graph already reports the codec, so await returns the sink at
	// once.
	got, err := change.await(context.Background(), testSpeakerAddress, "aptx")
	if err != nil {
		t.Fatalf("waiting for the codec: %v", err)
	}
	if got.Node != testSpeakerNode {
		t.Errorf("sink = %+v, want the node %q", got, testSpeakerNode)
	}
}

func TestCodecNamesKeepsTheOrderOfTheCodecs(t *testing.T) {
	sink := bluezSink{Codecs: []bluezCodec{{ID: 1, Name: "sbc"}, {ID: 6, Name: "aptx"}}}
	if got := sink.codecNames(); !reflect.DeepEqual(got, []string{"sbc", "aptx"}) {
		t.Errorf("codecNames() = %v, want [sbc aptx]", got)
	}
	if got := (bluezSink{}).codecNames(); len(got) != 0 {
		t.Errorf("codecNames() of a speaker that offers no codec = %v, want none", got)
	}
}
