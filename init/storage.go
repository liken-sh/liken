package main

// Actuating spec.storage.
//
// The contract (machine/storage.go documents the API side): every
// boot, each declared role is *recognized* (found by the GPT
// partition name written when it was claimed) or, exactly once,
// *claimed*: a blank disk gets a partition table, fresh filesystems,
// and the roles' names written into it. Two rules make this safe to
// run on every machine, every boot:
//
//   - Reconciling never destroys data. Only a disk with no partition
//     table and no filesystem, nothing we or anyone else ever wrote,
//     may be claimed. Anything unrecognized is refused, with the
//     reason printed to the console.
//
//   - An unsatisfiable role stops the boot. If a declared role can't
//     be recognized or claimed, the machine powers off rather than
//     start k3s with that state in RAM. A machine that's down is
//     recoverable; a cluster that silently wrote its state to memory
//     is not. (Roles *absent* from the spec carry no such obligation:
//     those directories simply stay on the RAM root.)

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/chrisguidry/liken/machine"
)

// Where each role lands in the filesystem: liken's private
// translation, deliberately absent from the Machine API.
//
//	machineState     /var/lib/liken/machine: the machine's own durable
//	                 data, chiefly the staged and proven manifests
//	machineEphemeral /tmp, the OS's own scratch; nosuid/nodev and
//	                 world-writable-with-sticky-bit, per long Unix
//	                 tradition
//	clusterState     /var/lib/rancher: all of k3s's state, its
//	                 database, its TLS material, containerd's images
//	podStorage       the local-path provisioner's root; the path also
//	                 appears in the image's k3s config.yaml
//	podEphemeral     kubelet's root directory: emptyDirs, pod scratch
type roleMount struct {
	path  string
	flags uintptr
	mode  os.FileMode // applied to the mounted root; 0 leaves the default
}

var roleMounts = map[string]roleMount{
	"machineState":     {path: "/var/lib/liken/machine"},
	"machineEphemeral": {path: "/tmp", flags: unix.MS_NOSUID | unix.MS_NODEV, mode: 0o1777},
	"clusterState":     {path: "/var/lib/rancher"},
	"podStorage":       {path: "/var/lib/liken/pod-storage"},
	"podEphemeral":     {path: "/var/lib/kubelet"},
}

// A partition as sysfs presents it: a subdirectory of its disk's
// /sys/block entry. The kernel parsed the GPT; the name it read is in
// the partition's uevent, which is how recognition works without
// re-reading any partition table ourselves.
type partition struct {
	name      string // the kernel's node name: vda1, nvme0n1p2
	partName  string // the GPT partition name, "" if the table has none
	sizeBytes uint64
}

func discoverPartitions() []partition {
	var parts []partition
	for _, disk := range discoverBlockDevices() {
		dir := filepath.Join(sysBlock, disk.Name)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			// A disk's partitions appear as subdirectories named after
			// their device (vda → vda1); the `partition` file inside is
			// what distinguishes them from the disk's other attributes.
			if _, err := os.Stat(filepath.Join(dir, entry.Name(), "partition")); err != nil {
				continue
			}
			p := partition{name: entry.Name()}
			if raw, err := os.ReadFile(filepath.Join(dir, entry.Name(), "size")); err == nil {
				if sectors, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64); err == nil {
					p.sizeBytes = sectors * sectorSize
				}
			}
			// The uevent file is KEY=value lines; PARTNAME appears
			// only for tables that carry names, which GPT does.
			if raw, err := os.ReadFile(filepath.Join(dir, entry.Name(), "uevent")); err == nil {
				for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
					if v, ok := strings.CutPrefix(line, "PARTNAME="); ok {
						p.partName = v
					}
				}
			}
			parts = append(parts, p)
		}
	}
	return parts
}

