package main

// The version-convergence truth table: which ask (if any) a machine
// derives from the cluster's target, and how the fetcher's answer
// becomes the VersionConverged condition.

import (
	"strings"
	"testing"

	"github.com/chrisguidry/liken/machine"
)

func clusterWithTarget(version string) *machine.Cluster {
	return &machine.Cluster{
		Metadata: machine.ObjectMeta{Name: "lab"},
		Spec: machine.ClusterSpec{
			Version: version,
			Releases: machine.ClusterReleasesSpec{
				Source: "http://10.0.2.2:8017/releases",
				Catalog: []machine.ReleaseCatalogEntry{
					{Version: "0.1.0", Digest: "sha256:" + strings.Repeat("aa", 32)},
					{Version: "0.2.0", Digest: "sha256:" + strings.Repeat("bb", 32)},
				},
			},
		},
	}
}

func slotBackedFacts(runningVersion, slot string) *machine.MachineStatus {
	return &machine.MachineStatus{
		Version: machine.VersionStatus{Liken: runningVersion},
		Storage: machine.StorageStatus{
			SystemA: machine.StorageRoleStatus{Backing: machine.BackingPartition},
			SystemB: machine.StorageRoleStatus{Backing: machine.BackingPartition},
		},
		Boot: machine.BootStatus{Slot: slot},
	}
}

func TestVersionAskForAMachineBehindTheTarget(t *testing.T) {
	ask, _, ok := versionAsk(clusterWithTarget("0.2.0"), slotBackedFacts("0.1.0", "A"))
	if !ok {
		t.Fatal("a machine behind the target should have an ask")
	}
	if ask.version != "0.2.0" || ask.digest != "sha256:"+strings.Repeat("bb", 32) {
		t.Errorf("ask names the wrong release: %+v", ask)
	}
	if ask.slot != "B" || ask.slotDir != "/var/lib/liken/system/b" {
		t.Errorf("a machine running slot A downloads to slot B: %+v", ask)
	}
	if ask.source != "http://10.0.2.2:8017/releases" {
		t.Errorf("source: %q", ask.source)
	}
}

func TestVersionAskAimsAtTheOtherSlot(t *testing.T) {
	ask, _, ok := versionAsk(clusterWithTarget("0.2.0"), slotBackedFacts("0.1.0", "B"))
	if !ok || ask.slot != "A" {
		t.Errorf("a machine running slot B downloads to slot A: %+v", ask)
	}
}

func TestVersionAskShortCircuits(t *testing.T) {
	noSlots := slotBackedFacts("0.1.0", "A")
	noSlots.Storage.SystemA.Backing = machine.BackingMemory

	cases := []struct {
		name       string
		cluster    *machine.Cluster
		facts      *machine.MachineStatus
		wantStatus machine.ConditionStatus
		wantReason string
	}{
		{"no facts yet", clusterWithTarget("0.2.0"), nil, "Unknown", "FactsIncomplete"},
		{"no target declared", clusterWithTarget(""), slotBackedFacts("0.1.0", "A"), "True", "NoTarget"},
		{"already on the target", clusterWithTarget("0.1.0"), slotBackedFacts("0.1.0", "A"), "True", "Converged"},
		{"target missing from the catalog", clusterWithTarget("9.9.9"), slotBackedFacts("0.1.0", "A"), "False", "VersionNotInCatalog"},
		{"no system slots", clusterWithTarget("0.2.0"), noSlots, "False", "NoSystemSlots"},
		{"not booted from a slot", clusterWithTarget("0.2.0"), slotBackedFacts("0.1.0", ""), "False", "NotInstalled"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, cond, ok := versionAsk(tt.cluster, tt.facts)
			if ok {
				t.Fatal("no ask expected")
			}
			if cond.Type != "VersionConverged" || cond.Status != tt.wantStatus || cond.Reason != tt.wantReason {
				t.Errorf("got %s/%s (%s)", cond.Status, cond.Reason, cond.Type)
			}
		})
	}
}

