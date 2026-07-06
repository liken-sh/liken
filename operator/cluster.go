package main

// The operator's half of the cluster document lifecycle: promotion.
//
// Init boots a staged cluster document tentatively — it cannot prove
// one, because the document's failure modes are downstream of the
// boot (a bad endpoint just means the machine never joins, which init
// only sees as k3s never settling). The proof is the operator itself:
// it runs as a pod, so if this code is executing, then containerd,
// the kubelet, and the machine's registration with its cluster all
// work under the document this boot ran. That is the moment the
// staged document has earned proven, and the operator holds the pen
// (it already has the read-write machineState mount it stages
// through).
//
// The same authority records a first boot's seed as the first proven
// copy, which is what closes the loop for a machine that has never
// had a staged document: from then on the durable store, not the
// image, carries the cluster document forward.

import (
	"fmt"
	"os"

	"github.com/chrisguidry/liken/machine"
)

// settleClusterLifecycle promotes whatever this boot proved. It runs
// every reconcile pass and is idempotent: once promoted (or when a
// newer document is staged for its own proving boot), there is
// nothing left to do. The facts identify exactly which bytes this
// boot ran; the operator promotes those bytes and nothing else.
func settleClusterLifecycle(root, seedPath string, facts *machine.MachineStatus) {
	if facts == nil || facts.Storage.MachineState.Backing != machine.BackingPartition {
		return // nothing durable to settle
	}
	store := machine.ClusterManifests(root)

	switch facts.Boot.ClusterManifestSource {
	case machine.ManifestSourceStaged:
		raw, err := store.LoadStaged()
		if err != nil || raw == nil {
			return // already promoted, or nothing to see
		}
		if machine.ManifestHash(raw) != facts.Boot.ClusterManifestHash {
			// A newer document arrived since this boot: it hasn't had
			// its proving boot, and promoting it would skip the trial.
			return
		}
		if err := store.Promote(); err != nil {
			fmt.Fprintf(os.Stderr, "promoting the cluster document: %v\n", err)
			return
		}
		fmt.Printf("the cluster document proved out; %.12s is now proven\n", facts.Boot.ClusterManifestHash)

	case machine.ManifestSourceSeed:
		if proven, err := store.LoadProven(); proven != nil || err != nil {
			return
		}
		raw, err := os.ReadFile(seedPath)
		if err != nil {
			return
		}
		if machine.ManifestHash(raw) != facts.Boot.ClusterManifestHash {
			// The seed file changed since this machine booted;
			// recording it would prove bytes nobody ran.
			return
		}
		if err := store.WriteProven(raw); err != nil {
			fmt.Fprintf(os.Stderr, "recording the seed cluster document as proven: %v\n", err)
			return
		}
		fmt.Printf("the seed cluster document is now proven (%.12s)\n", facts.Boot.ClusterManifestHash)
	}
}
