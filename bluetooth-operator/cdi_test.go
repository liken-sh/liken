package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// cdiTempDir sets cdiDir to a temporary directory for the test.
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
		t.Fatalf("reading the spec file: %v", err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("decoding the spec file: %v", err)
	}
	return spec
}

// prepareClaim writes the spec file that prepare writes for a claim,
// so the code under test finds a consumer that holds the device
// through these nodes.
func prepareClaim(t *testing.T, claimUID, device string, nodes ...string) {
	t.Helper()
	spec := cdiSpec{Version: "0.6.0", Kind: cdiKind, Devices: []cdiDevice{{
		Name:           claimUID + "-" + device,
		ContainerEdits: cdiEdits{DeviceNodes: deviceNodes(nodes)},
	}}}
	raw, err := json.Marshal(&spec)
	if err != nil {
		t.Fatalf("encoding the spec file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cdiDir, cdiPrefix+claimUID+".json"), raw, 0o644); err != nil {
		t.Fatalf("writing the spec file: %v", err)
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
		t.Fatalf("writing the CDI spec: %v", err)
	}

	path := filepath.Join(dir, "bluetooth.liken.sh-"+uid+".json")
	spec := readSpec(t, path)
	if spec.Kind != "bluetooth.liken.sh/controller" {
		t.Errorf("the spec kind is %q, want bluetooth.liken.sh/controller", spec.Kind)
	}
	if spec.Devices[0].ContainerEdits.DeviceNodes[0].Path != "/dev/input/event5" {
		t.Errorf("the device nodes are %+v, want /dev/input/event5 first", spec.Devices[0].ContainerEdits.DeviceNodes)
	}

	if err := removeCDISpec(uid); err != nil {
		t.Fatalf("removing the CDI spec: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the spec file still exists after the remove, want it deleted")
	}
	if err := removeCDISpec(uid); err != nil {
		t.Fatalf("removing the CDI spec a second time: %v", err)
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
			t.Errorf("claimUIDFromSpecName(%q) = %q, %v, want %q, %v", c.name, uid, ok, c.uid, c.ok)
		}
	}
}

// The container runtime rejects a spec with a field outside the CDI
// schema, and then the consumer's pod cannot start.
func TestADeviceEntryCarriesOnlyTheSchemasFields(t *testing.T) {
	device := cdiDevice{
		Name:           "claim-controller",
		ContainerEdits: cdiEdits{DeviceNodes: deviceNodes([]string{"/dev/input/event8"})},
		Annotations:    cdiAnnotations([]string{"joystick", "key"}, "ABS_RX=4:"),
	}
	encoded, err := json.Marshal(device)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for field := range fields {
		switch field {
		case "name", "containerEdits", "annotations":
		default:
			t.Errorf("the device entry carries %q, which the CDI schema does not allow", field)
		}
	}
	if got := device.inputs(); len(got) != 2 || got[0] != "joystick" || got[1] != "key" {
		t.Errorf("inputs() = %v, want the two classes back", got)
	}
	if got := device.axes(); got != "ABS_RX=4:" {
		t.Errorf("axes() = %q, want the applied values back", got)
	}
}

func TestADeviceEntryCarriesNoAnnotationForTheDefault(t *testing.T) {
	device := cdiDevice{Name: "claim-controller", Annotations: cdiAnnotations(nil, "")}
	if device.Annotations != nil {
		t.Errorf("annotations = %v, want none", device.Annotations)
	}
	if got := device.inputs(); got != nil {
		t.Errorf("inputs() = %v, want none", got)
	}
	if got := device.axes(); got != "" {
		t.Errorf("axes() = %q, want none", got)
	}
}
