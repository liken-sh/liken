package main

// Tests for the cluster document's convergence: whether the Cluster
// resource matches what this boot ran, and what the operator stages
// when it does not. clusterlifecycle_test.go tests the promotion.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
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
			// decideClusterConvergence reads an empty ManifestSource
			// as facts that are not complete yet.
			ManifestSource:        machine.ManifestSourceProven,
			ClusterManifestSource: source,
			ClusterManifestHash:   hash,
		},
	}
}

// decisionCluster is the Cluster resource that the tests converge to.
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
		t.Fatalf("rendering the cluster document: %v", err)
	}
	facts := partitionBackedFacts(machine.ManifestSourceProven, hash)

	conv := decideClusterConvergence(clusterDoc, nil, machineWithPolicy(""), facts, nil, nil, hash, "", "", turnGranted)
	if conv.condition.Status != "True" || conv.condition.Reason != "Converged" {
		t.Errorf("the condition is %+v, want Status True and Reason Converged", conv.condition)
	}
	if conv.condition.Type != "ClusterConverged" {
		t.Errorf("the condition type is %q, want ClusterConverged", conv.condition.Type)
	}
}

func TestClusterConvergedWithdrawsAStaleStagedDocument(t *testing.T) {
	clusterDoc := decisionCluster()
	_, hash, _ := renderCluster(clusterDoc.Metadata.Name, clusterDoc.Spec)
	facts := partitionBackedFacts(machine.ManifestSourceProven, hash)
	rejection := &machine.Rejection{Hash: "old", Reason: "history"}

	conv := decideClusterConvergence(clusterDoc, nil, machineWithPolicy(""), facts, rejection, nil, hash, "some-other-hash", "", turnGranted)
	if !conv.withdraw || !conv.clearRejection {
		t.Errorf("after the edit is taken back, the convergence is %+v, want withdraw and clearRejection", conv)
	}
}

// A staged copy that the machine runs, or that init marked attempted,
// is no reverted edit: init applied it or is applying it, so the pass
// keeps it even though the Cluster asks for the running document.
func TestClusterConvergedKeepsAStagedDocumentThatRunsOrIsBeingApplied(t *testing.T) {
	clusterDoc := decisionCluster()
	_, hash, _ := renderCluster(clusterDoc.Metadata.Name, clusterDoc.Spec)
	cases := []struct {
		name                  string
		stagedHash, attempted string
	}{
		{"a staged copy that is the running document", hash, ""},
		{"a staged copy that init marked attempted", "being-applied", "being-applied"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			facts := partitionBackedFacts(machine.ManifestSourceProven, hash)

			conv := decideClusterConvergence(clusterDoc, nil, machineWithPolicy(""), facts, nil, nil, hash, c.stagedHash, c.attempted, turnGranted)
			if conv.withdraw || conv.condition.Reason != "Converged" {
				t.Errorf("the convergence is %+v, want Converged and no withdraw", conv)
			}
		})
	}
}

func TestClusterDriftStagesAndReportsAPendingReboot(t *testing.T) {
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(decisionCluster(), nil, machineWithPolicy(""), facts, nil, nil, "some-old-hash", "", "", turnGranted)
	if conv.condition.Status != "False" || conv.condition.Reason != "RebootPending" {
		t.Errorf("the condition is %+v, want Status False and Reason RebootPending", conv.condition)
	}
	if !conv.stage || conv.requestReboot {
		t.Errorf("under Manual policy the convergence is %+v, want stage and no requestReboot", conv)
	}
}

func TestClusterDriftRequestsARebootUnderAutoPolicy(t *testing.T) {
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(decisionCluster(), nil, machineWithPolicy(machine.RebootAuto), facts, nil, nil, "some-old-hash", "", "", turnGranted)
	if conv.condition.Reason != "RebootRequested" || !conv.requestReboot {
		t.Errorf("the convergence is %+v, want Reason RebootRequested and requestReboot", conv)
	}
}

func TestClusterDriftDoesNotRestageTheSameBytes(t *testing.T) {
	clusterDoc := decisionCluster()
	_, hash, _ := renderCluster(clusterDoc.Metadata.Name, clusterDoc.Spec)
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(clusterDoc, nil, machineWithPolicy(""), facts, nil, nil, "some-old-hash", hash, "", turnGranted)
	if conv.stage {
		t.Error("the convergence stages bytes that are already staged, want no staging")
	}
}

