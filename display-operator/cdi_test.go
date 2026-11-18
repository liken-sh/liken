package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestOutputEditsSetTheSocketMountAndEnvironment(t *testing.T) {
	edits := outputEdits("/var/run/display.liken.sh", "wayland-0", "hdmi-a-1")

	want := []string{
		"XDG_RUNTIME_DIR=/var/run/display.liken.sh",
		"WAYLAND_DISPLAY=wayland-0",
		"DISPLAY_APP_ID=hdmi-a-1",
	}
	if !slices.Equal(edits.Env, want) {
		t.Errorf("env = %v, want %v", edits.Env, want)
	}
	if len(edits.Mounts) != 1 {
		t.Fatalf("the edits hold %d mounts, want 1: %+v", len(edits.Mounts), edits.Mounts)
	}
	mount := edits.Mounts[0]
	if mount.HostPath != "/var/run/display.liken.sh" || mount.ContainerPath != mount.HostPath {
		t.Errorf("the mount is %+v, want the socket directory at the same path in the container", mount)
	}
	if !slices.Contains(mount.Options, "rw") || !slices.Contains(mount.Options, "bind") {
		t.Errorf("the mount options are %v, want rw and bind", mount.Options)
	}
}

func TestControlEditsSetTheDeviceNodeAndItsPath(t *testing.T) {
	edits := controlEdits("/dev/i2c-4")

	if !slices.Equal(edits.Env, []string{"DISPLAY_CONTROL_BUS=/dev/i2c-4"}) {
		t.Errorf("env is %v, want DISPLAY_CONTROL_BUS=/dev/i2c-4", edits.Env)
	}
	if len(edits.DeviceNodes) != 1 {
		t.Fatalf("the edits hold %d device nodes, want 1: %+v", len(edits.DeviceNodes), edits.DeviceNodes)
	}
	node := edits.DeviceNodes[0]
	if node.Path != "/dev/i2c-4" || node.Permissions != "rw" {
		t.Errorf("the device node is %+v, want /dev/i2c-4 with rw", node)
	}
	if len(edits.Mounts) != 0 {
		t.Errorf("the edits hold mounts %+v, want none", edits.Mounts)
	}
}

// cdiDocument declares the CDI 0.6.0 fields apart from the operator's
// structs, so a JSON tag changed in the operator fails the test.
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

func TestWriteCDISpecWritesOutputAndControlDevicesToOneFile(t *testing.T) {
	cdiDir = t.TempDir()

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
		t.Fatalf("writing the CDI spec: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(cdiDir, "display.liken.sh-claim-uid.json"))
	if err != nil {
		t.Fatalf("reading the CDI spec file: %v", err)
	}
	var document cdiDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parsing the CDI spec: %v", err)
	}
	if document.CDIVersion != "0.6.0" || document.Kind != "display.liken.sh/output" {
		t.Errorf("the document is %+v, want cdiVersion 0.6.0 and kind display.liken.sh/output", document)
	}
	if len(document.Devices) != 2 {
		t.Fatalf("the document holds %d devices, want 2: %+v", len(document.Devices), document.Devices)
	}
	screen, control := document.Devices[0], document.Devices[1]
	if len(screen.ContainerEdits.Mounts) != 1 || len(screen.ContainerEdits.DeviceNodes) != 0 {
		t.Errorf("the screen's edits are %+v, want one mount and no device nodes", screen.ContainerEdits)
	}
	if control.Name != "claim-uid-hdmi-a-1-control" {
		t.Errorf("the control device is named %q, want claim-uid-hdmi-a-1-control", control.Name)
	}
	if len(control.ContainerEdits.DeviceNodes) != 1 {
		t.Fatalf("the control device's edits are %+v, want one device node", control.ContainerEdits)
	}
	node := control.ContainerEdits.DeviceNodes[0]
	if node.Path != "/dev/i2c-4" || node.Permissions != "rw" {
		t.Errorf("the device node is %+v, want /dev/i2c-4 with rw", node)
	}

	held, err := preparedDevices("claim-uid")
	if err != nil {
		t.Fatalf("reading the prepared devices: %v", err)
	}
	if !slices.Equal(held, []string{"hdmi-a-1", "hdmi-a-1-control"}) {
		t.Errorf("preparedDevices is %v, want hdmi-a-1 and hdmi-a-1-control", held)
	}
}

