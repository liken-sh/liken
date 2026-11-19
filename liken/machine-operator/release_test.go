package main

// Version convergence: which download a machine starts for the
// cluster's target, and how the fetcher's state becomes the
// VersionConverged condition.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

func clusterWithTarget(version string) *cluster.Cluster {
	return &cluster.Cluster{
		Metadata: api.ObjectMeta{Name: "lab"},
		Spec: cluster.ClusterSpec{
			Version: version,
			Releases: cluster.ClusterReleasesSpec{
				Source: "http://10.0.2.2:8017/releases",
				Catalog: []cluster.ReleaseCatalogEntry{
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

func TestReleaseToFetchForAMachineBehindTheTarget(t *testing.T) {
	fetch, _, ok := releaseToFetch(clusterWithTarget("0.2.0"), slotBackedFacts("0.1.0", "A"))
	if !ok {
		t.Fatal("releaseToFetch returned no fetch for a machine behind the target, want one")
	}
	if fetch.version != "0.2.0" || fetch.digest != "sha256:"+strings.Repeat("bb", 32) {
		t.Errorf("fetch is %+v, want version 0.2.0 with the digest of the catalog entry", fetch)
	}
	if fetch.slot != "B" || fetch.slotDir != "/var/lib/liken/system/b" {
		t.Errorf("fetch is %+v, want slot B and /var/lib/liken/system/b for a machine running slot A", fetch)
	}
	if fetch.activeSlotDir != "/var/lib/liken/system/a" {
		t.Errorf("fetch is %+v, want activeSlotDir /var/lib/liken/system/a", fetch)
	}
	if fetch.source != "http://10.0.2.2:8017/releases" {
		t.Errorf("source is %q, want the catalog source", fetch.source)
	}
}

func TestReleaseToFetchTargetsTheInactiveSlot(t *testing.T) {
	fetch, _, ok := releaseToFetch(clusterWithTarget("0.2.0"), slotBackedFacts("0.1.0", "B"))
	if !ok || fetch.slot != "A" {
		t.Errorf("fetch is %+v, want slot A for a machine running slot B", fetch)
	}
}

func TestReleaseToFetchReturnsAConditionWhenThereIsNothingToFetch(t *testing.T) {
	noSlots := slotBackedFacts("0.1.0", "A")
	noSlots.Storage.SystemA.Backing = machine.BackingMemory

	cases := []struct {
		name       string
		clusterDoc *cluster.Cluster
		facts      *machine.MachineStatus
		wantStatus api.ConditionStatus
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
			_, cond, ok := releaseToFetch(tt.clusterDoc, tt.facts)
			if ok {
				t.Fatal("releaseToFetch returned a fetch, want none")
			}
			if cond.Type != "VersionConverged" || cond.Status != tt.wantStatus || cond.Reason != tt.wantReason {
				t.Errorf("condition is %s/%s/%s, want VersionConverged/%s/%s", cond.Type, cond.Status, cond.Reason, tt.wantStatus, tt.wantReason)
			}
		})
	}
}

func TestReleaseToFetchWithoutASource(t *testing.T) {
	clusterDoc := clusterWithTarget("0.2.0")
	clusterDoc.Spec.Releases.Source = ""
	_, cond, ok := releaseToFetch(clusterDoc, slotBackedFacts("0.1.0", "A"))
	if ok || cond.Reason != "NoReleaseSource" {
		t.Errorf("fetch ok = %v, condition %+v, want no fetch and reason NoReleaseSource", ok, cond)
	}
}

func TestVersionConditionFollowsTheFetch(t *testing.T) {
	fetch := releaseFetch{version: "0.2.0", slot: "B"}
	cases := []struct {
		name       string
		state      fetchState
		retrying   bool
		wantStatus api.ConditionStatus
		wantReason string
	}{
		{"idle", fetchIdle, false, "False", "Downloading"},
		{"running", fetchRunning, false, "False", "Downloading"},
		{"failed", fetchFailed, true, "False", "DownloadRetrying"},
		{"running a retry", fetchRunning, true, "False", "DownloadRetrying"},
		{"rejected", fetchRejected, false, "False", "DigestMismatch"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cond := versionCondition(fetch, fetchSnapshot{fetch: fetch, state: tt.state, detail: "downloading", retrying: tt.retrying})
			if cond.Type != "VersionConverged" || cond.Status != tt.wantStatus || cond.Reason != tt.wantReason {
				t.Errorf("condition is %s/%s/%s, want VersionConverged/%s/%s", cond.Type, cond.Status, cond.Reason, tt.wantStatus, tt.wantReason)
			}
		})
	}
}

func verifiedSnap(fetch releaseFetch) fetchSnapshot {
	return fetchSnapshot{fetch: fetch, state: fetchVerified, detail: "verified"}
}

func autoMachine() *machine.Machine {
	return &machine.Machine{Spec: machine.MachineSpec{RebootPolicy: machine.RebootAuto}}
}

func TestAVerifiedDownloadStagesAndFollowsTheRebootPolicy(t *testing.T) {
	fetch := releaseFetch{version: "0.2.0", digest: "sha256:" + strings.Repeat("bb", 32), slot: "B"}
	_, hash, err := machine.RenderSystemRelease(fetch.version, fetch.slot, fetch.digest)
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
		{"auto without a turn awaits cluster-operator", autoMachine(), turnAwaiting, "AwaitingTurn", false},
		{"a granted turn requests the proving reboot", autoMachine(), turnGranted, "RebootRequested", true},
		{"standalone machines reboot at will", autoMachine(), turnStandalone, "RebootRequested", true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			conv := decideSystemStaging(fetch, verifiedSnap(fetch), tt.m, nil, "", tt.turn)
			if conv.condition.Reason != tt.wantReason || conv.requestReboot != tt.wantReboot {
				t.Errorf("reason %s with requestReboot %v, want %s with %v", conv.condition.Reason, conv.requestReboot, tt.wantReason, tt.wantReboot)
			}
			if !conv.stage || conv.hash != hash {
				t.Errorf("stage = %v, hash = %q, want stage true and hash %q", conv.stage, conv.hash, hash)
			}
		})
	}
}

