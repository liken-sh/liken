package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/disks"
	"github.com/liken-sh/liken/liken/machine"
)

// claimedTable is the table of a disk of claimedSectors as init
// claimed it: a 64 MiB machineState, and a clusterState with no
// declared size that runs to the last usable sector of that disk.
func claimedTable(claimedSectors uint64) *disks.Table {
	return &disks.Table{
		DiskGUID: disks.MustGUID("11111111-2222-3333-4455-66778899AABB"),
		Entries: []disks.Entry{
			{
				TypeGUID:   disks.LinuxFilesystemData,
				UniqueGUID: disks.MustGUID("AAAAAAAA-BBBB-CCCC-DDEE-FF0011223344"),
				FirstLBA:   2_048,
				LastLBA:    2_048 + (64<<20)/disks.SectorSize - 1, // 64Mi: sectors 2048..133119
				Name:       "liken:machineState",
			},
			{
				TypeGUID:   disks.LinuxFilesystemData,
				UniqueGUID: disks.MustGUID("99999999-8888-7777-6655-443322110000"),
				FirstLBA:   133_120,
				LastLBA:    disks.LastUsableLBA(claimedSectors),
				Name:       "liken:clusterState",
			},
		},
		LastUsableLBA: disks.LastUsableLBA(claimedSectors),
		AlternateLBA:  claimedSectors - 1,
	}
}

const claimedSectors = 2_097_152 // 1 GiB

const testDiskSectors = 262_144 // 128 MiB

// diskFile writes a table into a sparse file of totalSectors sectors.
// The tests of the disks package have a copy, because Go does not
// export test helpers to other packages.
func diskFile(t *testing.T, table *disks.Table, totalSectors uint64) *os.File {
	t.Helper()
	chunks, err := disks.SerializeGPT(table, totalSectors)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(t.TempDir(), "disk"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	if err := f.Truncate(int64(totalSectors) * disks.SectorSize); err != nil {
		t.Fatal(err)
	}
	for _, chunk := range chunks {
		if _, err := f.WriteAt(chunk.Data, int64(chunk.LBA)*disks.SectorSize); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestPlanGrowthLeavesASatisfiedDiskAlone(t *testing.T) {
	roles := []machine.DeclaredRole{
		declared("machineState", "/dev/vda", "64Mi"),
		declared("clusterState", "/dev/vda", ""),
	}
	edits, rewrite, err := planGrowth("/dev/vda", roles, claimedTable(claimedSectors), claimedSectors)
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) != 0 || rewrite {
		t.Errorf("the plan has edits %v and rewrite %v, want no edits and no rewrite when nothing changed", edits, rewrite)
	}
}

func TestPlanGrowthGrowsTheRemainderWhenTheDiskGrows(t *testing.T) {
	// clusterState has no declared size, so it grows with the disk.
	// machineState is already at its declared size.
	const grownSectors = 2 * claimedSectors
	roles := []machine.DeclaredRole{
		declared("machineState", "/dev/vda", "64Mi"),
		declared("clusterState", "/dev/vda", ""),
	}
	edits, rewrite, err := planGrowth("/dev/vda", roles, claimedTable(claimedSectors), grownSectors)
	if err != nil {
		t.Fatal(err)
	}
	if !rewrite || len(edits) != 1 {
		t.Fatalf("the plan has edits %v and rewrite %v, want one edit, to the remainder, and a rewrite", edits, rewrite)
	}
	if edits[0].entryIndex != 1 || edits[0].newLastLBA != disks.LastUsableLBA(grownSectors) {
		t.Errorf("the edit is %+v, want the remainder, entry 1, to end at the new last usable sector", edits[0])
	}
}

func TestPlanGrowthRelocatesTheBackupEvenWithNothingToGrow(t *testing.T) {
	table := claimedTable(claimedSectors)
	table.Entries = table.Entries[:1] // only the sized machineState
	roles := []machine.DeclaredRole{declared("machineState", "/dev/vda", "64Mi")}

	edits, rewrite, err := planGrowth("/dev/vda", roles, table, 2*claimedSectors)
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) != 0 {
		t.Errorf("the plan has edits %v, want none, because no partition changes its extent", edits)
	}
	if !rewrite {
		t.Error("the plan has no rewrite, want one that moves the backup table of the grown disk")
	}
}

func TestApplyGrowthRelocatesWithoutAskingTheKernel(t *testing.T) {
	// BLKRRPART fails on a plain file, so applyGrowth succeeds here
	// only when it does not ask the kernel to re-read the table.
	grownSectors := uint64(2 * claimedSectors)
	table := claimedTable(claimedSectors)
	table.Entries = table.Entries[:1]
	f := diskFile(t, table, claimedSectors)
	if err := f.Truncate(int64(grownSectors) * disks.SectorSize); err != nil {
		t.Fatal(err)
	}

	plan := growPlan{device: f.Name(), totalSectors: grownSectors, table: table}
	if err := applyGrowth(plan); err != nil {
		t.Fatalf("applyGrowth returned %v, want nil, because a plan that only moves the backup table needs no kernel call", err)
	}

	got, err := disks.ReadGPT(f, grownSectors)
	if err != nil {
		t.Fatal(err)
	}
	if got.AlternateLBA != grownSectors-1 {
		t.Errorf("the backup header is at sector %d, want the grown disk's last sector", got.AlternateLBA)
	}
}

func TestPlanGrowthGrowsASizedRoleIntoFreeSpace(t *testing.T) {
	table := claimedTable(claimedSectors)
	table.Entries = table.Entries[:1]
	roles := []machine.DeclaredRole{declared("machineState", "/dev/vda", "128Mi")}

	edits, _, err := planGrowth("/dev/vda", roles, table, claimedSectors)
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) != 1 {
		t.Fatalf("the plan has edits %v, want one", edits)
	}
	want := uint64(2_048 + (128<<20)/disks.SectorSize - 1)
	if edits[0].entryIndex != 0 || edits[0].newLastLBA != want {
		t.Errorf("got %+v, want lastLBA %d", edits[0], want)
	}
}

