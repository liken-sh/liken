package main

// Tests for the report boot's disk-side helpers: resolving the
// installation stick once, keeping it and the devices that cannot hold
// a role out of the walk, telling which disks exist only because a
// driver loaded, and telling a recommendation that serves the machine
// from one that serves only the stick. The helpers read the same fake
// /sys/block and /dev that the storage tests build (disks_test.go),
// because the report recognizes the stick exactly the way storage
// recognizes a role: by the name written into the GPT.

import (
	"os"
	"path/filepath"
	"testing"
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
		t.Errorf("the stick's disk: %+v", found)
	}
	if found.Partition != dev+"/sdb1" {
		t.Errorf("the volume to write to: %q", found.Partition)
	}
	if found.ambiguous() {
		t.Error("one stick is not ambiguous")
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
		t.Errorf("two sticks must resolve to none: %+v", found)
	}
	if !found.ambiguous() || len(found.Candidates) != 2 {
		t.Errorf("both disks must stay named as candidates: %+v", found)
	}
}

func TestResolveInstallStickWithNoStick(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "sda", 500<<30, nil)

	found := resolveInstallStick()
	if found.Disk != "" || found.ambiguous() || len(found.Candidates) != 0 {
		t.Errorf("no stick must exclude nothing: %+v", found)
	}
}

func TestAwaitInstallStickEndsTheMomentTheStickExists(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "sdb", 1<<30, nil)
	addPartition(t, sys, "sdb", "sdb1", "liken:install", 1<<30)

	if found := awaitInstallStick(0); found.Disk != "sdb" {
		t.Errorf("an already-present stick must be found at once: %+v", found)
	}
}

func TestAwaitInstallStickGivesUpAtTheCeiling(t *testing.T) {
	fakeMachine(t)

	start := time.Now()
	found := awaitInstallStick(time.Millisecond)
	if found.Disk != "" {
		t.Errorf("no stick must resolve to none: %+v", found)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("the wait must stop at its ceiling")
	}
}

func TestReadReportDisksExcludesTheStick(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "sda", 500<<30, nil)
	writeSysfs(t, filepath.Join(sys, "sda", "device"), "model", "REAL DISK\n")
	addDisk(t, sys, dev, "sdb", 1<<30, nil)
	addPartition(t, sys, "sdb", "sdb1", "liken:install", 1<<30)

	disks := readReportDisks(resolveInstallStick())
	if len(disks) != 1 {
		t.Fatalf("disks: %v", disks)
	}
	if disks[0].Path != dev+"/sda" || disks[0].Model != "REAL DISK" {
		t.Errorf("the kept disk: %+v", disks[0])
	}
	if disks[0].SizeBytes != 500<<30 || disks[0].Name != "sda" {
		t.Errorf("the kept disk's identity: %+v", disks[0])
	}
}

func TestReadReportDisksMarksEveryDiskThatMightBeTheStick(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "sda", 500<<30, nil)
	addDisk(t, sys, dev, "sdb", 1<<30, nil)
	addPartition(t, sys, "sdb", "sdb1", "liken:install", 1<<30)
	addDisk(t, sys, dev, "sdc", 1<<30, nil)
	addPartition(t, sys, "sdc", "sdc1", "liken:install", 1<<30)

	disks := readReportDisks(resolveInstallStick())
	if len(disks) != 3 {
		t.Fatalf("an unresolved stick hides no disk: %v", disks)
	}
	marked := map[string]bool{}
	for _, d := range disks {
		marked[d.Name] = d.MaybeStick
	}
	if marked["sda"] || !marked["sdb"] || !marked["sdc"] {
		t.Errorf("only the candidates may be marked: %v", marked)
	}
}

func TestReadReportDisksSkipsDevicesThatCannotHoldARole(t *testing.T) {
	sys, dev := fakeMachine(t)
	// A DVD drive with a disc in it: real storage with a real size, and
	// no place for a partition table liken writes.
	addDisk(t, sys, dev, "sr0", 4<<30, nil)
	writeSysfs(t, filepath.Join(sys, "sr0", "device"), "type", "5\n")
	// A card reader with no card: the device exists, the medium does
	// not, so the kernel reports zero sectors.
	addDisk(t, sys, dev, "mmcblk0", 0, nil)
	// A write-protected medium takes no role either.
	addDisk(t, sys, dev, "sdz", 8<<30, nil)
	writeSysfs(t, filepath.Join(sys, "sdz"), "ro", "1\n")
	// A small SATA DOM is a legitimate boot disk, and must survive
	// every one of the checks above.
	addDisk(t, sys, dev, "sda", 8<<30, nil)
	writeSysfs(t, filepath.Join(sys, "sda", "device"), "type", "0\n")

	disks := readReportDisks(installStick{})
	if len(disks) != 1 || disks[0].Name != "sda" {
		t.Errorf("only the disk that can hold a role belongs in the report: %+v", disks)
	}
}