func TestVersionAskWithoutASource(t *testing.T) {
	cluster := clusterWithTarget("0.2.0")
	cluster.Spec.Releases.Source = ""
	_, cond, ok := versionAsk(cluster, slotBackedFacts("0.1.0", "A"))
	if ok || cond.Reason != "NoReleaseSource" {
		t.Errorf("a catalog without a source can't be fetched from: %+v", cond)
	}
}

func TestVersionConditionFollowsTheFetch(t *testing.T) {
	ask := fetchAsk{version: "0.2.0", slot: "B"}
	cases := []struct {
		state      fetchState
		wantStatus machine.ConditionStatus
		wantReason string
	}{
		{fetchIdle, "False", "Downloading"},
		{fetchRunning, "False", "Downloading"},
		{fetchFailed, "False", "Downloading"},
		{fetchRejected, "False", "DigestMismatch"},
	}
	for _, tt := range cases {
		t.Run(string(tt.state), func(t *testing.T) {
			cond := versionCondition(ask, fetchSnapshot{ask: ask, state: tt.state, detail: "the story so far"})
			if cond.Type != "VersionConverged" || cond.Status != tt.wantStatus || cond.Reason != tt.wantReason {
				t.Errorf("got %s/%s", cond.Status, cond.Reason)
			}
		})
	}
}

func verifiedSnap(ask fetchAsk) fetchSnapshot {
	return fetchSnapshot{ask: ask, state: fetchVerified, detail: "verified"}
}

func autoMachine() *machine.Machine {
	return &machine.Machine{Spec: machine.MachineSpec{RebootPolicy: machine.RebootAuto}}
}

func TestAVerifiedDownloadStagesAndRidesTheRebootChain(t *testing.T) {
	ask := fetchAsk{version: "0.2.0", digest: "sha256:" + strings.Repeat("bb", 32), slot: "B"}
	_, hash, err := machine.RenderSystemRelease(ask.version, ask.slot, ask.digest)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		m          *machine.Machine
		turn       turn
		wantReason string
		wantReboot bool
	}{
		{"manual policy waits for a person", &machine.Machine{}, turnAwaiting, "RebootPending", false},
		{"auto without a turn awaits the conductor", autoMachine(), turnAwaiting, "AwaitingTurn", false},
		{"a granted turn requests the proving reboot", autoMachine(), turnGranted, "RebootRequested", true},
		{"standalone machines reboot at will", autoMachine(), turnStandalone, "RebootRequested", true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			conv := decideSystemStaging(ask, verifiedSnap(ask), tt.m, nil, "", tt.turn)
			if conv.condition.Reason != tt.wantReason || conv.requestReboot != tt.wantReboot {
				t.Errorf("got %s (reboot=%v)", conv.condition.Reason, conv.requestReboot)
			}
			if !conv.stage || conv.hash != hash {
				t.Errorf("the record should stage with its identity: %+v", conv)
			}
		})
	}
}

func TestStagingIsIdempotentAcrossPasses(t *testing.T) {
	ask := fetchAsk{version: "0.2.0", digest: "sha256:" + strings.Repeat("bb", 32), slot: "B"}
	_, hash, _ := machine.RenderSystemRelease(ask.version, ask.slot, ask.digest)
	conv := decideSystemStaging(ask, verifiedSnap(ask), autoMachine(), nil, hash, turnAwaiting)
	if conv.stage {
		t.Error("an already-staged record must not stage again")
	}
}