func TestPlanGrowthRefusesToGrowThroughANeighbor(t *testing.T) {
	// clusterState starts right after machineState.
	roles := []machine.DeclaredRole{
		declared("machineState", "/dev/vda", "128Mi"),
		declared("clusterState", "/dev/vda", ""),
	}
	_, _, err := planGrowth("/dev/vda", roles, claimedTable(claimedSectors), claimedSectors)
	if err == nil {
		t.Fatal("planGrowth returned no error, want a refusal to grow through a neighboring partition")
	}
	for _, want := range []string{"machineState", "liken:clusterState"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is %q, want it to mention %q", err, want)
		}
	}
}

func TestPlanGrowthRefusesToGrowPastTheDisk(t *testing.T) {
	table := claimedTable(claimedSectors)
	table.Entries = table.Entries[:1]
	roles := []machine.DeclaredRole{declared("machineState", "/dev/vda", "2Gi")}

	_, _, err := planGrowth("/dev/vda", roles, table, claimedSectors)
	if err == nil {
		t.Fatal("planGrowth returned no error, want a refusal to grow past the end of the disk")
	}
	if !strings.Contains(err.Error(), "/dev/vda") {
		t.Errorf("the error is %q, want it to name /dev/vda", err)
	}
}

func TestPlanGrowthToleratesAShrinkRequest(t *testing.T) {
	// The operator refuses to stage a shrink. If one arrives anyway,
	// init plans nothing.
	table := claimedTable(claimedSectors)
	table.Entries = table.Entries[:1]
	roles := []machine.DeclaredRole{declared("machineState", "/dev/vda", "1Mi")}

	edits, rewrite, err := planGrowth("/dev/vda", roles, table, claimedSectors)
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) != 0 || rewrite {
		t.Errorf("the plan has edits %v and rewrite %v, want neither for a request to shrink", edits, rewrite)
	}
}

func TestPlanGrowthIgnoresRolesOnOtherDisks(t *testing.T) {
	table := claimedTable(claimedSectors)
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
		t.Errorf("the plan has edits %v and rewrite %v, want neither, because the role on another disk does not affect this one", edits, rewrite)
	}
}

func TestPlanAllGrowthWithNothingRecognized(t *testing.T) {
	roles := []machine.DeclaredRole{declared("clusterState", "/dev/vda", "1Gi")}
	plans, err := planAllGrowth(roles, map[machine.StorageRoleName]partition{})
	if err != nil || plans != nil {
		t.Errorf("planAllGrowth returned %v, %v, want nil, nil", plans, err)
	}
}

func TestPlanAllGrowthNeedsTheDiskInTheInventory(t *testing.T) {
	fakeMachine(t)
	roles := []machine.DeclaredRole{declared("clusterState", "/dev/vda", "1Gi")}
	found := map[machine.StorageRoleName]partition{
		"clusterState": {name: "vda1", disk: "vda", partName: "liken:clusterState"},
	}
	if _, err := planAllGrowth(roles, found); err == nil {
		t.Error("planAllGrowth returned no error for a disk missing from the inventory, want one")
	}
}