func TestDiskTransportReadsTheBusFromTheDeviceTree(t *testing.T) {
	sys, _ := fakeMachine(t)
	// A SATA disk's sysfs entry is a symlink whose target path passes
	// through the ata layer; the helper reads the bus from that
	// resolved path.
	target := filepath.Join(sys, "devices", "pci0000:00", "ata2", "host1", "block", "sdx")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(sys, "sdx")); err != nil {
		t.Fatal(err)
	}

	if got := diskTransport("sdx"); got != "sata" {
		t.Errorf("transport: %q", got)
	}
	if got := diskTransport("absent"); got != "" {
		t.Errorf("an absent disk has no transport: %q", got)
	}
}

// hbaTree builds a disk that hangs off one PCI function, the shape
// both the unreachable-disk check and the stick check judge: a
// /sys/block entry that resolves under the controller's directory.
func hbaTree(t *testing.T, sys, controller, disk string) (diskName, controllerDir string) {
	t.Helper()
	controllerDir = filepath.Join(sys, "devices", "pci0000:00", controller)
	target := filepath.Join(controllerDir, "host0", "target0:0:0", "block", disk)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(sys, disk)); err != nil {
		t.Fatal(err)
	}
	return disk, controllerDir
}

func TestMarkDisksBehindDriversNamesTheChainThatReachedThem(t *testing.T) {
	sys, dev := fakeMachine(t)
	name, controller := hbaTree(t, sys, "0000:03:00.0", "sda")
	elsewhere := filepath.Join(sys, "devices", "pci0000:00", "0000:00:1f.2")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	onboard, _ := hbaTree(t, sys, "0000:00:17.0", "sdb")

	disks := []reportDisk{{Name: name, Path: dev + "/" + name}, {Name: onboard, Path: dev + "/" + onboard}}
	recs := []moduleRecommendation{{
		Device: "an HBA", Class: "storage", Chain: []string{"mpt3sas"},
		SysfsDirs: []string{controller},
	}}

	marked := markDisksBehindDrivers(disks, recs)
	if len(marked[0].BehindModules) != 1 || marked[0].BehindModules[0] != "mpt3sas" {
		t.Errorf("the disk below the loaded controller must name its chain: %+v", marked[0])
	}
	if len(marked[1].BehindModules) != 0 {
		t.Errorf("a disk the boot path already reached is not behind anything: %+v", marked[1])
	}
}

// stickTree builds the shape withoutStickRecommendations judges: a
// stick disk whose /sys/block entry resolves under a USB device's
// directory, and the sysfs directory of that USB device.
func stickTree(t *testing.T, sys string) (stickName, usbDevice string) {
	t.Helper()
	usbDevice = filepath.Join(sys, "devices", "usb2", "2-1", "2-1:1.0")
	disk := filepath.Join(usbDevice, "host8", "block", "sdd")
	if err := os.MkdirAll(disk, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(disk, filepath.Join(sys, "sdd")); err != nil {
		t.Fatal(err)
	}
	return "sdd", usbDevice
}

func TestWithoutStickRecommendationsDropsOnlyTheSticksDriver(t *testing.T) {
	sys, _ := fakeMachine(t)
	stickName, usbDevice := stickTree(t, sys)
	elsewhere := filepath.Join(sys, "devices", "pci0000:00", "00:03.0")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}

	recs := []moduleRecommendation{
		{Device: "the stick", Chain: []string{"usb-storage"}, SysfsDirs: []string{usbDevice}},
		{Device: "a NIC", Chain: []string{"e1000"}, SysfsDirs: []string{elsewhere}},
	}

	kept := withoutStickRecommendations(recs, stickName)
	if len(kept) != 1 || kept[0].Device != "a NIC" {
		t.Errorf("only the stick's driver may drop: %+v", kept)
	}
}

func TestWithoutStickRecommendationsKeepsEverythingWithoutAStick(t *testing.T) {
	recs := []moduleRecommendation{
		{Device: "a NIC", Chain: []string{"e1000"}, SysfsDirs: []string{"/nowhere"}},
	}

	if kept := withoutStickRecommendations(recs, ""); len(kept) != 1 {
		t.Errorf("no stick must drop nothing: %+v", kept)
	}
}

func TestWithoutStickRecommendationsKeepsEverythingWhenTheStickIsUnreadable(t *testing.T) {
	fakeMachine(t)
	recs := []moduleRecommendation{
		{Device: "a NIC", Chain: []string{"e1000"}, SysfsDirs: []string{"/nowhere"}},
	}

	if kept := withoutStickRecommendations(recs, "ghost"); len(kept) != 1 {
		t.Errorf("an unresolvable stick must drop nothing: %+v", kept)
	}
}
