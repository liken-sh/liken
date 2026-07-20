package main

// This file actuates spec.storage.
//
// machine/storage.go documents the API side of the contract. On every
// boot, each declared role is either recognized or claimed. A role is
// recognized when a partition carries the GPT partition name that was
// written at claim time. Claiming happens exactly once. When a disk
// is blank, claiming writes a partition table to it, adds fresh
// filesystems, and writes the roles' names into it. Two rules make it
// safe to run this process on every machine, on every boot:
//
//   - Reconciling never destroys data. The process may only claim a
//     disk with no partition table and no filesystem: a disk that
//     neither liken nor anything else has written to before. It
//     refuses any disk it does not recognize, and it prints the
//     reason to the console.
//
//   - An unsatisfiable role stops the boot. If the process cannot
//     recognize or claim a declared role, the machine powers off. It
//     does not start k3s with that state only in RAM. A person can
//     repair a powered-off machine and boot it again. A cluster's
//     state can end up only in memory without warning, and that
//     state is lost at the next power cycle. (This rule does not
//     apply to roles that are absent from the spec. Their
//     directories stay on the RAM root.)
//
// This file owns the part of the process that runs on every boot:
// recognition, orchestration, and mounting. claim.go describes how
// the process claims a blank disk. grow.go describes how it grows a
// recognized partition.

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

// This comment lists where each role lands in the filesystem. liken
// defines this translation; the Machine API does not include it.
//
//	biosBoot         Not mounted. It is a raw partition that holds
//	                 GRUB's core image. The MBR's boot code reads
//	                 that image before any filesystem exists. liken
//	                 writes to it through the partition's device
//	                 node, never through a mount.
//	bootHome         /var/lib/liken/boot. It holds GRUB's config and
//	                 its environment block, the values a BIOS
//	                 machine uses in place of boot variables. It is
//	                 FAT32 because GRUB reads it with the same
//	                 driver that reads the slots.
//	systemA/systemB  /var/lib/liken/system/{a,b}. These are the
//	                 OS's own boot slots. They are FAT32 because the
//	                 firmware reads them. They allow no suid bit, no
//	                 device files, and no executables, because
//	                 nothing runs from a slot. The firmware only
//	                 loads what is in a slot.
//	machineState     /var/lib/liken/machine. It holds the machine's
//	                 own durable data, chiefly the staged and proven
//	                 manifests.
//	machineEphemeral /tmp, the OS's own scratch space. It disallows
//	                 suid and device files. It is world-writable
//	                 with the sticky bit set, the standard Unix
//	                 setup for /tmp.
//	clusterState     /var/lib/rancher. It holds all of k3s's state:
//	                 its database, its TLS material, and
//	                 containerd's images.
//	podStorage       The local-path provisioner's root directory.
//	                 The same path also appears in the image's
//	                 k3s config.yaml.
//	podEphemeral     kubelet's root directory: emptyDirs and pod
//	                 scratch space.
type roleMount struct {
	path   string
	flags  uintptr
	mode   os.FileMode // mode for the mounted root; 0 means the default mode
	fstype string      // "" means ext4, the default file system for data roles
}

const slotMountFlags = unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC

// bootHomeDir is the mount point for the bootHome role. It holds
// GRUB's config and environment block. init writes to this block
// when it arms or settles a slot trial on a BIOS machine.
const bootHomeDir = "/var/lib/liken/boot"

var roleMounts = map[machine.StorageRoleName]roleMount{
	machine.BootHomeRole:         {path: bootHomeDir, flags: slotMountFlags, fstype: "vfat"},
	machine.SystemARole:          {path: machine.SystemSlotDir("A"), flags: slotMountFlags, fstype: "vfat"},
	machine.SystemBRole:          {path: machine.SystemSlotDir("B"), flags: slotMountFlags, fstype: "vfat"},
	machine.MachineStateRole:     {path: machine.MachineStateDir},
	machine.MachineEphemeralRole: {path: "/tmp", flags: unix.MS_NOSUID | unix.MS_NODEV, mode: 0o1777},
	machine.ClusterStateRole:     {path: "/var/lib/rancher"},
	machine.PodStorageRole:       {path: "/var/lib/liken/pod-storage"},
	machine.PodEphemeralRole:     {path: "/var/lib/kubelet"},
}

// isSystemSlot reports whether a role is one of the two system slots
// that the firmware reads. These two roles use FAT32. Each one is
// typed as an EFI system partition, so the firmware can find it. Each
// one has a fixed size from the day it is claimed, because FAT cannot
// grow in place.
func isSystemSlot(name machine.StorageRoleName) bool {
	return name == machine.SystemARole || name == machine.SystemBRole
}

