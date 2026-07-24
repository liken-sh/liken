package machine

// These tests drive the error paths of the facts codec. A read walks
// many small files, and each parse can fail on a corrupt file. A write
// of a collection can meet a key that is not a plain name. Both paths
// must report the fault and name the thing at fault, so an operator can
// find it. The round-trip tests prove the happy path; these prove that
// a fault surfaces instead of a silent zero value.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeBadFact plants one unparseable file at rel under the tree. It
// makes the file's parent directories first, because the writer that
// normally makes them does not run here. An empty tree reads clean, so
// this one bad file is the only fault the read can meet.
func writeBadFact(t *testing.T, tree FactsTree, rel, value string) {
	t.Helper()
	path := filepath.Join(tree.Dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadReportsUnparseableValues(t *testing.T) {
	cases := map[string]struct {
		rel   string
		value string
	}{
		"cpus":                   {"hardware/cpus", "three"},
		"memoryBytes":            {"hardware/memoryBytes", "lots"},
		"bootedAt":               {"bootedAt", "soon"},
		"time lastSync":          {"time/lastSync", "never"},
		"time stratum":           {"time/stratum", "high"},
		"lastCrash time":         {"lastCrash/time", "yesterday"},
		"network leaseExpires":   {"network/leaseExpires", "soon"},
		"interface leaseExpires": {"network/interfaces/eth0/leaseExpires", "soon"},
		"blockDevice sizeBytes":  {"hardware/blockDevices/vda/sizeBytes", "big"},
		"storage capacityBytes":  {"storage/machineState/capacityBytes", "huge"},
		"boot restarts":          {"boot/restarts", "many"},
		"rejection rejectedAt":   {"boot/rejection/rejectedAt", "whenever"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tree := FactsTree{Dir: t.TempDir()}
			writeBadFact(t, tree, tc.rel, tc.value)
			_, err := tree.Read()
			if err == nil {
				t.Fatalf("a bad %s file should be an error", tc.rel)
			}
			if !strings.Contains(err.Error(), filepath.FromSlash(tc.rel)) {
				t.Errorf("the error should name %s, got %v", tc.rel, err)
			}
		})
	}
}

func TestWritersRejectUnsafeKeys(t *testing.T) {
	cases := map[string]struct {
		key   string
		write func(tree FactsTree, key string) error
	}{
		"network interface": {
			key: "eth 0",
			write: func(tree FactsTree, key string) error {
				return tree.WriteNetwork(NetworkStatus{
					Interfaces: []InterfaceStatus{{Name: key}},
				})
			},
		},
		"block device": {
			key: "bad disk",
			write: func(tree FactsTree, key string) error {
				return tree.WriteBlockDevices([]BlockDevice{{Name: key}})
			},
		},
		"module": {
			key: "bad module",
			write: func(tree FactsTree, key string) error {
				return tree.WriteModules([]ModuleStatus{{Name: key, State: ModuleLoaded}})
			},
		},
		"feature": {
			key: "bad feature",
			write: func(tree FactsTree, key string) error {
				return tree.WriteFeatures([]FeatureStatus{{Name: key, State: FeatureActive}})
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tree := FactsTree{Dir: t.TempDir()}
			err := tc.write(tree, tc.key)
			if err == nil {
				t.Fatalf("the writer should reject the key %q", tc.key)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("the error should name the key %q, got %v", tc.key, err)
			}
		})
	}
}
