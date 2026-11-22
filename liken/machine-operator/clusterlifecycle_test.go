package main

// Tests for promotion, the operator's part of the cluster document
// lifecycle. The operator runs only after the machine joined the
// cluster, so a running operator proves the document that the machine
// runs. Each test gives settleClusterLifecycle the running document as
// init published it and the boot facts, and checks what the operator
// writes to the store.

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

func seedFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cluster.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing the seed file: %v", err)
	}
	return path
}

// runningDocument publishes raw as init does and reads it back as a
// pass does.
func runningDocument(t *testing.T, raw []byte) runningCluster {
	t.Helper()
	return readRunningCluster(seedFile(t, string(raw)))
}

// clusterDocument returns the operator's rendering of the lab Cluster
// with the given features turned on, which are the bytes that it
// stages. Each set of features gives other bytes.
func clusterDocument(t *testing.T, features ...string) []byte {
	t.Helper()
	doc := decisionCluster()
	if len(features) > 0 {
		doc.Spec.Features = map[string]*cluster.FeatureConfig{}
		for _, slug := range features {
			doc.Spec.Features[slug] = &cluster.FeatureConfig{}
		}
	}
	raw, _, err := renderCluster(doc.Metadata.Name, doc.Spec)
	if err != nil {
		t.Fatalf("rendering the cluster document: %v", err)
	}
	return raw
}

// clusterStore is what a test writes to the store before a pass.
type clusterStore struct {
	staged, proven []byte
	attempted      string
}

// writeClusterStore writes the files of contents into a store under a
// new machineState root, and returns the root and the store.
func writeClusterStore(t *testing.T, contents clusterStore) (string, machine.ManifestStore) {
	t.Helper()
	root := t.TempDir()
	store := machine.ClusterManifests(root)
	if contents.proven != nil {
		if err := store.WriteProven(contents.proven); err != nil {
			t.Fatalf("writing the proven document: %v", err)
		}
	}
	if contents.staged != nil {
		if err := store.WriteStaged(contents.staged); err != nil {
			t.Fatalf("writing the staged document: %v", err)
		}
	}
	if contents.attempted != "" {
		if err := store.WriteAttempted(contents.attempted); err != nil {
			t.Fatalf("writing the attempted marker: %v", err)
		}
	}
	return root, store
}

// A staged document that the machine runs is promoted, whichever boot
// record the pass read. A restart writes the boot record while a pass
// can be running, so the facts can be older than the running document.
func TestPromotesTheStagedClusterDocumentThatTheMachineRuns(t *testing.T) {
	older, newer := clusterDocument(t), clusterDocument(t, "metrics-server")
	cases := []struct {
		name  string
		facts *machine.MachineStatus
	}{
		{"facts that name the staged document", partitionBackedFacts(machine.ManifestSourceStaged, machine.ManifestHash(newer))},
		{"facts from before a restart of a boot that ran the proven document", partitionBackedFacts(machine.ManifestSourceProven, machine.ManifestHash(older))},
		{"facts from before a restart of a boot that ran the seed", partitionBackedFacts(machine.ManifestSourceSeed, machine.ManifestHash(older))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, store := writeClusterStore(t, clusterStore{proven: older, staged: newer, attempted: machine.ManifestHash(newer)})

			settleClusterLifecycle(root, c.facts, runningDocument(t, newer), machineEvents{}, nil)

			staged, _ := store.LoadStaged()
			proven, _ := store.LoadProven()
			attempted, _ := store.LoadAttempted()
			if staged != nil || string(proven) != string(newer) || attempted != "" {
				t.Errorf("staged %q, proven %q, attempted %q; want the running document proven and nothing staged or attempted", staged, proven, attempted)
			}
		})
	}
}

