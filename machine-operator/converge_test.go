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

func TestStorageDriftSeesNoDriftInTheSameSpec(t *testing.T) {
	if diffs := storageDrift(labStorage(), labStorage()); len(diffs) != 0 {
		t.Errorf("identical specs should not drift: %v", diffs)
	}
}

func TestStorageDriftNormalizesSizes(t *testing.T) {
	desired := labStorage()
	desired.PodStorage.Size = "2048Mi" // the same ask as 2Gi, spelled differently
	if diffs := storageDrift(desired, labStorage()); len(diffs) != 0 {
		t.Errorf("2048Mi and 2Gi are the same size: %v", diffs)
	}
}

func TestStorageDriftSeesAGrow(t *testing.T) {
	desired := labStorage()
	desired.PodStorage.Size = "3Gi"
	diffs := storageDrift(desired, labStorage())
	if len(diffs) != 1 || !strings.Contains(diffs[0], "podStorage") {
		t.Errorf("expected one podStorage diff: %v", diffs)
	}
}

func TestStorageDriftSeesAnAddedRole(t *testing.T) {
	actuated := labStorage()
	actuated.PodStorage = nil
	diffs := storageDrift(labStorage(), actuated)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "declared but not actuated") {
		t.Errorf("expected an added-role diff: %v", diffs)
	}
}

func TestStorageDriftSeesARemovedRole(t *testing.T) {
	desired := labStorage()
	desired.PodEphemeral = nil
	diffs := storageDrift(desired, labStorage())
	if len(diffs) != 1 || !strings.Contains(diffs[0], "no longer declared") {
		t.Errorf("expected a removed-role diff: %v", diffs)
	}
}

func TestStorageDriftSeesADeviceChange(t *testing.T) {
	desired := labStorage()
	desired.ClusterState.Device = "/dev/vdc"
	diffs := storageDrift(desired, labStorage())
	if len(diffs) != 1 || !strings.Contains(diffs[0], "device") {
		t.Errorf("expected a device diff: %v", diffs)
	}
}

func TestStorageDriftFallsBackToStringsForUnparseableSizes(t *testing.T) {
	// Validation will refuse these anyway; drift detection just has to
	// not panic on them, and string equality is the only comparison
	// left.
	desired := labStorage()
	desired.PodStorage.Size = "a-whole-bunch"
	actuated := labStorage()
	actuated.PodStorage.Size = "a-whole-bunch"
	if diffs := storageDrift(desired, actuated); len(diffs) != 0 {
		t.Errorf("identical spellings should not drift, parseable or not: %v", diffs)
	}
	actuated.PodStorage.Size = "even-more"
	if diffs := storageDrift(desired, actuated); len(diffs) != 1 {
		t.Errorf("different spellings should drift: %v", diffs)
	}
}

func TestStorageDriftNamesTheRemainder(t *testing.T) {
	// A remainder role's size is spelled "" in the spec; the diff
	// message should say "(remainder)" rather than showing nothing.
	desired := labStorage()
	desired.ClusterState.Size = "3Gi" // was the remainder
	diffs := storageDrift(desired, labStorage())
	if len(diffs) != 1 || !strings.Contains(diffs[0], "(remainder)") {
		t.Errorf("expected the diff to name the remainder: %v", diffs)
	}
}

func TestModulesDriftIgnoresOrderAndRepetition(t *testing.T) {
	diffs := modulesDrift([]string{"nvidia", "zram", "nvidia"}, []string{"zram", "nvidia"})
	if len(diffs) != 0 {
		t.Errorf("the lists are the same set: %v", diffs)
	}
}

func TestModulesDriftTreatsNilAndEmptyAlike(t *testing.T) {
	if diffs := modulesDrift(nil, []string{}); len(diffs) != 0 {
		t.Errorf("nothing declared, nothing actuated: %v", diffs)
	}
}

func TestModulesDriftSeesAnAddedModule(t *testing.T) {
	diffs := modulesDrift([]string{"nvidia"}, nil)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "nvidia declared but this boot ran without it") {
		t.Errorf("got %v", diffs)
	}
}

func TestModulesDriftSeesARemovedModule(t *testing.T) {
	diffs := modulesDrift(nil, []string{"zram"})
	if len(diffs) != 1 || !strings.Contains(diffs[0], "zram no longer declared") {
		t.Errorf("got %v", diffs)
	}
}

func TestDecideConvergenceStagesOnModulesOnlyDrift(t *testing.T) {
	m := labMachine()
	m.Spec.Modules = []string{"nvidia"}
	conv := decideConvergence(m, labFacts(), nil, "", turnStandalone)
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
