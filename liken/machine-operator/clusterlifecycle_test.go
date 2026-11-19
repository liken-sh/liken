package main

// Tests for promotion, the operator's part of the cluster document
// lifecycle. The operator runs only after the machine joined the
// cluster, so a running operator proves the document that this boot
// ran. Each test passes boot facts with that document's source and
// hash, and checks what the operator writes to the store.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/liken-sh/liken/liken/machine"
)

func seedFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cluster.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing the seed file: %v", err)
	}
	return path
}

func TestPromotesTheStagedClusterDocumentThisBootRuns(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	if err := store.WriteStaged([]byte(testCluster)); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	hash := machine.ManifestHash([]byte(testCluster))
	if err := store.WriteAttempted(hash); err != nil {
		t.Fatalf("writing the attempted hash: %v", err)
	}

	settleClusterLifecycle(root, seedFile(t, testCluster), partitionBackedFacts(machine.ManifestSourceStaged, hash), nil)

	if raw, _ := store.LoadStaged(); raw != nil {
		t.Error("the staged document is still there, want it removed by the promotion")
	}
	if raw, _ := store.LoadProven(); machine.ManifestHash(raw) != hash {
		t.Error("the proven document differs from the one this boot ran, want them equal")
	}
	if h, _ := store.LoadAttempted(); h != "" {
		t.Errorf("the attempted hash is %q, want none after the promotion", h)
	}
}

func TestDoesNotPromoteADocumentThisBootIsNotRunning(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	// A newer document was staged after this boot started. It has not
	// had its own proving boot, and promoting it would skip that proof.
	newer := testCluster + "  endpoint: https://10.10.0.1:6443\n"
	if err := store.WriteStaged([]byte(newer)); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}

	settleClusterLifecycle(root, seedFile(t, testCluster),
		partitionBackedFacts(machine.ManifestSourceStaged, machine.ManifestHash([]byte(testCluster))), nil)

	if raw, _ := store.LoadStaged(); raw == nil {
		t.Error("the newer staged document is gone, want it staged for its own proving boot")
	}
	if raw, _ := store.LoadProven(); raw != nil {
		t.Error("a proven document exists, want none because nothing was promoted")
	}
}

func TestRecordsTheSeedAsFirstProven(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	seed := seedFile(t, testCluster)

	settleClusterLifecycle(root, seed, partitionBackedFacts(machine.ManifestSourceSeed, machine.ManifestHash([]byte(testCluster))), nil)

	raw, _ := store.LoadProven()
	if machine.ManifestHash(raw) != machine.ManifestHash([]byte(testCluster)) {
		t.Error("the proven document differs from the seed this boot ran, want them equal")
	}
}

func TestDoesNotRecordASeedTheBootDidNotRun(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	// The seed file changed after this machine booted, because an
	// image swap is in progress. Recording it would mark as proven a
	// document that no boot ran.
	seed := seedFile(t, testCluster+"  endpoint: https://10.10.0.9:6443\n")

	settleClusterLifecycle(root, seed, partitionBackedFacts(machine.ManifestSourceSeed, machine.ManifestHash([]byte(testCluster))), nil)

	if raw, _ := store.LoadProven(); raw != nil {
		t.Error("a proven document exists, want none because the boot did not run this seed")
	}
}

func TestDoesNotTouchAMemoryBackedMachine(t *testing.T) {
	root := t.TempDir()
	facts := partitionBackedFacts(machine.ManifestSourceSeed, "abc")
	facts.Storage.MachineState.Backing = machine.BackingMemory

	settleClusterLifecycle(root, seedFile(t, testCluster), facts, nil)

	if entries, _ := os.ReadDir(filepath.Join(root, "cluster")); len(entries) != 0 {
		t.Error("the cluster directory has entries, want none on a memory-backed machine")
	}
}

func TestAPromotionIsAWrite(t *testing.T) {
	root := t.TempDir()
	if err := machine.ClusterManifests(root).WriteStaged([]byte(testCluster)); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	out := &passOutcome{}

	settleClusterLifecycle(root, "", partitionBackedFacts(machine.ManifestSourceStaged, machine.ManifestHash([]byte(testCluster))), out)

	if !slices.Equal(out.writes, []string{"promoting the cluster document"}) {
		t.Errorf("writes = %q, want [promoting the cluster document]", out.writes)
	}
}

// A promotion that cannot write leaves the document staged and records
// a failure, so the loop retries and a later pass promotes it.
func TestAPromotionThatCannotWriteIsRetried(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	if err := store.WriteStaged([]byte(testCluster)); err != nil {
		t.Fatalf("writing the staged document: %v", err)
	}
	readOnly(t, filepath.Join(root, "cluster"))
	out := &passOutcome{}

	settleClusterLifecycle(root, "", partitionBackedFacts(machine.ManifestSourceStaged, machine.ManifestHash([]byte(testCluster))), out)

	staged, _ := store.LoadStaged()
	if staged == nil || len(out.failures) != 1 {
		t.Errorf("staged document %q and failures %v, want the document still staged and one failure", staged, out.failures)
	}
}

// A seed that cannot be recorded records a failure, so the loop
// retries, as it does for a promotion.
func TestASeedThatCannotBeRecordedIsRetried(t *testing.T) {
	root := t.TempDir()
	readOnly(t, root)
	out := &passOutcome{}

	settleClusterLifecycle(root, seedFile(t, testCluster), partitionBackedFacts(machine.ManifestSourceSeed, machine.ManifestHash([]byte(testCluster))), out)

	if len(out.failures) != 1 {
		t.Errorf("failures = %v, want one", out.failures)
	}
}

// A seed boot writes nothing when the store already has a proven
// document or the seed file is gone, and neither case is a failure.
func TestASeedBootWithNothingToRecordWritesNothing(t *testing.T) {
	cases := []struct {
		name   string
		proven []byte
		seed   func(t *testing.T) string
	}{
		{"a proven document already recorded", []byte("kind: Cluster\n"), func(t *testing.T) string { return seedFile(t, testCluster) }},
		{"a seed file that is gone", nil, func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing.yaml") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			store := machine.ClusterManifests(root)
			if c.proven != nil {
				if err := store.WriteProven(c.proven); err != nil {
					t.Fatalf("writing the proven document: %v", err)
				}
			}
			out := &passOutcome{}

			settleClusterLifecycle(root, c.seed(t), partitionBackedFacts(machine.ManifestSourceSeed, machine.ManifestHash([]byte(testCluster))), out)

			proven, _ := store.LoadProven()
			if string(proven) != string(c.proven) || len(out.writes) != 0 || len(out.failures) != 0 {
				t.Errorf("proven document %q, writes %q, failures %v, want the store unchanged", proven, out.writes, out.failures)
			}
		})
	}
}
