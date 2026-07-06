package main

// Choosing the manifest a boot runs under.
//
// The machine's most important input is a file on a disk it hasn't
// set up yet. The way out of that circle is the same recognition that
// drives all of storage: the machineState partition is found by the
// name written on it, which needs no spec at all. Init peeks at it
// first thing: mount read-only, read the staged and proven manifests,
// unmount. The unmount matters: the same disk may need its partition
// table rewritten minutes later (a grow), and the kernel refuses to
// re-read the table of a disk in use. Because the peek leaves nothing
// mounted, machineState's own disk stays growable.
//
// Then the attempt order: a staged manifest gets tried first, and if
// its storage can't be reconciled, the boot does not stop. The staged
// manifest is quarantined (durably, with the reason), storage is torn
// back down, and the proven manifest (the last spec that actually
// booted) is reconciled instead: the machine comes up degraded but
// present, which beats a machine that is off. Power-off remains the
// answer only when even the proven spec fails, because at that point
// the machine has no configuration it can trust.
//
// The image's baked-in manifest participates only when the machine
// has no machineState partition or it's empty: it seeds the very
// first boot, and that boot's success writes it down as the first
// proven manifest. From then on the file in the image is inert.
//
// First boot, end to end: no machineState partition exists, so the
// seed is chosen; reconciliation claims the disks (machineState is
// first in canonical order, so it's partition 1); the role mounts at
// its path; the seed's bytes become proven.yaml. Every crash point
// along the way resumes: a claim that died before mkfs is finished by
// recognition (the name goes on first), and an empty manifests
// directory just re-selects the seed.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/chrisguidry/liken/machine"
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
	source string
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
// liken:machineState. Absent is a first boot, not an error; two of
// them is the same cloned-disk ambiguity matchRoles refuses, refused
// here for the same reason.
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

// loadManifestCandidates is the peek. A machineState partition with
// no filesystem yet (a boot that died between claim and mkfs) yields
// no candidates; the seed carries that boot and mountRole's resumable
// claiming makes the filesystem.
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
	// Read-only means "we write nothing", not "the device is
	// untouched": mounting ext4 replays its journal if the last boot
	// died mid-write, which is the filesystem healing itself and
	// exactly what we want.
	if err := unix.Mount(dev, manifestPeekPoint, "ext4", unix.MS_RDONLY, ""); err != nil {
		return c, fmt.Errorf("peeking at machineState on %s: %w", dev, err)
	}
	staged, stagedRaw, stagedErr := machine.LoadStaged(manifestPeekPoint)
	proven, provenRaw, provenErr := machine.LoadProven(manifestPeekPoint)
	c.rejection, _ = machine.LoadRejection(manifestPeekPoint)
	if err := unix.Unmount(manifestPeekPoint, 0); err != nil {
		// Loud but not fatal: if this mount lingers, a later rewrite
		// of this disk's table fails with EBUSY and names the problem.
		fmt.Fprintf(os.Stderr, "liken: storage: unmounting the manifest peek: %v\n", err)
	}
	_ = os.Remove(manifestPeekPoint)

	if provenErr != nil {
		// A proven manifest that won't parse is a corrupted
		// last-known-good: report it and carry on without one, rather
		// than dying over a file whose whole job is recovery.
		fmt.Fprintf(os.Stderr, "liken: storage: the proven manifest is unreadable: %v\n", provenErr)
	} else if proven != nil {
		c.proven = &manifestChoice{m: proven, raw: provenRaw, source: machine.ManifestSourceProven, hash: machine.ManifestHash(provenRaw)}
	}

	// A staged manifest is vetted before it's even a candidate: one
	// that won't parse, or that doesn't declare the machineState role
	// its own lifecycle lives on, would fail every future boot the
	// same way, so it is rejected without being tried.
	if stagedErr != nil {
		c.rejection = rejectStaged(part, stagedRaw, fmt.Sprintf("the staged manifest does not parse: %v", stagedErr))
	} else if staged != nil && staged.Spec.Storage.MachineState == nil {
		c.rejection = rejectStaged(part, stagedRaw, "the staged manifest does not declare the machineState role its own lifecycle lives on")
	} else if staged != nil {
		c.staged = &manifestChoice{m: staged, raw: stagedRaw, source: machine.ManifestSourceStaged, hash: machine.ManifestHash(stagedRaw)}
	}
	return c, nil
}

