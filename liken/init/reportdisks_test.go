package main

// These tests read the fake /sys/block and /dev that disks_test.go
// builds.

import (
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

func TestResolveInstallStickFindsTheStickByPartitionName(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "sda", 500<<30, nil)
	addPartition(t, sys, "sda", "sda1", "liken:systemA", 1<<30)
	addDisk(t, sys, dev, "sdb", 1<<30, nil)
	addPartition(t, sys, "sdb", "sdb1", "liken:install", 1<<30)

	found := resolveInstallStick()
	if found.Disk != "sdb" || found.Path != dev+"/sdb" {
		t.Errorf("the stick is %+v, want disk sdb at %s/sdb", found, dev)
	}
	if found.Partition != dev+"/sdb1" {
		t.Errorf("the partition is %q, want %q", found.Partition, dev+"/sdb1")
	}
	if found.ambiguous() {
		t.Error("one install stick is ambiguous, want not ambiguous")
	}
}

func TestResolveInstallStickReportsAnAmbiguousStick(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "sdb", 1<<30, nil)
	addPartition(t, sys, "sdb", "sdb1", "liken:install", 1<<30)
	addDisk(t, sys, dev, "sdc", 1<<30, nil)
	addPartition(t, sys, "sdc", "sdc1", "liken:install", 1<<30)

	found := resolveInstallStick()
	if found.Disk != "" || found.Partition != "" {
		t.Errorf("two install sticks resolved to %+v, want no disk and no partition", found)
	}
	if !found.ambiguous() || len(found.Candidates) != 2 {
		t.Errorf("the candidates are %+v, want both install sticks", found)
	}
}

func TestResolveInstallStickWithNoStick(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "sda", 500<<30, nil)

	found := resolveInstallStick()
	if found.Disk != "" || found.ambiguous() || len(found.Candidates) != 0 {
		t.Errorf("with no install stick the result is %+v, want no disk, not ambiguous, no candidates", found)
	}
}

func TestAwaitInstallStickEndsTheMomentTheStickExists(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "sdb", 1<<30, nil)
	addPartition(t, sys, "sdb", "sdb1", "liken:install", 1<<30)

	if found := awaitInstallStick(); found.Disk != "sdb" {
		t.Errorf("the stick is %+v, want disk sdb", found)
	}
}

func TestAwaitInstallStickGivesUpAtTheCeiling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fakeMachine(t)

		start := time.Now()
		found := awaitInstallStick()
		if found.Disk != "" {
			t.Errorf("with no install stick the result is %+v, want no disk", found)
		}
		// The wait polls every 200ms and stops at the first poll after
		// the ceiling.
		if elapsed := time.Since(start); elapsed != stickCeiling+200*time.Millisecond {
			t.Errorf("the wait stopped after %s, want the first look past its ceiling of %s", elapsed, stickCeiling)
		}
	})
}

func TestReadReportDisksExcludesTheStick(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "sda", 500<<30, nil)
	writeSysfs(t, filepath.Join(sys, "sda", "device"), "model", "REAL DISK\n")
	addDisk(t, sys, dev, "sdb", 1<<30, nil)
	addPartition(t, sys, "sdb", "sdb1", "liken:install", 1<<30)

	disks, _ := readReportDisks(resolveInstallStick())
	if len(disks) != 1 {
		t.Fatalf("the report has %d disks, want 1: %v", len(disks), disks)
	}
	if disks[0].Path != dev+"/sda" || disks[0].Model != "REAL DISK" {
		t.Errorf("the disk is %+v, want the path %s/sda and the model REAL DISK", disks[0], dev)
	}
	if disks[0].SizeBytes != 500<<30 || disks[0].Name != "sda" {
		t.Errorf("the disk is %+v, want size 500 GiB and name sda", disks[0])
	}
}

func TestReadReportDisksMarksEveryDiskThatMightBeTheStick(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "sda", 500<<30, nil)
	addDisk(t, sys, dev, "sdb", 1<<30, nil)
	addPartition(t, sys, "sdb", "sdb1", "liken:install", 1<<30)
	addDisk(t, sys, dev, "sdc", 1<<30, nil)
	addPartition(t, sys, "sdc", "sdc1", "liken:install", 1<<30)

	disks, _ := readReportDisks(resolveInstallStick())
	if len(disks) != 3 {
		t.Fatalf("the report has %d disks, want 3: %v", len(disks), disks)
	}
	marked := map[string]bool{}
	for _, d := range disks {
		marked[d.Name] = d.MaybeStick
	}
	if marked["sda"] || !marked["sdb"] || !marked["sdc"] {
		t.Errorf("the disks marked MaybeStick are %v, want only sdb and sdc", marked)
	}
}