func TestAStagedReleaseIsNotStagedAgain(t *testing.T) {
	fetch := releaseFetch{version: "0.2.0", digest: "sha256:" + strings.Repeat("bb", 32), slot: "B"}
	_, hash, _ := machine.RenderSystemRelease(fetch.version, fetch.slot, fetch.digest)
	conv := decideSystemStaging(fetch, verifiedSnap(fetch), autoMachine(), nil, hash, turnAwaiting)
	if conv.stage {
		t.Error("stage is true for a record that is already staged, want false")
	}
}

func TestAFallenBackTrialIsNotStagedAgain(t *testing.T) {
	fetch := releaseFetch{version: "0.2.0", digest: "sha256:" + strings.Repeat("bb", 32), slot: "B"}
	_, hash, _ := machine.RenderSystemRelease(fetch.version, fetch.slot, fetch.digest)
	rejection := &machine.Rejection{Hash: hash, Reason: "the machine fell back to slot A"}

	conv := decideSystemStaging(fetch, verifiedSnap(fetch), autoMachine(), rejection, "", turnGranted)
	if conv.condition.Reason != "RejectedLastBoot" || conv.stage || conv.requestReboot {
		t.Errorf("convergence is %+v, want RejectedLastBoot with no stage and no reboot", conv)
	}

	// A republished release has a different digest, so its record
	// has a different hash and the rejection does not match it.
	fixed := fetch
	fixed.digest = "sha256:" + strings.Repeat("cc", 32)
	conv = decideSystemStaging(fixed, verifiedSnap(fixed), autoMachine(), rejection, "", turnGranted)
	if conv.condition.Reason != "RebootRequested" {
		t.Errorf("reason is %s for a republished digest, want RebootRequested", conv.condition.Reason)
	}
}

func TestAConvergedTargetWithdrawsTheStagedRecordAndClearsRejections(t *testing.T) {
	cond := converged("VersionConverged", "Converged", "running the target")
	conv := versionConvergence(cond, "somehash", &machine.Rejection{Hash: "old"})
	if !conv.withdraw || !conv.clearRejection {
		t.Errorf("convergence is %+v, want withdraw and clearRejection for a converged machine", conv)
	}
	conv = versionConvergence(notConverged("VersionConverged", "Downloading", "busy"), "", nil)
	if conv.withdraw || conv.clearRejection {
		t.Error("an unconverged machine withdraws or clears a rejection, want neither")
	}
}

func slotFacts(version, slot string) *machine.MachineStatus {
	return &machine.MachineStatus{
		Version: machine.VersionStatus{Liken: version},
		Storage: machine.StorageStatus{
			MachineState: machine.StorageRoleStatus{Backing: machine.BackingPartition},
		},
		Boot: machine.BootStatus{Slot: slot},
	}
}