func TestWriteCDISpecWritesAFileTheRuntimeCanRead(t *testing.T) {
	cdiDir = t.TempDir()

	devices := []cdiDevice{{
		Name:           "claim-uid-hdmi-a-1",
		ContainerEdits: outputEdits("/var/run/display.liken.sh", "wayland-0", "hdmi-a-1"),
	}}
	if err := writeCDISpec("claim-uid", devices); err != nil {
		t.Fatalf("writing the CDI spec: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(cdiDir, "display.liken.sh-claim-uid.json"))
	if err != nil {
		t.Fatalf("reading the CDI spec file: %v", err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parsing the CDI spec: %v", err)
	}
	if spec.Kind != "display.liken.sh/output" || spec.Version != "0.6.0" {
		t.Errorf("the spec is %+v, want version 0.6.0 and kind display.liken.sh/output", spec)
	}
	if len(spec.Devices) != 1 || spec.Devices[0].Name != "claim-uid-hdmi-a-1" {
		t.Fatalf("the spec holds devices %+v, want claim-uid-hdmi-a-1 alone", spec.Devices)
	}
	if len(spec.Devices[0].ContainerEdits.Env) != 3 || len(spec.Devices[0].ContainerEdits.Mounts) != 1 {
		t.Errorf("the edits are %+v, want 3 environment variables and 1 mount", spec.Devices[0].ContainerEdits)
	}

	// The temporary file was renamed into place.
	entries, err := os.ReadDir(cdiDir)
	if err != nil {
		t.Fatalf("writing the CDI spec: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("the directory holds %d files, want only the spec", len(entries))
	}
}

func TestRemoveCDISpecIsIdempotent(t *testing.T) {
	cdiDir = t.TempDir()

	if err := writeCDISpec("claim-uid", nil); err != nil {
		t.Fatalf("listing the CDI directory: %v", err)
	}
	if err := removeCDISpec("claim-uid"); err != nil {
		t.Fatalf("writing the first CDI spec: %v", err)
	}
	if err := removeCDISpec("claim-uid"); err != nil {
		t.Fatalf("removing a spec that is already gone: %v", err)
	}
}

func TestPreparedOutputsReturnsTheOutputsClaimsHold(t *testing.T) {
	restoreCDIDir := cdiDir
	t.Cleanup(func() { cdiDir = restoreCDIDir })
	cdiDir = t.TempDir()

	if err := writeCDISpec("film", []cdiDevice{{
		Name:           "film-hdmi-a-1",
		ContainerEdits: outputEdits("/var/run/display.liken.sh", "wayland-0", "hdmi-a-1"),
	}}); err != nil {
		t.Fatalf("removing the CDI spec: %v", err)
	}
	// Draw and control devices do not set the screen's mode.
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
		t.Fatalf("writing the film's CDI spec: %v", err)
	}

	held, err := preparedOutputs()
	if err != nil {
		t.Fatalf("writing the idle CDI spec: %v", err)
	}

	if len(held) != 1 || !held["hdmi-a-1"] {
		t.Errorf("the prepared outputs are %v, want the one screen the film holds", held)
	}
}

// A node where the kubelet has prepared nothing has no spec directory.
func TestPreparedOutputsReadsANodeWithNoClaims(t *testing.T) {
	restoreCDIDir := cdiDir
	t.Cleanup(func() { cdiDir = restoreCDIDir })
	cdiDir = filepath.Join(t.TempDir(), "never-written")

	held, err := preparedOutputs()
	if err != nil {
		t.Fatalf("reading the prepared outputs: %v", err)
	}
	if len(held) != 0 {
		t.Errorf("the prepared outputs are %v, want none", held)
	}
}