func TestDoesNotPromoteAStagedDocumentThatTheMachineDoesNotRun(t *testing.T) {
	older, newer := clusterDocument(t), clusterDocument(t, "metrics-server")
	// A newer document was staged after the machine started running
	// this one. It has not had its own trial, and promoting it would
	// skip that trial.
	root, store := writeClusterStore(t, clusterStore{proven: older, staged: newer})

	settleClusterLifecycle(root, partitionBackedFacts(machine.ManifestSourceProven, machine.ManifestHash(older)),
		runningDocument(t, older), machineEvents{}, nil)

	staged, _ := store.LoadStaged()
	proven, _ := store.LoadProven()
	if string(staged) != string(newer) || string(proven) != string(older) {
		t.Errorf("staged %q and proven %q, want the newer document still staged and the running one proven", staged, proven)
	}
}

func TestRecordsTheSeedAsFirstProven(t *testing.T) {
	root, store := writeClusterStore(t, clusterStore{})

	settleClusterLifecycle(root, partitionBackedFacts(machine.ManifestSourceSeed, machine.ManifestHash([]byte(testCluster))),
		runningDocument(t, []byte(testCluster)), machineEvents{}, nil)

	if proven, _ := store.LoadProven(); string(proven) != testCluster {
		t.Errorf("the proven document is %q, want the seed that this boot ran", proven)
	}
}

// Init boots the seed while a proven copy exists only when that copy
// does not parse, as after a rollback to a release that does not know
// one of its fields. The copy is the newest document that a boot
// proved, and a roll forward reads it again, so the seed does not
// replace it.
func TestASeedBootKeepsAProvenCopyThatDoesNotParse(t *testing.T) {
	unknownField := []byte("apiVersion: liken.sh/v1alpha1\nkind: Cluster\nmetadata:\n  name: lab\nspec:\n  leaders: [node-1]\n  fieldOfALaterRelease: true\n")
	root, store := writeClusterStore(t, clusterStore{proven: unknownField})
	out := &passOutcome{}

	settleClusterLifecycle(root, partitionBackedFacts(machine.ManifestSourceSeed, machine.ManifestHash([]byte(testCluster))),
		runningDocument(t, []byte(testCluster)), machineEvents{}, out)

	proven, _ := store.LoadProven()
	if string(proven) != string(unknownField) || len(out.writes) != 0 || len(out.failures) != 0 {
		t.Errorf("proven document %q, writes %q, failures %v, want the store unchanged", proven, out.writes, out.failures)
	}
}

func TestDoesNotTouchAMemoryBackedMachine(t *testing.T) {
	root := t.TempDir()
	facts := partitionBackedFacts(machine.ManifestSourceSeed, "abc")
	facts.Storage.MachineState.Backing = machine.BackingMemory

	settleClusterLifecycle(root, facts, runningDocument(t, []byte(testCluster)), machineEvents{}, nil)

	if entries, _ := os.ReadDir(filepath.Join(root, "cluster")); len(entries) != 0 {
		t.Error("the cluster directory has entries, want none on a memory-backed machine")
	}
}

func TestAPromotionIsAWrite(t *testing.T) {
	root, _ := writeClusterStore(t, clusterStore{staged: []byte(testCluster)})
	out := &passOutcome{}

	settleClusterLifecycle(root, partitionBackedFacts(machine.ManifestSourceStaged, machine.ManifestHash([]byte(testCluster))),
		runningDocument(t, []byte(testCluster)), machineEvents{}, out)

	if !slices.Equal(out.writes, []string{"promoting the cluster document"}) {
		t.Errorf("writes = %q, want [promoting the cluster document]", out.writes)
	}
}

// A promotion that cannot write leaves the document staged and records
// a failure, so the loop retries and a later pass promotes it.
func TestAPromotionThatCannotWriteIsRetried(t *testing.T) {
	root, store := writeClusterStore(t, clusterStore{staged: []byte(testCluster)})
	readOnly(t, filepath.Join(root, "cluster"))
	out := &passOutcome{}

	settleClusterLifecycle(root, partitionBackedFacts(machine.ManifestSourceStaged, machine.ManifestHash([]byte(testCluster))),
		runningDocument(t, []byte(testCluster)), machineEvents{}, out)

	staged, _ := store.LoadStaged()
	if staged == nil || len(out.failures) != 1 {
		t.Errorf("staged document %q and failures %v, want the document still staged and one failure", staged, out.failures)
	}
}