func TestAFallenBackTrialHolds(t *testing.T) {
	ask := fetchAsk{version: "0.2.0", digest: "sha256:" + strings.Repeat("bb", 32), slot: "B"}
	_, hash, _ := machine.RenderSystemRelease(ask.version, ask.slot, ask.digest)
	rejection := &machine.Rejection{Hash: hash, Reason: "the machine fell back to slot A"}

	conv := decideSystemStaging(ask, verifiedSnap(ask), autoMachine(), rejection, "", turnGranted)
	if conv.condition.Reason != "RejectedLastBoot" || conv.stage || conv.requestReboot {
		t.Errorf("an identical decision must hold at the rejection: %+v", conv)
	}

	// A republished release carries a different digest: a different
	// decision, and the rejection no longer applies.
	fixed := ask
	fixed.digest = "sha256:" + strings.Repeat("cc", 32)
	conv = decideSystemStaging(fixed, verifiedSnap(fixed), autoMachine(), rejection, "", turnGranted)
	if conv.condition.Reason != "RebootRequested" {
		t.Errorf("a different digest is a different decision: %+v", conv)
	}
}

func TestAConvergedTargetTidiesTheStore(t *testing.T) {
	cond := versionConverged("True", "Converged", "running the target")
	conv := versionConvergence(cond, "somehash", &machine.Rejection{Hash: "old"})
	if !conv.withdraw || !conv.clearRejection {
		t.Errorf("a converged machine withdraws its staged record and clears rejections: %+v", conv)
	}
	conv = versionConvergence(versionConverged("False", "Downloading", "busy"), "", nil)
	if conv.withdraw || conv.clearRejection {
		t.Error("an unconverged machine tidies nothing")
	}
}

func slotFacts(slot string) *machine.MachineStatus {
	return &machine.MachineStatus{
		Storage: machine.StorageStatus{
			MachineState: machine.StorageRoleStatus{Backing: machine.BackingPartition},
		},
		Boot: machine.BootStatus{Slot: slot},
	}
}

func TestPromotesTheReleaseThisBootProves(t *testing.T) {
	root := t.TempDir()
	store := machine.SystemReleases(root)
	raw, _, err := machine.RenderSystemRelease(machine.Version, "B", "sha256:abcd")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteStaged(raw); err != nil {
		t.Fatal(err)
	}

	settleSystemReleaseLifecycle(root, slotFacts("B"))

	if staged, _ := store.LoadStaged(); staged != nil {
		t.Error("promotion consumes the staged record")
	}
	if proven, _ := store.LoadProven(); string(proven) != string(raw) {
		t.Error("the proven record is the exact staged bytes")
	}
}

func TestPromotionRequiresTheMatchingSlotAndVersion(t *testing.T) {
	cases := map[string]struct {
		recordSlot, recordVersion, bootSlot string
	}{
		"wrong slot":    {"B", machine.Version, "A"},
		"wrong version": {"B", "someone-else", "B"},
		"no slot":       {"B", machine.Version, ""},
	}
	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			store := machine.SystemReleases(root)
			raw, _, err := machine.RenderSystemRelease(tt.recordVersion, tt.recordSlot, "sha256:abcd")
			if err != nil {
				t.Fatal(err)
			}
			if err := store.WriteStaged(raw); err != nil {
				t.Fatal(err)
			}

			settleSystemReleaseLifecycle(root, slotFacts(tt.bootSlot))

			if staged, _ := store.LoadStaged(); staged == nil {
				t.Error("a trial this boot didn't run must not promote")
			}
		})
	}
}

func TestRecordsTheRunningReleaseAsTheFirstProven(t *testing.T) {
	root := t.TempDir()
	store := machine.SystemReleases(root)

	settleSystemReleaseLifecycle(root, slotFacts("A"))

	proven, _ := store.LoadProven()
	if proven == nil {
		t.Fatal("a slot-booted machine with no record writes its standing down")
	}
	record, err := machine.ParseSystemRelease(proven)
	if err != nil || record.Slot != "A" || record.Version != machine.Version {
		t.Errorf("the seed record names the running release: %+v, %v", record, err)
	}

	// And only once: a second pass leaves it alone.
	settleSystemReleaseLifecycle(root, slotFacts("A"))
	if again, _ := store.LoadProven(); string(again) != string(proven) {
		t.Error("the seed record is written once, not re-asserted")
	}
}
