package main

// Tests for the growth decisions. planGrowth is pure: a table and a
// disk size go in, edits come out. So every rule in grow.go's header
// gets a case here. Actually rewriting a table and telling the
// kernel belongs to the QEMU harness.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/disks"
	"github.com/liken-sh/liken/machine"
)

// grownTable is a claimed 1 GiB disk's table: a 64 MiB machineState
// and a clusterState remainder that ran to the last usable sector at
// claim time.
func grownTable(claimedSectors uint64) *disks.Table {
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

const claimedSectors = 2_097_152 // the 1 GiB the disk had at claim time

const testDiskSectors = 262_144 // 128 MiB

// diskFile writes a table's chunks into a sparse file of the given
// sector count: the closest thing to a block device that a test can
// have. (The disks package's tests carry the original version; this
// copy exists because test helpers do not cross package boundaries.)
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
	edits, rewrite, err := planGrowth("/dev/vda", roles, grownTable(claimedSectors), claimedSectors)
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) != 0 || rewrite {
		t.Errorf("nothing changed, nothing should be planned: edits=%v rewrite=%v", edits, rewrite)
	}
}

func TestPlanGrowthGrowsTheRemainderWhenTheDiskGrows(t *testing.T) {
	// The disk doubled in size since it was claimed. The remainder
	// role grows to the new last usable sector, and the code leaves
	// the machineState entry ahead of it untouched.
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
	if edits[0].entryIndex != 1 || edits[0].newLastLBA != disks.LastUsableLBA(grownSectors) {
		t.Errorf("remainder should reach the new last usable sector: %+v", edits[0])
	}
}

func TestPlanGrowthRelocatesTheBackupEvenWithNothingToGrow(t *testing.T) {
	// All roles are sized and satisfied, but the disk grew. No
	// extent changes, yet the code must rewrite the table so the
	// backup copy moves to the new end of the disk.
	table := grownTable(claimedSectors)
	table.Entries = table.Entries[:1] // only the sized machineState
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

func TestApplyGrowthRelocatesWithoutAskingTheKernel(t *testing.T) {
	// A relocation-only rewrite, with no extent changing, must not
	// ask the kernel to re-read the table. The kernel's view is
	// already correct, and on the disk that carries the running
	// system the kernel would refuse the request anyway, because the
	// OS's own slot is mounted from the moment early boot finds the
	// system image on it. A plain file stands in for the disk here
	// exactly because it cannot answer that ioctl: applyGrowth
	// succeeding proves that it never asks.
	grownSectors := uint64(2 * claimedSectors)
	table := grownTable(claimedSectors)
	table.Entries = table.Entries[:1]
	f := diskFile(t, table, claimedSectors)
	if err := f.Truncate(int64(grownSectors) * disks.SectorSize); err != nil {
		t.Fatal(err)
	}

	plan := growPlan{device: f.Name(), totalSectors: grownSectors, table: table}
	if err := applyGrowth(plan); err != nil {
		t.Fatalf("a pure relocation should succeed without the kernel: %v", err)
	}

	got, err := disks.ReadGPT(f, grownSectors)
	if err != nil {
		t.Fatal(err)
	}
	if got.AlternateLBA != grownSectors-1 {
		t.Errorf("the backup should land on the grown disk's last sector: %d", got.AlternateLBA)
	}
}

func TestPlanGrowthGrowsASizedRoleIntoFreeSpace(t *testing.T) {
	// machineState is the only partition, and it is declared larger
	// than it is now, so it grows in place to its declared size.
	table := grownTable(claimedSectors)
	table.Entries = table.Entries[:1]
	roles := []machine.DeclaredRole{declared("machineState", "/dev/vda", "128Mi")}

	edits, _, err := planGrowth("/dev/vda", roles, table, claimedSectors)
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) != 1 {
		t.Fatalf("expected one edit: %v", edits)
	}
	want := uint64(2_048 + (128<<20)/disks.SectorSize - 1)
	if edits[0].entryIndex != 0 || edits[0].newLastLBA != want {
		t.Errorf("got %+v, want lastLBA %d", edits[0], want)
	}
}

