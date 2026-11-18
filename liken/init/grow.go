package main

// Sizes in the storage spec only grow. Shrinking or moving a partition
// would mean relocating live data, and liken does not migrate data. To
// grow a partition, init rewrites the last sector of its table entry,
// and ext4.go then grows the filesystem inside it.
//
// A partition can grow only into free space directly after it. When
// another partition starts in that space, init refuses the spec like
// any other invalid spec, so the machine never runs with less storage
// than it declares.
//
// After it changes an entry, init asks the kernel to re-read the
// table, and the kernel refuses (BLKRRPART returns EBUSY) while any
// partition of that disk is mounted. So growth runs before init mounts
// any role.
//
// When the disk itself grew, for example after qemu-img resize or a
// cloud volume expansion, the table must be rewritten even when no
// partition grows, because the backup GPT belongs at the last sector
// of the disk. A role with no declared size extends to the new last
// usable sector in the same rewrite.

import (
	"fmt"
	"os"

	"github.com/liken-sh/liken/liken/disks"
	"github.com/liken-sh/liken/liken/machine"
)

// A growth extends one partition table entry to a new last sector.
type growth struct {
	entryIndex int
	newLastLBA uint64
}

// planGrowth returns the entries of this disk's table that must grow
// to match the declared roles. device is used only in error messages.
// rewrite is also true when no entry grows but the disk did, because
// the backup table must move to the new last sector.
func planGrowth(device string, roles []machine.DeclaredRole, t *disks.Table, totalSectors uint64) (edits []growth, rewrite bool, err error) {
	lastUsable := disks.LastUsableLBA(totalSectors)
	for _, role := range roles {
		idx := -1
		for i, e := range t.Entries {
			if e.Name == role.PartitionName() {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue // this role's partition is on another disk
		}
		e := t.Entries[idx]

		var target uint64
		if role.Size == "" {
			target = lastUsable
			if target <= e.LastLBA {
				continue
			}
		} else {
			bytes, _ := machine.ParseSize(role.Size) // validated before any disk is touched
			declared := (bytes + disks.SectorSize - 1) / disks.SectorSize
			if declared <= e.LastLBA-e.FirstLBA+1 {
				// A declared shrink also ends up here. The operator
				// refuses one, and init ignores any that arrives.
				continue
			}
			target = e.FirstLBA + declared - 1
			if target > lastUsable {
				return nil, false, fmt.Errorf("growing %s on %s to %s needs sector %d, but the usable space of the disk ends at sector %d",
					role.Name, device, role.Size, target, lastUsable)
			}
		}

		for j, other := range t.Entries {
			if j == idx {
				continue
			}
			if other.FirstLBA > e.LastLBA && other.FirstLBA <= target {
				return nil, false, fmt.Errorf("%s on %s cannot grow to sector %d: partition %q begins at sector %d, and growing does not move partitions",
					role.Name, device, target, other.Name, other.FirstLBA)
			}
		}
		edits = append(edits, growth{entryIndex: idx, newLastLBA: target})
	}
	return edits, len(edits) > 0 || t.AlternateLBA != totalSectors-1, nil
}

// A growPlan is the table rewrite of one disk. reconcileStorage plans
// every disk before it applies any plan, so an invalid spec changes
// no disk.
type growPlan struct {
	device       string
	totalSectors uint64
	table        *disks.Table
	edits        []growth
}

// planAllGrowth reads the partition table of each disk that holds a
// recognized role and returns the rewrite that disk needs. It writes
// nothing.
func planAllGrowth(roles []machine.DeclaredRole, found map[machine.StorageRoleName]partition) ([]growPlan, error) {
	byDisk := map[string][]machine.DeclaredRole{}
	var order []string
	for _, role := range roles {
		p, ok := found[role.Name]
		if !ok {
			continue
		}
		// The boot roles never grow. The system slots and bootHome are
		// FAT, which cannot grow in place, and the MBR's boot code
		// addresses biosBoot by sector number. A spec that grows one
		// is refused here, before anything is written.
		if isFixedSizeRole(role.Name) {
			if role.Size != "" {
				if bytes, _ := machine.ParseSize(role.Size); bytes > p.sizeBytes {
					return nil, fmt.Errorf(
						"%s is %s and can't grow to %s, because a boot role keeps the size it had when init created it",
						role.Name, gib(p.sizeBytes), role.Size)
				}
			}
			continue
		}
		if _, seen := byDisk[p.disk]; !seen {
			order = append(order, p.disk)
		}
		byDisk[p.disk] = append(byDisk[p.disk], role)
	}

	var plans []growPlan
	for _, diskName := range order {
		device := devRoot + "/" + diskName
		disk := diskByPath(device)
		if disk == nil {
			return nil, fmt.Errorf("%s holds recognized partitions, but the disk is not in the inventory", device)
		}
		totalSectors := disk.SizeBytes / disks.SectorSize

		f, err := os.Open(device)
		if err != nil {
			return nil, err
		}
		t, err := disks.ReadGPT(f, totalSectors)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("reading the partition table on %s: %w", device, err)
		}

		edits, rewrite, err := planGrowth(device, byDisk[diskName], t, totalSectors)
		if err != nil {
			return nil, err
		}
		if !rewrite {
			continue
		}
		plans = append(plans, growPlan{device: device, totalSectors: totalSectors, table: t, edits: edits})
	}
	return plans, nil
}

// applyGrowth writes one disk's planned table and waits until the
// kernel shows the partitions with their new sizes.
func applyGrowth(plan growPlan) error {
	// With no edits, only the backup table moves, and the kernel's
	// view of the partitions is already correct. So init skips the
	// re-read, which the kernel refuses on the disk of the running
	// system, because its boot slot is mounted. This happens on the
	// first boot after a disk image is written onto a disk larger than
	// the image.
	if len(plan.edits) == 0 {
		fmt.Printf("liken: storage: %s is larger than its partition table records, so init moves the backup table to the last sector\n", plan.device)
		if err := disks.WriteTableInPlace(plan.device, plan.totalSectors, plan.table); err != nil {
			return fmt.Errorf("rewriting the partition table on %s: %w", plan.device, err)
		}
		return nil
	}

	for _, g := range plan.edits {
		e := &plan.table.Entries[g.entryIndex]
		fmt.Printf("liken: storage: growing %s on %s from %s to %s\n",
			e.Name, plan.device,
			gib((e.LastLBA-e.FirstLBA+1)*disks.SectorSize),
			gib((g.newLastLBA-e.FirstLBA+1)*disks.SectorSize))
		e.LastLBA = g.newLastLBA
	}
	if err := disks.WriteTable(plan.device, plan.totalSectors, plan.table); err != nil {
		return fmt.Errorf("rewriting the partition table on %s: %w", plan.device, err)
	}

	var expect []disks.Partition
	for _, e := range plan.table.Entries {
		expect = append(expect, disks.Partition{Name: e.Name, FirstLBA: e.FirstLBA, LastLBA: e.LastLBA})
	}
	return waitForPartitions(expect)
}
