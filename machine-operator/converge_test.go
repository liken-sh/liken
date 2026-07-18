package main

// Tests for the convergence decisions. They follow the same rule as
// init's tests: decisions are pure functions over plain values, so
// every row of the convergence truth table runs without a cluster, a
// disk, or a mount.

import (
	"strings"
	"testing"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/machine"
)

func specWith(storage machine.StorageSpec) machine.MachineSpec {
	return machine.MachineSpec{Storage: storage}
}

// labStorage is the lab machine's shape: five roles across two disks.
func labStorage() machine.StorageSpec {
	return machine.StorageSpec{
		MachineState:     &machine.StorageRole{Device: "/dev/vda", Size: "64Mi"},
		MachineEphemeral: &machine.StorageRole{Device: "/dev/vdb", Size: "512Mi"},
		ClusterState:     &machine.StorageRole{Device: "/dev/vda"},
		PodStorage:       &machine.StorageRole{Device: "/dev/vdb", Size: "2Gi"},
		PodEphemeral:     &machine.StorageRole{Device: "/dev/vdb"},
	}
}

// labFacts builds the facts a healthy boot of the lab machine
// publishes: every role placed, the boot record filled in.
func labFacts() *machine.MachineStatus {
	facts := &machine.MachineStatus{
		Hardware: machine.HardwareStatus{
			BlockDevices: []machine.BlockDevice{
				{Name: "vda", SizeBytes: 2 << 30},
				{Name: "vdb", SizeBytes: 4 << 30},
			},
		},
		Storage: machine.StorageStatus{
			MachineState:     machine.StorageRoleStatus{Backing: machine.BackingPartition, Device: "vda1", CapacityBytes: 64 << 20},
			MachineEphemeral: machine.StorageRoleStatus{Backing: machine.BackingPartition, Device: "vdb1", CapacityBytes: 512 << 20},
			ClusterState:     machine.StorageRoleStatus{Backing: machine.BackingPartition, Device: "vda2", CapacityBytes: 1 << 30},
			PodStorage:       machine.StorageRoleStatus{Backing: machine.BackingPartition, Device: "vdb2", CapacityBytes: 2 << 30},
			PodEphemeral:     machine.StorageRoleStatus{Backing: machine.BackingPartition, Device: "vdb3", CapacityBytes: 1 << 30},
		},
		Boot: machine.BootStatus{
			ManifestSource: machine.ManifestSourceProven,
			ManifestHash:   "abc123",
			Storage:        labStorage(),
		},
	}
	return facts
}

func labMachine() *machine.Machine {
	return &machine.Machine{
		APIVersion: api.APIVersion,
		Kind:       "Machine",
		Metadata:   api.ObjectMeta{Name: "liken-dev"},
		Spec:       specWith(labStorage()),
	}
}

func TestDecideConvergenceLoadsAddedModulesLive(t *testing.T) {
	// An additive modules edit is the one machine-spec change that
	// needs no disruption: the manifest stages (for durability), and
	// instead of a reboot the operator asks init to load the added
	// modules into the running kernel.
	m := labMachine()
	m.Spec.Modules = []string{"nvidia"}
	conv := decideConvergence(m, labFacts(), nil, "", turnStandalone)
	if conv.condition.Reason != "LoadRequested" {
		t.Fatalf("got %+v", conv.condition)
	}
	if !conv.stage || !conv.requestLoad || conv.requestReboot || conv.requestRestart {
		t.Errorf("an additive modules edit stages and loads, nothing more: %+v", conv)
	}
}

func TestLiveLoadNeedsNoPolicyOrTurn(t *testing.T) {
	// Loading a module is not a disruption: nothing drains, nothing
	// restarts. So it takes no reboot turn and waits for no Manual
	// approval, exactly like the sysctls the operator reconciles live.
	m := labMachine()
	m.Spec.Modules = []string{"nvidia"}
	m.Spec.RebootPolicy = machine.RebootManual
	conv := decideConvergence(m, labFacts(), nil, "", turnAwaiting)
	if conv.condition.Reason != "LoadRequested" || !conv.requestLoad {
		t.Errorf("a live load should not wait on policy or turns: %+v", conv.condition)
	}
}

func TestModuleRetractionStillConvergesByReboot(t *testing.T) {
	// Loading is one-way: the kernel offers no safe way to pull a
	// driver out from under whatever started using it, so retracting
	// a module keeps the reboot tier.
	m := labMachine()
	facts := labFacts()
	facts.Boot.Modules = []string{"nvidia"}
	conv := decideConvergence(m, facts, nil, "", turnStandalone)
	if conv.condition.Reason != "RebootPending" || conv.requestLoad {
		t.Errorf("a retraction needs the reboot: %+v", conv.condition)
	}
}