// isRawRole reports whether a role is a bare partition. The process
// recognizes, claims, and reports a bare partition like any other
// role, but it never formats or mounts one. biosBoot is the only
// bare-partition role. The MBR's boot code reads GRUB's core image
// long before any filesystem driver exists, so a filesystem there
// would only get in the way. liken writes to this partition through
// its device node.
func isRawRole(name machine.StorageRoleName) bool {
	return name == machine.BIOSBootRole
}

// isFixedSizeRole reports whether a role's size is set on the day it
// is claimed. The FAT32 roles have a fixed size, because FAT cannot
// grow in place. biosBoot also has a fixed size: GRUB's boot code
// stores the core image's location as literal sector numbers, and the
// boot-sector healing rewrite depends on a layout that never changes.
func isFixedSizeRole(name machine.StorageRoleName) bool {
	return isSystemSlot(name) || name == machine.BootHomeRole || name == machine.BIOSBootRole
}

// partitionTypeFor selects a role's GPT partition type. The system
// slots are EFI system partitions; the firmware finds them by this
// type GUID. biosBoot uses GRUB's own well-known type. Every other
// role uses the ordinary Linux data type.
func partitionTypeFor(name machine.StorageRoleName) [16]byte {
	switch {
	case isSystemSlot(name):
		return disks.EFISystemPartition
	case name == machine.BIOSBootRole:
		return disks.BIOSBootPartition
	}
	return disks.LinuxFilesystemData
}

// teardownStorage unmounts everything that reconciliation may have
// mounted, in reverse canonical order. This returns the machine to
// the state that a fresh reconcile expects, so the process can try a
// different spec. teardownStorage works from the mount table, not
// from a status record, because a reconcile that failed partway
// through may have mounted a role that it never reported. When a path
// is not a mount point, unmounting it returns EINVAL; here that only
// means there was nothing to unmount. Nothing else runs this early in
// boot, so nothing can keep these mounts busy.
func teardownStorage() {
	// mountAndSeedClusterState (in k3s.go) mounts clusterState's file
	// system at the staging point for a short time while it seeds it.
	// If this step fails partway through, the file system stays
	// mounted there.
	_ = unix.Unmount(clusterStateStaging, 0)
	unmountRoleMounts(0, true)
}

// unmountRoleMounts detaches every role file system in reverse
// canonical order. Two different shutdown paths share this function.
// Boot-time teardown unmounts each file system directly and reports
// any failure, because nothing else runs this early in boot, and a
// failed unmount here is useful information. The reboot path
// (reboot.go) passes MNT_DETACH and ignores errors: a container that
// was just killed can still hold its mount namespace open for a
// moment, which can pin a file system in place, and lazy detachment
// lets the kernel finish the unmount as those references clear, after
// the sync has already made the data safe. When a path is not a mount
// point, unmounting it returns EINVAL, which only means there was
// nothing to unmount.
func unmountRoleMounts(flags int, reportErrors bool) {
	for _, name := range slices.Backward(machine.StorageRoleNames) {
		target := roleMounts[name].path
		if target == "" {
			continue // raw roles are never mounted
		}
		err := unix.Unmount(target, flags)
		switch {
		case err == nil:
			fmt.Printf("liken: storage: unmounted %s\n", target)
		case reportErrors && !errors.Is(err, unix.EINVAL) && !errors.Is(err, fs.ErrNotExist):
			fmt.Fprintf(os.Stderr, "liken: storage: unmounting %s: %v\n", target, err)
		}
	}
}

// partition is a partition as sysfs presents it: a subdirectory of
// its disk's /sys/block entry. The kernel parses the GPT and writes
// the name it reads into the partition's uevent file. Recognition
// reads that name from uevent, so it never has to re-read any
// partition table itself.
type partition struct {
	name      string // the kernel's node name, for example vda1 or nvme0n1p2
	disk      string // the parent disk's node name, for example vda or nvme0n1
	partName  string // the GPT partition name; "" if the table has no names
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
			// their device; for example, vda gives vda1. The `partition`
			// file inside each one distinguishes it from the disk's
			// other attribute directories.
			if _, err := os.Stat(filepath.Join(dir, entry.Name(), "partition")); err != nil {
				continue
			}
			p := partition{name: entry.Name(), disk: disk.Name}
			if raw, err := os.ReadFile(filepath.Join(dir, entry.Name(), "size")); err == nil {
				if sectors, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64); err == nil {
					p.sizeBytes = sectors * disks.SectorSize
				}
			}
			// The uevent file holds KEY=value lines. PARTNAME
			// appears only in tables that carry names, and GPT is
			// such a table.
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

