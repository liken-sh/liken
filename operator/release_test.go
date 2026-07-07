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
		{fetchVerified, "False", "Fetched"},
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
