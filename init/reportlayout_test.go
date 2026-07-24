package main

// Tests for the capacity-aware storage layout. The planner is pure
// arithmetic over the disks the report measured, so these tests hand
// it disks by hand and read the plan back. The layout's promise is
// that the roles it plans fit the disk it plans them on, so several
// tests below run the plan through the same partition math that the
// install runs (planPartitions in claim.go).

import (
	"strings"
	"testing"

	"github.com/liken-sh/liken/disks"
	"github.com/liken-sh/liken/machine"
)

// role finds one planned role by name, and fails the test when the
// layout left it out.
func role(t *testing.T, layout storageLayout, name machine.StorageRoleName) plannedRole {
	t.Helper()
	for _, r := range layout.Roles {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("the layout has no %s role: %+v", name, layout.Roles)
	return plannedRole{}
}

// hasRole reports whether the layout placed a role at all.
func hasRole(layout storageLayout, name machine.StorageRoleName) bool {
	for _, r := range layout.Roles {
		if r.Name == name {
			return true
		}
	}
	return false
}

// mustFit runs the planned roles through the install's own partition
// math, disk by disk. This is the check that the layout exists to
// pass: an install claims each disk with exactly this arithmetic, and
// refuses a disk whose roles do not fit.
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
			t.Fatalf("the layout placed roles on %s, which is not a disk it was given", device)
		}
		if _, err := planPartitions(device, roles, size/disks.SectorSize); err != nil {
			t.Errorf("the planned roles must fit %s: %v", device, err)
		}
	}
}

func TestLayoutUsesTheConventionalSizesOnALargeDisk(t *testing.T) {
	measured := []reportDisk{{Path: "/dev/sda", SizeBytes: 500 << 30}}
	layout := planStorageLayout(measured, true)

	if got := role(t, layout, machine.SystemARole).Size; got != "1Gi" {
		t.Errorf("systemA size: %q", got)
	}
	if got := role(t, layout, machine.ClusterStateRole).Size; got != "4Gi" {
		t.Errorf("clusterState size: %q", got)
	}
	if got := role(t, layout, machine.PodEphemeralRole).Size; got != "" {
		t.Errorf("podEphemeral must take the rest: %q", got)
	}
	mustFit(t, layout, measured)
}

func TestLayoutSharesASmallDiskAmongTheDataRoles(t *testing.T) {
	// A 4 GiB disk holds the machine's own roles with about 1.4 GiB
	// left. The conventional 4Gi data roles cannot fit, so they take
	// an equal share of what is left instead.
	measured := []reportDisk{{Path: "/dev/sda", SizeBytes: 4 << 30}}
	layout := planStorageLayout(measured, true)

	cluster := role(t, layout, machine.ClusterStateRole)
	if cluster.Size == "4Gi" || cluster.Size == "" {
		t.Errorf("clusterState must scale to the disk: %q", cluster.Size)
	}
	if role(t, layout, machine.PodEphemeralRole).Size != "" {
		t.Error("podEphemeral must still take the rest")
	}
	mustFit(t, layout, measured)
}

func TestLayoutRefusesADiskTooSmallForTheMachinesOwnRoles(t *testing.T) {
	// Two 1Gi system slots, machine state, and /tmp need about 2.6
	// GiB before any data role exists, so a 2 GiB disk cannot carry a
	// liken install at all.
	layout := planStorageLayout([]reportDisk{{Path: "/dev/sda", SizeBytes: 2 << 30}}, true)

	if len(layout.Roles) != 0 {
		t.Errorf("a disk that cannot hold the minimum must get no roles: %+v", layout.Roles)
	}
	if len(layout.Notes) == 0 || !strings.Contains(strings.Join(layout.Notes, " "), "2.0 GiB") {
		t.Errorf("the notes must say what the disk offers: %v", layout.Notes)
	}
}

func TestLayoutDropsDataRolesThatDoNotFit(t *testing.T) {
	// Just enough room for the machine's own roles and a little more:
	// the data roles that cannot reach a useful size are left out, and
	// the layout says so.
	measured := []reportDisk{{Path: "/dev/sda", SizeBytes: 3 << 30}}
	layout := planStorageLayout(measured, true)

	if hasRole(layout, machine.PodStorageRole) {
		t.Error("podStorage must be left out when there is no room for it")
	}
	if len(layout.Notes) == 0 {
		t.Error("a dropped role must be explained")
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
		t.Errorf("the system slots belong on the first usable disk: %q", got)
	}
	if got := role(t, layout, machine.ClusterStateRole).Device; got != "/dev/sdb" {
		t.Errorf("the durable roles belong on the second disk: %q", got)
	}
	if !hasRole(layout, machine.BIOSBootRole) || !hasRole(layout, machine.BootHomeRole) {
		t.Error("a BIOS machine needs the GRUB roles")
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
		t.Errorf("a disk with less room than the system disk's leftover must not take the data: %q", got)
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
			t.Errorf("no role may land on a disk that might be the stick: %+v", r)
		}
	}
	mustFit(t, layout, measured)
}

func TestLayoutPrefersADiskTheInstallCanReach(t *testing.T) {
	// The first disk appeared only after the report loaded a driver,
	// so an install from a stock image never sees it. The system slots
	// belong on the disk the install can reach.
	measured := []reportDisk{
		{Path: "/dev/sda", SizeBytes: 100 << 30, BehindModules: []string{"megaraid_sas"}},
		{Path: "/dev/sdb", SizeBytes: 100 << 30},
	}
	layout := planStorageLayout(measured, true)

	if got := role(t, layout, machine.SystemARole).Device; got != "/dev/sdb" {
		t.Errorf("the reachable disk must carry the system slots: %q", got)
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
			t.Errorf("%s has %d roles that take the rest of it", device, count)
		}
	}
	mustFit(t, layout, measured)
}

func TestLayoutWithNoUsableDiskPlansNothing(t *testing.T) {
	layout := planStorageLayout(nil, true)
	if len(layout.Roles) != 0 {
		t.Errorf("no disk means no roles: %+v", layout.Roles)
	}
}

func TestSizeTextRendersTheLargestExactUnit(t *testing.T) {
	for _, c := range []struct {
		bytes uint64
		want  string
	}{
		{1 << 30, "1Gi"},
		{512 << 20, "512Mi"},
		{1 << 20, "1Mi"},
		{5 << 30, "5Gi"},
		{1536 << 20, "1536Mi"},
	} {
		if got := sizeText(c.bytes); got != c.want {
			t.Errorf("sizeText(%d) = %q, want %q", c.bytes, got, c.want)
		}
	}
}
