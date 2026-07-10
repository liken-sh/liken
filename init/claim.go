package main

// Claiming a blank disk.
//
// Claiming is the once-in-a-disk's-life half of storage reconciliation
// (storage.go owns the every-boot half, recognition and mounting, and
// grow.go owns growth). A machine's first boot finds declared roles
// with no partitions to recognize; claiming is how those partitions
// come to exist: a blank disk gets a GPT, and each role's name is
// written into its partition entry, which is the identity every later
// boot recognizes.
//
// Two properties make claiming safe to attempt on every boot:
//
//   - Only a blank disk may be claimed. Blank means no partition
//     table and no filesystem signature at all (isBlank below): a
//     disk that neither we nor anything else ever wrote to. Anything
//     else is refused with the reason on the console, because a disk
//     someone else formatted holds someone else's data.
//
//   - Nothing is written until every disk's plan has been validated.
//     A claim is laid out entirely in memory first (planClaim), so a
//     spec that can't fit fails before the first byte lands on any
//     disk, and the boot can fall back to a different spec (the
//     proven manifest, after a staged one is rejected) against disks
//     that are exactly as it expects them.
//
// Claiming is also resumable. The role's name goes into the partition
// table first, before any filesystem exists, so a boot that dies
// between partitioning and mkfs leaves partitions the next boot
// recognizes by name and finishes formatting (mountRole's half of the
// bargain, in storage.go).

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chrisguidry/liken/machine"
)

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

	// 2048 bytes covers all three signatures: the MBR's 0x55AA at 510,
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

// A claimPlan is one blank disk's pending partition table. It is
// validated and laid out up front, and written only after every
// disk's plan has succeeded.
type claimPlan struct {
	device       string
	totalSectors uint64
	parts        []gptPartition
	roleCount    int
}

// planClaim validates that a disk may be claimed for every declared
// role naming it, and lays out its table: sized roles first at their
// exact sizes, in canonical order, and the remainder role taking
// whatever is left. Nothing is written.
func planClaim(device string, roles []machine.DeclaredRole, found map[machine.StorageRoleName]partition) (claimPlan, error) {
	var mine []machine.DeclaredRole
	for _, role := range roles {
		if role.Device == device {
			// A disk where some roles are recognized but others need
			// claiming is a disk whose table liken wrote and then
			// something changed. It is not blank, so it can't be
			// claimed, and it isn't safe to repair automatically.
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

	totalSectors := disk.SizeBytes / sectorSize
	parts, err := planPartitions(device, mine, totalSectors)
	if err != nil {
		return claimPlan{}, err
	}
	return claimPlan{device: device, totalSectors: totalSectors, parts: parts, roleCount: len(mine)}, nil
}

// applyClaim writes one planned table and waits for the kernel to
// surface its partitions.
func applyClaim(plan claimPlan) error {
	fmt.Printf("liken: storage: claiming %s (%s) for %d role(s)\n",
		plan.device, gib(plan.totalSectors*sectorSize), plan.roleCount)
	if err := writeGPT(plan.device, plan.totalSectors, plan.parts); err != nil {
		return fmt.Errorf("partitioning %s: %w", plan.device, err)
	}
	return waitForPartitions(plan.parts, 5*time.Second)
}

// planPartitions lays out a claimed disk's table: sized roles pack
// from the front in canonical order, each start aligned to 1MiB, and
// the (single, validated) sizeless role takes the rest of the disk.
// The device name appears only in error messages; the layout math
// works purely in sectors.
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
		p := gptPartition{name: role.PartitionName(), firstLBA: next, lastLBA: next + sectors - 1,
			typeGUID: partitionTypeFor(role.Name)}
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
		parts = append(parts, gptPartition{name: remainder.PartitionName(), firstLBA: next, lastLBA: lastUsable,
			typeGUID: partitionTypeFor(remainder.Name)})
	}
	return parts, nil
}

// waitForPartitions gives the kernel a moment to surface the devices
// for a table just written: BLKRRPART is synchronous but the devtmpfs
// nodes and sysfs entries appear slightly later. Each partition must
// appear at its planned size, which lets the same wait serve growth
// as well as claiming: a partition still showing its old geometry is
// as wrong as one that hasn't appeared. If the deadline passes, the
// error names each partition that never appeared as expected.
func waitForPartitions(parts []gptPartition, patience time.Duration) error {
	deadline := time.Now().Add(patience)
	for {
		visible := map[string]uint64{}
		for _, p := range discoverPartitions() {
			visible[p.partName] = p.sizeBytes
		}
		var missing []string
		for _, want := range parts {
			wantBytes := (want.lastLBA - want.firstLBA + 1) * sectorSize
			got, ok := visible[want.name]
			switch {
			case !ok:
				missing = append(missing, want.name)
			case got != wantBytes:
				missing = append(missing, fmt.Sprintf("%s (still %d bytes, want %d)", want.name, got, wantBytes))
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
