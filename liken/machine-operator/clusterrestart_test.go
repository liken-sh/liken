package main

// Tests for a pass that runs while init applies a cluster document with
// a k3s restart. Init writes the attempted marker, then publishes the
// running document, then writes the boot record. A pass reads the facts
// once at its start, so it can read a boot record from before the
// restart and a running document from after it. Each test sets up the
// files as one such pass reads them, and runs the cluster document's
// steps of a pass in the order that reconcile runs them.

import (
	"slices"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/kubernetes/fakeapi"
	"github.com/liken-sh/liken/liken/machine"
)

// featuresAPI serves the lab Cluster with the given features turned on.
func featuresAPI(features ...string) *fakeapi.Server {
	declared := map[string]any{}
	for _, slug := range features {
		declared[slug] = map[string]any{}
	}
	spec := map[string]any{"leaders": []any{"node-1"}, "endpoint": "https://10.10.0.1:6443"}
	if len(features) > 0 {
		spec["features"] = declared
	}
	return fakeapi.New(map[string]*fakeapi.Collection{
		kubernetes.ClustersPath: {APIVersion: "liken.sh/v1alpha1", Kind: "Cluster", Items: []map[string]any{
			fakeapi.Object("liken.sh/v1alpha1", "Cluster", "", "lab", map[string]any{"spec": spec}),
		}},
	})
}

// runClusterPass runs the cluster document's steps of a pass: the
// promotion, the convergence against the Cluster that api serves, and
// the writes that the convergence decides. It returns the pass's
// outcome.
func runClusterPass(t *testing.T, api *fakeapi.Server, root string, facts *machine.MachineStatus, running runningCluster) *passOutcome {
	t.Helper()
	out := &passOutcome{}
	settleClusterLifecycle(root, facts, running, machineEvents{}, out)
	r := &reader{client: testClient(t, api)}
	store := machine.ClusterManifests(root)
	conv, _ := convergeClusterDocument(r, store, "lab", machineWithPolicy(machine.RebootAuto), facts, running, turnGranted)
	carryOutConvergence(conv, store, t.TempDir(), "cluster document", testNow, out)
	return out
}

// The pass that lost documents on a single-leader cluster: it read the
// facts before the restart wrote its boot record, and it read the
// running document after the restart published it. That pass withdrew
// the staged copy and left proven.yaml on the older document.
func TestAPassThatRacesARestartKeepsTheDocumentThatTheMachineRuns(t *testing.T) {
	cases := []struct {
		name  string
		facts func(older []byte) *machine.MachineStatus
	}{
		{"facts of a boot that ran the proven document", func(older []byte) *machine.MachineStatus {
			return partitionBackedFacts(machine.ManifestSourceProven, machine.ManifestHash(older))
		}},
		{"facts of an earlier restart", func(older []byte) *machine.MachineStatus {
			return partitionBackedFacts(machine.ManifestSourceStaged, machine.ManifestHash(older))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				older, newer := clusterDocument(t), clusterDocument(t, "metrics-server")
				root, store := writeClusterStore(t, clusterStore{proven: older, staged: newer, attempted: machine.ManifestHash(newer)})

				out := runClusterPass(t, featuresAPI("metrics-server"), root, c.facts(older), runningDocument(t, newer))

				staged, _ := store.LoadStaged()
				proven, _ := store.LoadProven()
				if staged != nil || string(proven) != string(newer) {
					t.Errorf("staged %q and proven %q, want the running document proven", staged, proven)
				}
				if slices.Contains(out.writes, "withdrawing the staged cluster document") {
					t.Errorf("the pass wrote %q, want no withdraw", out.writes)
				}
			})
		})
	}
}

// A document staged for an edit that the Cluster then takes back, before
// any restart applies it, is withdrawn, and the proven copy stays the
// document that the machine runs.
func TestAStagedDocumentThatNeverRanIsWithdrawnWhenTheClusterReturns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		older, newer := clusterDocument(t), clusterDocument(t, "metrics-server")
		root, store := writeClusterStore(t, clusterStore{proven: older, staged: newer})

		out := runClusterPass(t, featuresAPI(), root, partitionBackedFacts(machine.ManifestSourceProven, machine.ManifestHash(older)), runningDocument(t, older))

		staged, _ := store.LoadStaged()
		proven, _ := store.LoadProven()
		if staged != nil || string(proven) != string(older) || !slices.Contains(out.writes, "withdrawing the staged cluster document") {
			t.Errorf("staged %q, proven %q, writes %q; want the staged copy withdrawn and the running document still proven", staged, proven, out.writes)
		}
	})
}

// Init writes the attempted marker before it actuates the features, and
// publishes the running document after. A pass between the two reads
// the older running document. The Cluster has been edited back to that
// document, but the staged copy is about to run, so the pass keeps it.
func TestAStagedDocumentThatInitIsApplyingIsKept(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		older, newer := clusterDocument(t), clusterDocument(t, "metrics-server")
		root, store := writeClusterStore(t, clusterStore{proven: older, staged: newer, attempted: machine.ManifestHash(newer)})

		runClusterPass(t, featuresAPI(), root, partitionBackedFacts(machine.ManifestSourceProven, machine.ManifestHash(older)), runningDocument(t, older))

		staged, _ := store.LoadStaged()
		proven, _ := store.LoadProven()
		if string(staged) != string(newer) || string(proven) != string(older) {
			t.Errorf("staged %q and proven %q, want the document that init applies still staged", staged, proven)
		}
	})
}

// A pass can read no attempted marker, decide to withdraw, and lose the
// race to a restart that applies the staged copy before the withdraw.
// The next pass finds the machine running a document that the store
// has nowhere, records it as proven, and stages the Cluster's document
// again.
func TestTheNextPassRecordsADocumentThatAWithdrawOvertook(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		older, newer := clusterDocument(t), clusterDocument(t, "metrics-server")
		root, store := writeClusterStore(t, clusterStore{proven: older})

		runClusterPass(t, featuresAPI(), root, partitionBackedFacts(machine.ManifestSourceStaged, machine.ManifestHash(newer)), runningDocument(t, newer))

		staged, _ := store.LoadStaged()
		proven, _ := store.LoadProven()
		if string(staged) != string(older) || string(proven) != string(newer) {
			t.Errorf("staged %q and proven %q, want the running document proven and the Cluster's document staged", staged, proven)
		}
	})
}
