package main

// Growing partitions in place.
//
// Sizes in the storage spec are grow-only: a role may be declared
// larger than its partition, never smaller, because growing never
// moves data. A partition grows by rewriting its table entry's end
// sector and telling the filesystem inside (ext4.go's half of the
// job). Shrinking or moving would mean relocating live data, which
// is a data migration, and liken does not do migrations.
//
// Two rules bound what a grow can do:
//
//   - A partition can only grow into empty space directly after it.
//     If another partition starts there, the grow is unsatisfiable,
//     and an unsatisfiable spec fails reconciliation the same as any
//     other one. The machine does not quietly run with less than it
//     declared.
//
//   - Every table edit happens while nothing from that disk is
//     mounted. The kernel refuses to re-read the partition table of
//     a disk in use (BLKRRPART returns EBUSY), which is why growth
//     runs after recognition and before the code mounts any role.
//
// A disk that was itself grown, for example by the lab's qemu-img
// resize or a cloud volume expansion, needs its table rewritten even
// when no partition changes: the backup table belongs at the end of
// the disk, and the end just moved. Remainder roles, which have no
// declared size, grow to the new last usable sector when that
// happens. Their size was always defined as the rest of the disk, so
// when the disk grows, they grow with it.

import (
	"fmt"
	"os"
	"time"

	"github.com/liken-sh/liken/disks"
	"github.com/liken-sh/liken/machine"
)

// A growth is one entry's extension: which table slot, and the new
// final sector.
type growth struct {
	entryIndex int
	newLastLBA uint64
}

// planGrowth compares each declared role recognized on this disk
// against its table entry, and decides what must grow. It is pure;
// the device name appears only in error messages. rewrite reports
// whether the table needs rewriting even when no extent changes,
// which is how a grown disk gets its backup table moved to the new
// end.
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
			continue // this role's partition lives on another disk
		}
		e := t.Entries[idx]

		var target uint64
		if role.Size == "" {
			// A remainder role's size is "the rest of the disk", so
			// growth for it means the disk itself grew.
			target = lastUsable
			if target <= e.LastLBA {
				continue
			}
		} else {
			bytes, _ := machine.ParseSize(role.Size) // validated before any disk is touched
			declared := (bytes + disks.SectorSize - 1) / disks.SectorSize
			if declared <= e.LastLBA-e.FirstLBA+1 {
				// Already at or above the declared size: satisfied.
				// (A shrink that the operator failed to refuse also
				// lands here, tolerated and never acted on.)
				continue
			}
			target = e.FirstLBA + declared - 1
			if target > lastUsable {
				return nil, false, fmt.Errorf("disk %s is too small to grow %s to %s: needs sector %d but the disk's usable space ends at %d",
					device, role.Name, role.Size, target, lastUsable)
			}
		}

		// Growing never moves data, so anything that starts in the
		// space this partition needs leaves the spec unsatisfiable.
		for j, other := range t.Entries {
			if j == idx {
				continue
			}
			if other.FirstLBA > e.LastLBA && other.FirstLBA <= target {
				return nil, false, fmt.Errorf("cannot grow %s on %s to sector %d: partition %q begins at sector %d, in the way (growing never moves data)",
					role.Name, device, target, other.Name, other.FirstLBA)
			}
		}
		edits = append(edits, growth{entryIndex: idx, newLastLBA: target})
	}
	return edits, len(edits) > 0 || t.AlternateLBA != totalSectors-1, nil
}

// A growPlan is one disk's pending rewrite. The code computes it up
// front (the plan-everything-then-apply rule in storage.go's
// header), and applies it only after every disk's plan succeeds.
type growPlan struct {
	device       string
	totalSectors uint64
	table        *disks.Table
	edits        []growth
}

// planAllGrowth reads each recognized disk's table and plans its
// growth, and writes nothing.
func planAllGrowth(roles []machine.DeclaredRole, found map[machine.StorageRoleName]partition) ([]growPlan, error) {
	byDisk := map[string][]machine.DeclaredRole{}
	var order []string
	for _, role := range roles {
		p, ok := found[role.Name]
		if !ok {
			continue
		}
		// The boot roles never grow: ext4 grows in place by ioctl, but
		// FAT's geometry is fixed at format time (the slots, bootHome),
		// and biosBoot's whole purpose is a layout that the MBR's
		// literal sector numbers can rely on. Their sizes are settled
		// on the day they are claimed. The code refuses a spec asking
		// for more here, at planning time, before it writes anything,
		// and the refusal follows the usual staged-spec path: the code
		// rejects the spec, and the boot falls back to the proven
		// manifest.
		if isFixedSizeRole(role.Name) {
			if role.Size != "" {
				if bytes, _ := machine.ParseSize(role.Size); bytes > p.sizeBytes {
					return nil, fmt.Errorf(
						"%s is %s and can't grow to %s: boot roles are fixed when claimed",
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
		totalSectors := disk.SizeBytes / disks.SectorSize

		f, err := os.Open(device)
		if err != nil {
			return nil, fmt.Errorf("examining %s: %w", device, err)
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

// applyGrowth rewrites one disk's table with its planned extensions
// and waits for the kernel to show the new geometry.
func applyGrowth(plan growPlan) error {
	// A plan with no edits is a pure relocation: the disk grew, no
	// partition's extent changes, and only the backup copy of the
	// table needs to move to the new end. The kernel's view of the
	// partitions is already correct, so the code does not ask it to
	// re-read the table, and it could not ask anyway. On the disk
	// carrying the running system, the boot slot has been mounted
	// since early boot found the system image on it, and the kernel
	// refuses to re-read a disk in use. (This is the normal first
	// boot after the liken.sh deployment stamps its disk image onto
	// a slightly larger disk.)
	if len(plan.edits) == 0 {
		fmt.Printf("liken: storage: %s grew; relocating its backup partition table to the new end\n", plan.device)
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
	return waitForPartitions(expect, 5*time.Second)
}
