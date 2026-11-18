package main

// This file reconciles spec.storage on every boot. It finds the
// partition of each declared role by its GPT partition name, claims a
// blank disk for a role that has no partition, and mounts every role.
// claim.go partitions a blank disk, grow.go grows a partition, and
// machine/storage.go defines the API.
//
// It runs on every boot, so it follows two rules:
//
//   - It never destroys data. It claims only a disk with no partition
//     table and no filesystem, and it refuses any other disk, with the
//     reason on the console.
//
//   - A declared role that it cannot find or claim stops the boot, and
//     the machine powers off. Otherwise k3s would start with that
//     role's directories on the RAM root, and the next power cycle
//     would lose the cluster's state with no warning. A role that the
//     spec does not declare stays on the RAM root and does not stop
//     the boot.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/disks"
	"github.com/liken-sh/liken/liken/machine"
)

// roleMount is where a role mounts and with which options. The Machine
// API names only the roles, and init chooses the paths:
//
//	biosBoot         Not mounted. It is a raw partition for GRUB's
//	                 core image, which the MBR's boot code reads before
//	                 any filesystem driver exists. liken writes to it
//	                 through the partition's device node.
//	bootHome         /var/lib/liken/boot. It holds GRUB's config and
//	                 its environment block, which a BIOS machine uses
//	                 in place of boot variables. It is FAT32 because
//	                 GRUB reads it with the driver that reads the slots.
//	systemA/systemB  /var/lib/liken/system/{a,b}, the OS's boot slots.
//	                 They are FAT32 because the firmware reads them.
//	                 They mount noexec, nosuid, and nodev, because the
//	                 firmware loads from a slot and nothing runs from
//	                 it.
//	machineState     /var/lib/liken/machine, the machine's own durable
//	                 data, such as the staged and proven manifests.
//	machineEphemeral /tmp, nosuid and nodev, with mode 1777 as on any
//	                 Unix.
//	clusterState     /var/lib/rancher, all of k3s's state: its
//	                 database, its TLS material, and containerd's
//	                 images.
//	podStorage       The root directory of the local-path provisioner.
//	                 The image's k3s config.yaml sets the same path.
//	podEphemeral     kubelet's root directory, with emptyDirs and pod
//	                 scratch space. podlogs.go bind mounts a directory
//	                 of it on /var/log/pods, so the pod logs are on this
//	                 disk too.
type roleMount struct {
	path   string
	flags  uintptr
	mode   os.FileMode // mode for the mounted root, or 0 for the default mode
	fstype string      // "" means ext4, the default file system for data roles
}

const slotMountFlags = unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC

// bootHomeDir is where the bootHome role mounts. On a BIOS machine,
// init writes GRUB's environment block there to start or end a trial
// boot of a slot. No role may mount on bootMountsDir or above it,
// because that tree holds the mounts of the slot and the system image
// that this boot started from (switchroot.go).
const bootHomeDir = "/var/lib/liken/boot"

// machineStateWritable is true while machineState is mounted. failBoot
// writes its record only then, because a record on the RAM root is
// lost at power-off (failstop.go). mountRole sets it as soon as
// machineState mounts, so a boot that stops on a later role can still
// record why.
var machineStateWritable bool

var roleMounts = map[machine.StorageRoleName]roleMount{
	machine.BootHomeRole:         {path: bootHomeDir, flags: slotMountFlags, fstype: "vfat"},
	machine.SystemARole:          {path: machine.SystemSlotDir("A"), flags: slotMountFlags, fstype: "vfat"},
	machine.SystemBRole:          {path: machine.SystemSlotDir("B"), flags: slotMountFlags, fstype: "vfat"},
	machine.MachineStateRole:     {path: machine.MachineStateDir},
	machine.MachineEphemeralRole: {path: "/tmp", flags: unix.MS_NOSUID | unix.MS_NODEV, mode: tmpMode},
	machine.ClusterStateRole:     {path: "/var/lib/rancher"},
	machine.PodStorageRole:       {path: "/var/lib/liken/pod-storage"},
	machine.PodEphemeralRole:     {path: "/var/lib/kubelet"},
}

// mountFilesystem and unmountFilesystem are variables so that a test
// can record each mount(2) and umount(2) call. A test process cannot
// mount anything, and a mount that always fails would leave the code
// after it untested.
var (
	mountFilesystem   = unix.Mount
	unmountFilesystem = unix.Unmount
)

func isSystemSlot(name machine.StorageRoleName) bool {
	return name == machine.SystemARole || name == machine.SystemBRole
}

