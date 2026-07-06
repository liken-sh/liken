package main

// The cluster document's boot-time selection.
//
// The Cluster manifest goes through the same lifecycle as the Machine
// manifest — staged, proven, seed — but its selection is simpler,
// because the two documents answer to different moments of the boot.
// The Machine manifest *drives* storage reconciliation, so init must
// peek at it before any filesystem is properly mounted; the Cluster
// document is only needed after storage settles (its consumers are
// role derivation, k3s configuration, and time sources), so by the
// time it's read, machineState is an ordinary mounted filesystem and
// the store can be read in place.
//
// Vetting happens at the door, like the Machine manifest's: a staged
// document that won't parse or isn't a Cluster would fail every
// future boot the same way, so it is rejected without being tried
// and the boot falls back to proven, then seed. What init cannot do
// is *prove* a staged cluster document — its failure modes are
// downstream (a bad endpoint means the follower never joins, which
// init only discovers by k3s never coming up) — so promotion belongs
// to the operator, whose own existence as a pod is the proof that
// the join worked (cluster.go's operator half, and the attempted
// marker, are the next chapter of this story).

import (
	"fmt"
	"os"
	"time"

	"github.com/chrisguidry/liken/machine"
)

// chooseCluster returns the cluster document this boot runs under and
// records the choice in the boot record: staged (vetted) over proven
// over the image's seed. A memory-backed machine has no durable
// store, so it reads only the seed, every boot. No document anywhere
// is a valid answer — a machine alone is its own cluster — but a
// seed that exists and won't parse is an error the caller treats as
// fatal, because a machine that can't tell its role must not guess.
func chooseCluster(stateRoot, seedPath string, durable bool, boot *machine.BootStatus) (*machine.Cluster, error) {
	if durable {
		store := machine.ClusterManifests(stateRoot)

		// The standing rejection, if any, is republished every boot:
		// facts die with the boot, this record must not.
		boot.ClusterRejection, _ = store.LoadRejection()

		if raw, err := store.LoadStaged(); err != nil {
			fmt.Fprintf(os.Stderr, "liken: cluster: the staged document is unreadable: %v\n", err)
		} else if raw != nil {
			c, perr := machine.ParseCluster(raw)
			if perr != nil {
				boot.ClusterRejection = rejectStagedCluster(store, raw, fmt.Sprintf("the staged cluster document does not parse: %v", perr))
			} else {
				boot.ClusterManifestSource = machine.ManifestSourceStaged
				boot.ClusterManifestHash = machine.ManifestHash(raw)
				fmt.Printf("liken: cluster: booting under the Staged document (%.12s)\n", boot.ClusterManifestHash)
				return c, nil
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
				return c, nil
			}
		}
	}

	raw, err := os.ReadFile(seedPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c, err := machine.ParseCluster(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", seedPath, err)
	}
	boot.ClusterManifestSource = machine.ManifestSourceSeed
	boot.ClusterManifestHash = machine.ManifestHash(raw)
	return c, nil
}

// rejectStagedCluster quarantines a staged cluster document with its
// reason, and reports the rejection for this boot's facts.
func rejectStagedCluster(store machine.ManifestStore, raw []byte, reason string) *machine.Rejection {
	fmt.Fprintf(os.Stderr, "liken: cluster: rejecting the staged document: %s\n", reason)
	rejection := machine.Rejection{
		Hash:       machine.ManifestHash(raw),
		Reason:     reason,
		RejectedAt: time.Now().UTC(),
	}
	if err := store.Reject(rejection); err != nil {
		fmt.Fprintf(os.Stderr, "liken: cluster: recording the rejection: %v\n", err)
	}
	return &rejection
}