func TestMixedModuleAndStorageDriftConvergesByReboot(t *testing.T) {
	m := labMachine()
	m.Spec.Modules = []string{"nvidia"}
	m.Spec.Storage.PodStorage.Size = "3Gi"
	conv := decideConvergence(m, labFacts(), nil, "", turnStandalone)
	if conv.condition.Reason != "RebootPending" || conv.requestLoad {
		t.Errorf("storage drift forces the reboot tier: %+v", conv.condition)
	}
}

func TestDecideConvergenceStagesOnModulesDrift(t *testing.T) {
	m := labMachine()
	facts := labFacts()
	facts.Boot.Modules = []string{"zram"}
	m.Spec.Modules = []string{"nvidia"}
	conv := decideConvergence(m, facts, nil, "", turnStandalone)
	if conv.condition.Reason != "RebootPending" {
		t.Fatalf("got %+v", conv.condition)
	}
	if !conv.stage {
		t.Errorf("a modules edit stages like any other machine change: %+v", conv)
	}
	if !strings.Contains(conv.condition.Message, "nvidia") {
		t.Errorf("the message should carry the diff: %q", conv.condition.Message)
	}
}

func TestValidateStagingAcceptsAGrow(t *testing.T) {
	spec := labStorage()
	spec.PodStorage.Size = "3Gi"
	if err := validateStaging(spec, labFacts()); err != nil {
		t.Error(err)
	}
}

func TestValidateStagingRefusesAShrink(t *testing.T) {
	spec := labStorage()
	spec.PodStorage.Size = "1Gi" // the partition is already 2Gi
	err := validateStaging(spec, labFacts())
	if err == nil {
		t.Fatal("expected a refusal to shrink")
	}
	if !strings.Contains(err.Error(), "grow-only") {
		t.Errorf("error should teach the rule: %v", err)
	}
}

func TestValidateStagingRefusesFixingARemainderBelowItsSize(t *testing.T) {
	// clusterState is a remainder occupying 1Gi; giving it a fixed
	// 512Mi is still a shrink, even though the declared size grew from
	// nothing.
	spec := labStorage()
	spec.ClusterState.Size = "512Mi"
	if err := validateStaging(spec, labFacts()); err == nil {
		t.Error("expected a refusal to shrink a remainder by fixing it")
	}
}

func TestValidateStagingRefusesAnUnparseableSize(t *testing.T) {
	spec := labStorage()
	spec.PodStorage.Size = "quite-large"
	if err := validateStaging(spec, labFacts()); err == nil {
		t.Error("expected a refusal for a size that does not parse")
	}
}

