package main

// These tests cover the storage decisions: which partition backs each
// role, how a blank disk is partitioned, and when a spec is refused.
// They run on plain files and temporary directories. Writing a GPT
// through the kernel, mke2fs, the mounts, and the power-off after a
// refusal need a machine, such as a guest of the dev cluster in
// dev-cluster/.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/liken/disks"
	"github.com/liken-sh/liken/liken/machine"
)

func declared(name machine.StorageRoleName, device, size string) machine.DeclaredRole {
	return machine.DeclaredRole{
		Name:        name,
		StorageRole: machine.StorageRole{Device: device, Size: size},
	}
}

func TestMatchRolesFindsRolesByPartitionName(t *testing.T) {
	roles := []machine.DeclaredRole{
		declared("clusterState", "/dev/vda", ""),
		declared("podStorage", "/dev/vdb", ""),
	}
	parts := []partition{
		{name: "vda1", partName: "liken:clusterState", sizeBytes: 2 << 30},
		{name: "vdb1", partName: "some-other-os"}, // foreign, ignored
		{name: "vdb2", partName: ""},              // a table with no names at all
	}
	found, err := matchRoles(roles, parts)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("matched %d roles, want 1: %v", len(found), found)
	}
	if found["clusterState"].name != "vda1" {
		t.Errorf("clusterState matched %q, want vda1", found["clusterState"].name)
	}
}

func TestMatchRolesRefusesDuplicatePartitionNames(t *testing.T) {
	roles := []machine.DeclaredRole{declared("clusterState", "/dev/vda", "")}
	parts := []partition{
		{name: "vda1", partName: "liken:clusterState"},
		{name: "vdb1", partName: "liken:clusterState"},
	}
	_, err := matchRoles(roles, parts)
	if err == nil {
		t.Fatal("matchRoles returned no error, want one for duplicate partition names")
	}
	for _, want := range []string{"liken:clusterState", "vda1", "vdb1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is %v, want it to contain %q", err, want)
		}
	}
}

func TestPlanPartitionsPacksSizedRolesAligned(t *testing.T) {
	// On a 2 GiB disk of 4,194,304 sectors, the 1 KiB role takes two
	// sectors and the next role still starts on the next MiB boundary.
	// The role with no size ends at the last usable sector.
	mine := []machine.DeclaredRole{
		declared("machineEphemeral", "/dev/vdb", "1Ki"),
		declared("podStorage", "/dev/vdb", "512Mi"),
		declared("podEphemeral", "/dev/vdb", ""),
	}
	parts, err := planPartitions("/dev/vdb", mine, 4_194_304)
	if err != nil {
		t.Fatal(err)
	}
	want := []disks.Partition{
		{Name: "liken:machineEphemeral", FirstLBA: 2_048, LastLBA: 2_049, TypeGUID: disks.LinuxFilesystemData},
		{Name: "liken:podStorage", FirstLBA: 4_096, LastLBA: 1_052_671, TypeGUID: disks.LinuxFilesystemData},
		{Name: "liken:podEphemeral", FirstLBA: 1_052_672, LastLBA: 4_194_269, TypeGUID: disks.LinuxFilesystemData},
	}
	if len(parts) != len(want) {
		t.Fatalf("planned %d partitions, want %d: %v", len(parts), len(want), parts)
	}
	for i, w := range want {
		if parts[i] != w {
			t.Errorf("partition %d: got %+v, want %+v", i, parts[i], w)
		}
	}
}

