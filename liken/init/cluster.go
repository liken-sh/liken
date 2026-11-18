package main

// Init chooses the Cluster document at boot.
//
// The Cluster document has a staged, a proven, and a seed copy, as the
// Machine manifest does, and init chooses among them the same way.
// init chooses the Machine manifest before it mounts any filesystem,
// because that manifest declares the storage. It chooses the Cluster
// document after storage is up, because only role derivation, the k3s
// configuration, and the time sources read it. By then machineState is
// mounted, so init reads the store in place.
//
// A staged document that does not parse, or is not a Cluster, would
// fail every boot the same way, so init rejects it without a trial boot
// and falls back to proven, then to the seed. init cannot tell whether
// a staged document that parses works. A wrong endpoint, for example,
// shows only as a follower that never joins, which init cannot tell
// apart from a k3s that never starts. So machine-operator promotes the
// staged document when it runs as a pod under it, because that proves
// the machine joined. machine-operator/cluster.go has the operator's
// half, and machine/staging.go defines the attempted marker.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/machine"
)

// checkSeedCluster returns an error when the image's Cluster document
// exists and cannot be read or parsed. An install boot calls it before
// it touches a disk, because the install copies the document into both
// slots, and chooseCluster fails every later boot on a seed that does
// not parse. A missing document is valid, because a machine with no
// Cluster document is its own cluster.
func checkSeedCluster(seedPath string) error {
	raw, err := os.ReadFile(seedPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: reading %s: %v", errIdentity, seedPath, err)
	}
	if _, err := cluster.ParseCluster(raw); err != nil {
		return fmt.Errorf("%w: parsing %s: %v", errIdentity, seedPath, err)
	}
	return nil
}

// chooseCluster returns the Cluster document this boot runs under, both
// parsed and as its exact bytes, and records the choice in the boot
// record. The caller publishes the bytes to /run so the operator can
// detect drift. It prefers a staged document that parses and has not
// had its trial boot, then proven, then the image's seed. A
// memory-backed machine has no durable store and reads only the seed.
// When no document exists, it
// returns nil, because a machine with no Cluster document is its own
// cluster. A seed that exists but does not parse is an error, and the
// caller treats it as fatal because the machine cannot work out its
// role without it.
func chooseCluster(stateRoot, seedPath string, durable bool, boot *machine.BootStatus) (*cluster.Cluster, []byte, error) {
	if durable {
		store := machine.ClusterManifests(stateRoot)

		// The rejection record stays on disk after its document is
		// quarantined, so each boot reports it again in the boot
		// record.
		boot.ClusterRejection, _ = store.LoadRejection()

		if raw, err := store.LoadStaged(); err != nil {
			fmt.Fprintf(os.Stderr, "liken: cluster: reading the staged document: %v\n", err)
		} else if raw != nil {
			hash := machine.ManifestHash(raw)
			attempted, _ := store.LoadAttempted()
			c, perr := cluster.ParseCluster(raw)
			switch {
			case perr != nil:
				boot.ClusterRejection = rejectStagedDocument("cluster", "document", store.Reject,
					raw, fmt.Sprintf("the staged cluster document does not parse: %v", perr))
			case attempted == hash:
				// An earlier boot already tried this document and the
				// operator never promoted it, so the machine did not
				// join its cluster under it. A staged document gets one
				// trial boot.
				boot.ClusterRejection = rejectStagedDocument("cluster", "document", store.Reject,
					raw, "the last boot ran this staged cluster document and never joined the cluster under it")
			default:
				if err := store.WriteAttempted(hash); err != nil {
					fmt.Fprintf(os.Stderr, "liken: cluster: marking the staged document attempted: %v\n", err)
				}
				boot.ClusterManifestSource = machine.ManifestSourceStaged
				boot.ClusterManifestHash = hash
				fmt.Printf("liken: cluster: booting under the staged document %.12s, which the operator's first pass proves\n", hash)
				return c, raw, nil
			}
		}

		if raw, err := store.LoadProven(); err != nil {
			fmt.Fprintf(os.Stderr, "liken: cluster: reading the proven document: %v\n", err)
		} else if raw != nil {
			c, perr := cluster.ParseCluster(raw)
			if perr != nil {
				// A proven document that does not parse is a damaged
				// copy. Fall back to the seed instead of failing the
				// boot over a recovery file.
				fmt.Fprintf(os.Stderr, "liken: cluster: parsing the proven document: %v\n", perr)
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
		return nil, nil, fmt.Errorf("parsing %s: %w", seedPath, err)
	}
	boot.ClusterManifestSource = machine.ManifestSourceSeed
	boot.ClusterManifestHash = machine.ManifestHash(raw)
	return c, raw, nil
}
