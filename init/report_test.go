package main

// Tests for the report boot's disk-side helpers: finding the
// installation stick, keeping it out of the disk walk, and telling a
// recommendation that serves the machine from one that serves only
// the stick. The helpers read the same fake /sys/block and /dev that
// the storage tests build (disks_test.go), because the report
// recognizes the stick exactly the way storage recognizes a role: by
// the name written into the GPT.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStickDiskFindsTheStickByPartitionName(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "sda", 500<<30, nil)
	addPartition(t, sys, "sda", "sda1", "liken:systemA", 1<<30)
	addDisk(t, sys, dev, "sdb", 1<<30, nil)
	addPartition(t, sys, "sdb", "sdb1", "liken:install", 1<<30)

	name, path := stickDisk()
	if name != "sdb" {
		t.Errorf("stick disk: %q", name)
	}
	if path != dev+"/sdb" {
		t.Errorf("stick path: %q", path)
	}
}

func TestStickDiskRefusesAnAmbiguousStick(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "sdb", 1<<30, nil)
	addPartition(t, sys, "sdb", "sdb1", "liken:install", 1<<30)
	addDisk(t, sys, dev, "sdc", 1<<30, nil)
	addPartition(t, sys, "sdc", "sdc1", "liken:install", 1<<30)

	name, path := stickDisk()
	if name != "" || path != "" {
		t.Errorf("two sticks must exclude nothing: %q %q", name, path)
	}
}

func TestStickDiskWithNoStick(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "sda", 500<<30, nil)

	name, path := stickDisk()
	if name != "" || path != "" {
		t.Errorf("no stick must exclude nothing: %q %q", name, path)
	}
}

func TestAwaitInstallStickEndsTheMomentTheStickExists(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "sdb", 1<<30, nil)
	addPartition(t, sys, "sdb", "sdb1", "liken:install", 1<<30)

	name, _ := awaitInstallStick(0)
	if name != "sdb" {
		t.Errorf("an already-present stick must be found at once: %q", name)
	}
}

func TestAwaitInstallStickGivesUpAtTheCeiling(t *testing.T) {
	fakeMachine(t)

	start := time.Now()
	name, _ := awaitInstallStick(time.Millisecond)
	if name != "" {
		t.Errorf("no stick must resolve to none: %q", name)
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

	disks := readReportDisks("sdb")
	if len(disks) != 1 {
		t.Fatalf("disks: %v", disks)
	}
	if disks[0].Path != dev+"/sda" || disks[0].Model != "REAL DISK" {
		t.Errorf("the kept disk: %+v", disks[0])
	}
	if disks[0].SizeBytes != 500<<30 {
		t.Errorf("the kept disk's size: %d", disks[0].SizeBytes)
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
	stick, usbDevice := stickTree(t, sys)
	elsewhere := filepath.Join(sys, "devices", "pci0000:00", "00:03.0")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}

	recs := []moduleRecommendation{
		{Device: "the stick", Chain: []string{"usb-storage"}, SysfsDirs: []string{usbDevice}},
		{Device: "a NIC", Chain: []string{"e1000"}, SysfsDirs: []string{elsewhere}},
	}

	kept := withoutStickRecommendations(recs, stick)
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