func TestPlanPartitionsTypesSystemSlotsAsESP(t *testing.T) {
	// UEFI firmware looks for boot files only on a partition with the
	// EFI system partition type.
	mine := []machine.DeclaredRole{
		declared("systemA", "/dev/vdc", "512Mi"),
		declared("systemB", "/dev/vdc", "512Mi"),
	}
	parts, err := planPartitions("/dev/vdc", mine, 4_194_304)
	if err != nil {
		t.Fatal(err)
	}
	want := []disks.Partition{
		{Name: "liken:systemA", FirstLBA: 2_048, LastLBA: 1_050_623, TypeGUID: disks.EFISystemPartition},
		{Name: "liken:systemB", FirstLBA: 1_050_624, LastLBA: 2_099_199, TypeGUID: disks.EFISystemPartition},
	}
	if len(parts) != len(want) {
		t.Fatalf("planned %d partitions, want %d: %v", len(parts), len(want), parts)
	}
	for i, w := range want {
		if parts[i] != w {
			t.Errorf("partition %d: got %+v, want %+v", i, parts[i], w)
		}
	}
}

func TestPlanPartitionsTypesTheBIOSBootRoles(t *testing.T) {
	// bootHome has the Linux filesystem data type, because GRUB finds
	// it by its filesystem label LIKEN-BOOT (grub/early.cfg).
	mine := []machine.DeclaredRole{
		declared("biosBoot", "/dev/vdc", "1Mi"),
		declared("bootHome", "/dev/vdc", "64Mi"),
		declared("systemA", "/dev/vdc", "512Mi"),
	}
	parts, err := planPartitions("/dev/vdc", mine, 4_194_304)
	if err != nil {
		t.Fatal(err)
	}
	want := []disks.Partition{
		{Name: "liken:biosBoot", FirstLBA: 2_048, LastLBA: 4_095, TypeGUID: disks.BIOSBootPartition},
		{Name: "liken:bootHome", FirstLBA: 4_096, LastLBA: 135_167, TypeGUID: disks.LinuxFilesystemData},
		{Name: "liken:systemA", FirstLBA: 135_168, LastLBA: 1_183_743, TypeGUID: disks.EFISystemPartition},
	}
	if len(parts) != len(want) {
		t.Fatalf("planned %d partitions, want %d: %v", len(parts), len(want), parts)
	}
	for i, w := range want {
		if parts[i] != w {
			t.Errorf("partition %d: got %+v, want %+v", i, parts[i], w)
		}
	}
}

func TestPlanPartitionsRejectsDiskTooSmall(t *testing.T) {
	// The disk is 2 MiB, but the GPT uses part of it.
	mine := []machine.DeclaredRole{declared("clusterState", "/dev/vda", "2Mi")}
	_, err := planPartitions("/dev/vda", mine, 4_096)
	if err == nil {
		t.Fatal("planPartitions returned no error, want one for a role bigger than the disk")
	}
	for _, want := range []string{"too small", "clusterState", "2Mi"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is %v, want it to contain %q", err, want)
		}
	}
}

func TestPlanPartitionsRejectsNoRoomForRemainder(t *testing.T) {
	// The 1 MiB role ends at the last usable sector.
	mine := []machine.DeclaredRole{
		declared("machineEphemeral", "/dev/vdb", "1Mi"),
		declared("podStorage", "/dev/vdb", ""),
	}
	_, err := planPartitions("/dev/vdb", mine, 4_130)
	if err == nil {
		t.Fatal("planPartitions returned no error, want one when nothing is left for the remainder")
	}
	if !strings.Contains(err.Error(), "podStorage") {
		t.Errorf("the error is %v, want it to name podStorage", err)
	}
}

// signedDevice writes a fake device file of 2 KiB of zeros with the
// signature bytes that stamp sets. isBlank and hasExt4 read only the
// first 2 KiB.
func signedDevice(t *testing.T, stamp func(b []byte)) string {
	t.Helper()
	buf := make([]byte, 2_048)
	if stamp != nil {
		stamp(buf)
	}
	path := filepath.Join(t.TempDir(), "disk")
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestIsBlank(t *testing.T) {
	cases := []struct {
		name  string
		stamp func(b []byte)
		blank bool
	}{
		{"zeros", nil, true},
		{"mbr", func(b []byte) { b[510], b[511] = 0x55, 0xAA }, false},
		{"gpt", func(b []byte) { copy(b[512:], "EFI PART") }, false},
		{"ext4", func(b []byte) { b[1080], b[1081] = 0x53, 0xEF }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			blank, err := isBlank(signedDevice(t, c.stamp))
			if err != nil {
				t.Fatal(err)
			}
			if blank != c.blank {
				t.Errorf("isBlank = %v, want %v", blank, c.blank)
			}
		})
	}
}

