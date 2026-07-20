package main

// Choosing the manifest a boot runs under.
//
// The machine's most important input is a file on a disk that it has
// not set up yet. The way out of that circle is the same recognition
// that drives all of storage: this code finds the machineState
// partition by the name written on it, which needs no spec at all.
// Init peeks at it first: mount read-only, read the staged and
// proven manifests, unmount. The unmount matters. The same disk may
// need its partition table rewritten minutes later (a grow), and the
// kernel refuses to re-read the table of a disk that is in use.
// Because the peek leaves nothing mounted, machineState's own disk
// stays growable.
//
// Then the attempt order applies: a staged manifest is tried first,
// and if its storage cannot be reconciled, the boot does not stop.
// This code quarantines the staged manifest durably, with the
// reason, tears storage back down, and reconciles the proven manifest
// (the last spec that actually booted) instead. The machine comes up
// degraded but present, which beats a machine that is off.
// Power-off remains the answer only when even the proven spec fails,
// because at that point the machine has no configuration it can
// trust.
//
// The image's baked-in manifest takes part only when the machine has
// no machineState partition, or that partition is empty. It seeds
// the very first boot, and that boot's success writes it down as the
// first proven manifest. From then on, the file in the image plays no
// further role.
//
// A first boot runs end to end like this: no machineState partition
// exists, so this code chooses the seed. Reconciliation claims the
// disks (machineState is first in canonical order, so it becomes
// partition 1). The role mounts at its path. The seed's bytes become
// proven.yaml. Every crash point along the way resumes correctly: a
// claim that died before mkfs completes through recognition (the
// name goes on first), and an empty manifests directory simply
// re-selects the seed.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/machine"
)

// manifestPeekPoint is the private mountpoint for the early look at
// machineState, used and released before storage reconciliation runs.
const manifestPeekPoint = "/.liken-machine-state"

// A manifestChoice is one candidate manifest and its identity: the
// hash travels into facts (and rejections) so the operator can tell
// exactly which bytes this boot ran.
type manifestChoice struct {
	m      *machine.Machine
	raw    []byte
	source machine.ManifestSource
	hash   string
}

// manifestCandidates is everything the peek learned.
type manifestCandidates struct {
	part      *partition // the machineState partition; nil on first boot
	staged    *manifestChoice
	proven    *manifestChoice
	rejection *machine.Rejection // the standing quarantine record, if any
}

// findMachineStatePartition scans for the one partition named
// liken:machineState. A missing partition means a first boot, not an
// error. Two of them is the same cloned-disk ambiguity that
// matchRoles refuses, and this function refuses it for the same
// reason.
func findMachineStatePartition() (*partition, error) {
	var found *partition
	for _, p := range discoverPartitions() {
		if p.partName != machine.PartitionPrefix+"machineState" {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("two partitions claim to be %smachineState (%s and %s); refusing to guess",
				machine.PartitionPrefix, found.name, p.name)
		}
		found = &p
	}
	return found, nil
}

