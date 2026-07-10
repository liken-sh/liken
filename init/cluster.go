package main

// The cluster document's boot-time selection.
//
// The Cluster manifest goes through the same lifecycle as the Machine
// manifest (staged, proven, seed), but its selection is simpler,
// because the two documents are needed at different moments of the
// boot. The Machine manifest *drives* storage reconciliation, so init
// must peek at it before any filesystem is properly mounted; the
// Cluster document is only needed after storage settles (its
// consumers are role derivation, k3s configuration, and time
// sources), so by the time it's read, machineState is an ordinary
// mounted filesystem and the store can be read in place.
//
// A staged document is vetted before it is tried, like the Machine
// manifest's: a staged document that won't parse or isn't a Cluster
// would fail every future boot the same way, so it is rejected
// without being tried and the boot falls back to proven, then seed.
// What init cannot do is *prove* a staged cluster document, because
// its failure modes are downstream: a bad endpoint means the
// follower never joins, which init only discovers by k3s never
// coming up. Promotion therefore belongs to the operator, whose own
// existence as a pod is the proof that the join worked. The
// operator's half of the lifecycle, and the attempted marker, are
// described in cluster.go on the operator side.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/liken-sh/liken/machine"
)

// chooseCluster returns the cluster document this boot runs under,
// both parsed and as its exact bytes (which are published to /run for
// the operator's drift detection), and records the choice in the boot
// record. The preference order is staged (vetted), then proven, then
// the image's seed. A memory-backed machine has no durable store, so
// it reads only the seed, every boot. No document anywhere is a valid
// answer, because a machine alone is its own cluster. A seed that
// exists and won't parse is an error the caller treats as fatal,
// because a machine that can't tell its role must not guess.
func chooseCluster(stateRoot, seedPath string, durable bool, boot *machine.BootStatus) (*machine.Cluster, []byte, error) {
	if durable {
		store := machine.ClusterManifests(stateRoot)

		// The standing rejection is republished into the boot record
		// every boot (rejectStagedDocument explains why).
		boot.ClusterRejection, _ = store.LoadRejection()

		if raw, err := store.LoadStaged(); err != nil {
			fmt.Fprintf(os.Stderr, "liken: cluster: the staged document is unreadable: %v\n", err)
		} else if raw != nil {
			hash := machine.ManifestHash(raw)
			attempted, _ := store.LoadAttempted()
			c, perr := machine.ParseCluster(raw)
			switch {
			case perr != nil:
				boot.ClusterRejection = rejectStagedDocument("cluster", "document", store.Reject,
					raw, fmt.Sprintf("the staged cluster document does not parse: %v", perr))
			case attempted == hash:
				// A previous boot ran this exact document and nobody
				// promoted it: the machine never joined its cluster
				// under it, and the operator that would have carried
				// the proof never ran. A staged document gets exactly
				// one trial boot.
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
			c, perr := machine.ParseCluster(raw)
			if perr != nil {
				// A proven document that won't parse is a corrupted
				// last-known-good: report it and fall through to the
				// seed rather than dying over a recovery file.
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
	c, err := machine.ParseCluster(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", seedPath, err)
	}
	boot.ClusterManifestSource = machine.ManifestSourceSeed
	boot.ClusterManifestHash = machine.ManifestHash(raw)
	return c, raw, nil
}