func TestPlanGrowthRefusesToGrowThroughANeighbor(t *testing.T) {
	// machineState is declared to double, but clusterState begins
	// right after it. Growing never moves data, so the spec is
	// unsatisfiable.
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
	table.Entries = table.Entries[:1]
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
	// Grow-only rules mean a declared size below the partition's
	// actual size is already satisfied, and the code never acts on
	// it. (The operator refuses to stage shrinks; init must simply
	// not misbehave.)
	table := grownTable(claimedSectors)
	table.Entries = table.Entries[:1]
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
	// podStorage lives on another disk entirely, so its declared size
	// has no place in this disk's plan.
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
	// Roles whose partitions the code did not find have nothing to
	// grow; the plan is empty, and the code never opens a disk.
	roles := []machine.DeclaredRole{declared("clusterState", "/dev/vda", "1Gi")}
	plans, err := planAllGrowth(roles, map[machine.StorageRoleName]partition{})
	if err != nil || plans != nil {
		t.Errorf("got %v, %v", plans, err)
	}
}

func TestPlanAllGrowthNeedsTheDiskInTheInventory(t *testing.T) {
	// A recognized partition on a disk that the inventory does not
	// show is a contradiction. The code stops, rather than planning
	// around it.
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
	// ext4 grows in place by ioctl, but FAT's geometry is fixed at
	// format time. So the code refuses a spec that asks a slot to
	// grow at planning time, before it writes anything.
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
	// enters the plan; the code opens no disk.
	roles := []machine.DeclaredRole{declared("systemA", "/dev/vdc", "512Mi")}
	found := map[machine.StorageRoleName]partition{
		"systemA": {name: "vdc1", disk: "vdc", partName: "liken:systemA", sizeBytes: 512 << 20},
	}
	plans, err := planAllGrowth(roles, found)
	if err != nil || plans != nil {
		t.Errorf("got %v, %v", plans, err)
	}
}

func TestPlanAllGrowthRefusesToGrowASystemSlot(t *testing.T) {
	roles := []machine.DeclaredRole{declared(machine.SystemARole, "/dev/vdc", "1Gi")}
	found := map[machine.StorageRoleName]partition{
		machine.SystemARole: {name: "vdc1", disk: "vdc", sizeBytes: 512 << 20},
	}
	_, err := planAllGrowth(roles, found)
	if err == nil || !strings.Contains(err.Error(), "fixed when claimed") {
		t.Errorf("slots are fixed when claimed: %v", err)
	}
}

func TestPlanAllGrowthRefusesToGrowTheBootRoles(t *testing.T) {
	// bootHome is FAT32, the same as the slots, and biosBoot's
	// location is baked into the MBR's boot code as literal sectors.
	// Neither may grow after the day the code claims it.
	for _, role := range []machine.StorageRoleName{machine.BIOSBootRole, machine.BootHomeRole} {
		roles := []machine.DeclaredRole{declared(role, "/dev/vdc", "1Gi")}
		found := map[machine.StorageRoleName]partition{
			role: {name: "vdc1", disk: "vdc", sizeBytes: 64 << 20},
		}
		_, err := planAllGrowth(roles, found)
		if err == nil || !strings.Contains(err.Error(), "fixed when claimed") {
			t.Errorf("%s should be fixed when claimed: %v", role, err)
		}
	}
}

func TestPlanAllGrowthAcceptsASlotAtItsClaimedSize(t *testing.T) {
	// Declaring the size a slot already has is not a grow; the slot
	// simply does not take part in growth planning.
	roles := []machine.DeclaredRole{declared(machine.SystemARole, "/dev/vdc", "512Mi")}
	found := map[machine.StorageRoleName]partition{
		machine.SystemARole: {name: "vdc1", disk: "vdc", sizeBytes: 512 << 20},
	}
	plans, err := planAllGrowth(roles, found)
	if err != nil || len(plans) != 0 {
		t.Errorf("a satisfied slot plans nothing: %v, %v", plans, err)
	}
}

func TestPlanAllGrowthReadsASatisfiedDiskAndPlansNothing(t *testing.T) {
	// The whole walk against a fake disk: inventory, the table read
	// back from the device file, and planGrowth concluding that
	// there is nothing to do, because the disk never grew.
	sys, dev := fakeMachine(t)
	table := grownTable(testDiskSectors)
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
		t.Errorf("an unchanged disk plans no rewrites: %+v", plans)
	}
}

func TestPlanAllGrowthReportsAnUnreadableDisk(t *testing.T) {
	// The disk is in the inventory, but its device will not open.
	// Growth planning must report that, rather than plan around it.
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 1<<30, nil) // sysfs entry, no device file
	roles := []machine.DeclaredRole{declared(machine.MachineStateRole, "/dev/vda", "")}
	found := map[machine.StorageRoleName]partition{
		machine.MachineStateRole: {name: "vda1", disk: "vda"},
	}
	if _, err := planAllGrowth(roles, found); err == nil {
		t.Error("an unexaminable disk is an error")
	}
}
