package main

// The acting halves that work against real files: carrying out a
// convergence decision against a manifest store, and settling the
// cluster document's lifecycle after a boot proved (or didn't prove)
// it.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chrisguidry/liken/machine"
)

var lifecycleNow = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

func TestCarryOutConvergenceStages(t *testing.T) {
	store := machine.ClusterManifests(t.TempDir())
	conv := convergence{
		condition: clusterNotConverged("RebootPending", "staged"),
		stage:     true,
		manifest:  []byte("kind: Cluster\n"),
		hash:      "abc123",
	}
	condition := carryOutConvergence(conv, store, "cluster document", lifecycleNow)
	if condition.Reason != "RebootPending" {
		t.Errorf("the decision's condition comes back: %s", condition.Reason)
	}
	staged, err := store.LoadStaged()
	if err != nil || string(staged) != "kind: Cluster\n" {
		t.Errorf("the manifest should be staged: %q, %v", staged, err)
	}
}

func TestCarryOutConvergenceWithdrawsAndClears(t *testing.T) {
	store := machine.ClusterManifests(t.TempDir())
	if err := store.WriteStaged([]byte("kind: Cluster\n")); err != nil {
		t.Fatal(err)
	}
	if err := store.Reject(machine.Rejection{Hash: "stale", Reason: "the test says so", RejectedAt: lifecycleNow}); err != nil {
		t.Fatal(err)
	}
	conv := convergence{
		condition:      clusterConverged("Converged", "current"),
		withdraw:       true,
		clearRejection: true,
	}
	carryOutConvergence(conv, store, "cluster document", lifecycleNow)
	if staged, _ := store.LoadStaged(); staged != nil {
		t.Error("the staged copy should be withdrawn")
	}
	if rejection, _ := store.LoadRejection(); rejection != nil {
		t.Error("the rejection record should be cleared")
	}
}

func TestCarryOutConvergenceReportsAFailedStaging(t *testing.T) {
	// A store rooted somewhere unwritable can't stage; the condition
	// downgrades to StagingFailed rather than reporting a reboot that
	// will not happen.
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	store := machine.ClusterManifests(filepath.Join(parent, "unwritable"))
	conv := convergence{
		condition: clusterNotConverged("RebootPending", "staged"),
		stage:     true,
		manifest:  []byte("kind: Cluster\n"),
	}
	condition := carryOutConvergence(conv, store, "cluster document", lifecycleNow)
	if condition.Reason != "StagingFailed" {
		t.Errorf("got %s", condition.Reason)
	}
}

// partitionFacts is the durable-machineState precondition every
// lifecycle path checks first.
func partitionFacts(source machine.ManifestSource, hash string) *machine.MachineStatus {
	facts := &machine.MachineStatus{}
	facts.Storage.MachineState.Backing = machine.BackingPartition
	facts.Boot.ClusterManifestSource = source
	facts.Boot.ClusterManifestHash = hash
	return facts
}

func TestSettleClusterLifecyclePromotesTheBootedStagedDocument(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	raw := []byte("kind: Cluster\nmetadata: {name: lab}\n")
	if err := store.WriteStaged(raw); err != nil {
		t.Fatal(err)
	}
	settleClusterLifecycle(root, "", partitionFacts(machine.ManifestSourceStaged, machine.ManifestHash(raw)))
	if staged, _ := store.LoadStaged(); staged != nil {
		t.Error("the staged copy should be promoted away")
	}
	if proven, _ := store.LoadProven(); string(proven) != string(raw) {
		t.Errorf("the booted document becomes proven: %q", proven)
	}
}

func TestSettleClusterLifecycleLeavesANewerStagedDocumentAlone(t *testing.T) {
	// A document staged after this boot hasn't had its proving boot;
	// promoting it would skip the trial.
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	if err := store.WriteStaged([]byte("kind: Cluster\nmetadata: {name: newer}\n")); err != nil {
		t.Fatal(err)
	}
	settleClusterLifecycle(root, "", partitionFacts(machine.ManifestSourceStaged, "hash-of-what-this-boot-ran"))
	if staged, _ := store.LoadStaged(); staged == nil {
		t.Error("an unproven newer document must stay staged")
	}
}

func TestSettleClusterLifecycleRecordsTheSeedAsProven(t *testing.T) {
	root := t.TempDir()
	seed := filepath.Join(t.TempDir(), "cluster.yaml")
	raw := []byte("kind: Cluster\nmetadata: {name: lab}\n")
	if err := os.WriteFile(seed, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	settleClusterLifecycle(root, seed, partitionFacts(machine.ManifestSourceSeed, machine.ManifestHash(raw)))
	if proven, _ := machine.ClusterManifests(root).LoadProven(); string(proven) != string(raw) {
		t.Errorf("a first boot's seed becomes the first proven copy: %q", proven)
	}
}

func TestSettleClusterLifecycleNeedsDurableStorage(t *testing.T) {
	facts := &machine.MachineStatus{}
	facts.Storage.MachineState.Backing = machine.BackingMemory
	facts.Boot.ClusterManifestSource = machine.ManifestSourceSeed
	root := t.TempDir()
	settleClusterLifecycle(root, "", facts)
	if proven, _ := machine.ClusterManifests(root).LoadProven(); proven != nil {
		t.Error("a memory-backed machine has nothing durable to settle")
	}
}