func TestIsBlankReportsMissingDevices(t *testing.T) {
	if _, err := isBlank(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("isBlank returned no error for a device that does not exist, want one")
	}
}

func TestIsBlankReportsUnreadableDevices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "truncated")
	if err := os.WriteFile(path, make([]byte, 100), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := isBlank(path); err == nil {
		t.Error("no error for a device too short to probe, want one")
	}
}

func TestHasExt4(t *testing.T) {
	withMagic := signedDevice(t, func(b []byte) { b[1080], b[1081] = 0x53, 0xEF })
	if !hasExt4(withMagic) {
		t.Error("hasExt4 is false for a device with the ext4 superblock magic, want true")
	}
	if hasExt4(signedDevice(t, nil)) {
		t.Error("hasExt4 is true for a device of zeros, want false")
	}
	if hasExt4(filepath.Join(t.TempDir(), "absent")) {
		t.Error("hasExt4 is true for a device that does not exist, want false")
	}
}

func TestReconcileStorageEmptySpecStaysInMemory(t *testing.T) {
	status, err := reconcileStorage(machine.StorageSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if status != machine.AllRolesInMemory() {
		t.Errorf("the status is %+v, want every role in memory for an empty spec", status)
	}
}

func TestReconcileStorageRejectsInvalidSpec(t *testing.T) {
	// Validation runs before sysfs is read, so this test needs no fake
	// machine.
	_, err := reconcileStorage(machine.StorageSpec{ClusterState: &machine.StorageRole{}})
	if err == nil {
		t.Fatal("reconcileStorage returned no error, want one for a role with no device")
	}
	if !strings.Contains(err.Error(), "clusterState") {
		t.Errorf("the error is %v, want it to name clusterState", err)
	}
}

func TestPlanAllClaimsRefusesADiskThatAlreadyHasARole(t *testing.T) {
	// clusterState has a partition on vda, and podStorage, declared on
	// the same disk, has none. Claiming vda for podStorage would write
	// a new table over clusterState. planAllClaims, not planClaim, makes
	// this check, because it groups the roles by disk.
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 2<<30, make([]byte, 2_048))
	device := filepath.Join(dev, "vda")
	roles := []machine.DeclaredRole{
		declared("clusterState", device, "1Mi"),
		declared("podStorage", device, ""),
	}
	found := map[machine.StorageRoleName]partition{
		"clusterState": {name: "vda1", disk: "vda", partName: "liken:clusterState"},
	}
	_, err := planAllClaims(roles, found)
	if err == nil {
		t.Fatal("planAllClaims returned no error, want one for a disk that already has a role")
	}
	for _, want := range []string{"liken:clusterState", "does not change the disk"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is %v, want it to contain %q", err, want)
		}
	}
}

func TestPlanAllClaimsSkipsADiskWhoseRolesAreAllRecognized(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 2<<30, nil)
	device := filepath.Join(dev, "vda")
	roles := []machine.DeclaredRole{declared("clusterState", device, "")}
	found := map[machine.StorageRoleName]partition{
		"clusterState": {name: "vda1", disk: "vda", partName: "liken:clusterState"},
	}
	claims, err := planAllClaims(roles, found)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Errorf("planAllClaims returned the claims %+v, want none for a disk whose roles were all found", claims)
	}
}

