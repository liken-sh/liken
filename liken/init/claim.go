package main

// When a declared storage role has no partition yet, init claims a
// blank disk for it: it writes a GPT and sets each partition's name to
// the role's PartitionName. Later boots find the partition by that
// name (storage.go), and grow.go resizes it.
//
// Claiming runs on every boot. It is safe for these reasons:
//
//   - init claims only a blank disk (isBlank). A disk with a partition
//     table or a filesystem can hold someone's data, so planClaim
//     refuses it with an error.
//
//   - init writes nothing until it has planned the table of every disk
//     in memory. A spec that does not fit fails while every disk is
//     unchanged, so the boot can still fall back to the proven
//     manifest after it rejects a staged one.
//
//   - The partition name is written before any filesystem. When a boot
//     stops between partitioning and mkfs, the next boot recognizes
//     the partition and makes its filesystem (needsFilesystem in
//     storage.go).
//
// A new partition holds whatever bytes the disk held before, and those
// can include an old filesystem's superblock. So storage.go makes a new
// filesystem on each partition that the same boot created.
//
// A role's device can be a kernel name or a stable name under
// /dev/disk/by-id/ or /dev/disk/by-path/, and resolve.go finds the disk
// for either.

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/liken-sh/liken/liken/disks"
	"github.com/liken-sh/liken/liken/machine"
)