func TestClusterRejectedLastBootIsNotStaged(t *testing.T) {
	clusterDoc := decisionCluster()
	_, hash, _ := renderCluster(clusterDoc.Metadata.Name, clusterDoc.Spec)
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")
	rejection := &machine.Rejection{Hash: hash, Reason: "never joined"}

	conv := decideClusterConvergence(clusterDoc, nil, machineWithPolicy(machine.RebootAuto), facts, rejection, nil, "some-old-hash", "", "", turnGranted)
	if conv.condition.Reason != "RejectedLastBoot" || conv.stage || conv.requestReboot {
		t.Errorf("for a rejected document the convergence is %+v, want Reason RejectedLastBoot, no stage, and no requestReboot", conv)
	}
}

func TestClusterConvergenceIsUnknownWithoutFacts(t *testing.T) {
	conv := decideClusterConvergence(decisionCluster(), nil, machineWithPolicy(""), nil, nil, nil, "", "", "", turnGranted)
	if conv.condition.Status != "Unknown" {
		t.Errorf("the condition is %+v, want Status Unknown", conv.condition)
	}
}

func TestClusterConvergenceRefusesMemoryBackedStaging(t *testing.T) {
	facts := partitionBackedFacts(machine.ManifestSourceSeed, "some-old-hash")
	facts.Storage.MachineState.Backing = machine.BackingMemory

	conv := decideClusterConvergence(decisionCluster(), nil, machineWithPolicy(""), facts, nil, nil, "some-old-hash", "", "", turnGranted)
	if conv.condition.Reason != "MachineStateEphemeral" || conv.stage {
		t.Errorf("the convergence is %+v, want Reason MachineStateEphemeral and no stage", conv)
	}
}

func TestClusterConvergenceWaitsForItsTurn(t *testing.T) {
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-raw-hash")
	conv := decideClusterConvergence(decisionCluster(), nil, machineWithPolicy(machine.RebootAuto), facts, nil, nil, "some-old-hash", "", "", turnAwaiting)
	if conv.condition.Reason != "AwaitingTurn" {
		t.Fatalf("the condition is %+v, want Reason AwaitingTurn", conv.condition)
	}
	if !conv.stage || conv.requestReboot {
		t.Errorf("with no turn granted the convergence is %+v, want stage and no requestReboot", conv)
	}
}

func TestClusterFeatureDriftConvergesByRestart(t *testing.T) {
	// The desired document differs from the boot's only in
	// spec.features, which k3s reads when its process starts. A
	// restart applies it, so the machine and its pods stay up.
	desired := decisionCluster()
	desired.Spec.Features = map[string]*cluster.FeatureConfig{"traefik": {}}
	bootDoc := decisionCluster()
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(desired, nil, machineWithPolicy(machine.RebootAuto), facts, nil, bootDoc, "some-old-hash", "", "", turnGranted)
	if conv.condition.Reason != "RestartRequested" || !conv.requestRestart || conv.requestReboot {
		t.Errorf("after a features-only edit the convergence is %+v, want Reason RestartRequested and requestRestart only", conv)
	}
}

func TestClusterRegistriesDriftConvergesByRestart(t *testing.T) {
	desired := decisionCluster()
	desired.Spec.Registries = cluster.RegistriesSpec{
		Mirrors: map[string][]string{"docker.io": {"https://mirror.example:5000"}},
	}
	bootDoc := decisionCluster()
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(desired, nil, machineWithPolicy(machine.RebootAuto), facts, nil, bootDoc, "some-old-hash", "", "", turnGranted)
	if conv.condition.Reason != "RestartRequested" || !conv.requestRestart {
		t.Errorf("after a registries-only edit the convergence is %+v, want Reason RestartRequested and requestRestart", conv)
	}
}

func TestClusterRebootClassDriftStillReboots(t *testing.T) {
	// The boot applies spec.network to the node's addresses and routes
	// before k3s starts, so only a reboot applies a change to it.
	desired := decisionCluster()
	desired.Spec.Network.ClusterCIDR = "10.44.0.0/16"
	bootDoc := decisionCluster()
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(desired, nil, machineWithPolicy(machine.RebootAuto), facts, nil, bootDoc, "some-old-hash", "", "", turnGranted)
	if conv.condition.Reason != "RebootRequested" || !conv.requestReboot || conv.requestRestart {
		t.Errorf("after a network edit the convergence is %+v, want Reason RebootRequested and requestReboot only", conv)
	}
}

func TestClusterMixedDriftFallsToReboot(t *testing.T) {
	// An edit that needs a restart and a reboot gets the reboot,
	// because a reboot also restarts k3s.
	desired := decisionCluster()
	desired.Spec.Features = map[string]*cluster.FeatureConfig{"traefik": {}}
	desired.Spec.Network.ClusterCIDR = "10.44.0.0/16"
	bootDoc := decisionCluster()
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(desired, nil, machineWithPolicy(machine.RebootAuto), facts, nil, bootDoc, "some-old-hash", "", "", turnGranted)
	if conv.condition.Reason != "RebootRequested" || !conv.requestReboot {
		t.Errorf("after a features and network edit the convergence is %+v, want Reason RebootRequested and requestReboot", conv)
	}
}