func TestPlanAllClaimsRefusesTwoSizelessRolesOnOneDiskDeclaredTwoWays(t *testing.T) {
	// Validate allows one role without a size per disk, but it compares
	// the declared strings. Here clusterState declares the disk by its
	// kernel path and podStorage by its by-id name. Without this check,
	// planPartitions would keep only one of the two roles.
	sys, dev := fakeMachine(t)
	dir := fakeDisk(t, sys, "vda", "pci0000:00", "0000:00:05.0", "virtio2", "block", "vda")
	writeSysfs(t, dir, "serial", "liken-a\n")
	writeSysfs(t, dir, "size", fmt.Sprintf("%d\n", (2<<30)/disks.SectorSize))
	if err := os.WriteFile(filepath.Join(dev, "vda"), make([]byte, 2_048), 0o600); err != nil {
		t.Fatal(err)
	}

	roles := []machine.DeclaredRole{
		declared("clusterState", filepath.Join(dev, "vda"), ""),
		declared("podStorage", "/dev/disk/by-id/virtio-liken-a", ""),
	}
	_, err := planAllClaims(roles, nil)
	if err == nil {
		t.Fatal("planAllClaims returned no error, want one for two roles without a size on one disk")
	}
	for _, want := range []string{"clusterState", "podStorage", "only one role on a disk may omit its size"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is %v, want it to contain %q", err, want)
		}
	}
}

func TestPlanAllClaimsRefusesTheDeclaredDiskOfAFoundRole(t *testing.T) {
	// clusterState has a partition on vda but declares vdb, the blank
	// disk that podStorage needs. planAllClaims groups roles by their
	// declared disk, so it refuses to claim vdb, even though
	// clusterState's partition is on another disk.
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vdb", 2<<30, make([]byte, 2_048)) // blank
	deviceB := filepath.Join(dev, "vdb")
	roles := []machine.DeclaredRole{
		declared("clusterState", deviceB, "1Mi"),
		declared("podStorage", deviceB, ""),
	}
	found := map[machine.StorageRoleName]partition{
		"clusterState": {name: "vda1", disk: "vda", partName: "liken:clusterState"},
	}
	claims, err := planAllClaims(roles, found)
	if err == nil {
		t.Fatal("planAllClaims returned no error, want a refusal")
	}
	if !strings.Contains(err.Error(), "does not change the disk") {
		t.Errorf("the error is %v, want it to contain \"does not change the disk\"", err)
	}
	if len(claims) != 0 {
		t.Errorf("a refused plan returned claims %+v, want none", claims)
	}
}

func TestPlanAllClaimsPropagatesAResolverError(t *testing.T) {
	// Two disks have the same WWN, so the declared by-id name matches
	// both.
	sys, _ := fakeMachine(t)
	for _, name := range []string{"sda", "sdb"} {
		dir := fakeDisk(t, sys, name, "pci0000:00", "0000:00:1f.2", "ata3", "host2",
			"target2:0:0", "2:0:0:0", "block", name)
		writeSysfs(t, filepath.Join(dir, "device"), "wwid", "naa.5002538d40a45c88\n")
	}
	roles := []machine.DeclaredRole{declared("clusterState", "/dev/disk/by-id/wwn-0x5002538d40a45c88", "")}
	_, err := planAllClaims(roles, nil)
	if err == nil || !strings.Contains(err.Error(), "refusing to guess") {
		t.Errorf("the error is %v, want one that reports the ambiguous disk", err)
	}
}

func TestReconcileStorageRefusesAnAmbiguousDeclaredDisk(t *testing.T) {
	// The same ambiguity through reconcileStorage: the wait ends at
	// once, and planAllClaims returns the error.
	sys, _ := fakeMachine(t)
	for _, name := range []string{"sda", "sdb"} {
		dir := fakeDisk(t, sys, name, "pci0000:00", "0000:00:1f.2", "ata3", "host2",
			"target2:0:0", "2:0:0:0", "block", name)
		writeSysfs(t, filepath.Join(dir, "device"), "wwid", "naa.5002538d40a45c88\n")
	}
	spec := machine.StorageSpec{ClusterState: &machine.StorageRole{Device: "/dev/disk/by-id/wwn-0x5002538d40a45c88"}}

	_, err := reconcileStorage(spec)
	if err == nil || !strings.Contains(err.Error(), "refusing to guess") {
		t.Errorf("the error is %v, want one that reports the ambiguous disk", err)
	}
}