// loadManifestCandidates performs the peek. A machineState partition
// with no filesystem yet (a boot that died between claim and mkfs)
// yields no candidates. The seed carries that boot, and mountRole's
// resumable claiming makes the filesystem.
func loadManifestCandidates() (manifestCandidates, error) {
	var c manifestCandidates
	part, err := findMachineStatePartition()
	if err != nil {
		return c, err
	}
	c.part = part
	if part == nil {
		return c, nil
	}
	dev := devRoot + "/" + part.name
	if !hasExt4(dev) {
		return c, nil
	}

	if err := os.MkdirAll(manifestPeekPoint, 0o755); err != nil {
		return c, err
	}
	// Read-only means "this code writes nothing", not "the device is
	// untouched". Mounting ext4 replays its journal if the last boot
	// died mid-write, which is the filesystem's recovery mechanism
	// working exactly as intended.
	if err := unix.Mount(dev, manifestPeekPoint, "ext4", unix.MS_RDONLY, ""); err != nil {
		return c, fmt.Errorf("peeking at machineState on %s: %w", dev, err)
	}
	// The store returns bytes. Whether they parse as a Machine is
	// this caller's question, asked below after the peek unmounts.
	store := machine.MachineManifests(manifestPeekPoint)
	stagedRaw, stagedErr := store.LoadStaged()
	provenRaw, provenErr := store.LoadProven()
	c.rejection, _ = store.LoadRejection()
	if err := unix.Unmount(manifestPeekPoint, 0); err != nil {
		// Loud but not fatal: if this mount lingers, a later rewrite
		// of this disk's table fails with EBUSY and names the problem.
		fmt.Fprintf(os.Stderr, "liken: storage: unmounting the manifest peek: %v\n", err)
	}
	_ = os.Remove(manifestPeekPoint)

	if provenErr != nil {
		fmt.Fprintf(os.Stderr, "liken: storage: the proven manifest is unreadable: %v\n", provenErr)
	} else if provenRaw != nil {
		proven, err := machine.Parse(provenRaw)
		if err != nil {
			// A proven manifest that does not parse is a corrupted
			// last-known-good record. This code reports it and
			// continues without one, rather than fail over a file
			// whose whole job is recovery.
			fmt.Fprintf(os.Stderr, "liken: storage: the proven manifest is unreadable: %v\n", err)
		} else {
			c.proven = &manifestChoice{m: proven, raw: provenRaw, source: machine.ManifestSourceProven, hash: machine.ManifestHash(provenRaw)}
		}
	}

	// A staged manifest is checked before it even becomes a
	// candidate. One that does not parse, or that does not declare
	// the machineState role its own lifecycle lives on, would fail
	// every future boot the same way, so this code rejects it without
	// trying it.
	if stagedErr != nil {
		c.rejection = rejectStaged(part, nil, fmt.Sprintf("the staged manifest is unreadable: %v", stagedErr))
	} else if stagedRaw != nil {
		staged, err := machine.Parse(stagedRaw)
		switch {
		case err != nil:
			c.rejection = rejectStaged(part, stagedRaw, fmt.Sprintf("the staged manifest does not parse: %v", err))
		case staged.Spec.Storage.MachineState == nil:
			c.rejection = rejectStaged(part, stagedRaw, "the staged manifest does not declare the machineState role its own lifecycle lives on")
		default:
			c.staged = &manifestChoice{m: staged, raw: stagedRaw, source: machine.ManifestSourceStaged, hash: machine.ManifestHash(stagedRaw)}
		}
	}
	return c, nil
}

// rejectStagedDocument renders the verdict that every staged
// lifecycle shares: announce the reason, quarantine the document
// durably (the store moves the bytes aside, so the same document is
// never tried twice), and return the rejection for this boot's
// facts. The document lifecycles differ in what they stage, such as
// the cluster document, registry credentials, a system release, or
// the Machine manifest itself, but they all reject in the same way.
// The domain and noun exist only to keep each console line specific
// to its owner. The record step is a parameter because most stores
// can write immediately (their filesystem is already mounted), while
// the Machine manifest's rejection must mount machineState around
// the write (rejectStaged below).
//
// The rejection outlasts the boot that rendered it. Each store keeps
// it standing, and because the facts rebuild from scratch every
// boot, each chooser republishes the standing rejection into the
// boot record before it consults anything staged.
func rejectStagedDocument(domain, what string, record func(machine.Rejection) error,
	raw []byte, reason string) *machine.Rejection {
	fmt.Fprintf(os.Stderr, "liken: %s: rejecting the staged %s: %s\n", domain, what, reason)
	rejection := machine.NewRejection(raw, reason, time.Now().UTC())
	if err := record(rejection); err != nil {
		fmt.Fprintf(os.Stderr, "liken: %s: recording the rejection: %v\n", domain, err)
	}
	return &rejection
}

