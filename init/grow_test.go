package main

// Tests for the growth decisions. planGrowth is pure (a table and a
// disk size in, edits out), so every rule in grow.go's header gets a
// case here; actually rewriting a table and telling the kernel is
// QEMU-harness territory.

import (
	"strings"
	"testing"

	"github.com/chrisguidry/liken/machine"
)

// grownTable is a claimed 1 GiB disk's table: a 64 MiB machineState
// and a clusterState remainder that ran to the last usable sector at
// claim time.
func grownTable(claimedSectors uint64) *gptTable {
	return &gptTable{
		diskGUID: mustGUID("11111111-2222-3333-4455-66778899AABB"),
		entries: []gptEntry{
			{
				typeGUID:   linuxFilesystemData,
				uniqueGUID: mustGUID("AAAAAAAA-BBBB-CCCC-DDEE-FF0011223344"),
				firstLBA:   2_048,
				lastLBA:    2_048 + (64<<20)/sectorSize - 1, // 64Mi: sectors 2048..133119
				name:       "liken:machineState",
			},
			{
				typeGUID:   linuxFilesystemData,
				uniqueGUID: mustGUID("99999999-8888-7777-6655-443322110000"),
				firstLBA:   133_120,
				lastLBA:    gptLastUsableLBA(claimedSectors),
				name:       "liken:clusterState",
			},
		},
		lastUsableLBA: gptLastUsableLBA(claimedSectors),
		alternateLBA:  claimedSectors - 1,
	}
}

const claimedSectors = 2_097_152 // the 1 GiB the disk had at claim time

func TestPlanGrowthLeavesASatisfiedDiskAlone(t *testing.T) {
	roles := []machine.DeclaredRole{
		declared("machineState", "/dev/vda", "64Mi"),
		declared("clusterState", "/dev/vda", ""),
	}
	edits, rewrite, err := planGrowth("/dev/vda", roles, grownTable(claimedSectors), claimedSectors)
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) != 0 || rewrite {
		t.Errorf("nothing changed, nothing should be planned: edits=%v rewrite=%v", edits, rewrite)
	}
}

func TestPlanGrowthGrowsTheRemainderWhenTheDiskGrows(t *testing.T) {
	// The disk doubled since it was claimed: the remainder role grows
	// to the new last usable sector, and the machineState entry ahead
	// of it is untouched.
	const grownSectors = 2 * claimedSectors
	roles := []machine.DeclaredRole{
		declared("machineState", "/dev/vda", "64Mi"),
		declared("clusterState", "/dev/vda", ""),
	}
	edits, rewrite, err := planGrowth("/dev/vda", roles, grownTable(claimedSectors), grownSectors)
	if err != nil {
		t.Fatal(err)
	}
	if !rewrite || len(edits) != 1 {
		t.Fatalf("expected exactly the remainder to grow: edits=%v rewrite=%v", edits, rewrite)
	}
	if edits[0].entryIndex != 1 || edits[0].newLastLBA != gptLastUsableLBA(grownSectors) {
		t.Errorf("remainder should reach the new last usable sector: %+v", edits[0])
	}
}

func TestPlanGrowthRelocatesTheBackupEvenWithNothingToGrow(t *testing.T) {
	// All roles sized and satisfied, but the disk grew: no extents
	// change, yet the table must be rewritten so the backup copy
	// moves to the new end of the disk.
	table := grownTable(claimedSectors)
	table.entries = table.entries[:1] // only the sized machineState
	roles := []machine.DeclaredRole{declared("machineState", "/dev/vda", "64Mi")}

	edits, rewrite, err := planGrowth("/dev/vda", roles, table, 2*claimedSectors)
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) != 0 {
		t.Errorf("no extent should change: %v", edits)
	}
	if !rewrite {
		t.Error("a grown disk's backup table must be relocated")
	}
}

func TestPlanGrowthGrowsASizedRoleIntoFreeSpace(t *testing.T) {
	// machineState is the only partition, declared larger than it is:
	// it grows in place to its declared size.
	table := grownTable(claimedSectors)
	table.entries = table.entries[:1]
	roles := []machine.DeclaredRole{declared("machineState", "/dev/vda", "128Mi")}

	edits, _, err := planGrowth("/dev/vda", roles, table, claimedSectors)
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) != 1 {
		t.Fatalf("expected one edit: %v", edits)
	}
	want := uint64(2_048 + (128<<20)/sectorSize - 1)
	if edits[0].entryIndex != 0 || edits[0].newLastLBA != want {
		t.Errorf("got %+v, want lastLBA %d", edits[0], want)
	}
}