// isBlank reports whether a disk carries nothing recognizable: no MBR
// or GPT, no ext4 filesystem written straight to the device. Blank is
// the only condition under which claiming is allowed; a disk
// something else formatted fails one of these checks and is left
// alone.
func isBlank(devPath string) (bool, error) {
	f, err := os.Open(devPath)
	if err != nil {
		return false, err
	}
	defer f.Close()

	// Enough to cover all three signatures: the MBR's 0x55AA at 510,
	// GPT's "EFI PART" at 512, ext4's magic at 1080.
	head := make([]byte, 2048)
	if _, err := f.ReadAt(head, 0); err != nil {
		return false, err
	}
	switch {
	case head[510] == 0x55 && head[511] == 0xAA:
		return false, nil
	case string(head[512:520]) == "EFI PART":
		return false, nil
	case head[1080] == 0x53 && head[1081] == 0xEF:
		return false, nil
	}
	return true, nil
}

// hasExt4 checks a partition for ext4's superblock magic: two bytes,
// 0xEF53 little-endian, at offset 1080 (the superblock starts at 1024;
// the magic is 56 bytes in). This is the same check blkid makes; a
// filesystem's identity really is this shallow.
func hasExt4(devPath string) bool {
	f, err := os.Open(devPath)
	if err != nil {
		return false
	}
	defer f.Close()
	magic := make([]byte, 2)
	if _, err := f.ReadAt(magic, 1080); err != nil {
		return false
	}
	return magic[0] == 0x53 && magic[1] == 0xEF
}

// reconcileStorage actuates the storage spec. On success every
// declared role is an ext4 filesystem mounted at its role's path, and
// the returned status records where each role landed: the same facts
// printed to the console, bound for the Machine's status, because
// anything reported only to the serial port is invisible to anyone
// operating the machine remotely. An error means a declared role
// can't be satisfied, and the caller (main, the only place with the
// authority) stops the boot rather than let k3s start with that
// state in RAM.
func reconcileStorage(spec machine.StorageSpec) (machine.StorageStatus, error) {
	status := machine.AllRolesInMemory()
	roles := spec.Roles()
	if len(roles) == 0 {
		return status, nil
	}
	if err := spec.Validate(); err != nil {
		return status, err
	}

	// Recognition: each declared role, found by the name written on
	// its partition. The device in the spec is not consulted; a disk
	// that moved controllers since it was claimed is still the same
	// disk.
	found, err := recognizeRoles(roles)
	if err != nil {
		return status, err
	}

	// Claiming: any role still missing must point at a blank disk.
	// Group by device, since roles sharing a disk are claimed together
	// in one partition table.
	var missing []machine.DeclaredRole
	for _, role := range roles {
		if _, ok := found[role.Name]; !ok {
			missing = append(missing, role)
		}
	}
	if len(missing) > 0 {
		claimed := map[string]bool{}
		for _, role := range missing {
			if !claimed[role.Device] {
				if err := claimDisk(role.Device, roles, found); err != nil {
					return status, err
				}
				claimed[role.Device] = true
			}
		}
		if found, err = recognizeRoles(roles); err != nil {
			return status, err
		}
		for _, role := range roles {
			if _, ok := found[role.Name]; !ok {
				return status, fmt.Errorf("role %s: partition %s did not appear after claiming %s",
					role.Name, role.PartitionName(), role.Device)
			}
		}
	}

	for _, role := range roles {
		p := found[role.Name]
		if err := mountRole(role, p); err != nil {
			return status, err
		}
		*status.Role(role.Name) = machine.StorageRoleStatus{
			Backing:       machine.BackingPartition,
			Device:        p.name,
			Partition:     p.partName,
			CapacityBytes: p.sizeBytes,
		}
	}
	return status, nil
}

// recognizeRoles matches the declared roles against the machine's
// partitions as sysfs reports them.
func recognizeRoles(roles []machine.DeclaredRole) (map[string]partition, error) {
	return matchRoles(roles, discoverPartitions())
}

// matchRoles matches declared roles to partitions by name. Two
// partitions carrying the same role name is unresolvable ambiguity (a
// transplanted or cloned disk), and guessing wrong about which one
// holds the real cluster would destroy data, so that's an error
// rather than a choice.
func matchRoles(roles []machine.DeclaredRole, parts []partition) (map[string]partition, error) {
	found := map[string]partition{}
	for _, role := range roles {
		for _, p := range parts {
			if p.partName != role.PartitionName() {
				continue
			}
			if existing, ok := found[role.Name]; ok {
				return nil, fmt.Errorf("two partitions claim to be %s (%s and %s); refusing to guess",
					role.PartitionName(), existing.name, p.name)
			}
			found[role.Name] = p
		}
	}
	return found, nil
}