// rejectStaged quarantines the staged manifest with its reason, and
// reports the rejection for this boot's facts.
func rejectStaged(part *partition, raw []byte, reason string) *machine.Rejection {
	fmt.Fprintf(os.Stderr, "liken: storage: rejecting the staged manifest: %s\n", reason)
	rejection := machine.Rejection{
		Hash:       machine.ManifestHash(raw),
		Reason:     reason,
		RejectedAt: time.Now().UTC(),
	}
	if err := touchMachineState(*part, func(root string) error {
		return machine.Reject(root, rejection)
	}); err != nil {
		fmt.Fprintf(os.Stderr, "liken: storage: recording the rejection: %v\n", err)
	}
	return &rejection
}

// touchMachineState mounts the machineState partition read-write at
// the private point, runs fn against it, and unmounts: the narrow
// window init allows itself to write manifests before (or after) the
// role is properly mounted.
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

// seedPath answers "which file in the manifests directory is mine?".
// One image boots many machines, so the image carries a manifest per
// machine and each boot selects its own: explicitly, by the
// liken.machine=<name> kernel parameter (the one channel the
// bootloader already owns), or implicitly when there is exactly one
// manifest to choose. Anything else is refused rather than guessed:
// a name that matches no manifest is a typo someone must see, and a
// directory of manifests with no name is ambiguity, the same
// situation as two disks claiming the same partition name. An empty
// (or absent) directory is no error: a machine with no manifest is
// still a valid machine.
func seedPath(dir, requested string) (string, error) {
	if requested != "" {
		path := filepath.Join(dir, requested+".yaml")
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("%w: liken.machine=%s names no manifest at %s", errIdentity, requested, path)
		}
		return path, nil
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
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

// loadSeed reads the image's baked-in manifest for this machine. A
// seed that can't be selected or parsed is an error rather than a
// silent default: loadSeed only runs on a boot with no proven
// manifest to fall back to, and a first boot under the wrong (or
// empty) identity could join the wrong cluster or claim the wrong
// disks. Wrong is worse than down.
func loadSeed() (*manifestChoice, error) {
	choice := &manifestChoice{m: &machine.Machine{}, source: machine.ManifestSourceSeed}
	path, err := seedPath(machine.MachineManifestDir, bootParamValue("liken.machine"))
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
// before proven. The seed isn't among the candidates on purpose: it
// is a first-boot input, not a fallback (a machine that has ever
// proven a manifest never consults the image again), so settleStorage
// loads it only when this comes back empty.
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
// and reports which one won. An error means even the last manifest in
// the attempt order failed (or, wrapped as errIdentity, that a first
// boot couldn't tell which manifest is its own); the caller stops the
// boot. The winning choice comes back whole, raw bytes and all,
// because init later publishes those exact bytes for the operator.
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
		// A machine with no durable manifests is on its first boot;
		// only now does the image's seed matter, and with it the
		// question of which seed is ours.
		seed, err := loadSeed()
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
			settleManifests(choice, status, &boot)
			return choice, status, boot, nil
		}
		if choice.source == machine.ManifestSourceStaged && i+1 < len(attempts) {
			fmt.Fprintf(os.Stderr, "liken: storage: the staged manifest failed: %v\n", err)
			fmt.Fprintln(os.Stderr, "liken: storage: falling back to the proven manifest")
			teardownStorage()
			rejection := machine.Rejection{
				Hash:       choice.hash,
				Reason:     err.Error(),
				RejectedAt: time.Now().UTC(),
			}
			if terr := touchMachineState(*candidates.part, func(root string) error {
				return machine.Reject(root, rejection)
			}); terr != nil {
				fmt.Fprintf(os.Stderr, "liken: storage: recording the rejection: %v\n", terr)
			}
			boot.Rejection = &rejection
			continue
		}
		return choice, status, boot, err
	}
	// attemptOrder never returns an empty list; the loop always returns.
	panic("unreachable")
}

// settleManifests finishes the lifecycle bookkeeping after a
// successful reconcile: a staged manifest that just proved itself is
// promoted, and a seed's first success becomes the first proven
// manifest. Failures here are loud but not fatal: the machine is up,
// and the next boot simply repeats the step (a staged manifest that
// boots once boots again).
func settleManifests(choice *manifestChoice, status machine.StorageStatus, boot *machine.BootStatus) {
	if status.MachineState.Backing != machine.BackingPartition {
		return // nothing durable to keep manifests on
	}
	switch choice.source {
	case machine.ManifestSourceStaged:
		if err := machine.Promote(machine.MachineStateDir); err != nil {
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
		if err := machine.WriteProven(machine.MachineStateDir, choice.raw); err != nil {
			fmt.Fprintf(os.Stderr, "liken: storage: recording the seed as proven: %v\n", err)
			return
		}
		fmt.Printf("liken: storage: the seed manifest is now proven (%.12s)\n", choice.hash)
		boot.ManifestSource = machine.ManifestSourceProven
	}
}