func TestPlanClaimRefusesUnattachedDevices(t *testing.T) {
	// A device that is not attached resolves to a nil disk. The error
	// still names the declared device, because that is the string the
	// person wrote in the spec.
	mine := []machine.DeclaredRole{declared("clusterState", "/dev/vda", "")}
	_, err := planClaim("/dev/vda", nil, mine)
	if err == nil || !strings.Contains(err.Error(), "/dev/vda") || !strings.Contains(err.Error(), "not attached") {
		t.Errorf("the error is %v, want one that names /dev/vda as not attached", err)
	}
}

func TestPlanClaimRefusesForeignDisks(t *testing.T) {
	cases := []struct {
		name  string
		stamp func(b []byte)
	}{
		{"mbr", func(b []byte) { b[510], b[511] = 0x55, 0xAA }},
		{"gpt", func(b []byte) { copy(b[512:], "EFI PART") }},
		{"ext4", func(b []byte) { b[1080], b[1081] = 0x53, 0xEF }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sys, dev := fakeMachine(t)
			contents := make([]byte, 2_048)
			c.stamp(contents)
			addDisk(t, sys, dev, "vda", 1<<30, contents)
			device := filepath.Join(dev, "vda")
			mine := []machine.DeclaredRole{declared("clusterState", device, "")}
			_, err := planClaim(device, diskByPath(device), mine)
			if err == nil || !strings.Contains(err.Error(), "does not recognize") {
				t.Errorf("the error is %v, want a refusal to touch a foreign disk", err)
			}
		})
	}
}

func TestPlanClaimReportsUnreadableDevices(t *testing.T) {
	// The device node is too short to read the first 2 KiB.
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 1<<30, make([]byte, 100))
	device := filepath.Join(dev, "vda")
	mine := []machine.DeclaredRole{declared("clusterState", device, "")}
	_, err := planClaim(device, diskByPath(device), mine)
	if err == nil || !strings.Contains(err.Error(), "examining") {
		t.Errorf("the error is %v, want one about examining the device", err)
	}
}

func TestWaitForPartitionsReportsMissingPartitions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sys, dev := fakeMachine(t)
		addDisk(t, sys, dev, "vda", 1<<30, nil)
		addPartition(t, sys, "vda", "vda1", "liken:clusterState", 1<<20)

		parts := []disks.Partition{
			// The size of clusterState matches the 1 MiB that the fixture reports.
			{Name: "liken:clusterState", FirstLBA: 2_048, LastLBA: 2_048 + (1<<20)/disks.SectorSize - 1},
			{Name: "liken:podStorage", FirstLBA: 4_096, LastLBA: 8_191},
		}
		begin := time.Now()
		err := waitForPartitions(parts)
		if err == nil {
			t.Fatal("waitForPartitions returned no error, want one for a partition that never appears")
		}
		// The wait checks every 100ms and gives up at the first check
		// after partitionWaitTimeout.
		if elapsed := time.Since(begin); elapsed != partitionWaitTimeout+100*time.Millisecond {
			t.Errorf("the wait gave up after %s, want the first look past %s", elapsed, partitionWaitTimeout)
		}
		if !strings.Contains(err.Error(), "liken:podStorage") {
			t.Errorf("the error is %v, want it to name liken:podStorage", err)
		}
		if strings.Contains(err.Error(), "liken:clusterState") {
			t.Errorf("the error is %v, want it not to name liken:clusterState", err)
		}
	})
}