// isRawRole reports whether a role is a bare partition, which init
// finds, claims, and reports like any other role but never formats or
// mounts.
func isRawRole(name machine.StorageRoleName) bool {
	return name == machine.BIOSBootRole
}

// isFixedSizeRole reports whether a role keeps the size it was claimed
// with. FAT cannot grow in place, so the system slots and bootHome are
// fixed. biosBoot is fixed because the MBR's boot code refers to GRUB's
// core image by its sector numbers.
func isFixedSizeRole(name machine.StorageRoleName) bool {
	return isSystemSlot(name) || name == machine.BootHomeRole || name == machine.BIOSBootRole
}

func partitionTypeFor(name machine.StorageRoleName) [16]byte {
	switch {
	case isSystemSlot(name):
		return disks.EFISystemPartition
	case name == machine.BIOSBootRole:
		return disks.BIOSBootPartition
	}
	return disks.LinuxFilesystemData
}

// teardownStorage unmounts everything that reconcileStorage may have
// mounted, so that the boot can try a different spec from a clean
// state. It tries every role's path, because a reconcile that failed
// partway may have mounted a role that its status does not list.
func teardownStorage() {
	// mountAndSeedClusterState (k3s.go) mounts clusterState at
	// clusterStateStaging while it copies the seed files in, and a
	// failure partway leaves it mounted there.
	_ = unmountFilesystem(clusterStateStaging, 0)
	unmountRoleMounts(0, true)
	machineStateWritable = false
}

// unmountRoleMounts unmounts every role's filesystem in reverse
// canonical order. Boot-time teardown passes no flags and reports
// failures. The reboot path (reboot.go) passes MNT_DETACH and ignores
// errors, because a container that was just killed can keep its mount
// namespace open and so keep a filesystem busy. With a lazy unmount,
// the kernel finishes the unmount when the last reference goes. On the
// reboot path, quiesceDisks remounts the filesystems read-only before
// this runs, and the reboot calls sync after it.
func unmountRoleMounts(flags int, reportErrors bool) {
	// The pod-log bind mount keeps podEphemeral's filesystem busy, so
	// it is unmounted first.
	unmountPodLogs(flags, reportErrors)
	for _, name := range slices.Backward(machine.StorageRoleNames) {
		target := roleMounts[name].path
		if target == "" {
			continue // a raw role is never mounted
		}
		detachMount(target, flags, reportErrors)
	}
}

// detachMount unmounts target and prints the result. It does not report
// EINVAL, which umount(2) returns for a path that is not a mount point,
// or ErrNotExist, and it reports no failure when reportErrors is false.
func detachMount(target string, flags int, reportErrors bool) {
	err := unmountFilesystem(target, flags)
	switch {
	case err == nil:
		fmt.Printf("liken: storage: unmounted %s\n", target)
	case reportErrors && !errors.Is(err, unix.EINVAL) && !errors.Is(err, fs.ErrNotExist):
		fmt.Fprintf(os.Stderr, "liken: storage: unmounting %s: %v\n", target, err)
	}
}

// partition is a partition as sysfs lists it under its disk's
// /sys/block entry. The kernel parses the GPT and puts each partition's
// name in its uevent file, so init does not read the partition table
// itself.
type partition struct {
	name      string // the kernel's node name, for example vda1 or nvme0n1p2
	disk      string // the parent disk's node name, for example vda or nvme0n1
	partName  string // the GPT partition name, or "" when the table has no names
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
			// Only a partition's directory has a `partition` file. The
			// disk's other subdirectories are attribute groups.
			if _, err := os.Stat(filepath.Join(dir, entry.Name(), "partition")); err != nil {
				continue
			}
			p := partition{name: entry.Name(), disk: disk.Name}
			if raw, err := os.ReadFile(filepath.Join(dir, entry.Name(), "size")); err == nil {
				if sectors, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64); err == nil {
					p.sizeBytes = sectors * disks.SectorSize
				}
			}
			// PARTNAME is present only when the table has names, as GPT
			// does.
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

