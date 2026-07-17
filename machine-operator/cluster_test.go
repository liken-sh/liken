package main

// Tests for the operator's half of the cluster document lifecycle:
// promotion. The operator's own existence proves the join, so these
// tests simulate a running operator with facts naming the document
// this boot ran, and check what happens to the store.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
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
			// The machine manifest's record is what indicates that
			// this init publishes boot records at all; the cluster
			// fields sit beside it.
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
func decisionCluster() *cluster.Cluster {
	return &cluster.Cluster{
		APIVersion: api.APIVersion,
		Kind:       "Cluster",
		Metadata:   api.ObjectMeta{Name: "lab"},
		Spec: cluster.ClusterSpec{
			Leaders:  []string{"node-1"},
			Endpoint: "https://10.10.0.1:6443",
		},
	}
}

func machineWithPolicy(policy machine.RebootPolicy) *machine.Machine {
	return &machine.Machine{
		Metadata: api.ObjectMeta{Name: "node-1"},
		Spec:     machine.MachineSpec{RebootPolicy: policy},
	}
}

func TestClusterConvergedWhenTheBootRunsTheCurrentDocument(t *testing.T) {
	clusterDoc := decisionCluster()
	_, hash, err := renderCluster(clusterDoc.Metadata.Name, clusterDoc.Spec)
	if err != nil {
		t.Fatal(err)
	}
	facts := partitionBackedFacts(machine.ManifestSourceProven, hash)

	conv := decideClusterConvergence(clusterDoc, machineWithPolicy(""), facts, nil, nil, hash, "", turnGranted)
	if conv.condition.Status != "True" || conv.condition.Reason != "Converged" {
		t.Errorf("got %+v", conv.condition)
	}
	if conv.condition.Type != "ClusterConverged" {
		t.Errorf("condition type: got %q", conv.condition.Type)
	}
}

func TestClusterConvergedWithdrawsAStaleStagedDocument(t *testing.T) {
	clusterDoc := decisionCluster()
	_, hash, _ := renderCluster(clusterDoc.Metadata.Name, clusterDoc.Spec)
	facts := partitionBackedFacts(machine.ManifestSourceProven, hash)
	rejection := &machine.Rejection{Hash: "old", Reason: "history"}

	conv := decideClusterConvergence(clusterDoc, machineWithPolicy(""), facts, rejection, nil, hash, "some-other-hash", turnGranted)
	if !conv.withdraw || !conv.clearRejection {
		t.Errorf("an edit taken back should withdraw and clear: %+v", conv)
	}
}

func TestClusterDriftStagesAndReportsAPendingReboot(t *testing.T) {
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(decisionCluster(), machineWithPolicy(""), facts, nil, nil, "some-old-hash", "", turnGranted)
	if conv.condition.Status != "False" || conv.condition.Reason != "RebootPending" {
		t.Errorf("got %+v", conv.condition)
	}
	if !conv.stage || conv.requestReboot {
		t.Errorf("Manual policy stages without rebooting: %+v", conv)
	}
}

func TestClusterDriftRequestsARebootUnderAutoPolicy(t *testing.T) {
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(decisionCluster(), machineWithPolicy(machine.RebootAuto), facts, nil, nil, "some-old-hash", "", turnGranted)
	if conv.condition.Reason != "RebootRequested" || !conv.requestReboot {
		t.Errorf("got %+v", conv)
	}
}

func TestClusterDriftDoesNotRestageTheSameBytes(t *testing.T) {
	clusterDoc := decisionCluster()
	_, hash, _ := renderCluster(clusterDoc.Metadata.Name, clusterDoc.Spec)
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(clusterDoc, machineWithPolicy(""), facts, nil, nil, "some-old-hash", hash, turnGranted)
	if conv.stage {
		t.Error("the exact bytes already wait; staging again is disk churn")
	}
}

func TestClusterRejectedLastBootHolds(t *testing.T) {
	clusterDoc := decisionCluster()
	_, hash, _ := renderCluster(clusterDoc.Metadata.Name, clusterDoc.Spec)
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")
	rejection := &machine.Rejection{Hash: hash, Reason: "never joined"}

	conv := decideClusterConvergence(clusterDoc, machineWithPolicy(machine.RebootAuto), facts, rejection, nil, "some-old-hash", "", turnGranted)
	if conv.condition.Reason != "RejectedLastBoot" || conv.stage || conv.requestReboot {
		t.Errorf("a rejected document must not be re-staged: %+v", conv)
	}
}