// claimDisk gives a blank disk its partition table: every declared
// role that names this device, laid down in canonical order, sized
// roles first at their exact sizes and the remainder role taking
// whatever is left.
func claimDisk(device string, roles []machine.DeclaredRole, found map[string]partition) error {
	var mine []machine.DeclaredRole
	for _, role := range roles {
		if role.Device == device {
			// A disk where some roles are recognized but others need
			// claiming is a disk whose table liken wrote and then
			// something changed: not blank, not claimable, and not
			// safe to fix automatically.
			if _, ok := found[role.Name]; ok {
				return fmt.Errorf("disk %s already carries %s but is missing other declared roles; refusing to modify it",
					device, role.PartitionName())
			}
			mine = append(mine, role)
		}
	}

	disk := diskByPath(device)
	if disk == nil {
		return fmt.Errorf("declared device %s is not attached", device)
	}
	blank, err := isBlank(device)
	if err != nil {
		return fmt.Errorf("examining %s: %w", device, err)
	}
	if !blank {
		return fmt.Errorf("%s carries a partition table or filesystem liken doesn't recognize; refusing to touch it (wipe it yourself if it's expendable)", device)
	}

	totalSectors := disk.SizeBytes / sectorSize
	parts, err := planPartitions(device, mine, totalSectors)
	if err != nil {
		return err
	}

	fmt.Printf("liken: storage: claiming %s (%s) for %d role(s)\n", device, gib(disk.SizeBytes), len(mine))
	if err := writeGPT(device, totalSectors, parts); err != nil {
		return fmt.Errorf("partitioning %s: %w", device, err)
	}
	return waitForPartitions(parts, 5*time.Second)
}

// planPartitions lays out a claimed disk's table: sized roles pack
// from the front in canonical order, each start aligned to 1MiB, and
// the (single, validated) sizeless role takes the rest of the disk.
// The device name appears only in the errors; the math cares about
// sectors alone.
func planPartitions(device string, mine []machine.DeclaredRole, totalSectors uint64) ([]gptPartition, error) {
	lastUsable := gptLastUsableLBA(totalSectors)
	var parts []gptPartition
	next := uint64(partitionAlignment)
	var remainder *machine.DeclaredRole
	for _, role := range mine {
		if role.Size == "" {
			remainder = &role
			continue
		}
		bytes, _ := machine.ParseSize(role.Size) // validated before any disk is touched
		sectors := (bytes + sectorSize - 1) / sectorSize
		p := gptPartition{name: role.PartitionName(), firstLBA: next, lastLBA: next + sectors - 1}
		if p.lastLBA > lastUsable {
			return nil, fmt.Errorf("disk %s is too small: %s wants %s at sector %d but the disk's usable space ends at %d",
				device, role.Name, role.Size, p.firstLBA, lastUsable)
		}
		parts = append(parts, p)
		next = alignLBA(p.lastLBA + 1)
	}
	if remainder != nil {
		if next > lastUsable {
			return nil, fmt.Errorf("disk %s is too small: nothing left for %s", device, remainder.Name)
		}
		parts = append(parts, gptPartition{name: remainder.PartitionName(), firstLBA: next, lastLBA: lastUsable})
	}
	return parts, nil
}

