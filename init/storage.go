package main

// Actuating spec.storage.
//
// The contract (machine/storage.go documents the API side): on every
// boot, each declared role is either *recognized* or *claimed*. A
// role is recognized when a partition carries the GPT partition name
// written at claim time. Claiming happens exactly once: a blank disk
// gets a partition table, fresh filesystems, and the roles' names
// written into it. Two rules make this safe to run on every machine,
// every boot:
//
//   - Reconciling never destroys data. Only a disk with no partition
//     table and no filesystem, one that neither we nor anything else
//     ever wrote to, may be claimed. Anything unrecognized is
//     refused, with the reason printed to the console.
//
//   - An unsatisfiable role stops the boot. If a declared role can't
//     be recognized or claimed, the machine powers off rather than
//     start k3s with that state in RAM. A powered-off machine can be
//     repaired and booted again; a cluster that silently wrote its
//     state to memory loses that state at the next power cycle.
//     (Roles *absent* from the spec carry no such obligation: those
//     directories simply stay on the RAM root.)
//
// This file owns the every-boot half: recognition, orchestration, and
// mounting. Claiming a blank disk is claim.go's story, and growing a
// recognized partition is grow.go's.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/disks"
	"github.com/liken-sh/liken/machine"
)

// Where each role lands in the filesystem. This translation is
// liken's own, and it is deliberately kept out of the Machine API.
//
//	systemA/systemB  /var/lib/liken/system/{a,b}: the OS's own boot
//	                 slots, FAT32 because the firmware reads them;
//	                 no suid, no devices, no executables, because
//	                 nothing runs *from* a slot; the firmware only
//	                 loads it
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
	path   string
	flags  uintptr
	mode   os.FileMode // applied to the mounted root; 0 leaves the default
	fstype string      // "" means ext4, the default for data roles
}

const slotMountFlags = unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC

var roleMounts = map[machine.StorageRoleName]roleMount{
	machine.SystemARole:          {path: machine.SystemSlotDir("A"), flags: slotMountFlags, fstype: "vfat"},
	machine.SystemBRole:          {path: machine.SystemSlotDir("B"), flags: slotMountFlags, fstype: "vfat"},
	machine.MachineStateRole:     {path: machine.MachineStateDir},
	machine.MachineEphemeralRole: {path: "/tmp", flags: unix.MS_NOSUID | unix.MS_NODEV, mode: 0o1777},
	machine.ClusterStateRole:     {path: "/var/lib/rancher"},
	machine.PodStorageRole:       {path: "/var/lib/liken/pod-storage"},
	machine.PodEphemeralRole:     {path: "/var/lib/kubelet"},
}

// isSystemSlot reports whether a role is one of the firmware-read
// system slots. The slots are the two roles with FAT32 semantics:
// typed as EFI system partitions so the firmware finds them, and
// fixed in size from the day they're claimed, because FAT doesn't
// grow in place.
func isSystemSlot(name machine.StorageRoleName) bool {
	return name == machine.SystemARole || name == machine.SystemBRole
}

// partitionTypeFor picks a role's GPT partition type: the system
// slots are EFI system partitions (the type GUID is how firmware
// finds them), everything else is ordinary Linux data.
func partitionTypeFor(name machine.StorageRoleName) [16]byte {
	if isSystemSlot(name) {
		return disks.EFISystemPartition
	}
	return disks.LinuxFilesystemData
}

// teardownStorage unmounts whatever reconciliation may have mounted,
// in reverse canonical order. That returns the machine to the state a
// fresh reconcile expects, so a different spec can be tried. It works
// from the mount table rather than from a status record, because a
// reconcile that failed halfway may have mounted a role it never got
// to report. Unmounting a path that isn't a mountpoint returns
// EINVAL, which here just means there was nothing to unmount. Nothing
// else is running this early in boot, so nothing can hold these
// mounts busy.
func teardownStorage() {
	// mountAndSeedClusterState (k3s.go) temporarily mounts
	// clusterState's filesystem at the staging point while seeding it;
	// a failure partway through leaves it mounted there.
	_ = unix.Unmount(clusterStateStaging, 0)
	unmountRoleMounts(0, true)
}