// A seed that cannot be recorded records a failure, so the loop
// retries, as it does for a promotion.
func TestASeedThatCannotBeRecordedIsRetried(t *testing.T) {
	root := t.TempDir()
	readOnly(t, root)
	out := &passOutcome{}

	settleClusterLifecycle(root, partitionBackedFacts(machine.ManifestSourceSeed, machine.ManifestHash([]byte(testCluster))),
		runningDocument(t, []byte(testCluster)), machineEvents{}, out)

	if len(out.failures) != 1 {
		t.Errorf("failures = %v, want one", out.failures)
	}
}

// A pass writes nothing when the proven copy is the running document,
// or when init has published no running document.
func TestASettledStoreGetsNoWrite(t *testing.T) {
	cases := []struct {
		name    string
		running func(t *testing.T) runningCluster
	}{
		{"a proven copy that is the running document", func(t *testing.T) runningCluster { return runningDocument(t, []byte(testCluster)) }},
		{"no running document", func(t *testing.T) runningCluster {
			return readRunningCluster(filepath.Join(t.TempDir(), "missing.yaml"))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, store := writeClusterStore(t, clusterStore{proven: []byte(testCluster)})
			out := &passOutcome{}

			settleClusterLifecycle(root, partitionBackedFacts(machine.ManifestSourceProven, machine.ManifestHash([]byte(testCluster))),
				c.running(t), machineEvents{}, out)

			proven, _ := store.LoadProven()
			if string(proven) != testCluster || len(out.writes) != 0 || len(out.failures) != 0 {
				t.Errorf("proven document %q, writes %q, failures %v, want the store unchanged", proven, out.writes, out.failures)
			}
		})
	}
}

// A machine that runs a document from its store, with no staged copy
// of it and another document as proven.yaml, would run that other
// document at its next boot. An earlier release left machines in this
// state when it withdrew a staged document that a restart had applied.
// The pass writes the running document as proven, with no reboot.
func TestAProvenCopyThatIsNotTheRunningDocumentIsReplaced(t *testing.T) {
	older, newer := clusterDocument(t), clusterDocument(t, "metrics-server")
	cases := []struct {
		name   string
		source machine.ManifestSource
		proven []byte
	}{
		{"a restart's document whose staged copy was withdrawn", machine.ManifestSourceStaged, older},
		{"a proven copy that was replaced while the machine ran", machine.ManifestSourceProven, older},
		{"no proven copy at all", machine.ManifestSourceStaged, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, store := writeClusterStore(t, clusterStore{proven: c.proven})
			out := &passOutcome{}

			settleClusterLifecycle(root, partitionBackedFacts(c.source, machine.ManifestHash(newer)),
				runningDocument(t, newer), machineEvents{}, out)

			proven, _ := store.LoadProven()
			if string(proven) != string(newer) || !slices.Equal(out.writes, []string{"recording the running cluster document as proven"}) {
				t.Errorf("proven %q and writes %q, want the running document proven in one write", proven, out.writes)
			}
		})
	}
}

func TestReplacingTheProvenCopyPostsAnEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		older, newer := clusterDocument(t), clusterDocument(t, "metrics-server")
		root, _ := writeClusterStore(t, clusterStore{proven: older})
		_, recorder, recorded := recordingClient(t, http.NotFoundHandler())
		notes := machineEvents{recorder, machineReference(machineWithPolicy(""))}

		settleClusterLifecycle(root, partitionBackedFacts(machine.ManifestSourceStaged, machine.ManifestHash(newer)),
			runningDocument(t, newer), notes, &passOutcome{})

		want := []string{"Normal ClusterProvenRepaired: This machine runs the cluster document " + machine.ManifestHash(newer)[:12] +
			" and its store had the proven cluster document " + machine.ManifestHash(older)[:12] +
			", so the operator recorded the running document as proven, and the next boot runs it"}
		if got := posted(recorded); !slices.Equal(got, want) {
			t.Errorf("the Events are %q, want %q", got, want)
		}
	})
}
