package main

// Tests for the operator's half of the cluster document
// lifecycle: promotion. The operator's own existence proves the
// join, so these tests simulate a running operator with facts
// naming the document this boot ran, and check what happens to the
// store.

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
		t.Fatal(err)
	}
	return path
}

func TestPromotesTheStagedClusterDocumentThisBootRuns(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	if err := store.WriteStaged([]byte(testCluster)); err != nil {
		t.Fatal(err)
	}
	hash := machine.ManifestHash([]byte(testCluster))
	if err := store.WriteAttempted(hash); err != nil {
		t.Fatal(err)
	}

	settleClusterLifecycle(root, seedFile(t, testCluster), partitionBackedFacts(machine.ManifestSourceStaged, hash), nil)

	if raw, _ := store.LoadStaged(); raw != nil {
		t.Error("promotion should consume the staged document")
	}
	if raw, _ := store.LoadProven(); machine.ManifestHash(raw) != hash {
		t.Error("the document this boot proved should now be proven")
	}
	if h, _ := store.LoadAttempted(); h != "" {
		t.Errorf("the trial is over; the marker should be gone, got %q", h)
	}
}

func TestDoesNotPromoteADocumentThisBootIsNotRunning(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	// A newer document was staged after this boot came up. It has
	// not had its proving boot, and promoting it would skip the
	// trial.
	newer := testCluster + "  endpoint: https://10.10.0.1:6443\n"
	if err := store.WriteStaged([]byte(newer)); err != nil {
		t.Fatal(err)
	}

	settleClusterLifecycle(root, seedFile(t, testCluster),
		partitionBackedFacts(machine.ManifestSourceStaged, machine.ManifestHash([]byte(testCluster))), nil)

	if raw, _ := store.LoadStaged(); raw == nil {
		t.Error("the newer staged document must stay staged for its own proving boot")
	}
	if raw, _ := store.LoadProven(); raw != nil {
		t.Error("nothing should have been promoted")
	}
}

func TestRecordsTheSeedAsFirstProven(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	seed := seedFile(t, testCluster)

	settleClusterLifecycle(root, seed, partitionBackedFacts(machine.ManifestSourceSeed, machine.ManifestHash([]byte(testCluster))), nil)

	raw, _ := store.LoadProven()
	if machine.ManifestHash(raw) != machine.ManifestHash([]byte(testCluster)) {
		t.Error("the seed this boot ran should be recorded as the first proven")
	}
}

func TestDoesNotRecordASeedTheBootDidNotRun(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	// The seed file changed after this machine booted, from an
	// image swap in progress. Recording it would mark as proven
	// bytes that nobody ran.
	seed := seedFile(t, testCluster+"  endpoint: https://10.10.0.9:6443\n")

	settleClusterLifecycle(root, seed, partitionBackedFacts(machine.ManifestSourceSeed, machine.ManifestHash([]byte(testCluster))), nil)

	if raw, _ := store.LoadProven(); raw != nil {
		t.Error("a seed the boot did not run must not become proven")
	}
}

func TestDoesNotTouchAMemoryBackedMachine(t *testing.T) {
	root := t.TempDir()
	facts := partitionBackedFacts(machine.ManifestSourceSeed, "abc")
	facts.Storage.MachineState.Backing = machine.BackingMemory

	settleClusterLifecycle(root, seedFile(t, testCluster), facts, nil)

	if entries, _ := os.ReadDir(filepath.Join(root, "cluster")); len(entries) != 0 {
		t.Error("a memory-backed machine has no durable lifecycle to settle")
	}
}

// A promotion that lands counts as a write, so the loop knows the pass
// changed the machine.
func TestAPromotionIsAWrite(t *testing.T) {
	root := t.TempDir()
	if err := machine.ClusterManifests(root).WriteStaged([]byte(testCluster)); err != nil {
		t.Fatal(err)
	}
	out := &passOutcome{}

	settleClusterLifecycle(root, "", partitionBackedFacts(machine.ManifestSourceStaged, machine.ManifestHash([]byte(testCluster))), out)

	if !slices.Equal(out.writes, []string{"promoting the cluster document"}) {
		t.Errorf("writes = %q, want the promotion", out.writes)
	}
}

// A promotion that cannot write leaves the document staged and a
// failure for the loop to retry, so a later pass proves it.
func TestAPromotionThatCannotWriteIsRetried(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	if err := store.WriteStaged([]byte(testCluster)); err != nil {
		t.Fatal(err)
	}
	readOnly(t, filepath.Join(root, "cluster"))
	out := &passOutcome{}

	settleClusterLifecycle(root, "", partitionBackedFacts(machine.ManifestSourceStaged, machine.ManifestHash([]byte(testCluster))), out)

	staged, _ := store.LoadStaged()
	if staged == nil || len(out.failures) != 1 {
		t.Errorf("staged %q, failures %v; want the document still staged and one failure", staged, out.failures)
	}
}

// A seed that cannot be recorded leaves a failure for the loop to
// retry, the same as a promotion.
func TestASeedThatCannotBeRecordedIsRetried(t *testing.T) {
	root := t.TempDir()
	readOnly(t, root)
	out := &passOutcome{}

	settleClusterLifecycle(root, seedFile(t, testCluster), partitionBackedFacts(machine.ManifestSourceSeed, machine.ManifestHash([]byte(testCluster))), out)

	if len(out.failures) != 1 {
		t.Errorf("failures = %v, want one", out.failures)
	}
}

// A seed boot settles nothing when the store already holds a proven
// document, or when the seed file is gone, and neither case is a
// failure.
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
					t.Fatal(err)
				}
			}
			out := &passOutcome{}

			settleClusterLifecycle(root, c.seed(t), partitionBackedFacts(machine.ManifestSourceSeed, machine.ManifestHash([]byte(testCluster))), out)

			proven, _ := store.LoadProven()
			if string(proven) != string(c.proven) || len(out.writes) != 0 || len(out.failures) != 0 {
				t.Errorf("proven %q, writes %q, failures %v; want the store unchanged", proven, out.writes, out.failures)
			}
		})
	}
}