// reconcileStorage mounts a filesystem for every declared role at the
// role's path, after it claims blank disks and grows partitions as the
// spec requires. The returned status records which partition backs
// each role, and it goes into the Machine's status, because a person
// who operates the machine remotely cannot read the serial console. An
// error means that a declared role cannot be mounted, and main then
// stops the boot.
//
// It plans every claim and every growth before it writes to any disk,
// because after a failure the boot can try a different spec, such as
// the proven manifest after a staged one is rejected, and partitions
// that the failed spec half-created would break that attempt. An I/O
// error partway through a write can still leave a disk claimed. When
// that leaves two partitions with one role's name, matchRoles refuses
// both and the boot stops.
func reconcileStorage(spec machine.StorageSpec) (machine.StorageStatus, error) {
	status := machine.AllRolesInMemory()
	roles := spec.Roles()
	if len(roles) == 0 {
		return status, nil
	}
	if err := spec.Validate(); err != nil {
		return status, err
	}

	// This runs early in boot, while a SATA link or a USB disk can still
	// take seconds to appear after its driver loads. A disk that has
	// not appeared yet looks the same as one that does not exist.
	awaitStorageDevices(roles)

	// Roles are found by partition name, not by the device in the
	// spec, so a disk that moved to another controller is still found.
	found, err := recognizeRoles(roles)
	if err != nil {
		return status, err
	}

	// A disk that holds a role that recognizeRoles found is not blank.
	// So when a role that was not found declares the same disk,
	// planAllClaims returns an error and leaves the disk unchanged.
	claims, err := planAllClaims(roles, found)
	if err != nil {
		return status, err
	}

	grows, err := planAllGrowth(roles, found)
	if err != nil {
		return status, err
	}

	// The plans are already validated, so a failure from here is an
	// I/O error. A partition that this boot created always gets a new
	// filesystem (needsFilesystem).
	created := map[machine.StorageRoleName]bool{}
	for _, plan := range claims {
		if err := applyClaim(plan); err != nil {
			return status, err
		}
		for _, role := range plan.roles {
			created[role.Name] = true
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
				return status, fmt.Errorf("partition %s of role %s did not appear on %s after its partition table was changed",
					role.PartitionName(), role.Name, role.Device)
			}
		}
	}

	for _, role := range roles {
		p := found[role.Name]
		// Read whether a FAT role was cleanly unmounted at the last stop
		// before mounting it, because a mount for writing sets the
		// filesystem's dirty bit (fatstate.go).
		var unclean bool
		if roleMounts[role.Name].fstype == "vfat" {
			unclean = readFATStop(role.Name, devRoot+"/"+p.name, created[role.Name])
		}
		// A raw role is not mounted, but its status still records the
		// partition that holds the boot code.
		if isRawRole(role.Name) {
			fmt.Printf("liken: storage: role %s is the raw partition %s/%s (%s)\n",
				role.Name, devRoot, p.name, p.partName)
		} else if err := mountRole(role, p, created[role.Name]); err != nil {
			return status, err
		}
		*status.Role(role.Name) = machine.StorageRoleStatus{
			Backing:         machine.BackingPartition,
			Device:          p.name,
			Partition:       p.partName,
			CapacityBytes:   p.sizeBytes,
			LastStopUnclean: unclean,
		}
	}
	return status, nil
}

func recognizeRoles(roles []machine.DeclaredRole) (map[machine.StorageRoleName]partition, error) {
	return matchRoles(roles, discoverPartitions())
}