func TestWaitForPartitionsReportsStaleSizes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// The partition exists, but sysfs still reports its old size.
		sys, dev := fakeMachine(t)
		addDisk(t, sys, dev, "vda", 1<<30, nil)
		addPartition(t, sys, "vda", "vda1", "liken:clusterState", 1<<20)

		parts := []disks.Partition{
			{Name: "liken:clusterState", FirstLBA: 2_048, LastLBA: 2_048 + (2<<20)/disks.SectorSize - 1},
		}
		err := waitForPartitions(parts)
		if err == nil {
			t.Fatal("waitForPartitions returned no error, want one for a stale partition size")
		}
		if !strings.Contains(err.Error(), "liken:clusterState") || !strings.Contains(err.Error(), "still") {
			t.Errorf("the error is %v, want it to name liken:clusterState and say its size is still old", err)
		}
	})
}

func TestNeedsFilesystemKeepsAFilesystemItRecognizes(t *testing.T) {
	ext4 := signedDevice(t, func(b []byte) { b[1080], b[1081] = 0x53, 0xEF })
	if needsFilesystem(ext4, "", false) {
		t.Error("needsFilesystem is true for an ext4 partition, want false")
	}
	if !needsFilesystem(signedDevice(t, nil), "", false) {
		t.Error("needsFilesystem is false for a partition with no filesystem, want true")
	}
}

func TestNeedsFilesystemRemakesAPartitionThisBootCreated(t *testing.T) {
	// A reinstall writes the same layout at the same offsets, so the
	// old ext4 or FAT32 filesystem is still on the partition.
	ext4 := signedDevice(t, func(b []byte) { b[1080], b[1081] = 0x53, 0xEF })
	if !needsFilesystem(ext4, "", true) {
		t.Error("needsFilesystem is false for an ext4 partition that this boot created, want true")
	}
	fat := signedDevice(t, func(b []byte) {
		b[510], b[511] = 0x55, 0xAA
		copy(b[82:90], "FAT32   ")
	})
	if needsFilesystem(fat, "vfat", false) {
		t.Error("needsFilesystem is true for a FAT32 slot that was found, want false")
	}
	if !needsFilesystem(fat, "vfat", true) {
		t.Error("needsFilesystem is false for a FAT32 slot that this boot created, want true")
	}
}

func TestMountRoleRejectsARoleThatRoleMountsDoesNotList(t *testing.T) {
	// mountRole looks up the role in roleMounts before it touches the
	// partition, so this test needs no device.
	err := mountRole(declared("archive", "/dev/vda", ""), partition{name: "vda9"}, false)
	if err == nil || !strings.Contains(err.Error(), "archive") {
		t.Errorf("mountRole returned %v, want an error that names role archive", err)
	}
}

func TestRecognizeRolesReadsTheFakeMachine(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 2<<30, nil)
	addPartition(t, sys, "vda", "vda1", "liken:clusterState", 1<<30)

	found, err := recognizeRoles([]machine.DeclaredRole{declared("clusterState", "/dev/vda", "")})
	if err != nil {
		t.Fatal(err)
	}
	if found["clusterState"].name != "vda1" {
		t.Errorf("recognizeRoles found %+v, want clusterState on vda1", found)
	}
}

func TestPlanClaimLaysOutABlankDisk(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vdb", 2<<30, make([]byte, 2_048)) // all zeros: blank
	device := filepath.Join(dev, "vdb")
	mine := []machine.DeclaredRole{
		declared("machineState", device, "512Mi"),
		declared("clusterState", device, ""),
	}

	plan, err := planClaim(device, diskByPath(device), mine)
	if err != nil {
		t.Fatal(err)
	}
	if plan.device != device || len(plan.roles) != 2 || len(plan.parts) != 2 {
		t.Errorf("the plan is %+v, want both roles in one plan on the device", plan)
	}
	if plan.totalSectors != (2<<30)/disks.SectorSize {
		t.Errorf("the plan has %d sectors, want the disk's %d", plan.totalSectors, (2<<30)/disks.SectorSize)
	}
}

