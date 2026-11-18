package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
)

// allocatedClaim pairs speakers with a screen. The screen's result
// belongs to another driver, so this driver must leave it alone.
const allocatedClaim = `{
  "metadata": {"name": "kitchen", "namespace": "media", "uid": "claim-1"},
  "status": {"allocation": {"devices": {"results": [
    {"request": "speakers", "driver": "audio.liken.sh", "pool": "liken-1", "device": "liken-1-pci-0000-00-1f-3-hdmi-0"},
    {"request": "screen", "driver": "display.liken.sh", "pool": "liken-1", "device": "card0-hdmi-a-1"}
  ]}}}
}`

// speakerClaim is allocated a Bluetooth speaker, whose device name is
// its MAC address with dashes.
const speakerClaim = `{
  "metadata": {"name": "kitchen", "namespace": "media", "uid": "claim-1"},
  "status": {"allocation": {"devices": {"results": [
    {"request": "speaker", "driver": "audio.liken.sh", "pool": "liken-1", "device": "a0-ab-51-33-b7-12"}
  ]}}}
}`

// testPlugin returns a plugin whose API server serves one claim and
// whose PipeWire graph is fixed.
func testPlugin(t *testing.T, claim string, graph pwGraph) *draPlugin {
	t.Helper()
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("the plugin sent %s %s, want only GET requests", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(claim))
	}))
	return &draPlugin{
		client:    client,
		endpoints: testInventory(),
		graph:     staticGraph(graph),
		changes:   func() <-chan struct{} { return nil },
		// A claim that selects no codec must not switch one. Every
		// prepare of a speaker writes volumes, and codecs_test.go
		// checks the levels.
		setCodec: func(context.Context, int, int) error {
			t.Error("the driver wrote a codec for a claim that selects none, want no codec write")
			return nil
		},
		setVolumes: func(context.Context, int, []float64) error { return nil },
	}
}

func prepare(t *testing.T, plugin *draPlugin, uid string) *drav1.NodePrepareResourceResponse {
	t.Helper()
	resp, err := plugin.NodePrepareResources(context.Background(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "kitchen", Uid: uid}},
	})
	if err != nil {
		t.Fatalf("preparing the claim: %v", err)
	}
	entry, found := resp.Claims[uid]
	if !found {
		t.Fatal("the response has no entry for the claim, which the kubelet reads as a failure to retry")
	}
	return entry
}

func TestPrepareDeliversTheSocketAndTheSinkName(t *testing.T) {
	dir := specDirectory(t)
	plugin := testPlugin(t, allocatedClaim, outputGraph(map[pcmAddress]string{
		{Card: 0, PCM: 3}: "alsa_output.pci-0000_00_1f.3.hdmi-stereo",
	}))

	entry := prepare(t, plugin, "claim-1")
	if entry.Error != "" {
		t.Fatalf("preparing the claim: %s", entry.Error)
	}
	// One device, because the other result belongs to another driver.
	if len(entry.Devices) != 1 {
		t.Fatalf("devices = %+v, want one", entry.Devices)
	}
	device := entry.Devices[0]
	if device.DeviceName != testSinkName || device.PoolName != "liken-1" {
		t.Errorf("device = %+v, want %s in the pool liken-1", device, testSinkName)
	}
	if !slices.Equal(device.RequestNames, []string{"speakers"}) {
		t.Errorf("requests = %v, want [speakers]", device.RequestNames)
	}
	want := []string{"audio.liken.sh/output=claim-1-" + testSinkName}
	if !slices.Equal(device.CdiDeviceIds, want) {
		t.Errorf("CDI ids = %v, want %v", device.CdiDeviceIds, want)
	}

	// The container runtime applies the CDI file, not the response.
	spec := readSpec(t, filepath.Join(dir, "audio.liken.sh-claim-1.json"))
	if spec.Kind != cdiKind {
		t.Errorf("kind = %q, want %q", spec.Kind, cdiKind)
	}
	edits := spec.Devices[0].ContainerEdits
	env := []string{
		"PIPEWIRE_REMOTE=/var/run/audio.liken.sh/pipewire-0",
		"PIPEWIRE_NODE=alsa_output.pci-0000_00_1f.3.hdmi-stereo",
	}
	if !slices.Equal(edits.Env, env) {
		t.Errorf("env = %v, want %v", edits.Env, env)
	}
	if len(edits.Mounts) != 1 || edits.Mounts[0].HostPath != runtimeDir {
		t.Errorf("mounts = %+v, want one mount of %s", edits.Mounts, runtimeDir)
	}

	// The kubelet can repeat an unprepare until it records a success,
	// so a second unprepare must succeed too.
	for range 2 {
		resp, err := plugin.NodeUnprepareResources(context.Background(), &drav1.NodeUnprepareResourcesRequest{
			Claims: []*drav1.Claim{{Namespace: "media", Name: "kitchen", Uid: "claim-1"}},
		})
		if err != nil {
			t.Fatalf("unpreparing the claim: %v", err)
		}
		if message := resp.Claims["claim-1"].Error; message != "" {
			t.Fatalf("unpreparing the claim: %s", message)
		}
	}
	if left := specFiles(t, dir); len(left) != 0 {
		t.Fatalf("unprepare left the spec files %v, want none", left)
	}
}