// isBlank reports whether the device has no MBR, no GPT, and no ext4
// filesystem written directly to it.
func isBlank(devPath string) (bool, error) {
	f, err := os.Open(devPath)
	if err != nil {
		return false, err
	}
	defer f.Close()

	// The MBR's 0x55AA is at byte 510, GPT's "EFI PART" at byte 512,
	// and ext4's magic at byte 1080.
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

// A claimPlan is the partition table that applyClaim writes to one
// blank disk.
type claimPlan struct {
	device       string
	totalSectors uint64
	parts        []disks.Partition
	roles        []machine.DeclaredRole
}

// planAllClaims returns a claimPlan for each disk that a missing role
// declares. It groups the roles by the disk that their device resolves
// to, because two roles can name one disk by its kernel name and by a
// stable name. When a disk has both a found role and a missing role,
// planAllClaims returns an error: liken wrote that disk's table, and
// something has changed it since.
//
// A found role's device only has to resolve when it names the same
// disk as a missing role, because recognition finds the partition by
// its name. A found role whose device does not resolve is skipped.
// A missing role's device must resolve to exactly one disk, or
// planAllClaims returns an error.
func planAllClaims(roles []machine.DeclaredRole, found map[machine.StorageRoleName]partition) ([]claimPlan, error) {
	type group struct {
		disk  *machine.BlockDevice
		roles []machine.DeclaredRole // missing roles that declare this disk
		found *machine.DeclaredRole  // a found role that declares this disk
	}
	groups := map[string]*group{}
	var order []string

	for _, role := range roles {
		_, isFound := found[role.Name]
		// Only a missing role waits for its disk to attach, because on
		// an install boot the controller drivers have just loaded. A
		// found role with a stale device would otherwise make every
		// boot wait for the whole attach deadline.
		disk, err := resolveDeclaredDisk(role.Device)
		if !isFound && err == nil && disk == nil {
			disk, err = awaitDeclaredDisk(role.Device,
				fmt.Sprintf("liken: storage: waiting for %s to attach", role.Device))
		}
		switch {
		case isFound:
			if err != nil || disk == nil {
				continue
			}
		case err != nil:
			return nil, err
		case disk == nil:
			return nil, fmt.Errorf("declared device %s is not attached", role.Device)
		}

		key := devicePath(*disk)
		g, ok := groups[key]
		if !ok {
			g = &group{disk: disk}
			groups[key] = g
			order = append(order, key)
		}
		if isFound {
			if g.found == nil {
				g.found = &role
			}
			continue
		}
		g.roles = append(g.roles, role)
	}

	var claims []claimPlan
	for _, key := range order {
		g := groups[key]
		if len(g.roles) == 0 {
			continue
		}
		if g.found != nil {
			return nil, fmt.Errorf("disk %s already has the partition %s but not the other roles declared on it, so init does not change the disk",
				key, g.found.PartitionName())
		}
		if err := oneSizelessRole(key, g.roles); err != nil {
			return nil, err
		}
		plan, err := planClaim(g.roles[0].Device, g.disk, g.roles)
		if err != nil {
			return nil, err
		}
		claims = append(claims, plan)
	}
	return claims, nil
}

// oneSizelessRole returns an error when two roles on one disk both
// omit their size. StorageSpec.Validate makes the same check by the
// declared device string, so it misses two names for one disk.
// Without this check, planPartitions would give the rest of the disk
// to the last sizeless role and leave the other out of the table.
func oneSizelessRole(disk string, roles []machine.DeclaredRole) error {
	var remainder *machine.DeclaredRole
	for _, role := range roles {
		if role.Size != "" {
			continue
		}
		if remainder != nil {
			return fmt.Errorf("storage roles %s and %s both omit their size on %s, and only one role on a disk may omit its size",
				remainder.Name, role.Name, disk)
		}
		remainder = &role
	}
	return nil
}

// planClaim checks that the disk is blank and returns the table for
// the roles. It writes nothing. The caller must already have checked
// that no found role is on the disk and that at most one role omits
// its size.
func planClaim(declared string, disk *machine.BlockDevice, roles []machine.DeclaredRole) (claimPlan, error) {
	if disk == nil {
		return claimPlan{}, fmt.Errorf("declared device %s is not attached", declared)
	}
	device := devicePath(*disk)
	blank, err := isBlank(device)
	if err != nil {
		return claimPlan{}, fmt.Errorf("examining %s: %w", device, err)
	}
	if !blank {
		return claimPlan{}, fmt.Errorf("%s has a partition table or filesystem that liken does not recognize, so init does not change it. If its data is not needed, erase the disk yourself", device)
	}

	totalSectors := disk.SizeBytes / disks.SectorSize
	parts, err := planPartitions(device, roles, totalSectors)
	if err != nil {
		return claimPlan{}, err
	}
	return claimPlan{device: device, totalSectors: totalSectors, parts: parts, roles: roles}, nil
}

// applyClaim writes the plan's table and waits for the kernel to
// show its partitions.
func applyClaim(plan claimPlan) error {
	fmt.Printf("liken: storage: claiming %s (%s) for %d role(s)\n",
		plan.device, gib(plan.totalSectors*disks.SectorSize), len(plan.roles))
	if err := disks.Write(plan.device, plan.totalSectors, plan.parts); err != nil {
		return fmt.Errorf("partitioning %s: %w", plan.device, err)
	}
	return waitForPartitions(plan.parts)
}

// planPartitions lays out the table. The roles with a size go first,
// in the order of roles, each one starting on a 1 MiB boundary. The
// role with no size takes the rest of the disk. device is used only
// in error messages.
func planPartitions(device string, roles []machine.DeclaredRole, totalSectors uint64) ([]disks.Partition, error) {
	lastUsable := disks.LastUsableLBA(totalSectors)
	var parts []disks.Partition
	next := uint64(disks.PartitionAlignment)
	var remainder *machine.DeclaredRole
	for _, role := range roles {
		if role.Size == "" {
			remainder = &role
			continue
		}
		bytes, _ := machine.ParseSize(role.Size) // validated before any disk is touched
		sectors := (bytes + disks.SectorSize - 1) / disks.SectorSize
		p := disks.Partition{Name: role.PartitionName(), FirstLBA: next, LastLBA: next + sectors - 1,
			TypeGUID: partitionTypeFor(role.Name)}
		if p.LastLBA > lastUsable {
			return nil, fmt.Errorf("disk %s is too small: %s needs %s starting at sector %d, and the usable space of the disk ends at sector %d",
				device, role.Name, role.Size, p.FirstLBA, lastUsable)
		}
		parts = append(parts, p)
		next = disks.AlignLBA(p.LastLBA + 1)
	}
	if remainder != nil {
		if next > lastUsable {
			return nil, fmt.Errorf("disk %s is too small: nothing left for %s", device, remainder.Name)
		}
		parts = append(parts, disks.Partition{Name: remainder.PartitionName(), FirstLBA: next, LastLBA: lastUsable,
			TypeGUID: partitionTypeFor(remainder.Name)})
	}
	return parts, nil
}

const partitionWaitTimeout = 5 * time.Second

// waitForPartitions waits up to partitionWaitTimeout until sysfs shows
// each partition in parts with its planned size, because the entries
// can appear after the BLKRRPART ioctl that rereads the table returns.
// The size check lets grow.go use the same wait, because a
// grown partition keeps its name.
func waitForPartitions(parts []disks.Partition) error {
	deadline := time.Now().Add(partitionWaitTimeout)
	for {
		visible := map[string]uint64{}
		for _, p := range discoverPartitions() {
			visible[p.partName] = p.sizeBytes
		}
		var missing []string
		for _, want := range parts {
			wantBytes := (want.LastLBA - want.FirstLBA + 1) * disks.SectorSize
			got, ok := visible[want.Name]
			switch {
			case !ok:
				missing = append(missing, want.Name)
			case got != wantBytes:
				missing = append(missing, fmt.Sprintf("%s (still %d bytes, want %d)", want.Name, got, wantBytes))
			}
		}
		if len(missing) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("after %s, sysfs does not show the planned partitions that the table has: %s",
				partitionWaitTimeout, strings.Join(missing, ", "))
		}
		time.Sleep(100 * time.Millisecond)
	}
}
