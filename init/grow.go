package main

// Growing partitions in place.
//
// Sizes in the storage spec are grow-only: a role may be declared
// larger than its partition, never smaller, because growing never
// moves data. A partition grows by rewriting its table entry's end
// sector and telling the filesystem inside (ext4.go's half of the
// job); shrinking or moving would mean relocating live data, which is
// a data migration, and liken doesn't do migrations.
//
// Two rules bound what a grow can do:
//
//   - A partition can only grow into empty space directly after it.
//     If another partition starts there, the grow is unsatisfiable,
//     and an unsatisfiable spec fails reconciliation like any other
//     (the machine does not quietly run with less than it declared).
//
//   - Every table edit happens while nothing from that disk is
//     mounted. The kernel refuses to re-read the partition table of
//     a disk in use (BLKRRPART returns EBUSY), which is why growth
//     runs after recognition and before any role is mounted.
//
// A disk that was itself grown (the lab's qemu-img resize, a cloud
// volume expansion) needs its table rewritten even when no partition
// changes: the backup table belongs at the end of the disk, and the
// end just moved. Remainder roles (no declared size) grow to the new
// last usable sector when that happens; their size was always defined
// as the rest of the disk, so when the disk grows, they grow with it.

import (
	"fmt"
	"os"
	"time"

	"github.com/liken-sh/liken/machine"
)

// A growth is one entry's extension: which table slot, and its new
// final sector.
type growth struct {
	entryIndex int
	newLastLBA uint64
}

// planGrowth compares each declared role recognized on this disk
// against its table entry and decides what must grow. It is pure; the
// device name appears only in error messages. rewrite reports whether
// the table needs rewriting even with no extents changing, which is
// how a grown disk gets its backup table relocated to the new end.
func planGrowth(device string, roles []machine.DeclaredRole, t *gptTable, totalSectors uint64) (edits []growth, rewrite bool, err error) {
	lastUsable := gptLastUsableLBA(totalSectors)
	for _, role := range roles {
		idx := -1
		for i, e := range t.entries {
			if e.name == role.PartitionName() {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue // this role's partition lives on another disk
		}
		e := t.entries[idx]

		var target uint64
		if role.Size == "" {
			// A remainder role's size is "the rest of the disk";
			// growth means the disk itself grew.
			target = lastUsable
			if target <= e.lastLBA {
				continue
			}
		} else {
			bytes, _ := machine.ParseSize(role.Size) // validated before any disk is touched
			declared := (bytes + sectorSize - 1) / sectorSize
			if declared <= e.lastLBA-e.firstLBA+1 {
				// At or above the declared size already: satisfied.
				// (A shrink the operator failed to refuse lands here
				// too, tolerated and never acted on.)
				continue
			}
			target = e.firstLBA + declared - 1
			if target > lastUsable {
				return nil, false, fmt.Errorf("disk %s is too small to grow %s to %s: needs sector %d but the disk's usable space ends at %d",
					device, role.Name, role.Size, target, lastUsable)
			}
		}

		// Growing never moves data, so anything that starts in the
		// space this partition wants makes the spec unsatisfiable.
		for j, other := range t.entries {
			if j == idx {
				continue
			}
			if other.firstLBA > e.lastLBA && other.firstLBA <= target {
				return nil, false, fmt.Errorf("cannot grow %s on %s to sector %d: partition %q begins at sector %d, in the way (growing never moves data)",
					role.Name, device, target, other.name, other.firstLBA)
			}
		}
		edits = append(edits, growth{entryIndex: idx, newLastLBA: target})
	}
	return edits, len(edits) > 0 || t.alternateLBA != totalSectors-1, nil
}

// A growPlan is one disk's pending rewrite: computed up front (the
// plan-everything-then-apply rule in storage.go's header), applied
// only after every disk's plan succeeded.
type growPlan struct {
	device       string
	totalSectors uint64
	table        *gptTable
	edits        []growth
}

// planAllGrowth reads each recognized disk's table and plans its
// growth, without writing anything.
func planAllGrowth(roles []machine.DeclaredRole, found map[machine.StorageRoleName]partition) ([]growPlan, error) {
	byDisk := map[string][]machine.DeclaredRole{}
	var order []string
	for _, role := range roles {
		p, ok := found[role.Name]
		if !ok {
			continue
		}
		// The system slots never grow: ext4 grows in place by ioctl,
		// but FAT's geometry is fixed at format time, so a slot's size
		// is settled the day it's claimed. A spec asking for more is
		// refused here at planning time, before anything is written,
		// and the refusal follows the usual staged-spec path: the spec
		// is rejected and the boot falls back to the proven manifest.
		if isSystemSlot(role.Name) {
			if role.Size != "" {
				if bytes, _ := machine.ParseSize(role.Size); bytes > p.sizeBytes {
					return nil, fmt.Errorf(
						"%s is %s and can't grow to %s: system slots are fixed when claimed (FAT32 doesn't grow in place)",
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
			return nil, fmt.Errorf("recognized partitions on %s but the disk is not in the inventory", device)
		}
		totalSectors := disk.SizeBytes / sectorSize

		f, err := os.Open(device)
		if err != nil {
			return nil, fmt.Errorf("examining %s: %w", device, err)
		}
		t, err := readGPT(f, totalSectors)
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

// applyGrowth rewrites one disk's table with its planned extensions
// and waits for the kernel to surface the new geometry.
func applyGrowth(plan growPlan) error {
	for _, g := range plan.edits {
		e := &plan.table.entries[g.entryIndex]
		fmt.Printf("liken: storage: growing %s on %s from %s to %s\n",
			e.name, plan.device,
			gib((e.lastLBA-e.firstLBA+1)*sectorSize),
			gib((g.newLastLBA-e.firstLBA+1)*sectorSize))
		e.lastLBA = g.newLastLBA
	}
	if len(plan.edits) == 0 {
		fmt.Printf("liken: storage: %s grew; relocating its backup partition table to the new end\n", plan.device)
	}
	if err := writeGPTTable(plan.device, plan.totalSectors, plan.table); err != nil {
		return fmt.Errorf("rewriting the partition table on %s: %w", plan.device, err)
	}

	var expect []gptPartition
	for _, e := range plan.table.entries {
		expect = append(expect, gptPartition{name: e.name, firstLBA: e.firstLBA, lastLBA: e.lastLBA})
	}
	return waitForPartitions(expect, 5*time.Second)
}
