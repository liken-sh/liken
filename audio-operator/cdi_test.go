package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// specDirectory points the CDI writes at a directory the test owns.
func specDirectory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cdiDir = dir
	t.Cleanup(func() { cdiDir = "/var/run/cdi" })
	return dir
}

func TestOutputEdits(t *testing.T) {
	edits := outputEdits("alsa_output.pci-0000_00_1f.3.hdmi-stereo")

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
		Name:           "claim-1-card0-pcm3",
		ContainerEdits: outputEdits("alsa_output.old-name"),
	}}); err != nil {
		t.Fatal(err)
	}

	refreshCDISpecs(map[pcmAddress]string{{Card: 0, PCM: 3}: "alsa_output.new-name"})

	spec := readSpec(t, filepath.Join(dir, "audio.liken.sh-claim-1.json"))
	want := "PIPEWIRE_NODE=alsa_output.new-name"
	if !slices.Contains(spec.Devices[0].ContainerEdits.Env, want) {
		t.Fatalf("env = %v, want %q", spec.Devices[0].ContainerEdits.Env, want)
	}
}

func TestRefreshKeepsTheNameWhenTheSinkIsGone(t *testing.T) {
	// An empty PIPEWIRE_NODE would start the next pod against
	// PipeWire's default sink with no error, while the taints on the
	// device hold that pod back until the output can play again.
	dir := specDirectory(t)
	if err := writeCDISpec("claim-1", []cdiDevice{{
		Name:           "claim-1-card0-pcm3",
		ContainerEdits: outputEdits("alsa_output.old-name"),
	}}); err != nil {
		t.Fatal(err)
	}

	refreshCDISpecs(map[pcmAddress]string{})

	spec := readSpec(t, filepath.Join(dir, "audio.liken.sh-claim-1.json"))
	want := "PIPEWIRE_NODE=alsa_output.old-name"
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

	refreshCDISpecs(map[pcmAddress]string{{Card: 0, PCM: 3}: "alsa_output.new-name"})

	raw, err := os.ReadFile(other)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"cdiVersion":"0.6.0"}` {
		t.Fatalf("liken's spec = %s", raw)
	}
}

func TestRemoveCDISpecIsIdempotent(t *testing.T) {
	// The kubelet repeats unprepare whenever it is not sure the call
	// succeeded.
	specDirectory(t)
	if err := writeCDISpec("claim-1", []cdiDevice{{Name: "claim-1-card0-pcm3"}}); err != nil {
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
