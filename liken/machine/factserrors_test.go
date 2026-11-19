package machine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeBadFact writes value to the file at rel in an otherwise empty
// tree. An empty tree reads without error, so the file is the only
// fault that Read can report.
func writeBadFact(t *testing.T, tree FactsTree, rel, value string) {
	t.Helper()
	path := filepath.Join(tree.Dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating the directory of the fact: %v", err)
	}
	if err := os.WriteFile(path, []byte(value+"\n"), 0o644); err != nil {
		t.Fatalf("writing the fact: %v", err)
	}
}

func TestReadReportsUnparseableValues(t *testing.T) {
	cases := map[string]struct {
		rel   string
		value string
	}{
		"cpus":                   {"hardware/cpus", "three"},
		"memoryBytes":            {"hardware/memoryBytes", "lots"},
		"boot time":              {"boot/time", "soon"},
		"time stratum":           {"time/standing", "stratum=high"},
		"lastCrash time":         {"lastCrash/time", "yesterday"},
		"lastFailStop time":      {"lastFailStop/time", "the other day"},
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
				t.Fatalf("reading a bad %s file returned no error, want one", tc.rel)
			}
			if !strings.Contains(err.Error(), filepath.FromSlash(tc.rel)) {
				t.Errorf("the error is %v, want it to name %s", err, tc.rel)
			}
		})
	}
}

// The boot network facts are strings with nothing to parse, so this
// test makes the address file a directory, which os.ReadFile fails to
// read.
func TestReadReportsAnUnreadableFact(t *testing.T) {
	tree := FactsTree{Dir: t.TempDir()}
	rel := filepath.Join("boot", "network", "interfaces", "0", "address")
	if err := os.MkdirAll(filepath.Join(tree.Dir, rel), 0o755); err != nil {
		t.Fatalf("creating the directory %s: %v", rel, err)
	}
	_, err := tree.Read()
	if err == nil {
		t.Fatal("reading a fact that is a directory returned no error, want one")
	}
	if !strings.Contains(err.Error(), rel) {
		t.Errorf("the error is %v, want it to name %s", err, rel)
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
				t.Fatalf("writing the key %q returned no error, want one", tc.key)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("the error is %v, want it to name the key %q", err, tc.key)
			}
		})
	}
}

// A writer that fails calls Report once and returns the same error.
func TestReportRunsOncePerFailure(t *testing.T) {
	var reported []error
	tree := FactsTree{
		Dir:    t.TempDir(),
		Report: func(err error) { reported = append(reported, err) },
	}

	err := tree.WriteBlockDevices([]BlockDevice{{Name: "bad disk"}})
	if err == nil {
		t.Fatal("writing the key \"bad disk\" returned no error, want one")
	}
	if len(reported) != 1 {
		t.Fatalf("Report ran %d times, want 1", len(reported))
	}
	if reported[0] != err {
		t.Errorf("Report got %v, want the returned error %v", reported[0], err)
	}
}