// unmountRoleMounts detaches every role filesystem in reverse
// canonical order: the shared tail of two different shutdowns.
// Boot-time teardown unmounts plainly and wants to hear about
// failures, because nothing else is running this early and a failed
// unmount is information. The reboot path (reboot.go) passes
// MNT_DETACH and tolerates errors: a just-killed container's mount
// namespace can pin a filesystem for a moment longer, and lazy
// detachment lets the kernel finish the job as those references
// drain, after the sync has already made the data safe. Unmounting a
// path that isn't a mountpoint returns EINVAL, which just means there
// was nothing to unmount.
func unmountRoleMounts(flags int, reportErrors bool) {
	for _, name := range slices.Backward(machine.StorageRoleNames) {
		target := roleMounts[name].path
		err := unix.Unmount(target, flags)
		switch {
		case err == nil:
			fmt.Printf("liken: storage: unmounted %s\n", target)
		case reportErrors && !errors.Is(err, unix.EINVAL) && !errors.Is(err, fs.ErrNotExist):
			fmt.Fprintf(os.Stderr, "liken: storage: unmounting %s: %v\n", target, err)
		}
	}
}

// A partition as sysfs presents it: a subdirectory of its disk's
// /sys/block entry. The kernel parsed the GPT; the name it read is in
// the partition's uevent, which is how recognition works without
// re-reading any partition table ourselves.
type partition struct {
	name      string // the kernel's node name: vda1, nvme0n1p2
	disk      string // the parent disk's node name: vda, nvme0n1
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
			p := partition{name: entry.Name(), disk: disk.Name}
			if raw, err := os.ReadFile(filepath.Join(dir, entry.Name(), "size")); err == nil {
				if sectors, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64); err == nil {
					p.sizeBytes = sectors * disks.SectorSize
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

// reconcileStorage actuates the storage spec. On success every
// declared role is a filesystem mounted at its role's path (ext4 for
// the data roles, FAT32 for the system slots the firmware reads), and
// the returned status records where each role landed. The status
// carries the same facts printed to the console, bound for the
// Machine's status, because anything reported only to the serial port
// is invisible to anyone operating the machine remotely. An error
// means a declared role can't be satisfied, and the caller (main, the
// only place with the authority) stops the boot rather than let k3s
// start with that state in RAM.
//
// The structure is plan everything, then apply: every claim's layout
// and every growth's table edit is computed before the first byte is
// written to any disk. A spec that will fail should fail before it
// changes anything on disk, because the boot may go on to try a
// different spec (the proven manifest, after a staged one is
// rejected), and partitions half-created under the failed spec would
// break that attempt too. What planning can't prevent is a genuine
// mid-write I/O failure: a disk claimed by a failed attempt stays
// claimed, and if that leaves two partitions carrying one role's
// name, recognition refuses to guess and the boot stops.
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

	// Plan the claims: any role still missing must point at a blank
	// disk. Group by device, since roles sharing a disk are claimed
	// together in one partition table.
	var claims []claimPlan
	planned := map[string]bool{}
	for _, role := range roles {
		if _, ok := found[role.Name]; ok || planned[role.Device] {
			continue
		}
		plan, err := planClaim(role.Device, roles, found)
		if err != nil {
			return status, err
		}
		claims = append(claims, plan)
		planned[role.Device] = true
	}

	// Plan the growth: recognized partitions may be smaller than the
	// spec now declares, or their disks may have grown underneath
	// them. grow.go explains the rules.
	grows, err := planAllGrowth(roles, found)
	if err != nil {
		return status, err
	}

	// Apply the plans. Every plan has already been validated, so any
	// failure from here on is a real I/O problem.
	for _, plan := range claims {
		if err := applyClaim(plan); err != nil {
			return status, err
		}
	}
	for _, plan := range grows {
		if err := applyGrowth(plan); err != nil {
			return status, err
		}
	}
	if len(claims) > 0 || len(grows) > 0 {
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
func recognizeRoles(roles []machine.DeclaredRole) (map[machine.StorageRoleName]partition, error) {
	return matchRoles(roles, discoverPartitions())
}

// matchRoles matches declared roles to partitions by name. Two
// partitions carrying the same role name usually means a disk was
// cloned or transplanted. Guessing wrong about which one holds the
// real cluster would destroy data, so the ambiguity is an error
// rather than a choice.
func matchRoles(roles []machine.DeclaredRole, parts []partition) (map[machine.StorageRoleName]partition, error) {
	found := map[machine.StorageRoleName]partition{}
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

func diskByPath(device string) *machine.BlockDevice {
	for _, d := range discoverBlockDevices() {
		if devicePath(d) == device {
			return &d
		}
	}
	return nil
}

// mountRole mounts a role's filesystem at the role's path, making the
// filesystem first if the partition is fresh from a claim.
// (Recognizing our own name on a partition with no filesystem also
// covers a boot that died between partitioning and mkfs; claiming is
// resumable because the name is written first.)
func mountRole(role machine.DeclaredRole, p partition) error {
	// The role's translation to a mount is looked up before anything
	// touches the partition: a role liken doesn't know how to mount
	// must be refused before mke2fs writes a filesystem onto it.
	rm, ok := roleMounts[role.Name]
	if !ok {
		return fmt.Errorf("role %s has no mount translation; liken and its manifest disagree about the role vocabulary", role.Name)
	}
	target := rm.path

	// Each filesystem type has its own maker. The system slots get
	// FAT32 from our own formatter (fat32.go), because the firmware
	// reads them and FAT is the only filesystem it reads; everything
	// else gets ext4 from the vendored static mke2fs. Either way,
	// recognizing our own name on a partition with no filesystem
	// covers a boot that died between partitioning and mkfs.
	dev := devRoot + "/" + p.name
	if rm.fstype == "vfat" {
		if !disks.HasFAT32(dev) {
			fmt.Printf("liken: storage: making a FAT32 filesystem on %s for %s\n", dev, role.Name)
			if err := formatSlot(dev, p.sizeBytes, role.Name); err != nil {
				return fmt.Errorf("formatting %s for %s: %w", dev, role.Name, err)
			}
		}
	} else if !hasExt4(dev) {
		fmt.Printf("liken: storage: making an ext4 filesystem on %s for %s\n", dev, role.Name)
		if !runNarrated("mke2fs | ", "/sbin/mke2fs", "-t", "ext4", dev) {
			return fmt.Errorf("mke2fs on %s for %s failed", dev, role.Name)
		}
	}

	// clusterState mounts through its own path: the image bakes k3s's
	// seed files underneath its mountpoint, and layering them in is
	// k3s's business, not partition mechanics (k3s.go owns it).
	if role.Name == machine.ClusterStateRole {
		if err := mountAndSeedClusterState(dev, target); err != nil {
			return err
		}
	} else {
		fstype := rm.fstype
		if fstype == "" {
			fstype = "ext4"
		}
		if err := os.MkdirAll(target, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", target, err)
		}
		if err := unix.Mount(dev, target, fstype, rm.flags, ""); err != nil {
			return fmt.Errorf("mounting %s for %s: %w", dev, role.Name, err)
		}
	}
	// A partition grown this boot still carries a filesystem sized for
	// the old extent; now that it's mounted, ext4 can be grown online
	// to fill it (ext4.go explains why mounted is the easy case). FAT
	// can't be grown in place, which is fine: slots are fixed-size by
	// design, and planAllGrowth refuses to grow them at the planning
	// stage.
	if rm.fstype == "" {
		if err := maybeGrowFilesystem(role, p, target); err != nil {
			return err
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