func TestReadReportDisksSkipsDevicesThatCannotHoldARole(t *testing.T) {
	sys, dev := fakeMachine(t)
	// A DVD drive with a disc has a size, but it cannot hold a
	// partition table.
	addDisk(t, sys, dev, "sr0", 4<<30, nil)
	writeSysfs(t, filepath.Join(sys, "sr0", "device"), "type", "5\n")
	// A card reader with no card reports zero sectors.
	addDisk(t, sys, dev, "mmcblk0", 0, nil)
	// A write-protected medium cannot take a role.
	addDisk(t, sys, dev, "sdz", 8<<30, nil)
	writeSysfs(t, filepath.Join(sys, "sdz"), "ro", "1\n")
	// A small SATA DOM is a valid boot disk and passes every check.
	addDisk(t, sys, dev, "sda", 8<<30, nil)
	writeSysfs(t, filepath.Join(sys, "sda", "device"), "type", "0\n")

	disks, _ := readReportDisks(installStick{})
	if len(disks) != 1 || disks[0].Name != "sda" {
		t.Errorf("the report has %+v, want only sda", disks)
	}
}

func TestReadReportDisksExcludesAnSDCard(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "mmcblk0", 64<<30, nil)
	writeSysfs(t, filepath.Join(sys, "mmcblk0", "device"), "type", "SD\n")
	addDisk(t, sys, dev, "nvme0n1", 256<<30, nil)

	disks, cards := readReportDisks(installStick{})
	if len(disks) != 1 || disks[0].Name != "nvme0n1" {
		t.Errorf("the report has %+v, want only nvme0n1", disks)
	}
	if len(cards) != 1 || cards[0].Path != dev+"/mmcblk0" {
		t.Errorf("the cards are %+v, want only mmcblk0", cards)
	}
	layout := planStorageLayout(disks, true)
	for _, role := range layout.Roles {
		if role.Device != dev+"/nvme0n1" {
			t.Errorf("role %s is on %s, want %s", role.Name, role.Device, dev+"/nvme0n1")
		}
	}
	if len(layout.Roles) == 0 {
		t.Error("the layout has no roles, want roles on nvme0n1")
	}
}

// Some machines have an eMMC module as their only disk.
func TestReadReportDisksProposesAnEMMCCard(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "mmcblk0", 64<<30, nil)
	writeSysfs(t, filepath.Join(sys, "mmcblk0", "device"), "type", "MMC\n")

	disks, cards := readReportDisks(installStick{})
	if len(disks) != 1 || disks[0].Name != "mmcblk0" {
		t.Errorf("the report has %+v, want only mmcblk0", disks)
	}
	if len(cards) != 0 {
		t.Errorf("the cards are %+v, want none", cards)
	}
}

func TestReadReportDisksRecordsTheFirstByIDName(t *testing.T) {
	sys, _ := fakeMachine(t)
	dir := fakeDisk(t, sys, "sda", "pci0000:00", "0000:00:1f.2", "ata3", "host2",
		"target2:0:0", "2:0:0:0", "block", "sda")
	writeSysfs(t, dir, "size", "16777216\n")
	writeSysfs(t, filepath.Join(dir, "device"), "wwid", "naa.5002538d40a45c88\n")

	disks, _ := readReportDisks(installStick{})
	if len(disks) != 1 || disks[0].StableName != "/dev/disk/by-id/wwn-0x5002538d40a45c88" {
		t.Errorf("the report has %+v, want the stable name /dev/disk/by-id/wwn-0x5002538d40a45c88", disks)
	}
}

func TestReadReportDisksWithOnlyAByPathNameHasNoStableName(t *testing.T) {
	sys, _ := fakeMachine(t)
	dir := fakeDisk(t, sys, "sda", "pci0000:00", "0000:00:1f.2", "ata3", "host2",
		"target2:0:0", "2:0:0:0", "block", "sda")
	writeSysfs(t, dir, "size", "16777216\n")

	// With no wwid, the disk's only stable name is a by-path name.
	disks, _ := readReportDisks(installStick{})
	if len(disks) != 1 || disks[0].StableName != "" {
		t.Errorf("the report has %+v, want one disk with no stable name", disks)
	}
}

