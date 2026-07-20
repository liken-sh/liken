package main

// Crash-safe image imports: init's half of the protocol.
//
// The machine package's imports.go explains why this file exists:
// containerd's unpack is not crash-ordered. A machine that dies at
// the wrong moment keeps a store that says "unpacked" over torn
// files, and containerd never re-unpacks a digest that it has a
// record for, so the damage is permanent. This file makes the
// decision that prevents that damage, once per boot, before k3s can
// touch the store: trust the store, discard it, or put new tarballs
// on trial.
//
// The rule rests on one bit of state. A staged imports record stands
// from the moment a trial boots until the operator proves it, so a
// boot that finds one standing knows the previous boot died unproven
// and the store may be lying. The only safe move that depends on no
// other component's internals is to discard the store completely.
// Every OS image unpacks fresh from the tarballs that this boot
// carries. Workload images re-pull from their registries (cheaply,
// when the embedded registry shares them between peers). The agent's
// credentials re-mint from the join token. The cost is bounded and
// rare: only a machine that died inside a window of a few minutes,
// and only on boots that had something new to unpack, pays this cost.
//
// Most boots pay only one hash pass: the tarballs match the proven
// record, and the store is trusted.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/liken-sh/liken/machine"
)

// These are package variables rather than constants, so tests can
// point the settle pass at trees of their own making. The images
// directory is read from clusterState (where seedClusterState
// refreshed it this boot) rather than from the image's baked copy,
// because clusterState mounts over the seed tree, and these are the
// exact bytes that k3s will hand to containerd. Hashing these bytes,
// rather than trusting a build-time claim about them, is also what
// catches a tarball whose own copy tore.
var (
	k3sAgentDir  = machine.K3sAgentDir
	k3sImagesDir = filepath.Join(machine.K3sAgentDir, "images")
)

// settleImageImports decides whether this boot's container store can
// be trusted, before k3s ever starts. It runs only when both sides
// of the question are durable. Without machineState, there is nowhere
// to remember a trial. Without durable clusterState, the store resets
// with every boot and cannot wedge in the first place.
func settleImageImports(stateRoot string, durable, clusterDurable bool, boot *machine.BootStatus) {
	if !durable || !clusterDurable {
		return
	}

	digests, err := machine.HashImageTarballs(k3sImagesDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: imports: hashing the image tarballs: %v\n", err)
		return
	}
	raw, hash, err := machine.RenderImportedImages(digests)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: imports: rendering the record: %v\n", err)
		return
	}
	store := machine.ImportedImagesStore(stateRoot)

	// A staged record's existence is the whole question. Its content
	// does not matter (even an unreadable record marks a dead trial),
	// so there is nothing to parse and nothing to reject. The
	// fallback is not an older document; it is a clean store.
	// Discarding is safe to interrupt for the same reason: the record
	// stays standing until the operator promotes it, so a partial
	// discard simply runs again on the next boot.
	staged, err := store.LoadStaged()
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: imports: reading the staged record: %v\n", err)
	}
	if staged != nil || err != nil {
		discardContainerStore()
		boot.ImportsDiscarded = true
		fmt.Println("liken: imports: the previous boot's imports were never proven; discarding the container store (OS images re-unpack from this boot's tarballs, workloads re-pull)")
	} else if proven, perr := store.LoadProven(); perr == nil && proven != nil && machine.ManifestHash(proven) == hash {
		// The quiet path, which covers almost every boot: the same
		// tarballs that this store already proved it can serve.
		boot.ImportsSource = machine.ManifestSourceProven
		boot.ImportsHash = hash
		fmt.Printf("liken: imports: %d image tarballs proven (%.12s)\n", len(digests), hash)
		return
	}

	// A trial: new digests, a first boot, or the retry after a
	// discard. This call stages the record durably before k3s can
	// touch the store, so a death anywhere after this line reads
	// correctly on the next boot.
	if err := store.WriteStaged(raw); err != nil {
		fmt.Fprintf(os.Stderr, "liken: imports: staging the record: %v\n", err)
		return
	}
	boot.ImportsSource = machine.ManifestSourceStaged
	boot.ImportsHash = hash
	fmt.Printf("liken: imports: trialing %d image tarballs (%.12s); the operator proves them once they serve\n", len(digests), hash)
}

// discardContainerStore empties the k3s agent directory, and spares
// only the images/ tarballs that this boot just seeded (k3s is about
// to import them, and they came from the image, not from the
// distrusted store). Everything else under agent/ is derived state
// that k3s re-creates from the join token and the cluster: the
// containerd store, the kubelet's credentials, and its caches. The
// same crash window can tear the kubelet's credentials too: a zeroed
// serving key wedges the agent just as surely as a torn snapshot.
func discardContainerStore() {
	entries, err := os.ReadDir(k3sAgentDir)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: imports: reading %s: %v\n", k3sAgentDir, err)
		return
	}
	for _, entry := range entries {
		if entry.Name() == "images" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(k3sAgentDir, entry.Name())); err != nil {
			fmt.Fprintf(os.Stderr, "liken: imports: discarding %s: %v\n", entry.Name(), err)
		}
	}
}
