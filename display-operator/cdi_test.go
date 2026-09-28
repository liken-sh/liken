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

func TestControlEditsDeliverTheNodeAndItsPath(t *testing.T) {
	edits := controlEdits("/dev/i2c-4")

	// The node is the DDC/CI wire, and the variable is the path,
	// because the kernel numbers i2c adapters in the order it
	// registers them and a consumer cannot guess the number.
	if !slices.Equal(edits.Env, []string{"DISPLAY_CONTROL_BUS=/dev/i2c-4"}) {
		t.Errorf("env = %v", edits.Env)
	}
	if len(edits.DeviceNodes) != 1 {
		t.Fatalf("deviceNodes = %+v", edits.DeviceNodes)
	}
	node := edits.DeviceNodes[0]
	// rw, because every DDC/CI exchange writes a request before it
	// reads the reply.
	if node.Path != "/dev/i2c-4" || node.Permissions != "rw" {
		t.Errorf("deviceNodes[0] = %+v", node)
	}
	// A control device grants the wire and nothing else. The socket
	// belongs to a claim on the output.
	if len(edits.Mounts) != 0 {
		t.Errorf("mounts = %+v", edits.Mounts)
	}
}

// cdiDocument is the CDI 0.6.0 schema's own spelling of the fields
// this driver writes. It is declared apart from the operator's own
// structs so that a field renamed on one side fails here instead of
// agreeing with itself.
type cdiDocument struct {
	CDIVersion string `json:"cdiVersion"`
	Kind       string `json:"kind"`
	Devices    []struct {
		Name           string `json:"name"`
		ContainerEdits struct {
			Env         []string `json:"env"`
			DeviceNodes []struct {
				Path        string `json:"path"`
				Permissions string `json:"permissions"`
			} `json:"deviceNodes"`
			Mounts []struct {
				HostPath      string `json:"hostPath"`
				ContainerPath string `json:"containerPath"`
			} `json:"mounts"`
		} `json:"containerEdits"`
	} `json:"devices"`
}

func TestWriteCDISpecCarriesBothDeliveriesUnderOneKind(t *testing.T) {
	cdiDir = t.TempDir()

	// A claim that holds a screen and that screen's control channel
	// gets one file, because a spec file states one kind and one file
	// is one thing for an unprepare to remove.
	devices := []cdiDevice{
		{
			Name:           "claim-uid-hdmi-a-1",
			ContainerEdits: outputEdits("/var/run/display.liken.sh", "wayland-0", "hdmi-a-1"),
		},
		{
			Name:           "claim-uid-hdmi-a-1-control",
			ContainerEdits: controlEdits("/dev/i2c-4"),
		},
	}
	if err := writeCDISpec("claim-uid", devices); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(cdiDir, "display.liken.sh-claim-uid.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document cdiDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if document.CDIVersion != "0.6.0" || document.Kind != "display.liken.sh/output" {
		t.Errorf("document = %+v", document)
	}
	if len(document.Devices) != 2 {
		t.Fatalf("devices = %+v", document.Devices)
	}
	screen, control := document.Devices[0], document.Devices[1]
	if len(screen.ContainerEdits.Mounts) != 1 || len(screen.ContainerEdits.DeviceNodes) != 0 {
		t.Errorf("the screen's edits = %+v", screen.ContainerEdits)
	}
	if control.Name != "claim-uid-hdmi-a-1-control" {
		t.Errorf("the control device is named %q", control.Name)
	}
	if len(control.ContainerEdits.DeviceNodes) != 1 {
		t.Fatalf("the control device's edits = %+v", control.ContainerEdits)
	}
	node := control.ContainerEdits.DeviceNodes[0]
	if node.Path != "/dev/i2c-4" || node.Permissions != "rw" {
		t.Errorf("deviceNodes[0] = %+v", node)
	}

	// Unprepare names a claim's UID and nothing else, so the file is
	// what says which devices the claim held.
	held, err := preparedDevices("claim-uid")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(held, []string{"hdmi-a-1", "hdmi-a-1-control"}) {
		t.Errorf("preparedDevices = %v", held)
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
	// The kubelet repeats unprepare whenever it has no record that
	// the call succeeded.
	if err := removeCDISpec("claim-uid"); err != nil {
		t.Fatalf("removing an absent spec: %v", err)
	}
}

// What a resting mode and a compositor restart wait on. Both
// take the screen away from whatever draws on it, and the specs this
// driver wrote are where the operator reads which screens a workload
// holds.
func TestPreparedOutputsNamesTheScreensClaimsHold(t *testing.T) {
	restoreCDIDir := cdiDir
	t.Cleanup(func() { cdiDir = restoreCDIDir })
	cdiDir = t.TempDir()

	if err := writeCDISpec("film", []cdiDevice{{
		Name:           "film-hdmi-a-1",
		ContainerEdits: outputEdits("/var/run/display.liken.sh", "wayland-0", "hdmi-a-1"),
	}}); err != nil {
		t.Fatal(err)
	}
	// The idle client's own claim: a shared draw device and the
	// panel's control wire, and neither owns a screen's mode.
	if err := writeCDISpec("idle", []cdiDevice{
		{
			Name:           "idle-hdmi-a-2-draw",
			ContainerEdits: outputEdits("/var/run/display.liken.sh", "wayland-0", "hdmi-a-2"),
		},
		{
			Name:           "idle-hdmi-a-2-control",
			ContainerEdits: controlEdits("/dev/i2c-4"),
		},
	}); err != nil {
		t.Fatal(err)
	}

	held, err := preparedOutputs()
	if err != nil {
		t.Fatal(err)
	}

	if len(held) != 1 || !held["hdmi-a-1"] {
		t.Errorf("the prepared outputs are %v, want the one screen the film holds", held)
	}
}

// A node where the kubelet has prepared nothing has no
// directory at all, and that is no failure.
func TestPreparedOutputsReadsANodeWithNoClaims(t *testing.T) {
	restoreCDIDir := cdiDir
	t.Cleanup(func() { cdiDir = restoreCDIDir })
	cdiDir = filepath.Join(t.TempDir(), "never-written")

	held, err := preparedOutputs()
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 0 {
		t.Errorf("the prepared outputs are %v, want none", held)
	}
}
