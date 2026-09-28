package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The lab card's endpoints, by the names a reconcile pass stamps on
// them. The HDMI slot's PCM device is 3 and the analog jack's is 0,
// which is what the graph fixtures key on, and the capture side of
// the analog jack is the source.
const (
	testSinkName   = "liken-1-pci-0000-00-1f-3-hdmi-0"
	testAnalogName = "liken-1-pci-0000-00-1f-3-alc236-analog"
	testSourceName = testAnalogName + captureSuffix
)

// testInventory is what a reconcile pass would have published for
// that card. The refresh and the prepare call both resolve a device
// name through it, because the name holds no card and no PCM number.
func testInventory() *endpointInventory {
	inventory := &endpointInventory{}
	inventory.publish([]alsaEndpoint{
		{Card: 0, PCM: 3, DeviceName: testSinkName},
		{Card: 0, PCM: 0, DeviceName: testAnalogName},
		{Card: 0, PCM: 0, Capture: true, DeviceName: testSourceName},
	})
	return inventory
}

// specDirectory points the CDI writes at a directory the test owns.
func specDirectory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cdiDir = dir
	t.Cleanup(func() { cdiDir = "/var/run/cdi" })
	return dir
}

func TestEndpointEdits(t *testing.T) {
	edits := endpointEdits("alsa_output.pci-0000_00_1f.3.hdmi-stereo")

	want := []string{
		"PIPEWIRE_REMOTE=/var/run/audio.liken.sh/pipewire-0",
		"PIPEWIRE_NODE=alsa_output.pci-0000_00_1f.3.hdmi-stereo",
	}
	if !slices.Equal(edits.Env, want) {
		t.Errorf("env = %v, want %v", edits.Env, want)
	}
	// A remote that starts with a slash is used as an absolute socket
	// path, so the mount and the variable name the same directory.
	if len(edits.Mounts) != 1 {
		t.Fatalf("mounts = %+v, want one", edits.Mounts)
	}
	mount := edits.Mounts[0]
	if mount.HostPath != runtimeDir || mount.ContainerPath != runtimeDir {
		t.Errorf("mount = %+v", mount)
	}
	if !slices.Contains(mount.Options, "ro") {
		t.Errorf("options = %v, want a read-only mount", mount.Options)
	}
	// No device node reaches the consumer. The operator holds every PCM
	// on the card, and a consumer connects to PipeWire instead.
	if len(edits.Env) != 2 {
		t.Errorf("env = %v", edits.Env)
	}
}

func TestClaimUIDFromSpecName(t *testing.T) {
	cases := []struct {
		name string
		uid  string
		ok   bool
	}{
		{name: "audio.liken.sh-abc-123.json", uid: "abc-123", ok: true},
		{name: "liken.sh-abc-123.json"},
		{name: "audio.liken.sh-abc-123.json.tmp"},
		{name: "something-else"},
	}
	for _, c := range cases {
		uid, ok := claimUIDFromSpecName(c.name)
		if ok != c.ok || uid != c.uid {
			t.Errorf("claimUIDFromSpecName(%q) = %q, %v", c.name, uid, ok)
		}
	}
}

func TestRefreshRewritesASinkThatCameBackUnderANewName(t *testing.T) {
	dir := specDirectory(t)
	if err := writeCDISpec("claim-1", []cdiDevice{{
		Name:           "claim-1-" + testSinkName,
		ContainerEdits: endpointEdits("alsa_output.old-name"),
	}}); err != nil {
		t.Fatal(err)
	}

	refreshCDISpecs(testInventory(),
		outputGraph(map[pcmAddress]string{{Card: 0, PCM: 3}: "alsa_output.new-name"}))

	spec := readSpec(t, filepath.Join(dir, "audio.liken.sh-claim-1.json"))
	want := "PIPEWIRE_NODE=alsa_output.new-name"
	if !slices.Contains(spec.Devices[0].ContainerEdits.Env, want) {
		t.Fatalf("env = %v, want %q", spec.Devices[0].ContainerEdits.Env, want)
	}
}

func TestRefreshKeepsTheNameWhenTheSinkIsGone(t *testing.T) {
	// An empty PIPEWIRE_NODE would start the next pod against
	// PipeWire's default sink with no error. The taints on the device
	// hold that pod back until the output can play again.
	dir := specDirectory(t)
	if err := writeCDISpec("claim-1", []cdiDevice{{
		Name:           "claim-1-" + testSinkName,
		ContainerEdits: endpointEdits("alsa_output.old-name"),
	}}); err != nil {
		t.Fatal(err)
	}

	refreshCDISpecs(testInventory(), outputGraph(map[pcmAddress]string{}))

	spec := readSpec(t, filepath.Join(dir, "audio.liken.sh-claim-1.json"))
	want := "PIPEWIRE_NODE=alsa_output.old-name"
	if !slices.Contains(spec.Devices[0].ContainerEdits.Env, want) {
		t.Fatalf("env = %v, want %q", spec.Devices[0].ContainerEdits.Env, want)
	}
}

// WirePlumber names a Bluetooth sink with the SPA object id on
// the end, and that id changes when the speaker reconnects, so a
// speaker's node renames more often than an output's does.
func TestRefreshRewritesASpeakersNodeAfterAReconnect(t *testing.T) {
	dir := specDirectory(t)
	if err := writeCDISpec("claim-1", []cdiDevice{{
		Name:           "claim-1-" + testSpeakerName,
		ContainerEdits: endpointEdits("bluez_output.A0_AB_51_33_B7_12.1"),
	}}); err != nil {
		t.Fatal(err)
	}

	refreshCDISpecs(testInventory(), speakerGraph(map[string]bluezSink{
		testSpeakerAddress: {Node: "bluez_output.A0_AB_51_33_B7_12.7", Codec: "sbc"},
	}))

	spec := readSpec(t, filepath.Join(dir, "audio.liken.sh-claim-1.json"))
	want := "PIPEWIRE_NODE=bluez_output.A0_AB_51_33_B7_12.7"
	if !slices.Contains(spec.Devices[0].ContainerEdits.Env, want) {
		t.Fatalf("env = %v, want %q", spec.Devices[0].ContainerEdits.Env, want)
	}
}

func TestRefreshLeavesAnotherDriversSpecAlone(t *testing.T) {
	dir := specDirectory(t)
	other := filepath.Join(dir, "liken.sh-claim-1.json")
	if err := os.WriteFile(other, []byte(`{"cdiVersion":"0.6.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	refreshCDISpecs(testInventory(),
		outputGraph(map[pcmAddress]string{{Card: 0, PCM: 3}: "alsa_output.new-name"}))

	raw, err := os.ReadFile(other)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"cdiVersion":"0.6.0"}` {
		t.Fatalf("liken's spec = %s", raw)
	}
}

func TestRemoveCDISpecIsIdempotent(t *testing.T) {
	// The kubelet repeats unprepare whenever it has no record that the
	// call succeeded.
	specDirectory(t)
	if err := writeCDISpec("claim-1", []cdiDevice{{Name: "claim-1-" + testSinkName}}); err != nil {
		t.Fatal(err)
	}
	if err := removeCDISpec("claim-1"); err != nil {
		t.Fatal(err)
	}
	if err := removeCDISpec("claim-1"); err != nil {
		t.Fatalf("a second unprepare failed: %v", err)
	}
}

func readSpec(t *testing.T, path string) cdiSpec {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Devices) != 1 {
		t.Fatalf("devices = %+v, want one", spec.Devices)
	}
	return spec
}
