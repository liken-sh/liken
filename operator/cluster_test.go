package main

// Tests for the operator's half of the cluster document lifecycle:
// promotion. The operator's own existence proves the join, so these
// tests simulate "I am running and the facts say which document this
// boot ran" and check what happens to the store.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chrisguidry/liken/machine"
)

const testCluster = `apiVersion: liken.sh/v1alpha1
kind: Cluster
metadata:
  name: lab
spec:
  leaders: [node-1]
`

func partitionBackedFacts(source machine.ManifestSource, hash string) *machine.MachineStatus {
	return &machine.MachineStatus{
		Storage: machine.StorageStatus{
			MachineState: machine.StorageRoleStatus{Backing: machine.BackingPartition},
		},
		Boot: machine.BootStatus{
			// The machine manifest's record is what says "this init
			// publishes boot records at all"; the cluster fields ride
			// beside it.
			ManifestSource:        machine.ManifestSourceProven,
			ClusterManifestSource: source,
			ClusterManifestHash:   hash,
		},
	}
}

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

	settleClusterLifecycle(root, seedFile(t, testCluster), partitionBackedFacts(machine.ManifestSourceStaged, hash))

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
	// A newer document was staged after this boot came up: it has not
	// had its proving boot, and promoting it would skip the trial.
	newer := testCluster + "  endpoint: https://10.10.0.1:6443\n"
	if err := store.WriteStaged([]byte(newer)); err != nil {
		t.Fatal(err)
	}

	settleClusterLifecycle(root, seedFile(t, testCluster),
		partitionBackedFacts(machine.ManifestSourceStaged, machine.ManifestHash([]byte(testCluster))))

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

	settleClusterLifecycle(root, seed, partitionBackedFacts(machine.ManifestSourceSeed, machine.ManifestHash([]byte(testCluster))))

	raw, _ := store.LoadProven()
	if machine.ManifestHash(raw) != machine.ManifestHash([]byte(testCluster)) {
		t.Error("the seed this boot ran should be recorded as the first proven")
	}
}

func TestDoesNotRecordASeedTheBootDidNotRun(t *testing.T) {
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	// The seed file changed since this machine booted (an image swap
	// mid-flight); recording it would prove bytes nobody ran.
	seed := seedFile(t, testCluster+"  endpoint: https://10.10.0.9:6443\n")

	settleClusterLifecycle(root, seed, partitionBackedFacts(machine.ManifestSourceSeed, machine.ManifestHash([]byte(testCluster))))

	if raw, _ := store.LoadProven(); raw != nil {
		t.Error("a seed the boot did not run must not become proven")
	}
}

// decisionCluster builds the in-cluster Cluster document the operator
// would be comparing against.
func decisionCluster() *machine.Cluster {
	return &machine.Cluster{
		APIVersion: machine.APIVersion,
		Kind:       "Cluster",
		Metadata:   machine.ObjectMeta{Name: "lab"},
		Spec: machine.ClusterSpec{
			Leaders:  []string{"node-1"},
			Endpoint: "https://10.10.0.1:6443",
		},
	}
}

func machineWithPolicy(policy machine.RebootPolicy) *machine.Machine {
	return &machine.Machine{
		Metadata: machine.ObjectMeta{Name: "node-1"},
		Spec:     machine.MachineSpec{RebootPolicy: policy},
	}
}

func TestClusterConvergedWhenTheBootRunsTheCurrentDocument(t *testing.T) {
	cluster := decisionCluster()
	_, hash, err := renderCluster(cluster.Metadata.Name, cluster.Spec)
	if err != nil {
		t.Fatal(err)
	}
	facts := partitionBackedFacts(machine.ManifestSourceProven, hash)

	conv := decideClusterConvergence(cluster, machineWithPolicy(""), facts, nil, hash, "", turnGranted)
	if conv.condition.Status != "True" || conv.condition.Reason != "Converged" {
		t.Errorf("got %+v", conv.condition)
	}
	if conv.condition.Type != "ClusterConverged" {
		t.Errorf("condition type: got %q", conv.condition.Type)
	}
}

