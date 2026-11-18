package main

// Init chooses the Machine manifest that a boot runs under.
//
// The manifests are files on the machineState partition, and init
// needs one before it has set up any disk. So init finds the partition
// by its GPT name, mounts it read-only, reads the manifests, and
// unmounts it. The unmount matters because storage reconciliation can
// rewrite the same disk's partition table next, and the kernel refuses
// to re-read the table of a disk with a mounted partition.
//
// A boot tries the staged manifest first. When storage cannot be
// reconciled under it, init rejects it, tears storage down, and tries
// the proven manifest, the last one that completed a boot. A machine
// on older configuration is better than one that is off, so init
// powers off only when the last manifest it tries fails too.
//
// The seed is the manifest in the image. init reads it only when
// machineState holds no manifest, which is a first boot. Reconciling
// storage under a seed that declares machineState creates the
// partition, and the boot writes the seed as proven.yaml, so later
// boots never read the image's manifest. A first boot that crashes
// before proven.yaml exists leaves no manifest, so the next boot
// selects the seed again.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
)

// manifestPeekPoint is where init mounts machineState while the role
// itself is not mounted.
var manifestPeekPoint = "/.liken-machine-state"

// A manifestChoice is one candidate manifest with the bytes it was
// parsed from. The boot record and each rejection include the hash of
// those bytes, so the operator can tell which manifest a boot ran.
type manifestChoice struct {
	m      *machine.Machine
	raw    []byte
	source machine.ManifestSource
	hash   string
}

type manifestCandidates struct {
	part      *partition // the machineState partition, or nil on the first boot
	staged    *manifestChoice
	proven    *manifestChoice
	rejection *machine.Rejection // the last rejection the store recorded, if any
}

// findMachineStatePartition returns the partition named
// liken:machineState, or nil on a first boot. Two partitions with that
// name, as on a cloned disk, are an error, because init cannot tell
// which one holds this machine's manifests.
func findMachineStatePartition() (*partition, error) {
	var found *partition
	for _, p := range discoverPartitions() {
		if p.partName != machine.PartitionPrefix+"machineState" {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("partitions %s and %s are both named %smachineState, so init cannot tell which one holds the manifests",
				found.name, p.name, machine.PartitionPrefix)
		}
		found = &p
	}
	return found, nil
}

// loadManifestCandidates reads the staged and proven manifests and the
// last rejection from machineState. A partition with no ext4
// filesystem returns no candidates. A boot that died after it named
// the partition and before mkfs leaves one, and the next boot then
// uses the seed and creates the filesystem.
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
	// A read-only mount writes no files, but ext4 still replays its
	// journal on mount when the last boot died during a write.
	if err := mountFilesystem(dev, manifestPeekPoint, "ext4", unix.MS_RDONLY, ""); err != nil {
		return c, fmt.Errorf("mounting %s read-only to read the manifests: %w", dev, err)
	}
	store := machine.MachineManifests(manifestPeekPoint)
	stagedRaw, stagedErr := store.LoadStaged()
	provenRaw, provenErr := store.LoadProven()
	c.rejection, _ = store.LoadRejection()
	if err := unmountFilesystem(manifestPeekPoint, 0); err != nil {
		// The boot continues. A later rewrite of this disk's
		// partition table then fails with EBUSY, and this line
		// explains why.
		fmt.Fprintf(os.Stderr, "liken: storage: unmounting %s: %v\n", manifestPeekPoint, err)
	}
	_ = os.Remove(manifestPeekPoint)

	if provenErr != nil {
		fmt.Fprintf(os.Stderr, "liken: storage: the proven manifest is unreadable: %v\n", provenErr)
	} else if provenRaw != nil {
		c.proven = provenCandidate(provenRaw)
	}

	// A staged manifest that does not parse, or that declares no
	// machineState role to store the manifests on, would fail every
	// boot. So init rejects it without trying it.
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

// provenCandidate parses the proven manifest with machine.ParseKnown,
// which skips fields this release does not know and returns their
// names. After a rollback, an older release boots under a manifest
// that a newer release proved, and it still needs that manifest's
// storage and network settings. A manifest that does not parse at all
// is corrupt: provenCandidate prints the error and returns nil, so the
// boot continues without a proven manifest.
func provenCandidate(raw []byte) *manifestChoice {
	proven, ignored, err := machine.ParseKnown(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: storage: the proven manifest is unreadable: %v\n", err)
		return nil
	}
	if len(ignored) > 0 {
		fmt.Fprintf(os.Stderr, "liken: storage: the proven manifest names fields this release does not know, and this boot leaves them out: %s\n",
			strings.Join(ignored, ", "))
	}
	return &manifestChoice{m: proven, raw: raw, source: machine.ManifestSourceProven, hash: machine.ManifestHash(raw)}
}