func TestValidateStagingRefusesUnknownDevicesForNewRoles(t *testing.T) {
	facts := labFacts()
	facts.Storage.PodStorage = machine.StorageRoleStatus{Backing: machine.BackingMemory}
	spec := labStorage()
	spec.PodStorage.Device = "/dev/vdz"
	err := validateStaging(spec, facts)
	if err == nil {
		t.Fatal("expected a refusal for a device the machine doesn't have")
	}
	for _, want := range []string{"/dev/vdz", "vda, vdb"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

func TestRenderManifestIsDeterministicAndCarriesNoStatus(t *testing.T) {
	m := labMachine()
	m.Status.Version.Liken = "should-not-appear"
	a, hashA, err := renderManifest(m.Metadata.Name, m.Spec)
	if err != nil {
		t.Fatal(err)
	}
	_, hashB, err := renderManifest(m.Metadata.Name, m.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if hashA != hashB {
		t.Error("the same spec must render to the same bytes")
	}
	if strings.Contains(string(a), "status") || strings.Contains(string(a), "should-not-appear") {
		t.Errorf("a staged manifest carries spec, never status:\n%s", a)
	}
	if parsed, err := machine.Parse(a); err != nil || parsed.Metadata.Name != "liken-dev" {
		t.Errorf("the rendered manifest must parse back: %v", err)
	}
}

// The decideConvergence truth table, one test per row.

func TestDecideConvergenceWithoutFacts(t *testing.T) {
	conv := decideConvergence(labMachine(), nil, nil, "", turnStandalone)
	if conv.condition.Status != "Unknown" || conv.condition.Reason != "FactsIncomplete" {
		t.Errorf("got %+v", conv.condition)
	}
	if conv.stage || conv.requestReboot {
		t.Error("no side effects without facts")
	}
}

func TestDecideConvergenceWithoutABootRecord(t *testing.T) {
	facts := labFacts()
	facts.Boot = machine.BootStatus{}
	conv := decideConvergence(labMachine(), facts, nil, "", turnStandalone)
	if conv.condition.Reason != "FactsIncomplete" {
		t.Errorf("got %+v", conv.condition)
	}
}

func TestDecideConvergenceWhenTheBootIsCurrent(t *testing.T) {
	conv := decideConvergence(labMachine(), labFacts(), nil, "", turnStandalone)
	if conv.condition.Status != "True" || conv.condition.Reason != "Converged" {
		t.Errorf("got %+v", conv.condition)
	}
	if conv.stage || conv.requestReboot || conv.withdraw || conv.clearRejection {
		t.Error("a converged machine needs no side effects")
	}
}

func TestDecideConvergenceWithdrawsAStagedManifestNobodyWants(t *testing.T) {
	// The spec was edited and then edited back before any reboot: no
	// drift, but the earlier edit still sits staged. Left there, the
	// next boot would apply it.
	conv := decideConvergence(labMachine(), labFacts(), nil, "some-staged-hash", turnStandalone)
	if conv.condition.Reason != "Converged" {
		t.Fatalf("got %+v", conv.condition)
	}
	if !conv.withdraw {
		t.Error("a staged manifest for a spec no longer asked for must be withdrawn")
	}
	if conv.stage || conv.requestReboot {
		t.Errorf("withdrawal is the only side effect here: %+v", conv)
	}
}

func TestDecideConvergenceClearsARejectionOnceTheSpecMovesOn(t *testing.T) {
	// A staged spec was rejected at boot, and the cluster's spec has
	// since been edited back to what the machine runs. The rejection
	// blocked exactly that abandoned spec; it has nothing left to do.
	rejection := &machine.Rejection{Hash: "the-abandoned-spec", Reason: "could not grow"}
	conv := decideConvergence(labMachine(), labFacts(), rejection, "", turnStandalone)
	if conv.condition.Reason != "Converged" {
		t.Fatalf("got %+v", conv.condition)
	}
	if !conv.clearRejection {
		t.Error("a rejection for a spec no longer asked for must be cleared")
	}
}

// grownLabMachine is the canonical drift: podStorage grown to 3Gi.
func grownLabMachine() *machine.Machine {
	m := labMachine()
	m.Spec.Storage.PodStorage.Size = "3Gi"
	return m
}

func TestDecideConvergenceStagesAndWaitsUnderManualPolicy(t *testing.T) {
	m := grownLabMachine()
	conv := decideConvergence(m, labFacts(), nil, "", turnStandalone)
	if conv.condition.Reason != "RebootPending" {
		t.Fatalf("got %+v", conv.condition)
	}
	if !conv.stage || conv.requestReboot {
		t.Errorf("Manual policy stages but never reboots: %+v", conv)
	}
	if !strings.Contains(conv.condition.Message, "podStorage") {
		t.Errorf("the message should carry the diff: %q", conv.condition.Message)
	}
}

func TestDecideConvergenceRequestsARebootUnderAutoPolicy(t *testing.T) {
	m := grownLabMachine()
	m.Spec.RebootPolicy = machine.RebootAuto
	conv := decideConvergence(m, labFacts(), nil, "", turnStandalone)
	if conv.condition.Reason != "RebootRequested" {
		t.Fatalf("got %+v", conv.condition)
	}
	if !conv.stage || !conv.requestReboot {
		t.Errorf("Auto policy stages and reboots: %+v", conv)
	}
}

func TestDecideConvergenceIsIdempotentAboutStaging(t *testing.T) {
	m := grownLabMachine()
	_, hash, err := renderManifest(m.Metadata.Name, m.Spec)
	if err != nil {
		t.Fatal(err)
	}
	conv := decideConvergence(m, labFacts(), nil, hash, turnStandalone)
	if conv.stage {
		t.Error("bytes already staged must not be rewritten every pass")
	}
	if conv.condition.Reason != "RebootPending" {
		t.Errorf("the condition still reports the pending reboot: %+v", conv.condition)
	}
}

func TestDecideConvergenceHonorsARejection(t *testing.T) {
	m := grownLabMachine()
	m.Spec.RebootPolicy = machine.RebootAuto
	_, hash, err := renderManifest(m.Metadata.Name, m.Spec)
	if err != nil {
		t.Fatal(err)
	}
	rejection := &machine.Rejection{Hash: hash, Reason: "disk on fire"}

	conv := decideConvergence(m, labFacts(), rejection, "", turnStandalone)
	if conv.condition.Reason != "RejectedLastBoot" {
		t.Fatalf("got %+v", conv.condition)
	}
	if conv.stage || conv.requestReboot {
		t.Error("a rejected spec must never re-stage or reboot (reject-once)")
	}
	if !strings.Contains(conv.condition.Message, "disk on fire") {
		t.Errorf("the message should carry init's reason: %q", conv.condition.Message)
	}
}

func TestDecideConvergenceStagesAgainForADifferentEdit(t *testing.T) {
	// The rejection blocks exactly one spec; a genuinely different
	// edit clears it naturally.
	m := grownLabMachine()
	rejection := &machine.Rejection{Hash: "some-other-hash", Reason: "old news"}
	conv := decideConvergence(m, labFacts(), rejection, "", turnStandalone)
	if conv.condition.Reason != "RebootPending" || !conv.stage {
		t.Errorf("a different spec should stage normally: %+v", conv)
	}
}

func TestDecideConvergenceRefusesToLoopOnAContradiction(t *testing.T) {
	m := grownLabMachine()
	m.Spec.RebootPolicy = machine.RebootAuto
	_, hash, err := renderManifest(m.Metadata.Name, m.Spec)
	if err != nil {
		t.Fatal(err)
	}
	facts := labFacts()
	facts.Boot.ManifestHash = hash // the facts claim this exact spec was actuated, yet drift computes

	conv := decideConvergence(m, facts, nil, "", turnStandalone)
	if conv.condition.Reason != "BootMismatch" {
		t.Fatalf("got %+v", conv.condition)
	}
	if conv.stage || conv.requestReboot {
		t.Error("a contradiction must wedge, not reboot-loop")
	}
}

func TestDecideConvergenceNeedsADurableMachineState(t *testing.T) {
	m := grownLabMachine()
	facts := labFacts()
	facts.Storage.MachineState = machine.StorageRoleStatus{Backing: machine.BackingMemory}
	conv := decideConvergence(m, facts, nil, "", turnStandalone)
	if conv.condition.Reason != "MachineStateEphemeral" {
		t.Fatalf("got %+v", conv.condition)
	}
	if conv.stage || conv.requestReboot {
		t.Error("nowhere durable to stage means no side effects")
	}
}

func TestDecideConvergenceRefusesInvalidStaging(t *testing.T) {
	m := labMachine()
	m.Spec.Storage.PodStorage.Size = "1Gi" // a shrink
	conv := decideConvergence(m, labFacts(), nil, "", turnStandalone)
	if conv.condition.Reason != "StagingRejected" {
		t.Fatalf("got %+v", conv.condition)
	}
	if conv.stage || conv.requestReboot {
		t.Error("an invalid spec must not stage")
	}
	if !strings.Contains(conv.condition.Message, "grow-only") {
		t.Errorf("the message should carry the validation error: %q", conv.condition.Message)
	}
}

func TestDecideConvergenceWaitsForItsTurnAsAClusterMember(t *testing.T) {
	m := grownLabMachine()
	m.Spec.RebootPolicy = machine.RebootAuto
	conv := decideConvergence(m, labFacts(), nil, "", turnAwaiting)
	if conv.condition.Reason != "AwaitingTurn" {
		t.Fatalf("got %+v", conv.condition)
	}
	if !conv.stage || conv.requestReboot {
		t.Errorf("an ungranted member stages but never reboots: %+v", conv)
	}
}

func TestDecideConvergenceRebootsOnItsGrantedTurn(t *testing.T) {
	m := grownLabMachine()
	m.Spec.RebootPolicy = machine.RebootAuto
	conv := decideConvergence(m, labFacts(), nil, "", turnGranted)
	if conv.condition.Reason != "RebootRequested" || !conv.requestReboot {
		t.Fatalf("a granted turn is taken: %+v", conv.condition)
	}
}

func TestDecideConvergenceManualPolicyIgnoresTheGrant(t *testing.T) {
	m := grownLabMachine()
	conv := decideConvergence(m, labFacts(), nil, "", turnGranted)
	if conv.condition.Reason != "RebootPending" || conv.requestReboot {
		t.Fatalf("Manual means the human decides, grant or not: %+v", conv.condition)
	}
}