// rejectStaged quarantines the staged Machine manifest with its
// reason. It runs during the peek, before the machineState role is
// properly mounted, so the record step mounts the partition itself.
func rejectStaged(part *partition, raw []byte, reason string) *machine.Rejection {
	return rejectStagedDocument("storage", "manifest", func(rejection machine.Rejection) error {
		return touchMachineState(*part, func(root string) error {
			return machine.MachineManifests(root).Reject(rejection)
		})
	}, raw, reason)
}

// touchMachineState mounts the machineState partition read-write at
// the private point, runs fn against it, and unmounts. This is the
// narrow window in which init allows itself to write manifests
// before, or after, the role is properly mounted.
func touchMachineState(p partition, fn func(root string) error) error {
	if err := os.MkdirAll(manifestPeekPoint, 0o755); err != nil {
		return err
	}
	if err := unix.Mount(devRoot+"/"+p.name, manifestPeekPoint, "ext4", 0, ""); err != nil {
		return err
	}
	ferr := fn(manifestPeekPoint)
	if err := unix.Unmount(manifestPeekPoint, 0); err != nil {
		fmt.Fprintf(os.Stderr, "liken: storage: unmounting %s: %v\n", manifestPeekPoint, err)
	}
	_ = os.Remove(manifestPeekPoint)
	return ferr
}

// errIdentity marks the failures where a machine could not tell
// which manifest is its own. main treats these differently from
// storage failures when it explains the power-off on the console.
var errIdentity = errors.New("machine identity")

// seedPath selects this machine's own file from the manifests
// directory. One image boots many machines, so the image carries a
// manifest per machine, and each boot selects its own: explicitly, by
// the liken.machine=<name> kernel parameter (the one parameter the
// bootloader already sets), or implicitly when there is exactly one
// manifest to choose. This function refuses anything else rather than
// guess. A name that matches no manifest is a typo that someone must
// see, and a directory of manifests with no name given is ambiguity,
// the same situation as two disks that claim the same partition
// name. An empty or absent directory is not an error: a machine with
// no manifest is still a valid machine.
func seedPath(dir, requested string) (string, error) {
	if requested != "" {
		path := filepath.Join(dir, requested+".yaml")
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("%w: liken.machine=%s names no manifest at %s", errIdentity, requested, path)
		}
		return path, nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".yaml") {
			names = append(names, entry.Name())
		}
	}
	switch len(names) {
	case 0:
		return "", nil
	case 1:
		return filepath.Join(dir, names[0]), nil
	default:
		return "", fmt.Errorf("%w: %d manifests in %s and no liken.machine=<name> on the kernel command line to choose among %s; refusing to guess",
			errIdentity, len(names), dir, strings.Join(names, ", "))
	}
}

// loadSeed reads the image's baked-in manifest for this machine,
// selecting by the requested name within the given directory. A seed
// that cannot be selected or parsed is an error rather than a silent
// default. loadSeed runs only on a boot with no proven manifest to
// fall back to, and a first boot under the wrong or empty identity
// could join the wrong cluster or claim the wrong disks. A machine
// that stays down can be fixed. A machine running under the wrong
// identity can do real damage.
func loadSeed(dir, requested string) (*manifestChoice, error) {
	choice := &manifestChoice{m: &machine.Machine{}, source: machine.ManifestSourceSeed}
	path, err := seedPath(dir, requested)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return choice, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: reading %s: %v", errIdentity, path, err)
	}
	m, err := machine.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", errIdentity, path, err)
	}
	fmt.Printf("liken: this is %s (manifest %s)\n", m.Metadata.Name, path)
	choice.m, choice.raw, choice.hash = m, raw, machine.ManifestHash(raw)
	return choice, nil
}

// attemptOrder is the whole preference policy in one place: staged
// before proven. The seed is deliberately not among the candidates.
// It is a first-boot input, not a fallback (a machine that has ever
// proven a manifest never consults the image again), so settleStorage
// loads the seed only when this function returns an empty list.
func attemptOrder(c manifestCandidates) []*manifestChoice {
	switch {
	case c.staged != nil && c.proven != nil:
		return []*manifestChoice{c.staged, c.proven}
	case c.staged != nil:
		return []*manifestChoice{c.staged}
	case c.proven != nil:
		return []*manifestChoice{c.proven}
	default:
		return nil
	}
}