func TestReconcileStorageReturnsTheErrorOfAFailedClaim(t *testing.T) {
	// Planning succeeds on the blank fake disk. The fake device is a
	// plain file, so the BLKRRPART ioctl fails on it partway through
	// the claim.
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vdb", 2<<30, make([]byte, 2_048))
	device := filepath.Join(dev, "vdb")
	spec := machine.StorageSpec{ClusterState: &machine.StorageRole{Device: device}}

	status, err := reconcileStorage(spec)
	if err == nil || !strings.Contains(err.Error(), "partitioning") {
		t.Errorf("the error is %v, want one about partitioning", err)
	}
	if status != machine.AllRolesInMemory() {
		t.Errorf("the status is %+v, want every role in memory after a failed claim", status)
	}
}

func TestReconcileStorageRefusesAnUnsatisfiableGrow(t *testing.T) {
	// A system slot declared larger than its partition is refused
	// while planning, before anything is written.
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 2<<30, nil)
	addPartition(t, sys, "vda", "vda1", "liken:systemA", 1<<20)
	device := filepath.Join(dev, "vda")
	spec := machine.StorageSpec{SystemA: &machine.StorageRole{Device: device, Size: "512Mi"}}

	_, err := reconcileStorage(spec)
	if err == nil || !strings.Contains(err.Error(), "can't grow") {
		t.Errorf("the error is %v, want a refusal to grow the partition", err)
	}
}

func TestWaitForPartitionsSucceedsWhenEverythingIsVisible(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 1<<30, nil)
	addPartition(t, sys, "vda", "vda1", "liken:clusterState", 1<<20)

	parts := []disks.Partition{
		{Name: "liken:clusterState", FirstLBA: 2_048, LastLBA: 2_048 + (1<<20)/disks.SectorSize - 1},
	}
	if err := waitForPartitions(parts); err != nil {
		t.Errorf("waitForPartitions returned %v, want nil when every partition is visible at its planned size", err)
	}
}

func TestAwaitStorageDevicesEndsImmediatelyOnAnAmbiguousName(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Two disks have the same WWN, so the declared by-id name
		// matches both, and another disk attaching cannot change that.
		sys, _ := fakeMachine(t)
		for _, name := range []string{"sda", "sdb"} {
			dir := fakeDisk(t, sys, name, "pci0000:00", "0000:00:1f.2", "ata3", "host2",
				"target2:0:0", "2:0:0:0", "block", name)
			writeSysfs(t, filepath.Join(dir, "device"), "wwid", "naa.5002538d40a45c88\n")
		}
		roles := []machine.DeclaredRole{declared("clusterState", "/dev/disk/by-id/wwn-0x5002538d40a45c88", "")}

		start := time.Now()
		awaitStorageDevices(roles)
		if elapsed := time.Since(start); elapsed != 0 {
			t.Errorf("the wait took %s, want 0 for a name that matches two disks", elapsed)
		}
	})
}

func TestAwaitStorageDevicesEndsImmediatelyOnTwoPartitionsWithOneRolesName(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Two partitions have clusterState's partition name, and
		// waiting cannot change that.
		sys, dev := fakeMachine(t)
		addDisk(t, sys, dev, "vda", 1<<30, nil)
		addPartition(t, sys, "vda", "vda1", "liken:clusterState", 1<<20)
		addDisk(t, sys, dev, "vdb", 1<<30, nil)
		addPartition(t, sys, "vdb", "vdb1", "liken:clusterState", 1<<20)
		roles := []machine.DeclaredRole{declared("clusterState", filepath.Join(dev, "vda"), "")}

		start := time.Now()
		awaitStorageDevices(roles)
		if elapsed := time.Since(start); elapsed != 0 {
			t.Errorf("the wait took %s, want 0 for two partitions with one role's name", elapsed)
		}
	})
}