// nextBootDrift returns decisionCluster with mutate applied.
func nextBootDrift(mutate func(*cluster.Cluster)) *cluster.Cluster {
	desired := decisionCluster()
	mutate(desired)
	return desired
}

func TestClusterNextBootDriftStagesWithoutADisruption(t *testing.T) {
	// Only a boot reads spec.origin and spec.endpoint, so the machine
	// stages either edit for its next boot under either policy and
	// without a turn. Each case gives its own boot document, so the
	// desired document differs from it only in the fields that the
	// case's name gives.
	adopted := nextBootDrift(func(c *cluster.Cluster) { c.Spec.Origin = cluster.OriginAdopted })
	promoted := nextBootDrift(func(c *cluster.Cluster) { c.Spec.Origin = cluster.OriginFounded })
	movedEndpoint := nextBootDrift(func(c *cluster.Cluster) { c.Spec.Endpoint = "https://10.10.0.2:6443" })
	promotedAndMoved := nextBootDrift(func(c *cluster.Cluster) {
		c.Spec.Origin = cluster.OriginFounded
		c.Spec.Endpoint = "https://10.10.0.2:6443"
	})
	cases := map[string]struct {
		boot    *cluster.Cluster
		desired *cluster.Cluster
		policy  machine.RebootPolicy
		t       turn
	}{
		"the origin alone under Auto":       {adopted, promoted, machine.RebootAuto, turnGranted},
		"the endpoint alone under Auto":     {decisionCluster(), movedEndpoint, machine.RebootAuto, turnGranted},
		"the endpoint alone under Manual":   {decisionCluster(), movedEndpoint, "", turnGranted},
		"both fields, with no turn granted": {adopted, promotedAndMoved, machine.RebootAuto, turnAwaiting},
	}
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			conv := decideClusterConvergence(tc.desired, nil, machineWithPolicy(tc.policy), facts, nil, tc.boot, "some-old-hash", "", "", tc.t)
			if conv.condition.Status != "False" || conv.condition.Reason != "StagedForNextBoot" {
				t.Errorf("the condition is %+v, want Status False and Reason StagedForNextBoot", conv.condition)
			}
			if !conv.stage {
				t.Error("the convergence does not stage the document, want a staged copy for the next boot to read")
			}
			if conv.requestReboot || conv.requestRestart {
				t.Errorf("the convergence is %+v, want no requestReboot and no requestRestart", conv)
			}
		})
	}
}

func TestClusterNextBootDriftExplainsItselfInTheMessage(t *testing.T) {
	// The condition can stay False for hours, so its message says that
	// the document is staged, that the next boot applies it, and that
	// the machine requests no turn.
	desired := nextBootDrift(func(c *cluster.Cluster) { c.Spec.Endpoint = "https://10.10.0.2:6443" })
	bootDoc := decisionCluster()
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(desired, nil, machineWithPolicy(machine.RebootAuto), facts, nil, bootDoc, "some-old-hash", "", "", turnGranted)
	for _, want := range []string{"staged", "next boot", "without a reboot turn"} {
		if !strings.Contains(conv.condition.Message, want) {
			t.Errorf("the message is %q, want it to contain %q", conv.condition.Message, want)
		}
	}
}

func TestClusterEndpointBesideARestartFieldTakesTheRestartTier(t *testing.T) {
	// An endpoint edit alone waits for the next boot. Beside a runtime
	// change it converges by a restart, which writes the k3s drop-in
	// again with the new endpoint. This tests the order of the cases in
	// decideClusterConvergence, not the classifier.
	desired := decisionCluster()
	desired.Spec.Endpoint = "https://10.10.0.2:6443"
	desired.Spec.Runtime.K3s.GoMemoryLimit = "25%"
	bootDoc := decisionCluster()
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(desired, nil, machineWithPolicy(machine.RebootAuto), facts, nil, bootDoc, "some-old-hash", "", "", turnGranted)
	if conv.condition.Reason != "RestartRequested" || !conv.requestRestart || conv.requestReboot {
		t.Errorf("after an endpoint and a restart-class edit the convergence is %+v, want Reason RestartRequested and requestRestart only", conv)
	}
}