// rejectStagedDocument prints the reason a staged document is rejected,
// calls record to store the rejection, and returns it for the boot
// record. The store renames the staged file to rejected, so later boots
// do not try the same bytes. domain and what only label the console
// lines. record is a parameter because the Machine manifest's store
// must mount machineState around the write (see rejectStaged).
//
// The stored rejection outlasts this boot, but the boot record is
// built again on every boot. So each caller copies the stored
// rejection into the boot record before it reads anything staged.
func rejectStagedDocument(domain, what string, record func(machine.Rejection) error,
	raw []byte, reason string) *machine.Rejection {
	fmt.Fprintf(os.Stderr, "liken: %s: rejecting the staged %s: %s\n", domain, what, reason)
	rejection := machine.NewRejection(raw, reason, time.Now().UTC())
	if err := record(rejection); err != nil {
		fmt.Fprintf(os.Stderr, "liken: %s: recording the rejection: %v\n", domain, err)
	}
	return &rejection
}

// rejectStaged rejects the staged Machine manifest. It runs before
// the machineState role is mounted, so it mounts the partition to
// write the rejection.
func rejectStaged(part *partition, raw []byte, reason string) *machine.Rejection {
	return rejectStagedDocument("storage", "manifest", func(rejection machine.Rejection) error {
		return touchMachineState(*part, func(root string) error {
			return machine.MachineManifests(root).Reject(rejection)
		})
	}, raw, reason)
}

// touchMachineState mounts the machineState partition read-write at
// manifestPeekPoint, runs fn on it, and unmounts.
func touchMachineState(p partition, fn func(root string) error) error {
	if err := os.MkdirAll(manifestPeekPoint, 0o755); err != nil {
		return err
	}
	if err := mountFilesystem(devRoot+"/"+p.name, manifestPeekPoint, "ext4", 0, ""); err != nil {
		return err
	}
	ferr := fn(manifestPeekPoint)
	if err := unmountFilesystem(manifestPeekPoint, 0); err != nil {
		fmt.Fprintf(os.Stderr, "liken: storage: unmounting %s: %v\n", manifestPeekPoint, err)
	}
	_ = os.Remove(manifestPeekPoint)
	return ferr
}

// errIdentity marks a failure to select this machine's own manifest,
// so that failBoot prints a different reason for the power-off.
var errIdentity = errors.New("machine identity")

// seedPath returns the path of this machine's manifest in dir. One
// image can boot many machines and holds one manifest for each. The
// kernel parameter liken.machine=<name> selects <name>.yaml. Without
// it, a directory with exactly one manifest selects that one. A name
// with no file, or several manifests and no name, is an errIdentity
// error. An empty or missing directory returns "" and no error,
// because a machine with no manifest is valid.
func seedPath(dir, requested string) (string, error) {
	if requested != "" {
		path := filepath.Join(dir, requested+".yaml")
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("%w: the kernel command line has liken.machine=%s, and %s does not exist", errIdentity, requested, path)
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
		return "", fmt.Errorf("%w: %s holds %d manifests (%s), and the kernel command line has no liken.machine=<name> to choose one",
			errIdentity, dir, len(names), strings.Join(names, ", "))
	}
}

// loadSeed reads the manifest that seedPath selects. A seed that cannot
// be selected or parsed is an error, not an empty manifest, because a
// first boot under the wrong identity could join the wrong cluster or
// claim the wrong disks.
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
		return nil, fmt.Errorf("%w: parsing %s: %v", errIdentity, path, err)
	}
	fmt.Printf("liken: this is %s (manifest %s)\n", m.Metadata.Name, path)
	choice.m, choice.raw, choice.hash = m, raw, machine.ManifestHash(raw)
	return choice, nil
}

// attemptOrder returns the manifests to try, staged first and then
// proven. It leaves out the seed, which settleStorage reads only when
// this list is empty.
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

// settleStorage reconciles storage under the first manifest in
// attemptOrder that works, and returns that manifest, the storage
// status, and the boot record. It returns an error when the last
// manifest fails, or an errIdentity error when a first boot cannot
// select its seed. The caller then stops the boot. The returned
// choice keeps its raw bytes, which init later publishes for the
// operator.
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
			// The boot record holds the network the manifest
			// declared, which the operator compares with the current
			// spec to detect drift. status.network reports what each
			// interface received.
			network := choice.m.Spec.Network
			boot.Network = &network
			settleManifests(machine.MachineManifests(machine.MachineStateDir), choice, status, &boot)
			return choice, status, boot, nil
		}
		if choice.source == machine.ManifestSourceStaged && i+1 < len(attempts) {
			fmt.Fprintf(os.Stderr, "liken: storage: reconciling storage under the staged manifest: %v\n", err)
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
	panic("unreachable")
}

// settleManifests promotes a staged manifest that booted to proven,
// and writes a seed with bytes as the first proven manifest. A failed
// write prints a message and does not stop the boot, and the boot
// record keeps the source the boot ran.
func settleManifests(store machine.ManifestStore, choice *manifestChoice, status machine.StorageStatus, boot *machine.BootStatus) {
	if status.MachineState.Backing != machine.BackingPartition {
		return // no partition to store manifests on
	}
	switch choice.source {
	case machine.ManifestSourceStaged:
		if err := store.Promote(); err != nil {
			fmt.Fprintf(os.Stderr, "liken: storage: promoting the staged manifest: %v\n", err)
			return
		}
		fmt.Printf("liken: storage: the staged manifest is now proven (%.12s)\n", choice.hash)
		boot.ManifestSource = machine.ManifestSourceProven
		boot.Rejection = nil // Promote deletes the stored rejection
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