func TestDiskTransportReadsTheBusFromTheDeviceTree(t *testing.T) {
	sys, _ := fakeMachine(t)
	target := filepath.Join(sys, "devices", "pci0000:00", "ata2", "host1", "block", "sdx")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("building the device tree: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(sys, "sdx")); err != nil {
		t.Fatalf("building the device tree: %v", err)
	}

	if got := diskTransport("sdx"); got != "sata" {
		t.Errorf("the transport of sdx is %q, want sata", got)
	}
	if got := diskTransport("absent"); got != "" {
		t.Errorf("the transport of a disk that is not there is %q, want none", got)
	}
}

// hbaTree builds a disk below one PCI function: a /sys/block entry
// that resolves to a path under the controller's directory.
func hbaTree(t *testing.T, sys, controller, disk string) (diskName, controllerDir string) {
	t.Helper()
	controllerDir = filepath.Join(sys, "devices", "pci0000:00", controller)
	target := filepath.Join(controllerDir, "host0", "target0:0:0", "block", disk)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("building the device tree: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(sys, disk)); err != nil {
		t.Fatalf("building the device tree: %v", err)
	}
	return disk, controllerDir
}

func TestMarkDisksBehindDriversNamesTheChainThatReachedThem(t *testing.T) {
	sys, dev := fakeMachine(t)
	name, controller := hbaTree(t, sys, "0000:03:00.0", "sda")
	elsewhere := filepath.Join(sys, "devices", "pci0000:00", "0000:00:1f.2")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatalf("building the device tree: %v", err)
	}
	onboard, _ := hbaTree(t, sys, "0000:00:17.0", "sdb")

	disks := []reportDisk{{Name: name, Path: dev + "/" + name}, {Name: onboard, Path: dev + "/" + onboard}}
	recs := []moduleRecommendation{{
		Device: "an HBA", Class: "storage", Chain: []string{"mpt3sas"},
		SysfsDirs: []string{controller},
	}}

	marked := markDisksBehindDrivers(disks, recs)
	if len(marked[0].BehindModules) != 1 || marked[0].BehindModules[0] != "mpt3sas" {
		t.Errorf("the disk behind the controller has BehindModules %+v, want [mpt3sas]", marked[0])
	}
	if len(marked[1].BehindModules) != 0 {
		t.Errorf("the disk on the boot path has BehindModules %+v, want none", marked[1])
	}
}

// stickTree builds a stick disk whose /sys/block entry resolves under
// a USB device's directory, and returns the disk and that directory.
func stickTree(t *testing.T, sys string) (stickName, usbDevice string) {
	t.Helper()
	usbDevice = filepath.Join(sys, "devices", "usb2", "2-1", "2-1:1.0")
	disk := filepath.Join(usbDevice, "host8", "block", "sdd")
	if err := os.MkdirAll(disk, 0o755); err != nil {
		t.Fatalf("building the device tree: %v", err)
	}
	if err := os.Symlink(disk, filepath.Join(sys, "sdd")); err != nil {
		t.Fatalf("building the device tree: %v", err)
	}
	return "sdd", usbDevice
}

func TestWithoutStickRecommendationsDropsOnlyTheSticksDriver(t *testing.T) {
	sys, _ := fakeMachine(t)
	stickName, usbDevice := stickTree(t, sys)
	elsewhere := filepath.Join(sys, "devices", "pci0000:00", "00:03.0")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatalf("building the device tree: %v", err)
	}

	recs := []moduleRecommendation{
		{Device: "the stick", Chain: []string{"usb-storage"}, SysfsDirs: []string{usbDevice}},
		{Device: "a NIC", Chain: []string{"e1000"}, SysfsDirs: []string{elsewhere}},
	}

	kept := withoutStickRecommendations(recs, stickName)
	if len(kept) != 1 || kept[0].Device != "a NIC" {
		t.Errorf("the kept recommendations are %+v, want only the NIC", kept)
	}
}

func TestWithoutStickRecommendationsKeepsEverythingWithoutAStick(t *testing.T) {
	recs := []moduleRecommendation{
		{Device: "a NIC", Chain: []string{"e1000"}, SysfsDirs: []string{"/nowhere"}},
	}

	if kept := withoutStickRecommendations(recs, ""); len(kept) != 1 {
		t.Errorf("with no stick the kept recommendations are %+v, want all 1", kept)
	}
}

func TestWithoutStickRecommendationsKeepsEverythingWhenTheStickIsUnreadable(t *testing.T) {
	fakeMachine(t)
	recs := []moduleRecommendation{
		{Device: "a NIC", Chain: []string{"e1000"}, SysfsDirs: []string{"/nowhere"}},
	}

	if kept := withoutStickRecommendations(recs, "ghost"); len(kept) != 1 {
		t.Errorf("with an unreadable stick the kept recommendations are %+v, want all 1", kept)
	}
}
