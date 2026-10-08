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

// A proven manifest that a newer release wrote: storage this release
// knows, and fields it does not.
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
    zone: den
  sysctls:
    net.ipv4.ip_forward: "1"
`

func TestParseKnownSkipsTheFieldsThisReleaseDoesNotKnow(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		ignored []string
	}{
		{name: "a manifest this release knows", raw: "kind: Machine\nmetadata:\n  name: node-1\n"},
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
				t.Fatalf("ParseKnown: %v", err)
			}
			if m.Kind != "Machine" {
				t.Errorf("parsed kind %q", m.Kind)
			}
			if !slices.Equal(ignored, one.ignored) {
				t.Errorf("ignored %v, want %v", ignored, one.ignored)
			}
		})
	}
}

// The fields this release knows survive the parse.
func TestParseKnownKeepsTheFieldsItKnows(t *testing.T) {
	m, _, err := ParseKnown([]byte(newerManifest))
	if err != nil {
		t.Fatal(err)
	}

	if m.Metadata.Name != "node-1" || m.Spec.Storage.MachineState == nil ||
		m.Spec.Storage.MachineState.Size != "1Gi" || m.Spec.NodeLabels["zone"] != "den" {
		t.Errorf("the known fields did not survive: %+v", m.Spec)
	}
}

// A manifest that is wrong, not newer, still fails.
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
				t.Error("parsed, want an error")
			}
		})
	}
}

func TestLoadKnownReadsAProvedManifestFile(t *testing.T) {
	dir := t.TempDir()
	newer := filepath.Join(dir, "newer.yaml")
	broken := filepath.Join(dir, "broken.yaml")
	if err := os.WriteFile(newer, []byte(newerManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, []byte("kind: Cluster\nspec:\n  futureDevices: [a]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, ignored, err := LoadKnown(newer)
	if err != nil || m.Metadata.Name != "node-1" || len(ignored) != 2 {
		t.Errorf("newer: %v, %v, %v", m, ignored, err)
	}
	if m, ignored, err := LoadKnown(filepath.Join(dir, "absent.yaml")); err != nil || m == nil || ignored != nil {
		t.Errorf("absent: %v, %v, %v; want a machine with every field at its default", m, ignored, err)
	}
	if _, _, err := LoadKnown(broken); err == nil || !strings.Contains(err.Error(), "broken.yaml") {
		t.Errorf("broken: %v, want an error that names the file", err)
	}
	if _, _, err := LoadKnown(dir); err == nil {
		t.Error("a directory: want an error")
	}
}

// The walk matches the names encoding/json matches: case aside, the
// fields an embedded struct promotes, and never a field tagged "-".
// A type that decodes its own JSON keeps its keys to itself.
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
