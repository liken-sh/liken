package machine

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// A proven manifest written by a newer release. It has storage that
// this release supports and fields that it does not.
const newerManifest = `apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: node-1
spec:
  storage:
    machineState:
      size: 1Gi
  futureDevices:
    - name: adapter
  network:
    hostEntries:
      - address: 10.0.0.1
        names: [nas]
        aliasOf: nas.local
  nodeLabels:
    zone: east
  sysctls:
    net.ipv4.ip_forward: "1"
`

func TestParseKnownSkipsTheFieldsThisReleaseDoesNotHave(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		ignored []string
	}{
		{name: "a manifest with only fields this release has", raw: "kind: Machine\nmetadata:\n  name: node-1\n"},
		{name: "a field a newer release added", raw: newerManifest, ignored: []string{"spec.futureDevices", "spec.network.hostEntries[0].aliasOf"}},
		{name: "a field nested in a known object", raw: `kind: Machine
spec:
  storage:
    machineState:
      size: 1Gi
      encryption: tpm
`, ignored: []string{"spec.storage.machineState.encryption"}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			m, ignored, err := ParseKnown([]byte(one.raw))

			if err != nil {
				t.Fatalf("parsing the manifest: %v", err)
			}
			if m.Kind != "Machine" {
				t.Errorf("the kind is %q, want Machine", m.Kind)
			}
			if !slices.Equal(ignored, one.ignored) {
				t.Errorf("the ignored fields are %v, want %v", ignored, one.ignored)
			}
		})
	}
}

func TestParseKnownKeepsTheFieldsThisReleaseHas(t *testing.T) {
	m, _, err := ParseKnown([]byte(newerManifest))
	if err != nil {
		t.Fatalf("parsing the newer manifest: %v", err)
	}

	if m.Metadata.Name != "node-1" || m.Spec.Storage.MachineState == nil ||
		m.Spec.Storage.MachineState.Size != "1Gi" || m.Spec.NodeLabels["zone"] != "east" {
		t.Errorf("the spec is %+v, want the known fields node-1, a 1Gi machineState, and the zone label", m.Spec)
	}
}

func TestParseKnownRefusesAManifestThatIsWrong(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{name: "a known field of the wrong type", raw: "kind: Machine\nspec:\n  nodeLabels: [a, b]\n"},
		{name: "another kind", raw: "kind: Cluster\n"},
		{name: "not YAML", raw: "kind: [Machine\n"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if _, _, err := ParseKnown([]byte(one.raw)); err == nil {
				t.Error("the manifest parsed, want an error")
			}
		})
	}
}

func TestLoadKnownReadsAProvedManifestFile(t *testing.T) {
	dir := t.TempDir()
	newer := filepath.Join(dir, "newer.yaml")
	broken := filepath.Join(dir, "broken.yaml")
	if err := os.WriteFile(newer, []byte(newerManifest), 0o644); err != nil {
		t.Fatalf("writing newer.yaml: %v", err)
	}
	if err := os.WriteFile(broken, []byte("kind: Cluster\nspec:\n  futureDevices: [a]\n"), 0o644); err != nil {
		t.Fatalf("writing broken.yaml: %v", err)
	}

	m, ignored, err := LoadKnown(newer)
	if err != nil || m.Metadata.Name != "node-1" || len(ignored) != 2 {
		t.Errorf("loading the newer manifest gave %v, %v, %v, want node-1, two ignored fields, and no error", m, ignored, err)
	}
	if m, ignored, err := LoadKnown(filepath.Join(dir, "absent.yaml")); err != nil || m == nil || ignored != nil {
		t.Errorf("loading an absent file gave %v, %v, %v, want a machine with every field at its default", m, ignored, err)
	}
	if _, _, err := LoadKnown(broken); err == nil || !strings.Contains(err.Error(), "broken.yaml") {
		t.Errorf("loading the broken manifest gave %v, want an error that names broken.yaml", err)
	}
	if _, _, err := LoadKnown(dir); err == nil {
		t.Error("loading a directory returned no error, want one")
	}
}

// The walk matches names as encoding/json does: ignoring case,
// including fields promoted from an embedded struct, and never a
// field tagged "-". It skips a type that decodes its own JSON.
type walkedBase struct {
	Shared string `json:"shared"`
}

type walked struct {
	walkedBase
	Hidden string    `json:"-"`
	Plain  string    // no tag: the Go name
	When   time.Time `json:"when"`
}

func TestTheWalkMatchesNamesTheWayJSONDoes(t *testing.T) {
	doc := map[string]any{
		"shared": "promoted",
		"SHARED": "any case",
		"plain":  "the Go name",
		"when":   map[string]any{"anything": true},
		"Hidden": "skipped by its tag",
	}

	unknown := unknownFields(reflect.TypeFor[walked](), doc, "")

	if !slices.Equal(unknown, []string{"Hidden"}) {
		t.Errorf("unknown = %v, want only the field tagged -", unknown)
	}
}