func TestClusterNextBootDriftDoesNotRestageTheSameBytes(t *testing.T) {
	desired := nextBootDrift(func(c *cluster.Cluster) { c.Spec.Endpoint = "https://10.10.0.2:6443" })
	_, hash, _ := renderCluster(desired.Metadata.Name, desired.Spec)
	bootDoc := decisionCluster()
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(desired, nil, machineWithPolicy(machine.RebootAuto), facts, nil, bootDoc, "some-old-hash", hash, "", turnGranted)
	if conv.stage {
		t.Error("the convergence stages bytes that are already staged, want no staging")
	}
}

func TestClusterDriftWithoutABootDocumentFallsToReboot(t *testing.T) {
	// Without a parsed boot document there is nothing to compare
	// against, and a restart could leave the change unapplied.
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(decisionCluster(), nil, machineWithPolicy(machine.RebootAuto), facts, nil, nil, "some-old-hash", "", "", turnGranted)
	if conv.condition.Reason != "RebootRequested" || !conv.requestReboot || conv.requestRestart {
		t.Errorf("with no boot document the convergence is %+v, want Reason RebootRequested and requestReboot only", conv)
	}
}

func TestClusterRestartDriftAwaitsItsTurn(t *testing.T) {
	// A k3s restart on a leader also restarts its embedded etcd, so a
	// restart waits for a turn the same way a reboot does.
	desired := decisionCluster()
	desired.Spec.Features = map[string]*cluster.FeatureConfig{"traefik": {}}
	bootDoc := decisionCluster()
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(desired, nil, machineWithPolicy(machine.RebootAuto), facts, nil, bootDoc, "some-old-hash", "", "", turnAwaiting)
	if conv.condition.Reason != "AwaitingTurn" || conv.requestRestart || conv.requestReboot {
		t.Errorf("with no turn granted the convergence is %+v, want Reason AwaitingTurn and no request", conv)
	}
}

func TestClusterRestartDriftUnderManualPolicyReportsRestartPending(t *testing.T) {
	desired := decisionCluster()
	desired.Spec.Features = map[string]*cluster.FeatureConfig{"traefik": {}}
	bootDoc := decisionCluster()
	facts := partitionBackedFacts(machine.ManifestSourceProven, "some-old-hash")

	conv := decideClusterConvergence(desired, nil, machineWithPolicy(""), facts, nil, bootDoc, "some-old-hash", "", "", turnGranted)
	if conv.condition.Reason != "RestartPending" || conv.requestRestart {
		t.Errorf("under Manual policy the convergence is %+v, want Reason RestartPending and no requestRestart", conv)
	}
}

func TestTheRunningClusterDocumentKeepsItsBytesAndItsCanonicalHash(t *testing.T) {
	running := readRunningCluster(seedFile(t, testCluster))
	if running.doc == nil || running.doc.Metadata.Name != "lab" {
		t.Fatalf("the document is %+v, want one named lab", running.doc)
	}
	_, want, _ := renderCluster(running.doc.Metadata.Name, running.doc.Spec)
	if running.hash != want || string(running.raw) != testCluster {
		t.Errorf("the hash is %q and the bytes are %q, want the canonical hash %q and the file's bytes", running.hash, running.raw, want)
	}
}

func TestAMissingRunningClusterDocumentIsEmpty(t *testing.T) {
	running := readRunningCluster(filepath.Join(t.TempDir(), "absent.yaml"))
	if running.raw != nil || running.doc != nil || running.hash != "" {
		t.Errorf("for a missing file the read is %+v, want no bytes, no document, and no hash", running)
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
		t.Fatalf("rendering the base cluster document: %v", err)
	}
	upgradedBytes, upgradedHash, err := renderCluster("lab", upgraded)
	if err != nil {
		t.Fatalf("rendering the cluster document with a release feed: %v", err)
	}
	if upgradedHash != baseHash || string(upgradedBytes) != string(baseBytes) {
		t.Error("setting spec.version and spec.releases changed the canonical document, want it unchanged so no machine stages a reboot")
	}
}

func TestRenderClusterIncludesFeatures(t *testing.T) {
	base := cluster.ClusterSpec{Leaders: []string{"node-1"}}
	opted := base
	opted.Features = map[string]*cluster.FeatureConfig{"metrics-server": {}}

	_, baseHash, err := renderCluster("lab", base)
	if err != nil {
		t.Fatalf("rendering the base cluster document: %v", err)
	}
	_, optedHash, err := renderCluster("lab", opted)
	if err != nil {
		t.Fatalf("rendering the cluster document with a feature: %v", err)
	}
	if optedHash == baseHash {
		t.Error("adding a feature left the canonical hash unchanged, want a new hash so the edit stages")
	}
}