// waitForPartitions gives the kernel a moment to surface the devices
// for a table just written: BLKRRPART is synchronous but the devtmpfs
// nodes and sysfs entries appear slightly later. Running out of
// patience is an error that names which partitions never showed:
// "the kernel wouldn't surface the table" is a different problem
// from "the table was never written", and the console should say
// which one happened.
func waitForPartitions(parts []gptPartition, patience time.Duration) error {
	deadline := time.Now().Add(patience)
	for {
		visible := map[string]bool{}
		for _, p := range discoverPartitions() {
			visible[p.partName] = true
		}
		var missing []string
		for _, want := range parts {
			if !visible[want.name] {
				missing = append(missing, want.name)
			}
		}
		if len(missing) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("partitions never appeared after their table was written: %s",
				strings.Join(missing, ", "))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func diskByPath(device string) *machine.BlockDevice {
	for _, d := range discoverBlockDevices() {
		if devicePath(d) == device {
			return &d
		}
	}
	return nil
}

// mountRole puts a role's filesystem where its purpose says it goes,
// making the filesystem first if the partition is fresh from a claim.
// (Recognizing our own name on a partition with no filesystem also
// covers a boot that died between partitioning and mkfs; claiming is
// resumable because the name goes on first.)
func mountRole(role machine.DeclaredRole, p partition) error {
	// The role's translation to a mount is looked up before anything
	// touches the partition: a role liken doesn't know how to mount
	// must be refused before mke2fs writes a filesystem onto it.
	rm, ok := roleMounts[role.Name]
	if !ok {
		return fmt.Errorf("role %s has no mount translation; liken and its manifest disagree about the role vocabulary", role.Name)
	}
	target := rm.path

	dev := devRoot + "/" + p.name
	if !hasExt4(dev) {
		fmt.Printf("liken: storage: making an ext4 filesystem on %s for %s\n", dev, role.Name)
		if !runNarrated("mke2fs | ", "/sbin/mke2fs", "-t", "ext4", dev) {
			return fmt.Errorf("mke2fs on %s for %s failed", dev, role.Name)
		}
	}

	// clusterState is special: the image bakes its seed files (the
	// pre-generated CAs, the operator's manifests and container
	// image) underneath this very mountpoint, and mounting over them
	// would hide them all. So the filesystem is first mounted to the
	// side, seeded from the image's copies, and only then moved into place;
	// MS_MOVE re-attaches a live mount atomically.
	if role.Name == "clusterState" {
		staging := "/.liken-claim"
		if err := os.MkdirAll(staging, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", staging, err)
		}
		if err := unix.Mount(dev, staging, "ext4", 0, ""); err != nil {
			return fmt.Errorf("mounting %s for %s: %w", dev, role.Name, err)
		}
		if err := seedClusterState(staging); err != nil {
			return fmt.Errorf("seeding %s: %w", role.Name, err)
		}
		if err := os.MkdirAll(target, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", target, err)
		}
		if err := unix.Mount(staging, target, "", unix.MS_MOVE, ""); err != nil {
			return fmt.Errorf("moving %s into place at %s: %w", role.Name, target, err)
		}
		_ = os.Remove(staging)
	} else {
		if err := os.MkdirAll(target, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", target, err)
		}
		if err := unix.Mount(dev, target, "ext4", rm.flags, ""); err != nil {
			return fmt.Errorf("mounting %s for %s: %w", dev, role.Name, err)
		}
	}
	// A freshly-made ext4 root is 0755 root-only; roles like /tmp need
	// their conventional permissions applied to the mounted root.
	if rm.mode != 0 {
		if err := os.Chmod(target, rm.mode); err != nil {
			fmt.Fprintf(os.Stderr, "liken: storage: chmod %s: %v\n", target, err)
		}
	}
	fmt.Printf("liken: storage: %s is %s (%s) on %s\n",
		role.Name, dev, p.partName, target)
	return nil
}

// seedClusterState copies the image's seed files into a clusterState
// filesystem. The TLS material is copied only if the disk has none:
// those keys are the cluster's identity, and a disk that already has
// an identity keeps it. The manifests and the operator image are
// refreshed every boot: they're pinned to the liken version of the
// running image, and an upgraded image must deliver its upgraded
// operator.
func seedClusterState(root string) error {
	for _, seed := range []struct {
		rel     string
		refresh bool
	}{
		{"k3s/server/tls", false},
		{"k3s/server/manifests", true},
		{"k3s/agent/images", true},
	} {
		src := filepath.Join("/var/lib/rancher", seed.rel)
		if _, err := os.Stat(src); err != nil {
			continue // an image without k3s has no seed files
		}
		dst := filepath.Join(root, seed.rel)
		if seed.refresh {
			if err := os.RemoveAll(dst); err != nil {
				return err
			}
		} else if _, err := os.Stat(dst); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
			return err
		}
	}
	return nil
}
