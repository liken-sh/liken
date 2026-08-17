package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestOutputEditsDeliverTheSocketAndTheAppID(t *testing.T) {
	edits := outputEdits("/var/run/display.liken.sh", "wayland-0", "hdmi-a-1")

	// A Wayland client resolves the socket from XDG_RUNTIME_DIR and
	// WAYLAND_DISPLAY, and it passes DISPLAY_APP_ID to its own toolkit
	// with a flag the pod spec carries.
	want := []string{
		"XDG_RUNTIME_DIR=/var/run/display.liken.sh",
		"WAYLAND_DISPLAY=wayland-0",
		"DISPLAY_APP_ID=hdmi-a-1",
	}
	if !slices.Equal(edits.Env, want) {
		t.Errorf("env = %v, want %v", edits.Env, want)
	}
	if len(edits.Mounts) != 1 {
		t.Fatalf("mounts = %+v", edits.Mounts)
	}
	mount := edits.Mounts[0]
	// One path on both ends. The operator's own pod mounts the socket
	// directory from the host at the path it names here, so it never
	// has to read its own mount table.
	if mount.HostPath != "/var/run/display.liken.sh" || mount.ContainerPath != mount.HostPath {
		t.Errorf("mount = %+v", mount)
	}
	// rw, because connecting to a Unix socket needs write permission
	// on it.
	if !slices.Contains(mount.Options, "rw") || !slices.Contains(mount.Options, "bind") {
		t.Errorf("options = %v", mount.Options)
	}
}

func TestWriteCDISpecLeavesTheFileTheRuntimeReads(t *testing.T) {
	cdiDir = t.TempDir()

	devices := []cdiDevice{{
		Name:           "claim-uid-hdmi-a-1",
		ContainerEdits: outputEdits("/var/run/display.liken.sh", "wayland-0", "hdmi-a-1"),
	}}
	if err := writeCDISpec("claim-uid", devices); err != nil {
		t.Fatal(err)
	}

	// The file name carries this driver's prefix, so liken's specs in
	// the same directory and this driver's specs never collide.
	raw, err := os.ReadFile(filepath.Join(cdiDir, "display.liken.sh-claim-uid.json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.Kind != "display.liken.sh/output" || spec.Version != "0.6.0" {
		t.Errorf("spec = %+v", spec)
	}
	if len(spec.Devices) != 1 || spec.Devices[0].Name != "claim-uid-hdmi-a-1" {
		t.Fatalf("devices = %+v", spec.Devices)
	}
	// The delivery is a mount and environment variables. A Wayland
	// client receives no device node at all.
	if len(spec.Devices[0].ContainerEdits.Env) != 3 || len(spec.Devices[0].ContainerEdits.Mounts) != 1 {
		t.Errorf("edits = %+v", spec.Devices[0].ContainerEdits)
	}

	// No temporary file is left behind. The runtime lists this
	// directory, and a spec it reads mid-write fails a container
	// creation.
	entries, err := os.ReadDir(cdiDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("the directory holds %d files", len(entries))
	}
}

func TestRemoveCDISpecIsIdempotent(t *testing.T) {
	cdiDir = t.TempDir()

	if err := writeCDISpec("claim-uid", nil); err != nil {
		t.Fatal(err)
	}
	if err := removeCDISpec("claim-uid"); err != nil {
		t.Fatal(err)
	}
	// The kubelet repeats unprepare whenever it is not sure the call
	// succeeded.
	if err := removeCDISpec("claim-uid"); err != nil {
		t.Fatalf("removing an absent spec: %v", err)
	}
}