func TestPlanAllGrowthRefusesToGrowSystemSlots(t *testing.T) {
	roles := []machine.DeclaredRole{declared("systemA", "/dev/vdc", "1Gi")}
	found := map[machine.StorageRoleName]partition{
		"systemA": {name: "vdc1", disk: "vdc", partName: "liken:systemA", sizeBytes: 512 << 20},
	}
	_, err := planAllGrowth(roles, found)
	if err == nil {
		t.Fatal("planAllGrowth returned no error, want a refusal to grow a system slot")
	}
	for _, want := range []string{"systemA", "boot role"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is %q, want it to mention %q", err, want)
		}
	}
}

func TestPlanAllGrowthLeavesRightSizedSystemSlotsAlone(t *testing.T) {
	roles := []machine.DeclaredRole{declared("systemA", "/dev/vdc", "512Mi")}
	found := map[machine.StorageRoleName]partition{
		"systemA": {name: "vdc1", disk: "vdc", partName: "liken:systemA", sizeBytes: 512 << 20},
	}
	plans, err := planAllGrowth(roles, found)
	if err != nil || plans != nil {
		t.Errorf("planAllGrowth returned %v, %v, want nil, nil", plans, err)
	}
}

func TestPlanAllGrowthRefusesToGrowASystemSlot(t *testing.T) {
	roles := []machine.DeclaredRole{declared(machine.SystemARole, "/dev/vdc", "1Gi")}
	found := map[machine.StorageRoleName]partition{
		machine.SystemARole: {name: "vdc1", disk: "vdc", sizeBytes: 512 << 20},
	}
	_, err := planAllGrowth(roles, found)
	if err == nil || !strings.Contains(err.Error(), "systemA") {
		t.Errorf("the result is %v, want an error that names systemA", err)
	}
}

func TestPlanAllGrowthRefusesToGrowTheBootRoles(t *testing.T) {
	for _, role := range []machine.StorageRoleName{machine.BIOSBootRole, machine.BootHomeRole} {
		roles := []machine.DeclaredRole{declared(role, "/dev/vdc", "1Gi")}
		found := map[machine.StorageRoleName]partition{
			role: {name: "vdc1", disk: "vdc", sizeBytes: 64 << 20},
		}
		_, err := planAllGrowth(roles, found)
		if err == nil || !strings.Contains(err.Error(), string(role)) {
			t.Errorf("the result is %v, want an error that names %s", err, role)
		}
	}
}

func TestPlanAllGrowthAcceptsASlotAtItsClaimedSize(t *testing.T) {
	roles := []machine.DeclaredRole{declared(machine.SystemARole, "/dev/vdc", "512Mi")}
	found := map[machine.StorageRoleName]partition{
		machine.SystemARole: {name: "vdc1", disk: "vdc", sizeBytes: 512 << 20},
	}
	plans, err := planAllGrowth(roles, found)
	if err != nil || len(plans) != 0 {
		t.Errorf("planAllGrowth returned %v, %v, want no plans and no error", plans, err)
	}
}

func TestPlanAllGrowthReadsASatisfiedDiskAndPlansNothing(t *testing.T) {
	// The whole path against a fake disk: the inventory lookup and
	// the read of the table from the device file.
	sys, dev := fakeMachine(t)
	table := claimedTable(testDiskSectors)
	addDisk(t, sys, dev, "vda", testDiskSectors*disks.SectorSize, nil)
	f := diskFile(t, table, testDiskSectors)
	if err := os.Rename(f.Name(), filepath.Join(dev, "vda")); err != nil {
		t.Fatal(err)
	}
	roles := []machine.DeclaredRole{
		declared(machine.MachineStateRole, "/dev/vda", "64Mi"),
		declared(machine.ClusterStateRole, "/dev/vda", ""),
	}
	found := map[machine.StorageRoleName]partition{
		machine.MachineStateRole: {name: "vda1", disk: "vda"},
		machine.ClusterStateRole: {name: "vda2", disk: "vda"},
	}
	plans, err := planAllGrowth(roles, found)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 0 {
		t.Errorf("planAllGrowth returned %+v, want no plans", plans)
	}
}

func TestPlanAllGrowthReportsAnUnreadableDisk(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 1<<30, nil) // sysfs entry, no device file
	roles := []machine.DeclaredRole{declared(machine.MachineStateRole, "/dev/vda", "")}
	found := map[machine.StorageRoleName]partition{
		machine.MachineStateRole: {name: "vda1", disk: "vda"},
	}
	if _, err := planAllGrowth(roles, found); err == nil {
		t.Error("planAllGrowth returned no error for a disk with no device file, want one")
	}
}