func TestPlanGrowthRefusesToGrowThroughANeighbor(t *testing.T) {
	// machineState wants to double, but clusterState begins right
	// after it: growing never moves data, so the spec is unsatisfiable.
	roles := []machine.DeclaredRole{
		declared("machineState", "/dev/vda", "128Mi"),
		declared("clusterState", "/dev/vda", ""),
	}
	_, _, err := planGrowth("/dev/vda", roles, grownTable(claimedSectors), claimedSectors)
	if err == nil {
		t.Fatal("expected a refusal to grow through a neighboring partition")
	}
	for _, want := range []string{"machineState", "liken:clusterState", "in the way"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

func TestPlanGrowthRefusesToGrowPastTheDisk(t *testing.T) {
	table := grownTable(claimedSectors)
	table.entries = table.entries[:1]
	roles := []machine.DeclaredRole{declared("machineState", "/dev/vda", "2Gi")}

	_, _, err := planGrowth("/dev/vda", roles, table, claimedSectors)
	if err == nil {
		t.Fatal("expected a refusal to grow past the end of the disk")
	}
	if !strings.Contains(err.Error(), "too small") {
		t.Errorf("error should say the disk is too small: %v", err)
	}
}

func TestPlanGrowthToleratesAShrinkRequest(t *testing.T) {
	// Grow-only semantics: a declared size below the partition's
	// actual size is satisfied as-is, never acted on. (The operator
	// refuses to stage shrinks; init just must not misbehave.)
	table := grownTable(claimedSectors)
	table.entries = table.entries[:1]
	roles := []machine.DeclaredRole{declared("machineState", "/dev/vda", "1Mi")}

	edits, rewrite, err := planGrowth("/dev/vda", roles, table, claimedSectors)
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) != 0 || rewrite {
		t.Errorf("a shrink request must plan nothing: edits=%v rewrite=%v", edits, rewrite)
	}
}

func TestPlanGrowthIgnoresRolesOnOtherDisks(t *testing.T) {
	// podStorage lives on another disk entirely; its declared size has
	// no business in this disk's plan.
	table := grownTable(claimedSectors)
	roles := []machine.DeclaredRole{
		declared("machineState", "/dev/vda", "64Mi"),
		declared("clusterState", "/dev/vda", ""),
		declared("podStorage", "/dev/vdb", "1Ti"),
	}
	edits, rewrite, err := planGrowth("/dev/vda", roles, table, claimedSectors)
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) != 0 || rewrite {
		t.Errorf("another disk's role must not affect this plan: edits=%v rewrite=%v", edits, rewrite)
	}
}

func TestPlanAllGrowthWithNothingRecognized(t *testing.T) {
	// Roles whose partitions weren't found have nothing to grow; the
	// plan is empty and no disk is ever opened.
	roles := []machine.DeclaredRole{declared("clusterState", "/dev/vda", "1Gi")}
	plans, err := planAllGrowth(roles, map[machine.StorageRoleName]partition{})
	if err != nil || plans != nil {
		t.Errorf("got %v, %v", plans, err)
	}
}

func TestPlanAllGrowthNeedsTheDiskInTheInventory(t *testing.T) {
	// A recognized partition on a disk the inventory doesn't show is
	// a contradiction; stop rather than plan around it.
	fakeMachine(t)
	roles := []machine.DeclaredRole{declared("clusterState", "/dev/vda", "1Gi")}
	found := map[machine.StorageRoleName]partition{
		"clusterState": {name: "vda1", disk: "vda", partName: "liken:clusterState"},
	}
	if _, err := planAllGrowth(roles, found); err == nil {
		t.Error("an uninventoried disk must be an error")
	}
}

func TestPlanAllGrowthRefusesToGrowSystemSlots(t *testing.T) {
	// ext4 grows in place by ioctl; FAT's geometry is fixed at format
	// time, so a spec asking a slot to grow is refused at planning,
	// before anything is written.
	roles := []machine.DeclaredRole{declared("systemA", "/dev/vdc", "1Gi")}
	found := map[machine.StorageRoleName]partition{
		"systemA": {name: "vdc1", disk: "vdc", partName: "liken:systemA", sizeBytes: 512 << 20},
	}
	_, err := planAllGrowth(roles, found)
	if err == nil {
		t.Fatal("expected an error growing a system slot")
	}
	for _, want := range []string{"systemA", "fixed when claimed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

func TestPlanAllGrowthLeavesRightSizedSystemSlotsAlone(t *testing.T) {
	// A slot at its declared size has nothing to grow and never
	// enters the plan; no disk is opened.
	roles := []machine.DeclaredRole{declared("systemA", "/dev/vdc", "512Mi")}
	found := map[machine.StorageRoleName]partition{
		"systemA": {name: "vdc1", disk: "vdc", partName: "liken:systemA", sizeBytes: 512 << 20},
	}
	plans, err := planAllGrowth(roles, found)
	if err != nil || plans != nil {
		t.Errorf("got %v, %v", plans, err)
	}
}