func TestClusterConvergedWithdrawsAStaleStagedDocument(t *testing.T) {
	cluster := decisionCluster()
	_, hash, _ := renderCluster(cluster.Metadata.Name, cluster.Spec)
	facts := partitionBackedFacts(machine.ManifestSourceProven, hash)
	rejection := &machine.Rejection{Hash: "old", Reason: "history"}

	conv := decideClusterConvergence(cluster, machineWithPolicy(""), facts, rejection, hash, "some-other-hash", turnGranted)
	if !conv.withdraw || !conv.clearRejection {
		t.Errorf("an edit taken back should withdraw and clear: %+v", conv)
	}
}

func TestClusterDriftStagesAndReportsAPendingReboot(t *testing.T) {
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(decisionCluster(), machineWithPolicy(""), facts, nil, "some-old-hash", "", turnGranted)
	if conv.condition.Status != "False" || conv.condition.Reason != "RebootPending" {
		t.Errorf("got %+v", conv.condition)
	}
	if !conv.stage || conv.requestReboot {
		t.Errorf("Manual policy stages without rebooting: %+v", conv)
	}
}

func TestClusterDriftRequestsARebootUnderAutoPolicy(t *testing.T) {
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(decisionCluster(), machineWithPolicy(machine.RebootAuto), facts, nil, "some-old-hash", "", turnGranted)
	if conv.condition.Reason != "RebootRequested" || !conv.requestReboot {
		t.Errorf("got %+v", conv)
	}
}

func TestClusterDriftDoesNotRestageTheSameBytes(t *testing.T) {
	cluster := decisionCluster()
	_, hash, _ := renderCluster(cluster.Metadata.Name, cluster.Spec)
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(cluster, machineWithPolicy(""), facts, nil, "some-old-hash", hash, turnGranted)
	if conv.stage {
		t.Error("the exact bytes already wait; staging again is disk churn")
	}
}

func TestClusterRejectedLastBootHolds(t *testing.T) {
	cluster := decisionCluster()
	_, hash, _ := renderCluster(cluster.Metadata.Name, cluster.Spec)
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")
	rejection := &machine.Rejection{Hash: hash, Reason: "never joined"}

	conv := decideClusterConvergence(cluster, machineWithPolicy(machine.RebootAuto), facts, rejection, "some-old-hash", "", turnGranted)
	if conv.condition.Reason != "RejectedLastBoot" || conv.stage || conv.requestReboot {
		t.Errorf("a rejected document must not be re-staged: %+v", conv)
	}
}

func TestClusterConvergenceIsUnknownWithoutFacts(t *testing.T) {
	conv := decideClusterConvergence(decisionCluster(), machineWithPolicy(""), nil, nil, "", "", turnGranted)
	if conv.condition.Status != "Unknown" {
		t.Errorf("got %+v", conv.condition)
	}
}

func TestClusterConvergenceRefusesMemoryBackedStaging(t *testing.T) {
	facts := partitionBackedFacts(machine.ManifestSourceSeed, "some-old-hash")
	facts.Storage.MachineState.Backing = machine.BackingMemory

	conv := decideClusterConvergence(decisionCluster(), machineWithPolicy(""), facts, nil, "some-old-hash", "", turnGranted)
	if conv.condition.Reason != "MachineStateEphemeral" || conv.stage {
		t.Errorf("got %+v", conv)
	}
}

func TestDoesNotTouchAMemoryBackedMachine(t *testing.T) {
	root := t.TempDir()
	facts := partitionBackedFacts(machine.ManifestSourceSeed, "abc")
	facts.Storage.MachineState.Backing = machine.BackingMemory

	settleClusterLifecycle(root, seedFile(t, testCluster), facts)

	if entries, _ := os.ReadDir(filepath.Join(root, "cluster")); len(entries) != 0 {
		t.Error("a memory-backed machine has no durable lifecycle to settle")
	}
}

func TestClusterConvergenceWaitsForItsTurn(t *testing.T) {
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-raw-hash")
	conv := decideClusterConvergence(decisionCluster(), machineWithPolicy(machine.RebootAuto), facts, nil, "some-old-hash", "", turnAwaiting)
	if conv.condition.Reason != "AwaitingTurn" {
		t.Fatalf("got %+v", conv.condition)
	}
	if !conv.stage || conv.requestReboot {
		t.Errorf("an ungranted member stages but never reboots: %+v", conv)
	}
}