// reconcileStorage actuates the storage spec. On success, every
// declared role becomes a file system mounted at its role's path:
// ext4 for the data roles, FAT32 for the system slots that the
// firmware reads. The returned status records where each role
// landed. The status carries the same facts that the process prints
// to the console, and it goes into the Machine's status, because
// anything reported only to the serial port stays invisible to
// anyone who operates the machine remotely. An error means the
// process cannot satisfy a declared role. The caller (main, the only
// place with the authority to do so) then stops the boot instead of
// letting k3s start with that state only in RAM.
//
// The function plans everything, then applies the plan. It computes
// every claim's layout and every growth's table edit before it
// writes the first byte to any disk. A spec that will fail must fail
// before it changes anything on disk, because the boot may go on to
// try a different spec (for example, the proven manifest, after the
// process rejects a staged one). Partitions half-created under the
// failed spec would break that later attempt too. Planning cannot
// prevent one problem: a genuine I/O failure partway through a write.
// A disk claimed by a failed attempt stays claimed. If that leaves
// two partitions carrying the same role's name, recognition refuses
// to guess between them, and the boot stops.
func reconcileStorage(spec machine.StorageSpec) (machine.StorageStatus, error) {
	status := machine.AllRolesInMemory()
	roles := spec.Roles()
	if len(roles) == 0 {
		return status, nil
	}
	if err := spec.Validate(); err != nil {
		return status, err
	}

	// Recognition finds each declared role by the name written on its
	// partition. It does not consult the device listed in the spec. A
	// disk that has moved to a different controller since it was
	// claimed is still the same disk.
	found, err := recognizeRoles(roles)
	if err != nil {
		return status, err
	}

	// Plan the claims. Any role that is still missing must point at a
	// blank disk. Group the roles by device, because roles that share
	// a disk are claimed together in one partition table.
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

	// Plan the growth. A recognized partition may be smaller than the
	// spec now declares, or its disk may have grown underneath it.
	// grow.go explains the rules for growth.
	grows, err := planAllGrowth(roles, found)
	if err != nil {
		return status, err
	}

	// Apply the plans. The process has already validated every plan,
	// so any failure from this point on is a real I/O problem.
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
		// A raw role needs nothing more once it is recognized: there
		// is no file system to make and no mount point to serve. The
		// status still records where the partition landed, because a
		// fact such as which device holds the boot code must not
		// live only on a serial console.
		if isRawRole(role.Name) {
			fmt.Printf("liken: storage: %s is %s/%s (%s), raw\n",
				role.Name, devRoot, p.name, p.partName)
		} else if err := mountRole(role, p); err != nil {
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
// partitions, as sysfs reports them.
func recognizeRoles(roles []machine.DeclaredRole) (map[machine.StorageRoleName]partition, error) {
	return matchRoles(roles, discoverPartitions())
}

// matchRoles matches declared roles to partitions by name. When two
// partitions carry the same role name, a disk was usually cloned or
// moved from another machine. A wrong guess about which partition
// holds the real cluster would destroy data, so this ambiguity is an
// error, not a choice to make.
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

// mountRole mounts a role's file system at the role's path. It makes
// the file system first if the partition is fresh from a claim.
// (Recognizing liken's own name on a partition with no file system
// also covers a boot that died between partitioning and mkfs.
// Claiming is resumable because the process writes the name first.)
func mountRole(role machine.DeclaredRole, p partition) error {
	// The process looks up the role's translation to a mount before
	// anything touches the partition. It must refuse a role that liken
	// does not know how to mount, before mke2fs writes a file system
	// onto it.
	rm, ok := roleMounts[role.Name]
	if !ok {
		return fmt.Errorf("role %s has no mount translation; liken and its manifest disagree about the role vocabulary", role.Name)
	}
	target := rm.path

	// Each file system type has its own maker. The system slots get
	// FAT32 from liken's own formatter (fat32.go), because the
	// firmware reads them and FAT is the only file system it reads.
	// Every other role gets ext4 from the vendored static mke2fs.
	// Either way, recognizing liken's own name on a partition with no
	// file system covers a boot that died between partitioning and
	// mkfs.
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

	// clusterState mounts through its own path. The image bakes k3s's
	// seed files in underneath its mount point. Layering those seed
	// files in belongs to k3s, not to partition mechanics; k3s.go
	// handles it.
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
	// A partition that grew this boot still carries a file system
	// sized for its old extent. Now that the file system is mounted,
	// ext4 can grow online to fill it (ext4.go explains why a mounted
	// file system is the easy case). FAT cannot grow in place, and
	// that is expected: slots have a fixed size by design, and
	// planAllGrowth refuses to grow them at the planning stage.
	if rm.fstype == "" {
		if err := maybeGrowFilesystem(role, p, target); err != nil {
			return err
		}
	}

	// A freshly made ext4 root has mode 0755, root-only. Roles such as
	// /tmp need their conventional permissions applied to the mounted
	// root.
	if rm.mode != 0 {
		if err := os.Chmod(target, rm.mode); err != nil {
			fmt.Fprintf(os.Stderr, "liken: storage: chmod %s: %v\n", target, err)
		}
	}
	fmt.Printf("liken: storage: %s is %s (%s) on %s\n",
		role.Name, dev, p.partName, target)
	return nil
}