// matchRoles matches declared roles to partitions by partition name.
// Two partitions with one role's name usually mean that a disk was
// cloned or moved from another machine. Choosing the wrong one could
// destroy data, so matchRoles returns an error.
func matchRoles(roles []machine.DeclaredRole, parts []partition) (map[machine.StorageRoleName]partition, error) {
	found := map[machine.StorageRoleName]partition{}
	for _, role := range roles {
		for _, p := range parts {
			if p.partName != role.PartitionName() {
				continue
			}
			if existing, ok := found[role.Name]; ok {
				return nil, fmt.Errorf("partitions %s and %s are both named %s, so init cannot choose the one for its role",
					existing.name, p.name, role.PartitionName())
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

// awaitStorageDevices waits until every declared role has a partition
// or a disk to claim, or until the deadline passes. It polls /sys/block
// instead of listening for uevents, because the wait happens once per
// boot and lasts seconds.
//
// Only a role with no partition needs its declared device resolved.
// The device of a found role is never claimed, so it does not matter
// when that device is stale or ambiguous.
//
// An error ends the wait at once, because another disk attaching
// cannot fix two partitions with one role's name or a device name that
// matches two disks. reconcileStorage gets the same error from
// recognizeRoles or planAllClaims and returns it. After the deadline,
// reconcileStorage reports what is missing.
func awaitStorageDevices(roles []machine.DeclaredRole) {
	const (
		poll     = 500 * time.Millisecond
		deadline = 30 * time.Second
	)
	ready := func() (bool, error) {
		found, err := recognizeRoles(roles)
		if err != nil {
			return false, err
		}
		for _, role := range roles {
			if _, ok := found[role.Name]; ok {
				continue
			}
			disk, err := resolveDeclaredDisk(role.Device)
			if err != nil {
				return false, err
			}
			if disk == nil {
				return false, nil
			}
		}
		return true, nil
	}

	ok, err := ready()
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: storage: %v\n", err)
		return
	}
	if ok {
		return
	}
	fmt.Println("liken: storage: waiting for the declared disks to attach")
	for begin := time.Now(); time.Since(begin) < deadline; {
		time.Sleep(poll)
		ok, err := ready()
		if err != nil {
			fmt.Fprintf(os.Stderr, "liken: storage: %v\n", err)
			return
		}
		if ok {
			fmt.Println("liken: storage: the declared disks attached")
			return
		}
	}
	fmt.Fprintf(os.Stderr, "liken: storage: the declared disks did not all attach within %s\n", deadline)
}

// needsFilesystem reports whether a partition needs a new filesystem
// before it is mounted.
//
// A partition that this boot created always does. A reinstall blanks
// the partition table and writes the same layout at the same offsets,
// and blanking leaves the old filesystems, with their superblocks, in
// place. Keeping one would carry the previous install's etcd database,
// proven manifest, and node password into the new install.
//
// A found partition with no filesystem needs one too. That happens
// when a boot stopped between partitioning and mkfs. The partition
// table already has the role's name, so the next boot finds the
// partition and makes the filesystem.
func needsFilesystem(dev, fstype string, created bool) bool {
	if created {
		return true
	}
	if fstype == "vfat" {
		return !disks.HasFAT32(dev)
	}
	return !hasExt4(dev)
}

// mountRole makes a role's filesystem when needsFilesystem returns
// true, and mounts it at the role's path. created is true when this
// boot created the partition.
func mountRole(role machine.DeclaredRole, p partition, created bool) error {
	// Refuse a role with no roleMounts entry before mke2fs writes to
	// its partition.
	rm, ok := roleMounts[role.Name]
	if !ok {
		return fmt.Errorf("role %s has no entry in roleMounts, so init has no path to mount it at", role.Name)
	}
	target := rm.path

	// formatSlot (slots.go) formats the FAT roles. Every other role gets
	// ext4 from the static mke2fs that liken/userland builds.
	dev := devRoot + "/" + p.name
	if needsFilesystem(dev, rm.fstype, created) {
		if rm.fstype == "vfat" {
			fmt.Printf("liken: storage: making a FAT32 filesystem on %s for role %s\n", dev, role.Name)
			if err := formatSlot(dev, p.sizeBytes, role.Name); err != nil {
				return fmt.Errorf("formatting %s for role %s: %w", dev, role.Name, err)
			}
		} else {
			fmt.Printf("liken: storage: making an ext4 filesystem on %s for role %s\n", dev, role.Name)
			if !runNarrated("mke2fs | ", "/sbin/mke2fs", "-t", "ext4", dev) {
				return fmt.Errorf("mke2fs did not make an ext4 filesystem on %s for role %s", dev, role.Name)
			}
		}
	}

	// mountAndSeedClusterState (k3s.go) mounts clusterState, because
	// it also copies the image's k3s seed files onto it.
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
			return fmt.Errorf("creating the mount point of role %s: %w", role.Name, err)
		}
		if err := mountFilesystem(dev, target, fstype, rm.flags, ""); err != nil {
			return fmt.Errorf("mounting %s for role %s: %w", dev, role.Name, err)
		}
	}
	// A partition that grew on this boot still has a filesystem of its
	// old size, and ext4 grows while mounted (ext4.go). planAllGrowth
	// never grows a FAT role.
	if rm.fstype == "" {
		if err := maybeGrowFilesystem(role, p, target); err != nil {
			return err
		}
	}

	// A new ext4 root has mode 0755, which is wrong for /tmp.
	if rm.mode != 0 {
		if err := os.Chmod(target, rm.mode); err != nil {
			fmt.Fprintf(os.Stderr, "liken: storage: setting the mode of %s: %v\n", target, err)
		}
	}
	if role.Name == machine.MachineStateRole {
		machineStateWritable = true
	}
	fmt.Printf("liken: storage: role %s is %s (%s), mounted on %s\n",
		role.Name, dev, p.partName, target)
	return nil
}