// A Bluetooth speaker gets the same CDI edits as an HDMI output,
// with the node name that WirePlumber's bluez monitor created.
func TestPrepareDeliversASpeakersNodeName(t *testing.T) {
	dir := specDirectory(t)
	plugin := testPlugin(t, speakerClaim, speakerGraph(map[string]bluezSink{
		testSpeakerAddress: {Node: testSpeakerNode, Codec: "sbc"},
	}))

	entry := prepare(t, plugin, "claim-1")
	if entry.Error != "" {
		t.Fatalf("preparing the claim: %s", entry.Error)
	}
	if len(entry.Devices) != 1 {
		t.Fatalf("devices = %+v, want one", entry.Devices)
	}
	if got := entry.Devices[0].DeviceName; got != testSpeakerName {
		t.Errorf("device = %q, want %q", got, testSpeakerName)
	}

	spec := readSpec(t, filepath.Join(dir, "audio.liken.sh-claim-1.json"))
	env := []string{
		"PIPEWIRE_REMOTE=/var/run/audio.liken.sh/pipewire-0",
		"PIPEWIRE_NODE=" + testSpeakerNode,
	}
	if got := spec.Devices[0].ContainerEdits.Env; !slices.Equal(got, env) {
		t.Errorf("env = %v, want %v", got, env)
	}
	if mounts := spec.Devices[0].ContainerEdits.Mounts; len(mounts) != 1 || mounts[0].HostPath != runtimeDir {
		t.Errorf("mounts = %+v, want one mount of %s", mounts, runtimeDir)
	}
}

// sourceClaim is allocated a capture endpoint. PipeWire reads
// PIPEWIRE_NODE as target.object for a capture stream as well as for
// playback.
var sourceClaim = `{
  "metadata": {"name": "kitchen", "namespace": "media", "uid": "claim-1"},
  "status": {"allocation": {"devices": {"results": [
    {"request": "microphone", "driver": "audio.liken.sh", "pool": "liken-1", "device": "` +
	testSourceName + `"}
  ]}}}
}`

// sourceGraph returns a graph with one capture node, on the PCM
// device that the lab card's analog jack records through.
func sourceGraph(node string) pwGraph {
	address := pcmAddress{Card: 0, PCM: 0}
	return pwGraph{
		Nodes: map[nodeAddress]pwNode{
			{pcmAddress: address, Direction: directionSource}: {Name: node},
		},
		Speakers: map[string]bluezSink{},
	}
}

func TestPrepareDeliversASourceNodeName(t *testing.T) {
	dir := specDirectory(t)
	plugin := testPlugin(t, sourceClaim, sourceGraph(sourceNodeName(0, 0)))

	entry := prepare(t, plugin, "claim-1")
	if entry.Error != "" {
		t.Fatalf("preparing the claim: %s", entry.Error)
	}
	if len(entry.Devices) != 1 {
		t.Fatalf("devices = %+v, want one", entry.Devices)
	}
	if got := entry.Devices[0].DeviceName; got != testSourceName {
		t.Errorf("device = %q, want %q", got, testSourceName)
	}

	spec := readSpec(t, filepath.Join(dir, "audio.liken.sh-claim-1.json"))
	env := []string{
		"PIPEWIRE_REMOTE=/var/run/audio.liken.sh/pipewire-0",
		"PIPEWIRE_NODE=" + sourceNodeName(0, 0),
	}
	if got := spec.Devices[0].ContainerEdits.Env; !slices.Equal(got, env) {
		t.Errorf("env = %v, want %v", got, env)
	}
	if mounts := spec.Devices[0].ContainerEdits.Mounts; len(mounts) != 1 || mounts[0].HostPath != runtimeDir {
		t.Errorf("mounts = %+v, want one mount of %s", mounts, runtimeDir)
	}
}