func TestClusterConvergenceIsUnknownWithoutFacts(t *testing.T) {
	conv := decideClusterConvergence(decisionCluster(), machineWithPolicy(""), nil, nil, nil, "", "", turnGranted)
	if conv.condition.Status != "Unknown" {
		t.Errorf("got %+v", conv.condition)
	}
}

func TestClusterConvergenceRefusesMemoryBackedStaging(t *testing.T) {
	facts := partitionBackedFacts(machine.ManifestSourceSeed, "some-old-hash")
	facts.Storage.MachineState.Backing = machine.BackingMemory

	conv := decideClusterConvergence(decisionCluster(), machineWithPolicy(""), facts, nil, nil, "some-old-hash", "", turnGranted)
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
	conv := decideClusterConvergence(decisionCluster(), machineWithPolicy(machine.RebootAuto), facts, nil, nil, "some-old-hash", "", turnAwaiting)
	if conv.condition.Reason != "AwaitingTurn" {
		t.Fatalf("got %+v", conv.condition)
	}
	if !conv.stage || conv.requestReboot {
		t.Errorf("an ungranted member stages but never reboots: %+v", conv)
	}
}

func TestClusterFeatureDriftConvergesByRestart(t *testing.T) {
	// The desired document differs from the boot's only in
	// spec.features, which k3s reads at process start: a restart
	// applies it, so the machine and its pods stay up.
	desired := decisionCluster()
	desired.Spec.Features = map[string]*cluster.FeatureConfig{"traefik": {}}
	bootDoc := decisionCluster()
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(desired, machineWithPolicy(machine.RebootAuto), facts, nil, bootDoc, "some-old-hash", "", turnGranted)
	if conv.condition.Reason != "RestartRequested" || !conv.requestRestart || conv.requestReboot {
		t.Errorf("a features-only edit should converge by restart: %+v", conv)
	}
}

func TestClusterRegistriesDriftConvergesByRestart(t *testing.T) {
	desired := decisionCluster()
	desired.Spec.Registries = cluster.RegistriesSpec{
		Mirrors: map[string][]string{"docker.io": {"https://mirror.example:5000"}},
	}
	bootDoc := decisionCluster()
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(desired, machineWithPolicy(machine.RebootAuto), facts, nil, bootDoc, "some-old-hash", "", turnGranted)
	if conv.condition.Reason != "RestartRequested" || !conv.requestRestart {
		t.Errorf("a registries-only edit should converge by restart: %+v", conv)
	}
}

func TestClusterRebootClassDriftStillReboots(t *testing.T) {
	// The endpoint is consumed at join time, not at k3s start: the
	// reboot tier owns it.
	desired := decisionCluster()
	desired.Spec.Endpoint = "https://10.10.0.2:6443"
	bootDoc := decisionCluster()
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(desired, machineWithPolicy(machine.RebootAuto), facts, nil, bootDoc, "some-old-hash", "", turnGranted)
	if conv.condition.Reason != "RebootRequested" || !conv.requestReboot || conv.requestRestart {
		t.Errorf("an endpoint edit needs the reboot tier: %+v", conv)
	}
}

func TestClusterMixedDriftFallsToReboot(t *testing.T) {
	// One edit touching a restart-class domain and a reboot-class one
	// takes the heavier tier: a reboot is a restart plus more.
	desired := decisionCluster()
	desired.Spec.Features = map[string]*cluster.FeatureConfig{"traefik": {}}
	desired.Spec.Endpoint = "https://10.10.0.2:6443"
	bootDoc := decisionCluster()
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(desired, machineWithPolicy(machine.RebootAuto), facts, nil, bootDoc, "some-old-hash", "", turnGranted)
	if conv.condition.Reason != "RebootRequested" || !conv.requestReboot {
		t.Errorf("mixed drift falls to the reboot tier: %+v", conv)
	}
}

