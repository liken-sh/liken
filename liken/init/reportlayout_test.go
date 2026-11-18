package main

// Tests for planStorageLayout. Each test passes hand-made disks and
// reads the plan back. The plan must fit the disks it names, so most
// tests also run it through planPartitions (claim.go), the partition
// math of the install.

import (
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/disks"
	"github.com/liken-sh/liken/liken/machine"
)

// role returns the planned role with the given name, and fails the
// test when the layout has none.
func role(t *testing.T, layout storageLayout, name machine.StorageRoleName) plannedRole {
	t.Helper()
	for _, r := range layout.Roles {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("the layout has roles %+v, want one named %s", layout.Roles, name)
	return plannedRole{}
}

func hasRole(layout storageLayout, name machine.StorageRoleName) bool {
	for _, r := range layout.Roles {
		if r.Name == name {
			return true
		}
	}
	return false
}

// mustFit runs the planned roles through planPartitions, disk by disk.
// The install claims each disk with the same function, and refuses a
// disk whose roles do not fit.
func mustFit(t *testing.T, layout storageLayout, sized []reportDisk) {
	t.Helper()
	byDevice := map[string][]machine.DeclaredRole{}
	for _, r := range layout.Roles {
		byDevice[r.Device] = append(byDevice[r.Device], machine.DeclaredRole{
			Name:        r.Name,
			StorageRole: machine.StorageRole{Device: r.Device, Size: r.Size},
		})
	}
	for device, roles := range byDevice {
		var size uint64
		for _, d := range sized {
			if d.Path == device {
				size = d.SizeBytes
			}
		}
		if size == 0 {
			t.Fatalf("the layout placed roles on %s, want only disks of the report", device)
		}
		if _, err := planPartitions(device, roles, size/disks.SectorSize); err != nil {
			t.Errorf("planPartitions refuses the roles planned for %s, want them to fit: %v", device, err)
		}
	}
}

func notes(layout storageLayout) string {
	return strings.Join(layout.Notes, " ")
}

func TestLayoutUsesTheConventionalSizesOnAnOrdinaryDisk(t *testing.T) {
	measured := []reportDisk{{Path: "/dev/sda", SizeBytes: 40 << 30}}
	layout := planStorageLayout(measured, true)

	if got := role(t, layout, machine.SystemARole).Size; got != "1Gi" {
		t.Errorf("the systemA size is %q, want 1Gi", got)
	}
	if got := role(t, layout, machine.ClusterStateRole).Size; got != "6Gi" {
		t.Errorf("the clusterState size is %q, want 6Gi", got)
	}
	if got := role(t, layout, machine.PodStorageRole).Size; got != "4Gi" {
		t.Errorf("the podStorage size is %q, want 4Gi", got)
	}
	if got := role(t, layout, machine.PodEphemeralRole).Size; got != "" {
		t.Errorf("the podEphemeral size is %q, want empty to take the rest of the disk", got)
	}
	if !strings.Contains(notes(layout), "containerd's image store") {
		t.Errorf("the notes are %v, want one that says what clusterState holds", layout.Notes)
	}
	mustFit(t, layout, measured)
}

func TestLayoutSpendsARoomyDiskOnTheRolesThatKeepSomething(t *testing.T) {
	// With the conventional sizes this disk would get 6Gi of image
	// store, 4Gi of volumes, and 466Gi of kubelet scratch space.
	measured := []reportDisk{{Path: "/dev/sda", SizeBytes: 477 << 30}}
	layout := planStorageLayout(measured, true)

	if got := role(t, layout, machine.ClusterStateRole).Size; got != "59Gi" {
		t.Errorf("the clusterState size is %q, want 59Gi, an eighth of the disk", got)
	}
	if got := role(t, layout, machine.PodStorageRole).Size; got != "351Gi" {
		t.Errorf("the podStorage size is %q, want 351Gi, what is left beyond podEphemeral's share", got)
	}
	if got := role(t, layout, machine.PodEphemeralRole).Size; got != "" {
		t.Errorf("the podEphemeral size is %q, want empty to take the rest of the disk", got)
	}
	if !strings.Contains(notes(layout), "cannot grow after the install") {
		t.Errorf("the notes are %v, want one that says clusterState cannot grow after the install", layout.Notes)
	}
	mustFit(t, layout, measured)
}

func TestLayoutCapsClusterStateOnAVeryLargeDisk(t *testing.T) {
	measured := []reportDisk{{Path: "/dev/sda", SizeBytes: 4 << 40}}
	layout := planStorageLayout(measured, true)

	if got := role(t, layout, machine.ClusterStateRole).Size; got != "64Gi" {
		t.Errorf("the clusterState size is %q, want 64Gi, its ceiling", got)
	}
	mustFit(t, layout, measured)
}

// smallDataDisk returns a machine whose 3 GiB system disk has only 446
// MiB left after liken's own roles, so the second disk, of dataBytes,
// holds the cluster's roles.
func smallDataDisk(dataBytes uint64) []reportDisk {
	return []reportDisk{
		{Path: "/dev/sda", SizeBytes: 3 << 30},
		{Path: "/dev/sdb", SizeBytes: dataBytes},
	}
}

func TestLayoutShrinksPodStorageBeforeItTouchesClusterState(t *testing.T) {
	// An 8 GiB data disk cannot hold 6Gi of clusterState beside 4Gi of
	// podStorage. clusterState keeps its full size, and podStorage takes
	// what is left after podEphemeral's minimum.
	measured := smallDataDisk(8 << 30)
	layout := planStorageLayout(measured, true)

	if got := role(t, layout, machine.ClusterStateRole).Size; got != "6Gi" {
		t.Errorf("the clusterState size is %q, want 6Gi", got)
	}
	if got := role(t, layout, machine.PodStorageRole).Size; got != "1728Mi" {
		t.Errorf("the podStorage size is %q, want 1728Mi, what is left after podEphemeral's minimum", got)
	}
	if !strings.Contains(notes(layout), "podStorage") {
		t.Errorf("the notes are %v, want one that explains the smaller podStorage", layout.Notes)
	}
	mustFit(t, layout, measured)
}

func TestLayoutDropsPodStorageToKeepClusterStateWhole(t *testing.T) {
	// On a 6.5 GiB disk, podStorage would get less than the 256Mi
	// minimum for a data role, so the layout leaves it out.
	measured := smallDataDisk(6656 << 20)
	layout := planStorageLayout(measured, true)

	if got := role(t, layout, machine.ClusterStateRole).Size; got != "6Gi" {
		t.Errorf("the clusterState size is %q, want 6Gi", got)
	}
	if hasRole(layout, machine.PodStorageRole) {
		t.Error("the layout has a podStorage role, want it left out")
	}
	if !strings.Contains(notes(layout), "RAM root") {
		t.Errorf("the notes are %v, want one that says what the missing podStorage costs", layout.Notes)
	}
	mustFit(t, layout, measured)
}

func TestLayoutReducesClusterStateOnlyAfterPodStorageIsGone(t *testing.T) {
	// A 4 GiB data disk has no room for podStorage, so clusterState
	// takes what is left after podEphemeral's minimum.
	measured := smallDataDisk(4 << 30)
	layout := planStorageLayout(measured, true)

	if got := role(t, layout, machine.ClusterStateRole).Size; got != "3776Mi" {
		t.Errorf("the clusterState size is %q, want 3776Mi, what is left after podEphemeral's minimum", got)
	}
	if hasRole(layout, machine.PodStorageRole) {
		t.Error("the layout has a podStorage role, want it left out")
	}
	if !strings.Contains(notes(layout), "pull") {
		t.Errorf("the notes are %v, want one that says what a short clusterState costs", layout.Notes)
	}
	mustFit(t, layout, measured)
}

func TestLayoutKeepsClusterStateAtItsFloor(t *testing.T) {
	// This disk holds the 2Gi clusterState floor and podEphemeral's
	// minimum and nothing more. It is the smallest disk that gets a
	// clusterState.
	measured := smallDataDisk(2306 << 20)
	layout := planStorageLayout(measured, true)

	if got := role(t, layout, machine.ClusterStateRole).Size; got != "2Gi" {
		t.Errorf("the clusterState size is %q, want 2Gi, its floor", got)
	}
	mustFit(t, layout, measured)
}

func TestLayoutLeavesClusterStateOutBelowItsFloor(t *testing.T) {
	// A 4 GiB disk has about 1.4 GiB left after liken's own roles,
	// which is under the 2Gi clusterState floor. The plan declares no
	// clusterState and its notes state the shortfall.
	measured := []reportDisk{{Path: "/dev/sda", SizeBytes: 4 << 30}}
	layout := planStorageLayout(measured, true)

	if hasRole(layout, machine.ClusterStateRole) {
		t.Errorf("the layout is %+v, want no clusterState role on a disk under its floor", layout.Roles)
	}
	if role(t, layout, machine.PodEphemeralRole).Size != "" {
		t.Error("the podEphemeral size is set, want empty to take the rest of the disk")
	}
	if !strings.Contains(notes(layout), "2.0 GiB") || !strings.Contains(notes(layout), "1.4 GiB") {
		t.Errorf("the notes are %v, want them to state 2.0 GiB and 1.4 GiB", layout.Notes)
	}
	mustFit(t, layout, measured)
}

func TestLayoutRefusesADiskTooSmallForTheMachinesOwnRoles(t *testing.T) {
	// Two 1Gi system slots, machine state, and /tmp need about 2.6 GiB
	// before any data role, so a 2 GiB disk cannot hold a liken
	// install.
	layout := planStorageLayout([]reportDisk{{Path: "/dev/sda", SizeBytes: 2 << 30}}, true)

	if len(layout.Roles) != 0 {
		t.Errorf("the layout is %+v, want no roles on a disk too small for liken's own roles", layout.Roles)
	}
	if len(layout.Notes) == 0 || !strings.Contains(strings.Join(layout.Notes, " "), "2.0 GiB") {
		t.Errorf("the notes are %v, want them to state the 2.0 GiB the roles need", layout.Notes)
	}
}

func TestLayoutDropsDataRolesThatDoNotFit(t *testing.T) {
	// The disk has 446 MiB beyond the machine's own roles, which is too
	// little for clusterState or podStorage. podEphemeral takes it and
	// the notes say why.
	measured := []reportDisk{{Path: "/dev/sda", SizeBytes: 3 << 30}}
	layout := planStorageLayout(measured, true)

	if hasRole(layout, machine.PodStorageRole) {
		t.Error("the layout has a podStorage role, want it left out")
	}
	if len(layout.Notes) == 0 {
		t.Error("the layout has no notes, want one that explains the dropped roles")
	}
	mustFit(t, layout, measured)
}

func TestLayoutPutsTheDataRolesOnASecondDisk(t *testing.T) {
	measured := []reportDisk{
		{Path: "/dev/sda", SizeBytes: 20 << 30},
		{Path: "/dev/sdb", SizeBytes: 50 << 30},
	}
	layout := planStorageLayout(measured, false)

	if got := role(t, layout, machine.SystemARole).Device; got != "/dev/sda" {
		t.Errorf("the systemA device is %q, want /dev/sda, the first usable disk", got)
	}
	if got := role(t, layout, machine.ClusterStateRole).Device; got != "/dev/sdb" {
		t.Errorf("the clusterState device is %q, want /dev/sdb", got)
	}
	if !hasRole(layout, machine.BIOSBootRole) || !hasRole(layout, machine.BootHomeRole) {
		t.Error("the layout lacks biosBoot or bootHome, want both on a BIOS machine")
	}
	mustFit(t, layout, measured)
}

func TestLayoutKeepsTheDataRolesHomeWhenTheOtherDiskIsTiny(t *testing.T) {
	measured := []reportDisk{
		{Path: "/dev/sda", SizeBytes: 200 << 30},
		{Path: "/dev/sdb", SizeBytes: 64 << 20},
	}
	layout := planStorageLayout(measured, true)

	if got := role(t, layout, machine.ClusterStateRole).Device; got != "/dev/sda" {
		t.Errorf("the clusterState device is %q, want /dev/sda because /dev/sdb has less room than the system disk has left", got)
	}
	mustFit(t, layout, measured)
}

func TestLayoutSkipsADiskThatMightBeTheInstallationStick(t *testing.T) {
	measured := []reportDisk{
		{Path: "/dev/sdb", SizeBytes: 60 << 30, MaybeStick: true},
		{Path: "/dev/sda", SizeBytes: 60 << 30},
	}
	layout := planStorageLayout(measured, true)

	for _, r := range layout.Roles {
		if r.Device == "/dev/sdb" {
			t.Errorf("the role %+v is on /dev/sdb, want no role on a disk that might be the install stick", r)
		}
	}
	mustFit(t, layout, measured)
}

func TestLayoutPrefersADiskTheInstallCanReach(t *testing.T) {
	// The first disk appeared only after the report loaded a driver, so
	// an install from a stock image cannot see it. The system slots go
	// on the second disk.
	measured := []reportDisk{
		{Path: "/dev/sda", SizeBytes: 100 << 30, BehindModules: []string{"megaraid_sas"}},
		{Path: "/dev/sdb", SizeBytes: 100 << 30},
	}
	layout := planStorageLayout(measured, true)

	if got := role(t, layout, machine.SystemARole).Device; got != "/dev/sdb" {
		t.Errorf("the systemA device is %q, want /dev/sdb, the disk that needs no module", got)
	}
}

func TestLayoutGivesEachDiskAtMostOneSizelessRole(t *testing.T) {
	measured := []reportDisk{
		{Path: "/dev/sda", SizeBytes: 40 << 30},
		{Path: "/dev/sdb", SizeBytes: 40 << 30},
	}
	layout := planStorageLayout(measured, false)

	remainders := map[string]int{}
	for _, r := range layout.Roles {
		if r.Size == "" {
			remainders[r.Device]++
		}
	}
	for device, count := range remainders {
		if count > 1 {
			t.Errorf("%s has %d roles with no size, want at most 1", device, count)
		}
	}
	mustFit(t, layout, measured)
}

func TestLayoutWithNoUsableDiskPlansNothing(t *testing.T) {
	layout := planStorageLayout(nil, true)
	if len(layout.Roles) != 0 {
		t.Errorf("the layout is %+v, want no roles when the report has no disk", layout.Roles)
	}
}
