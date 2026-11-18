package main

// containerd does not write an image unpack in a crash-safe order. A
// machine that crashes during an unpack can keep a database that marks
// the image as unpacked over torn files, and containerd never unpacks
// that digest again. machine/imports.go describes the failure.
//
// So before k3s starts, init hashes the image tarballs and compares
// them with the imported-images record. When the hashes match the
// proven record, init trusts the container store. When they do not,
// the boot is a trial: init writes a staged record, and the machine
// operator promotes it to proven once the images run containers. A
// staged record that still exists at the next boot means that boot
// crashed before the promotion, so the store can be torn. Init then
// deletes the whole store, because that recovery needs no knowledge of
// containerd's internals. k3s imports the OS images again from the
// tarballs and pulls the workload images again from their registries.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/liken-sh/liken/liken/machine"
)

// Tests replace these with temporary directories. The images
// directory is the copy on clusterState that seedClusterState
// refreshed this boot, because those are the files that k3s imports.
var (
	k3sAgentDir  = machine.K3sAgentDir
	k3sImagesDir = filepath.Join(machine.K3sAgentDir, "images")
)

// settleImageImports does nothing unless both machineState and
// clusterState are durable. Without a durable machineState there is
// nowhere to write the record, and without a durable clusterState the
// container store is empty on every boot, so it cannot be torn.
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
		fmt.Fprintf(os.Stderr, "liken: imports: rendering the imported-images record: %v\n", err)
		return
	}
	store := machine.ImportedImagesStore(stateRoot)

	// Only the existence of a staged record matters, so an unreadable
	// one also discards the store. A crash during the discard is safe,
	// because the staged record stays until the operator promotes it,
	// and the next boot discards again.
	staged, err := store.LoadStaged()
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: imports: reading the staged record: %v, so the container store is discarded\n", err)
	}
	if staged != nil || err != nil {
		discardContainerStore()
		boot.ImportsDiscarded = true
		fmt.Println("liken: imports: a staged record remains from the previous boot, so that boot did not prove its image imports. Discarding the container store. k3s unpacks the OS images again from this boot's tarballs and pulls the workload images again")
	} else if proven, perr := store.LoadProven(); perr == nil && proven != nil && machine.ManifestHash(proven) == hash {
		boot.ImportsSource = machine.ManifestSourceProven
		boot.ImportsHash = hash
		fmt.Printf("liken: imports: the hash of the %d image tarballs, %.12s, matches the proven record\n", len(digests), hash)
		return
	}

	// A trial: the first boot, new tarballs, or the boot after a
	// discard.
	if err := store.WriteStaged(raw); err != nil {
		fmt.Fprintf(os.Stderr, "liken: imports: writing the staged record: %v\n", err)
		return
	}
	boot.ImportsSource = machine.ManifestSourceStaged
	boot.ImportsHash = hash
	fmt.Printf("liken: imports: staged a record for the %d image tarballs, %.12s. The machine operator promotes it to proven when the images run containers\n", len(digests), hash)
}

// discardContainerStore deletes everything in the k3s agent directory
// except images/, which holds this boot's tarballs from the OS image.
// k3s creates the rest again from the join token and the cluster: the
// containerd store, the kubelet's credentials, and caches. The
// credentials are deleted too, because the same crash can leave a
// zeroed key file, and k3s cannot start the agent with one.
func discardContainerStore() {
	entries, err := os.ReadDir(k3sAgentDir)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: imports: reading the directory %s: %v\n", k3sAgentDir, err)
		return
	}
	for _, entry := range entries {
		if entry.Name() == "images" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(k3sAgentDir, entry.Name())); err != nil {
			fmt.Fprintf(os.Stderr, "liken: imports: deleting %s from the k3s agent directory: %v\n", entry.Name(), err)
		}
	}
}
