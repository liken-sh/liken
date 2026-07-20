package main

// The cluster document's boot-time selection.
//
// The Cluster manifest goes through the same lifecycle as the Machine
// manifest (staged, proven, seed), but its selection is simpler,
// because the boot needs the two documents at different moments. The
// Machine manifest drives storage reconciliation, so init must read
// it before the boot properly mounts any filesystem. The boot needs
// the Cluster document only after storage settles, because role
// derivation, k3s configuration, and time sources consume it. By the
// time the boot reads it, machineState is an ordinary mounted
// filesystem, and the code can read the store in place.
//
// The code vets a staged document before it tries the document, the
// same as with the Machine manifest. A staged document that will not
// parse, or is not a Cluster, will fail every future boot the same
// way. So the code rejects it without trying it, and the boot falls
// back to proven, then to seed. Init cannot prove a staged cluster
// document, because its failure modes appear downstream: a bad
// endpoint means the follower never joins, and init discovers this
// only when k3s never starts. Promotion therefore belongs to the
// operator. The operator's own existence as a pod is the proof that
// the join worked. The operator's half of the lifecycle, and the
// attempted marker, are described in cluster.go on the operator
// side.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
)

// chooseCluster returns the cluster document this boot runs under,
// both parsed and as its exact bytes, and records the choice in the
// boot record. The code publishes the exact bytes to /run for the
// operator's drift detection. The preference order is staged
// (vetted), then proven, then the image's seed. A memory-backed
// machine has no durable store, so it reads only the seed, every
// boot. Finding no document anywhere is a valid answer, because a
// machine alone is its own cluster. A seed that exists but will not
// parse is an error the caller treats as fatal, because a machine
// that cannot tell its role must not guess it.
func chooseCluster(stateRoot, seedPath string, durable bool, boot *machine.BootStatus) (*cluster.Cluster, []byte, error) {
	if durable {
		store := machine.ClusterManifests(stateRoot)

		// The code republishes the standing rejection into the boot
		// record every boot (rejectStagedDocument explains why).
		boot.ClusterRejection, _ = store.LoadRejection()

		if raw, err := store.LoadStaged(); err != nil {
			fmt.Fprintf(os.Stderr, "liken: cluster: the staged document is unreadable: %v\n", err)
		} else if raw != nil {
			hash := machine.ManifestHash(raw)
			attempted, _ := store.LoadAttempted()
			c, perr := cluster.ParseCluster(raw)
			switch {
			case perr != nil:
				boot.ClusterRejection = rejectStagedDocument("cluster", "document", store.Reject,
					raw, fmt.Sprintf("the staged cluster document does not parse: %v", perr))
			case attempted == hash:
				// A previous boot ran this exact document, and nobody
				// promoted it. The machine never joined its cluster
				// under the document, and the operator that would
				// have carried the proof never ran. A staged document
				// gets exactly one trial boot.
				boot.ClusterRejection = rejectStagedDocument("cluster", "document", store.Reject,
					raw, "the last boot ran this staged cluster document and never joined the cluster under it")
			default:
				if err := store.WriteAttempted(hash); err != nil {
					fmt.Fprintf(os.Stderr, "liken: cluster: marking the staged document attempted: %v\n", err)
				}
				boot.ClusterManifestSource = machine.ManifestSourceStaged
				boot.ClusterManifestHash = hash
				fmt.Printf("liken: cluster: booting under the Staged document (%.12s); the operator's first pass is the proof\n", hash)
				return c, raw, nil
			}
		}

		if raw, err := store.LoadProven(); err != nil {
			fmt.Fprintf(os.Stderr, "liken: cluster: the proven document is unreadable: %v\n", err)
		} else if raw != nil {
			c, perr := cluster.ParseCluster(raw)
			if perr != nil {
				// A proven document that will not parse is a
				// corrupted last-known-good copy. The code reports it
				// and falls through to the seed, rather than failing
				// the boot over a recovery file.
				fmt.Fprintf(os.Stderr, "liken: cluster: the proven document is unreadable: %v\n", perr)
			} else {
				boot.ClusterManifestSource = machine.ManifestSourceProven
				boot.ClusterManifestHash = machine.ManifestHash(raw)
				return c, raw, nil
			}
		}
	}

	raw, err := os.ReadFile(seedPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	c, err := cluster.ParseCluster(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", seedPath, err)
	}
	boot.ClusterManifestSource = machine.ManifestSourceSeed
	boot.ClusterManifestHash = machine.ManifestHash(raw)
	return c, raw, nil
}