// The records here are for version 0.2.0, and this test binary's
// machine.Version is "dev", so these tests also show that promotion
// reads the version from the facts.
func TestPromotesTheReleaseThisBootProves(t *testing.T) {
	root := t.TempDir()
	store := machine.SystemReleases(root)
	raw, _, err := machine.RenderSystemRelease("0.2.0", "B", "sha256:abcd")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteStaged(raw); err != nil {
		t.Fatal(err)
	}

	settleSystemReleaseLifecycle(root, slotFacts("0.2.0", "B"), nil)

	if staged, _ := store.LoadStaged(); staged != nil {
		t.Error("the staged record remains after promotion, want it removed")
	}
	if proven, _ := store.LoadProven(); string(proven) != string(raw) {
		t.Error("the proven record differs from the staged bytes, want them equal")
	}
}

func TestPromotionRequiresTheMatchingSlotAndVersion(t *testing.T) {
	cases := map[string]struct {
		recordSlot, recordVersion, bootVersion, bootSlot string
	}{
		"wrong slot":    {"B", "0.2.0", "0.2.0", "A"},
		"wrong version": {"B", "0.2.0", "0.1.0", "B"},
		"no slot":       {"B", "0.2.0", "0.2.0", ""},
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

			settleSystemReleaseLifecycle(root, slotFacts(tt.bootVersion, tt.bootSlot), nil)

			if staged, _ := store.LoadStaged(); staged == nil {
				t.Error("the staged record was promoted, want it left staged because this boot did not run the trial")
			}
		})
	}
}

func TestRecordsTheRunningReleaseAsTheFirstProvenRecord(t *testing.T) {
	root := t.TempDir()
	store := machine.SystemReleases(root)

	settleSystemReleaseLifecycle(root, slotFacts("0.1.0", "A"), nil)

	proven, _ := store.LoadProven()
	if proven == nil {
		t.Fatal("no proven record, want one for the running release of a machine booted from a slot")
	}
	record, err := machine.ParseSystemRelease(proven)
	if err != nil || record.Slot != "A" || record.Version != "0.1.0" {
		t.Errorf("proven record is %+v (parse error %v), want slot A and version 0.1.0", record, err)
	}

	// A second pass does not write it again.
	settleSystemReleaseLifecycle(root, slotFacts("0.1.0", "A"), nil)
	if again, _ := store.LoadProven(); string(again) != string(proven) {
		t.Error("the second pass rewrote the proven record, want it written once")
	}
}

// When the promotion cannot write, the record stays staged and the
// pass reports a failure, so a later pass promotes it.
func TestAReleasePromotionThatCannotWriteIsRetried(t *testing.T) {
	root := t.TempDir()
	store := machine.SystemReleases(root)
	raw, _, err := machine.RenderSystemRelease("0.2.0", "B", "sha256:abcd")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteStaged(raw); err != nil {
		t.Fatal(err)
	}
	readOnly(t, filepath.Join(root, "system"))
	out := &passOutcome{}

	settleSystemReleaseLifecycle(root, slotFacts("0.2.0", "B"), out)

	staged, _ := store.LoadStaged()
	if staged == nil || len(out.failures) != 1 {
		t.Errorf("staged %q, failures %v; want the record still staged and one failure", staged, out.failures)
	}
}

// When the first proven record cannot be written, the pass reports a
// failure and a later pass writes it.
func TestAFirstProvenReleaseThatCannotBeWrittenIsRetried(t *testing.T) {
	root := t.TempDir()
	readOnly(t, root)
	out := &passOutcome{}

	settleSystemReleaseLifecycle(root, slotFacts("0.1.0", "A"), out)

	if len(out.failures) != 1 {
		t.Errorf("failures = %v, want one", out.failures)
	}
}

// Init checks staged records at boot, so the operator leaves a record
// that does not parse in place and reports no failure.
func TestAnUnparseableStagedReleaseIsLeftForInit(t *testing.T) {
	root := t.TempDir()
	store := machine.SystemReleases(root)
	if err := store.WriteStaged([]byte(":: not a record ::")); err != nil {
		t.Fatal(err)
	}
	out := &passOutcome{}

	settleSystemReleaseLifecycle(root, slotFacts("0.2.0", "B"), out)

	staged, _ := store.LoadStaged()
	if staged == nil || len(out.failures) != 0 || len(out.writes) != 0 {
		t.Errorf("staged %q, failures %v, writes %q; want the record left alone", staged, out.failures, out.writes)
	}
}
