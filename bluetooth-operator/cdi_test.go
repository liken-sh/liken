package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// cdiTempDir points the CDI writer at a directory the test owns.
func cdiTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	previous := cdiDir
	cdiDir = dir
	t.Cleanup(func() { cdiDir = previous })
	return dir
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
	return spec
}

// prepareClaim writes the CDI spec file the DRA plugin writes when the
// kubelet prepares a claim on a device. The file's presence records
// that a consumer still holds the device, and the paths in it record
// which nodes that consumer received.
func prepareClaim(t *testing.T, claimUID, device string, nodes ...string) {
	t.Helper()
	spec := cdiSpec{Version: "0.6.0", Kind: cdiKind, Devices: []cdiDevice{{
		Name:           claimUID + "-" + device,
		ContainerEdits: cdiEdits{DeviceNodes: deviceNodes(nodes)},
	}}}
	raw, err := json.Marshal(&spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cdiDir, cdiPrefix+claimUID+".json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWriteAndRemoveCDISpec(t *testing.T) {
	dir := cdiTempDir(t)
	const uid = "0f8b1a2c-3d4e-5f60-8172-93a4b5c6d7e8"

	err := writeCDISpec(uid, []cdiDevice{{
		Name:           uid + "-a0-ab-51-33-b7-12",
		ContainerEdits: cdiEdits{DeviceNodes: deviceNodes([]string{"/dev/input/event5"})},
	}})
	if err != nil {
		t.Fatal(err)
	}

	// The file name starts with this driver's prefix, so liken's own specs
	// in the same directory never collide with these.
	path := filepath.Join(dir, "bluetooth.liken.sh-"+uid+".json")
	spec := readSpec(t, path)
	if spec.Kind != "bluetooth.liken.sh/controller" {
		t.Errorf("kind = %q", spec.Kind)
	}
	if spec.Devices[0].ContainerEdits.DeviceNodes[0].Path != "/dev/input/event5" {
		t.Errorf("node = %+v", spec.Devices[0].ContainerEdits.DeviceNodes)
	}

	if err := removeCDISpec(uid); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the spec file is still there")
	}
	// Unprepare must be idempotent: the kubelet repeats it whenever it
	// has no record that the call succeeded.
	if err := removeCDISpec(uid); err != nil {
		t.Fatalf("a repeated remove failed: %v", err)
	}
}

func TestClaimUIDFromSpecName(t *testing.T) {
	cases := []struct {
		name string
		uid  string
		ok   bool
	}{
		{name: "bluetooth.liken.sh-abc123.json", uid: "abc123", ok: true},
		{name: "liken.sh-abc123.json", ok: false},
		{name: "bluetooth.liken.sh-abc123.json.tmp", ok: false},
		{name: "vendor.example.com-abc123.json", ok: false},
	}
	for _, c := range cases {
		uid, ok := claimUIDFromSpecName(c.name)
		if ok != c.ok || (ok && uid != c.uid) {
			t.Errorf("claimUIDFromSpecName(%q) = %q, %v; want %q, %v", c.name, uid, ok, c.uid, c.ok)
		}
	}
}
