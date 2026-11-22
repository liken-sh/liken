package main

// Tests for status.nextBoot and the ClusterProven condition: which copy
// of the cluster document the next boot would run, and whether it is
// the document that the machine runs now.

import (
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

func TestTheStatusNamesTheDocumentThatTheNextBootRuns(t *testing.T) {
	older, newer := clusterDocument(t), clusterDocument(t, "metrics-server")
	cases := []struct {
		name    string
		store   clusterStore
		running []byte
		source  machine.ManifestSource
		next    []byte
		status  api.ConditionStatus
		reason  string
	}{
		{"a proven copy that is the running document", clusterStore{proven: older}, older,
			machine.ManifestSourceProven, older, api.ConditionTrue, "Current"},
		{"a staged edit that waits for its restart", clusterStore{proven: older, staged: newer}, older,
			machine.ManifestSourceStaged, newer, api.ConditionFalse, "ChangeStaged"},
		{"a proven copy older than the running document", clusterStore{proven: older}, newer,
			machine.ManifestSourceProven, older, api.ConditionFalse, "NextBootDiffers"},
		{"a restart's document that is not promoted yet", clusterStore{proven: older, staged: newer, attempted: machine.ManifestHash(newer)}, newer,
			machine.ManifestSourceProven, older, api.ConditionFalse, "NextBootDiffers"},
		{"a staged copy that init is applying", clusterStore{proven: older, staged: newer, attempted: machine.ManifestHash(newer)}, older,
			machine.ManifestSourceProven, older, api.ConditionTrue, "Current"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, store := writeClusterStore(t, c.store)
			facts := partitionBackedFacts(machine.ManifestSourceProven, machine.ManifestHash(c.running))

			next, condition := nextBootOfCluster(store, seedFile(t, testCluster), facts, runningDocument(t, c.running))

			want := machine.NextBootStatus{ClusterManifestSource: c.source, ClusterManifestHash: machine.ManifestHash(c.next)}
			if next != want {
				t.Errorf("status.nextBoot is %+v, want %+v", next, want)
			}
			if condition.Type != "ClusterProven" || condition.Status != c.status || condition.Reason != c.reason {
				t.Errorf("the condition is %+v, want ClusterProven %s with reason %s", condition, c.status, c.reason)
			}
		})
	}
}

// A memory-backed machineState has no store, so every boot runs the
// image's seed, and the machine runs the seed now.
func TestAMemoryBackedMachineRunsTheSeedAtItsNextBoot(t *testing.T) {
	facts := partitionBackedFacts(machine.ManifestSourceSeed, machine.ManifestHash([]byte(testCluster)))
	facts.Storage.MachineState.Backing = machine.BackingMemory

	next, condition := nextBootOfCluster(machine.ClusterManifests(t.TempDir()), seedFile(t, testCluster), facts, runningDocument(t, []byte(testCluster)))

	if next.ClusterManifestSource != machine.ManifestSourceSeed || condition.Status != api.ConditionTrue {
		t.Errorf("status.nextBoot is %+v and the condition is %+v, want the seed and True", next, condition)
	}
}

// The next boot skips a proven copy that does not parse and runs the
// seed, as init does.
func TestAProvenCopyThatDoesNotParseLeavesTheSeedForTheNextBoot(t *testing.T) {
	_, store := writeClusterStore(t, clusterStore{proven: []byte("kind: [")})
	facts := partitionBackedFacts(machine.ManifestSourceSeed, machine.ManifestHash([]byte(testCluster)))

	next, condition := nextBootOfCluster(store, seedFile(t, testCluster), facts, runningDocument(t, []byte(testCluster)))

	if next.ClusterManifestSource != machine.ManifestSourceSeed || condition.Status != api.ConditionTrue {
		t.Errorf("status.nextBoot is %+v and the condition is %+v, want the seed and True", next, condition)
	}
}

func TestClusterProvenIsUnknownWithoutTheBootRecordOrTheRunningDocument(t *testing.T) {
	cases := []struct {
		name    string
		facts   *machine.MachineStatus
		running func(t *testing.T) runningCluster
	}{
		{"no facts", nil, func(t *testing.T) runningCluster { return runningDocument(t, []byte(testCluster)) }},
		{"no running document", partitionBackedFacts(machine.ManifestSourceProven, "abc"), func(t *testing.T) runningCluster {
			return readRunningCluster(filepath.Join(t.TempDir(), "missing.yaml"))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			next, condition := nextBootOfCluster(machine.ClusterManifests(t.TempDir()), seedFile(t, testCluster), c.facts, c.running(t))

			if next != (machine.NextBootStatus{}) || condition.Status != api.ConditionUnknown || condition.Reason != "FactsIncomplete" {
				t.Errorf("status.nextBoot is %+v and the condition is %+v, want nothing and Unknown FactsIncomplete", next, condition)
			}
		})
	}
}

// A staged edit gives the phase that ClusterConverged already gives it,
// and a proven copy that differs from the running document is a fault.
func TestTheClusterProvenReasonsGiveTheirPhases(t *testing.T) {
	cases := []struct {
		reason string
		phase  api.Phase
	}{
		{"ChangeStaged", api.PhaseUpdatePending},
		{"NextBootDiffers", api.PhaseDegraded},
		{"StoreUnreadable", api.PhaseDegraded},
	}
	for _, c := range cases {
		t.Run(c.reason, func(t *testing.T) {
			condition := api.Condition{Type: "ClusterProven", Status: api.ConditionFalse, Reason: c.reason}

			if phase := decidePhase([]api.Condition{condition}); phase != c.phase {
				t.Errorf("the phase is %s, want %s", phase, c.phase)
			}
		})
	}
}