func TestClusterDriftWithoutABootDocumentFallsToReboot(t *testing.T) {
	// No parsed boot document means the classifier has nothing to
	// diff, and guessing lighter could under-apply: the reboot tier
	// always works.
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(decisionCluster(), machineWithPolicy(machine.RebootAuto), facts, nil, nil, "some-old-hash", "", turnGranted)
	if conv.condition.Reason != "RebootRequested" || !conv.requestReboot || conv.requestRestart {
		t.Errorf("an unreadable boot document falls to reboot: %+v", conv)
	}
}

func TestClusterRestartDriftAwaitsItsTurn(t *testing.T) {
	// A leader's k3s restart bounces embedded etcd, so restarts take
	// conductor turns exactly like reboots — same reason, so the
	// conductor needs no new vocabulary.
	desired := decisionCluster()
	desired.Spec.Features = map[string]*cluster.FeatureConfig{"traefik": {}}
	bootDoc := decisionCluster()
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(desired, machineWithPolicy(machine.RebootAuto), facts, nil, bootDoc, "some-old-hash", "", turnAwaiting)
	if conv.condition.Reason != "AwaitingTurn" || conv.requestRestart || conv.requestReboot {
		t.Errorf("an ungranted member stages and waits: %+v", conv)
	}
}

func TestClusterRestartDriftUnderManualPolicyReportsRestartPending(t *testing.T) {
	desired := decisionCluster()
	desired.Spec.Features = map[string]*cluster.FeatureConfig{"traefik": {}}
	bootDoc := decisionCluster()
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(desired, machineWithPolicy(""), facts, nil, bootDoc, "some-old-hash", "", turnGranted)
	if conv.condition.Reason != "RestartPending" || conv.requestRestart {
		t.Errorf("Manual policy stages and waits for a person: %+v", conv)
	}
}

func TestBootClusterDocumentParsesAndCanonicalizes(t *testing.T) {
	path := seedFile(t, testCluster)
	doc, hash := bootClusterDocument(path)
	if doc == nil || doc.Metadata.Name != "lab" {
		t.Fatalf("expected the parsed document, got %+v", doc)
	}
	_, want, _ := renderCluster(doc.Metadata.Name, doc.Spec)
	if hash != want {
		t.Errorf("the hash must be canonical: got %q, want %q", hash, want)
	}
}

func TestBootClusterDocumentAbsentIsNil(t *testing.T) {
	doc, hash := bootClusterDocument(filepath.Join(t.TempDir(), "absent.yaml"))
	if doc != nil || hash != "" {
		t.Errorf("a missing publication is nil and empty: %v %q", doc, hash)
	}
}

func TestRenderClusterExcludesTheReleaseFeed(t *testing.T) {
	base := cluster.ClusterSpec{Leaders: []string{"node-1"}}
	upgraded := base
	upgraded.Version = "0.2.0"
	upgraded.Releases = cluster.ClusterReleasesSpec{
		Source:  "http://10.0.2.2:8017/releases",
		Catalog: []cluster.ReleaseCatalogEntry{{Version: "0.2.0", Digest: "sha256:abcd"}},
	}

	baseBytes, baseHash, err := renderCluster("lab", base)
	if err != nil {
		t.Fatal(err)
	}
	upgradedBytes, upgradedHash, err := renderCluster("lab", upgraded)
	if err != nil {
		t.Fatal(err)
	}
	if upgradedHash != baseHash || string(upgradedBytes) != string(baseBytes) {
		t.Error("publishing a release or retargeting the fleet must not change the canonical cluster document; that would stage a fleet-wide reboot")
	}
}

func TestRenderClusterIncludesFeatures(t *testing.T) {
	base := cluster.ClusterSpec{Leaders: []string{"node-1"}}
	opted := base
	opted.Features = map[string]*cluster.FeatureConfig{"metrics-server": {}}

	_, baseHash, err := renderCluster("lab", base)
	if err != nil {
		t.Fatal(err)
	}
	_, optedHash, err := renderCluster("lab", opted)
	if err != nil {
		t.Fatal(err)
	}
	if optedHash == baseHash {
		t.Error("features are boot-actuated: an opt-in must change the canonical document's hash so the edit stages and rolls through reboots")
	}
}
