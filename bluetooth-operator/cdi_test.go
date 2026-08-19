package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

func TestRefreshCDISpecsFollowsAMovedNode(t *testing.T) {
	dir := cdiTempDir(t)
	const uid = "0f8b1a2c-3d4e-5f60-8172-93a4b5c6d7e8"
	name := uid + "-a0-ab-51-33-b7-12"

	if err := writeCDISpec(uid, []cdiDevice{{
		Name:           name,
		ContainerEdits: cdiEdits{DeviceNodes: deviceNodes([]string{"/dev/input/event5"})},
	}}); err != nil {
		t.Fatal(err)
	}
	// liken's spec for another claim is in the same directory. The
	// refresh must not read or rewrite it.
	likenSpec := filepath.Join(dir, "liken.sh-"+uid+".json")
	if err := os.WriteFile(likenSpec, []byte(`{"cdiVersion":"0.6.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// The controller reconnected on a different event number.
	refreshCDISpecs(map[string][]string{"a0:ab:51:33:b7:12": {"/dev/input/event9"}})

	spec := readSpec(t, filepath.Join(dir, "bluetooth.liken.sh-"+uid+".json"))
	if spec.Devices[0].ContainerEdits.DeviceNodes[0].Path != "/dev/input/event9" {
		t.Errorf("node = %+v", spec.Devices[0].ContainerEdits.DeviceNodes)
	}
	raw, err := os.ReadFile(likenSpec)
	if err != nil || string(raw) != `{"cdiVersion":"0.6.0"}` {
		t.Errorf("liken's spec changed: %q, %v", raw, err)
	}
}

// The bus delivers a fixed socket path, so nothing about it can move
// and the refresh must leave it exactly as prepare wrote it. Its name
// is not a MAC either, so a refresh that read it would look up a key
// that names nothing.
func TestRefreshCDISpecsLeavesTheMediaBusAlone(t *testing.T) {
	cdiTempDir(t)
	const uid = "0f8b1a2c-3d4e-5f60-8172-93a4b5c6d7e8"

	if err := writeCDISpec(uid, []cdiDevice{{
		Name:           uid + "-" + testBus,
		ContainerEdits: busEdits(),
	}}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(cdiSpecPath(uid))
	if err != nil {
		t.Fatal(err)
	}

	// A controller reconnected on a different event number, which is the
	// pass that rewrites a controller's spec.
	refreshCDISpecs(map[string][]string{"a0:ab:51:33:b7:12": {"/dev/input/event9"}})

	after, err := os.ReadFile(cdiSpecPath(uid))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("the spec changed:\n%s\n%s", before, after)
	}
}

// A moved controller in a mixed spec rewrites the file, and the bus
// entry beside it comes through the rewrite unchanged.
func TestRefreshCDISpecsKeepsTheBusThroughAControllersMove(t *testing.T) {
	cdiTempDir(t)
	const uid = "0f8b1a2c-3d4e-5f60-8172-93a4b5c6d7e8"

	if err := writeCDISpec(uid, []cdiDevice{
		{
			Name:           uid + "-a0-ab-51-33-b7-12",
			ContainerEdits: cdiEdits{DeviceNodes: deviceNodes([]string{"/dev/input/event5"})},
		},
		{Name: uid + "-" + testBus, ContainerEdits: busEdits()},
	}); err != nil {
		t.Fatal(err)
	}

	refreshCDISpecs(map[string][]string{"a0:ab:51:33:b7:12": {"/dev/input/event9"}})

	spec := readSpec(t, cdiSpecPath(uid))
	if spec.Devices[0].ContainerEdits.DeviceNodes[0].Path != "/dev/input/event9" {
		t.Errorf("the controller's node = %+v", spec.Devices[0].ContainerEdits.DeviceNodes)
	}
	if !reflect.DeepEqual(spec.Devices[1].ContainerEdits, busEdits()) {
		t.Errorf("the bus's edits = %+v", spec.Devices[1].ContainerEdits)
	}
}

func TestRefreshCDISpecsKeepsNodesOfAControllerThatLeft(t *testing.T) {
	cdiTempDir(t)
	const uid = "0f8b1a2c-3d4e-5f60-8172-93a4b5c6d7e8"

	if err := writeCDISpec(uid, []cdiDevice{{
		Name:           uid + "-a0-ab-51-33-b7-12",
		ContainerEdits: cdiEdits{DeviceNodes: deviceNodes([]string{"/dev/input/event5"})},
	}}); err != nil {
		t.Fatal(err)
	}

	// The controller is off the air, so it registers no node at all.
	// An empty edit list would start the next pod with no device and
	// no error.
	refreshCDISpecs(map[string][]string{})

	spec := readSpec(t, cdiSpecPath(uid))
	if spec.Devices[0].ContainerEdits.DeviceNodes[0].Path != "/dev/input/event5" {
		t.Errorf("node = %+v", spec.Devices[0].ContainerEdits.DeviceNodes)
	}
}