// The graph has the playback node of the same PCM device but no
// capture node. A prepare that used the playback node would give the
// speakers to a pod that requested the microphone.
func TestPrepareRefusesASourceWhoseNodeIsGone(t *testing.T) {
	specDirectory(t)
	plugin := testPlugin(t, sourceClaim,
		outputGraph(map[pcmAddress]string{{Card: 0, PCM: 0}: sinkNodeName(0, 0)}))

	entry := prepare(t, plugin, "claim-1")
	if entry.Error == "" {
		t.Fatalf("prepare returned %+v for a source with no node, want a refusal", entry.Devices)
	}
	if want := "source " + testSourceName + " has no PipeWire node right now"; entry.Error != want {
		t.Errorf("error = %q, want %q", entry.Error, want)
	}
}

func TestPrepareRefusesWhatItCannotDeliver(t *testing.T) {
	cases := []struct {
		name      string
		claim     string
		uid       string
		graph     pwGraph
		wantError string
	}{
		{
			name:      "the sink has no node",
			claim:     allocatedClaim,
			uid:       "claim-1",
			wantError: "sink " + testSinkName + " has no PipeWire node right now",
		},
		{
			name:      "the claim has no allocation yet",
			claim:     `{"metadata": {"name": "kitchen", "namespace": "media", "uid": "claim-1"}}`,
			uid:       "claim-1",
			graph:     outputGraph(map[pcmAddress]string{{Card: 0, PCM: 3}: "alsa_output.test"}),
			wantError: "the claim has no allocation yet",
		},
		{
			name:      "the claim was recreated",
			claim:     allocatedClaim,
			uid:       "claim-2",
			graph:     outputGraph(map[pcmAddress]string{{Card: 0, PCM: 3}: "alsa_output.test"}),
			wantError: "the claim's UID changed",
		},
		{
			// The speaker is paired but switched off. Its device's taints
			// keep pods off it, but the prepare must still refuse.
			name:      "the speaker has no sink",
			claim:     speakerClaim,
			uid:       "claim-1",
			graph:     outputGraph(map[pcmAddress]string{{Card: 0, PCM: 3}: "alsa_output.test"}),
			wantError: "speaker " + testSpeakerName + " has no PipeWire sink right now",
		},
		{
			name: "the allocation names something this driver did not publish",
			claim: `{
			  "metadata": {"name": "kitchen", "namespace": "media", "uid": "claim-1"},
			  "status": {"allocation": {"devices": {"results": [
			    {"request": "speakers", "driver": "audio.liken.sh", "pool": "liken-1", "device": "card0-hdmi-a-1"}
			  ]}}}
			}`,
			uid:       "claim-1",
			graph:     outputGraph(map[pcmAddress]string{{Card: 0, PCM: 3}: "alsa_output.test"}),
			wantError: "does not name a device of this driver",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := specDirectory(t)
			plugin := testPlugin(t, c.claim, c.graph)

			entry := prepare(t, plugin, c.uid)
			if entry.Error == "" {
				t.Fatalf("prepare returned %+v, want a refusal", entry.Devices)
			}
			if !strings.Contains(entry.Error, c.wantError) {
				t.Errorf("error = %q, want it to contain %q", entry.Error, c.wantError)
			}
			if left := specFiles(t, dir); len(left) != 0 {
				t.Errorf("a refused claim left the spec files %v, want none", left)
			}
		})
	}
}

func specFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("listing the CDI directory: %v", err)
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// status.claims lists the claims that hold an endpoint, so the plugin
// records a claim at prepare and drops it at unprepare. The CDI file
// contains only the claim's UID, so this record is the only place the
// plugin keeps the claim's namespace and name.
func TestPreparedClaimsRecordWhoHoldsEachDevice(t *testing.T) {
	specDirectory(t)
	plugin := testPlugin(t, allocatedClaim, outputGraph(map[pcmAddress]string{
		{Card: 0, PCM: 3}: "alsa_output.pci-0000_00_1f.3.hdmi-stereo",
	}))
	plugin.claims = &preparedClaims{}

	if entry := prepare(t, plugin, "claim-1"); entry.Error != "" {
		t.Fatalf("preparing the claim: %s", entry.Error)
	}
	if got, want := plugin.claims.holders(testSinkName), []EndpointClaim{{Namespace: "media", Name: "kitchen"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("holders = %+v, want %+v", got, want)
	}
	if held := plugin.claims.holders("card0-hdmi-a-1"); len(held) != 0 {
		t.Error("this driver recorded another driver's device, want no holders for it")
	}

	if _, err := plugin.NodeUnprepareResources(context.Background(), &drav1.NodeUnprepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "kitchen", Uid: "claim-1"}},
	}); err != nil {
		t.Fatalf("unpreparing the claim: %v", err)
	}
	if held := plugin.claims.holders(testSinkName); len(held) != 0 {
		t.Errorf("after unprepare, the device is held by %+v, want no holders", held)
	}
}