// settleStorage actuates storage under the best available manifest
// and reports which one won. An error means that even the last
// manifest in the attempt order failed (or, wrapped as errIdentity,
// that a first boot could not tell which manifest is its own), and
// the caller stops the boot. The winning choice comes back whole,
// raw bytes and all, because init later publishes those exact bytes
// for the operator.
func settleStorage() (*manifestChoice, machine.StorageStatus, machine.BootStatus, error) {
	status := machine.AllRolesInMemory()
	boot := machine.BootStatus{}

	candidates, err := loadManifestCandidates()
	if err != nil {
		return nil, status, boot, err
	}
	boot.Rejection = candidates.rejection

	attempts := attemptOrder(candidates)
	if len(attempts) == 0 {
		// A machine with no durable manifests is on its first boot.
		// Only now does the image's seed matter, along with the
		// question of which seed belongs to this machine.
		seed, err := loadSeed(machine.MachineManifestDir, bootParamValue("liken.machine"))
		if err != nil {
			return nil, status, boot, err
		}
		attempts = []*manifestChoice{seed}
	}
	for i, choice := range attempts {
		if choice.source != machine.ManifestSourceSeed {
			fmt.Printf("liken: storage: booting under the %s manifest (%.12s)\n", choice.source, choice.hash)
		}
		status, err = reconcileStorage(choice.m.Spec.Storage)
		if err == nil {
			boot.ManifestSource = choice.source
			boot.ManifestHash = choice.hash
			boot.Storage = choice.m.Spec.Storage
			settleManifests(machine.MachineManifests(machine.MachineStateDir), choice, status, &boot)
			return choice, status, boot, nil
		}
		if choice.source == machine.ManifestSourceStaged && i+1 < len(attempts) {
			fmt.Fprintf(os.Stderr, "liken: storage: the staged manifest failed: %v\n", err)
			fmt.Fprintln(os.Stderr, "liken: storage: falling back to the proven manifest")
			teardownStorage()
			rejection := machine.NewRejection(choice.raw, err.Error(), time.Now().UTC())
			if terr := touchMachineState(*candidates.part, func(root string) error {
				return machine.MachineManifests(root).Reject(rejection)
			}); terr != nil {
				fmt.Fprintf(os.Stderr, "liken: storage: recording the rejection: %v\n", terr)
			}
			boot.Rejection = &rejection
			continue
		}
		return choice, status, boot, err
	}
	// attemptOrder never returns an empty list, so the loop always returns.
	panic("unreachable")
}

// settleManifests finishes the lifecycle bookkeeping after a
// successful reconcile. A staged manifest that just proved itself is
// promoted, and a seed's first success becomes the first proven
// manifest. Failures here are loud but not fatal. The machine is up,
// and the next boot simply repeats the step (a staged manifest that
// boots once boots again).
func settleManifests(store machine.ManifestStore, choice *manifestChoice, status machine.StorageStatus, boot *machine.BootStatus) {
	if status.MachineState.Backing != machine.BackingPartition {
		return // nothing durable to keep manifests on
	}
	switch choice.source {
	case machine.ManifestSourceStaged:
		if err := store.Promote(); err != nil {
			fmt.Fprintf(os.Stderr, "liken: storage: promoting the staged manifest: %v\n", err)
			return
		}
		fmt.Printf("liken: storage: the staged manifest is now proven (%.12s)\n", choice.hash)
		boot.ManifestSource = machine.ManifestSourceProven
		boot.Rejection = nil // a success supersedes old history
	case machine.ManifestSourceSeed:
		if len(choice.raw) == 0 {
			return
		}
		if err := store.WriteProven(choice.raw); err != nil {
			fmt.Fprintf(os.Stderr, "liken: storage: recording the seed as proven: %v\n", err)
			return
		}
		fmt.Printf("liken: storage: the seed manifest is now proven (%.12s)\n", choice.hash)
		boot.ManifestSource = machine.ManifestSourceProven
	}
}
