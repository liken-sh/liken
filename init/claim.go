package main

// Claiming a blank disk.
//
// Claiming happens once in a disk's life; it is one half of storage
// reconciliation. storage.go owns the other half, which runs on every
// boot: recognition and mounting. grow.go owns growth. A machine's
// first boot finds declared roles with no partitions to recognize.
// Claiming is how those partitions come to exist: a blank disk gets a
// GPT, and each role's name is written into its partition entry. That
// name is the identity every later boot recognizes.
//
// Two properties make claiming safe to attempt on every boot:
//
//   - Only a blank disk may be claimed. Blank means no partition
//     table and no filesystem signature at all (isBlank below): a
//     disk that nothing, including liken, ever wrote to. The code
//     refuses anything else and states the reason on the console,
//     because a disk someone else formatted holds someone else's
//     data.
//
//   - Nothing is written until the code has validated every disk's
//     plan. planClaim lays out a claim entirely in memory first, so a
//     spec that cannot fit fails before the first byte lands on any
//     disk. The boot can then fall back to a different spec (the
//     proven manifest, after it rejects a staged one) against disks
//     that are exactly as it expects them.
//
// Claiming can also resume after an interruption. The role's name
// goes into the partition table first, before any filesystem exists.
// So a boot that dies between partitioning and mkfs leaves partitions
// that the next boot recognizes by name and finishes formatting
// (mountRole's half of the task, in storage.go).

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/liken-sh/liken/disks"
	"github.com/liken-sh/liken/machine"
)

// isBlank reports whether a disk carries nothing recognizable: no MBR
// or GPT, no ext4 filesystem written straight to the device. Claiming
// is allowed only when a disk is blank. A disk something else
// formatted fails one of these checks, and the code leaves it alone.
func isBlank(devPath string) (bool, error) {
	f, err := os.Open(devPath)
	if err != nil {
		return false, err
	}
	defer f.Close()

	// 2048 bytes covers all three signatures: the MBR's 0x55AA at
	// byte 510, GPT's "EFI PART" at byte 512, and ext4's magic at
	// byte 1080.
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

// A claimPlan is one blank disk's pending partition table. The code
// validates and lays it out up front, and writes it only after every
// disk's plan has succeeded.
type claimPlan struct {
	device       string
	totalSectors uint64
	parts        []disks.Partition
	roleCount    int
}

// planClaim validates that a disk may be claimed for every declared
// role that names it, and lays out its table. Sized roles come first,
// at their exact sizes, in canonical order. The remainder role, if
// any, takes whatever space is left. planClaim writes nothing.
func planClaim(device string, roles []machine.DeclaredRole, found map[machine.StorageRoleName]partition) (claimPlan, error) {
	var mine []machine.DeclaredRole
	for _, role := range roles {
		if role.Device == device {
			// A disk where the code recognizes some roles but others
			// still need claiming is a disk whose table liken wrote,
			// and something later changed. It is not blank, so the
			// code cannot claim it, and it is not safe to repair
			// automatically.
			if _, ok := found[role.Name]; ok {
				return claimPlan{}, fmt.Errorf("disk %s already carries %s but is missing other declared roles; refusing to modify it",
					device, role.PartitionName())
			}
			mine = append(mine, role)
		}
	}

	disk := diskByPath(device)
	if disk == nil {
		return claimPlan{}, fmt.Errorf("declared device %s is not attached", device)
	}
	blank, err := isBlank(device)
	if err != nil {
		return claimPlan{}, fmt.Errorf("examining %s: %w", device, err)
	}
	if !blank {
		return claimPlan{}, fmt.Errorf("%s carries a partition table or filesystem liken doesn't recognize; refusing to touch it (wipe it yourself if it's expendable)", device)
	}

	totalSectors := disk.SizeBytes / disks.SectorSize
	parts, err := planPartitions(device, mine, totalSectors)
	if err != nil {
		return claimPlan{}, err
	}
	return claimPlan{device: device, totalSectors: totalSectors, parts: parts, roleCount: len(mine)}, nil
}

// applyClaim writes one planned table and waits for the kernel to
// show its partitions.
func applyClaim(plan claimPlan) error {
	fmt.Printf("liken: storage: claiming %s (%s) for %d role(s)\n",
		plan.device, gib(plan.totalSectors*disks.SectorSize), plan.roleCount)
	if err := disks.Write(plan.device, plan.totalSectors, plan.parts); err != nil {
		return fmt.Errorf("partitioning %s: %w", plan.device, err)
	}
	return waitForPartitions(plan.parts, 5*time.Second)
}

// planPartitions lays out a claimed disk's table. Sized roles pack
// from the front, in canonical order, and each partition start
// aligns to 1MiB. The single, validated, sizeless role takes the
// rest of the disk. The device name appears only in error messages;
// the layout math works purely in sectors.
func planPartitions(device string, mine []machine.DeclaredRole, totalSectors uint64) ([]disks.Partition, error) {
	lastUsable := disks.LastUsableLBA(totalSectors)
	var parts []disks.Partition
	next := uint64(disks.PartitionAlignment)
	var remainder *machine.DeclaredRole
	for _, role := range mine {
		if role.Size == "" {
			remainder = &role
			continue
		}
		bytes, _ := machine.ParseSize(role.Size) // validated before any disk is touched
		sectors := (bytes + disks.SectorSize - 1) / disks.SectorSize
		p := disks.Partition{Name: role.PartitionName(), FirstLBA: next, LastLBA: next + sectors - 1,
			TypeGUID: partitionTypeFor(role.Name)}
		if p.LastLBA > lastUsable {
			return nil, fmt.Errorf("disk %s is too small: %s wants %s at sector %d but the disk's usable space ends at %d",
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

// waitForPartitions gives the kernel a moment to show the devices for
// a table just written. BLKRRPART works synchronously, but the
// devtmpfs nodes and sysfs entries appear slightly later. Each
// partition must appear at its planned size. This lets the same wait
// serve growth as well as claiming: a partition still showing its old
// geometry is as wrong as one that has not appeared. If the deadline
// passes, the error names each partition that did not appear as
// expected.
func waitForPartitions(parts []disks.Partition, patience time.Duration) error {
	deadline := time.Now().Add(patience)
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
			return fmt.Errorf("partitions never appeared after their table was written: %s",
				strings.Join(missing, ", "))
		}
		time.Sleep(100 * time.Millisecond)
	}
}
