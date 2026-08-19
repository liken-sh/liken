package main

// The prepare path, end to end: the kubelet's call, the claim the
// driver reads back, the CDI file it leaves for the runtime, and the
// unprepare that removes it.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"
)

// allocatedClaim is what the API server holds for a claim that the
// scheduler allocated. The second result belongs to another driver,
// which is what a claim that pairs a screen with its speakers looks
// like. This driver must leave that result to the other driver's own
// plugin.
const allocatedClaim = `{
  "metadata": {"name": "kitchen", "namespace": "media", "uid": "claim-1"},
  "status": {"allocation": {"devices": {"results": [
    {"request": "speakers", "driver": "audio.liken.sh", "pool": "liken-1", "device": "card0-pcm3"},
    {"request": "screen", "driver": "display.liken.sh", "pool": "liken-1", "device": "card0-hdmi-a-1"}
  ]}}}
}`

// A claim on one Bluetooth speaker. The device name is the peer
// MAC, and the driver is the same one that publishes the card's
// outputs, so the delivery is the same delivery.
const speakerClaim = `{
  "metadata": {"name": "kitchen", "namespace": "media", "uid": "claim-1"},
  "status": {"allocation": {"devices": {"results": [
    {"request": "speaker", "driver": "audio.liken.sh", "pool": "liken-1", "device": "a0-ab-51-33-b7-12"}
  ]}}}
}`

// testPlugin builds a DRA plugin over an API server that holds one
// claim, and over a fixed graph.
func testPlugin(t *testing.T, claim string, graph pwGraph) *draPlugin {
	t.Helper()
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(claim))
	}))
	return &draPlugin{client: client, graph: staticGraph(graph)}
}

func prepare(t *testing.T, plugin *draPlugin, uid string) *drav1.NodePrepareResourceResponse {
	t.Helper()
	resp, err := plugin.NodePrepareResources(context.Background(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "kitchen", Uid: uid}},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry, answered := resp.Claims[uid]
	if !answered {
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
		t.Fatalf("prepare failed: %s", entry.Error)
	}
	// One device, because the other result belongs to another driver.
	if len(entry.Devices) != 1 {
		t.Fatalf("devices = %+v, want one", entry.Devices)
	}
	device := entry.Devices[0]
	if device.DeviceName != "card0-pcm3" || device.PoolName != "liken-1" {
		t.Errorf("device = %+v", device)
	}
	if !slices.Equal(device.RequestNames, []string{"speakers"}) {
		t.Errorf("requests = %v", device.RequestNames)
	}
	want := []string{"audio.liken.sh/output=claim-1-card0-pcm3"}
	if !slices.Equal(device.CdiDeviceIds, want) {
		t.Errorf("CDI ids = %v, want %v", device.CdiDeviceIds, want)
	}

	// The runtime reads the file, so the delivery is what the file
	// says and not what the response says.
	spec := readSpec(t, filepath.Join(dir, "audio.liken.sh-claim-1.json"))
	if spec.Kind != cdiKind {
		t.Errorf("kind = %q", spec.Kind)
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
		t.Errorf("mounts = %+v", edits.Mounts)
	}

	// Unprepare takes the file back, and repeats of it succeed,
	// because the kubelet repeats the call whenever it has no record
	// that it succeeded.
	for range 2 {
		resp, err := plugin.NodeUnprepareResources(context.Background(), &drav1.NodeUnprepareResourcesRequest{
			Claims: []*drav1.Claim{{Namespace: "media", Name: "kitchen", Uid: "claim-1"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if message := resp.Claims["claim-1"].Error; message != "" {
			t.Fatalf("unprepare failed: %s", message)
		}
	}
	if left := specFiles(t, dir); len(left) != 0 {
		t.Fatalf("unprepare left %v behind", left)
	}
}

// A Bluetooth speaker receives what an HDMI output receives.
// The node name is the one WirePlumber's bluez monitor built, and
// nothing else in the delivery changes.
func TestPrepareDeliversASpeakersNodeName(t *testing.T) {
	dir := specDirectory(t)
	plugin := testPlugin(t, speakerClaim, speakerGraph(map[string]bluezSink{
		testSpeakerAddress: {Node: testSpeakerNode, Codec: "sbc"},
	}))

	entry := prepare(t, plugin, "claim-1")
	if entry.Error != "" {
		t.Fatalf("prepare failed: %s", entry.Error)
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
		t.Errorf("mounts = %+v", mounts)
	}
}

func TestPrepareRefusesWhatItCannotDeliver(t *testing.T) {
	cases := []struct {
		name  string
		claim string
		uid   string
		graph pwGraph
		says  string
	}{
		{
			// The pod waits in ContainerCreating, and the device's
			// taints are what the scheduler and the eviction controller
			// act on.
			name:  "the output has no sink",
			claim: allocatedClaim,
			uid:   "claim-1",
			says:  "output card0-pcm3 has no PipeWire sink right now",
		},
		{
			name:  "the claim has no allocation yet",
			claim: `{"metadata": {"name": "kitchen", "namespace": "media", "uid": "claim-1"}}`,
			uid:   "claim-1",
			graph: outputGraph(map[pcmAddress]string{{Card: 0, PCM: 3}: "alsa_output.test"}),
			says:  "the claim has no allocation yet",
		},
		{
			// The named claim was deleted and recreated after the
			// kubelet asked, so whatever it holds is not the grant this
			// pod was scheduled against.
			name:  "the claim was recreated",
			claim: allocatedClaim,
			uid:   "claim-2",
			graph: outputGraph(map[pcmAddress]string{{Card: 0, PCM: 3}: "alsa_output.test"}),
			says:  "the claim's UID changed",
		},
		{
			// The speaker is paired and switched off, so the slice
			// carries its two taints and no pod should reach this call.
			name:  "the speaker has no sink",
			claim: speakerClaim,
			uid:   "claim-1",
			graph: outputGraph(map[pcmAddress]string{{Card: 0, PCM: 3}: "alsa_output.test"}),
			says:  "speaker " + testSpeakerName + " has no PipeWire sink right now",
		},
		{
			name: "the allocation names something this driver did not publish",
			claim: `{
			  "metadata": {"name": "kitchen", "namespace": "media", "uid": "claim-1"},
			  "status": {"allocation": {"devices": {"results": [
			    {"request": "speakers", "driver": "audio.liken.sh", "pool": "liken-1", "device": "card0-hdmi-a-1"}
			  ]}}}
			}`,
			uid:   "claim-1",
			graph: outputGraph(map[pcmAddress]string{{Card: 0, PCM: 3}: "alsa_output.test"}),
			says:  "does not name a device of this driver",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := specDirectory(t)
			plugin := testPlugin(t, c.claim, c.graph)

			entry := prepare(t, plugin, c.uid)
			if entry.Error == "" {
				t.Fatalf("prepare accepted a claim it cannot deliver: %+v", entry.Devices)
			}
			if !strings.Contains(entry.Error, c.says) {
				t.Errorf("error = %q, want it to say %q", entry.Error, c.says)
			}
			// A refusal writes no file, so no later pod receives a
			// delivery from a claim that was never prepared.
			if left := specFiles(t, dir); len(left) != 0 {
				t.Errorf("a refused claim left %v behind", left)
			}
		})
	}
}

// specFiles lists what the CDI directory holds.
func specFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
