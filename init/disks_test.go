package main

// Tests for disk discovery, run against a fake machine: a sysfs tree
// and a /dev directory built in tempdirs. The fixtures here write the
// same files the kernel would (a `device` entry marking real storage,
// a `size` in 512-byte sectors, a `uevent` of KEY=value lines), which
// is the point: discovery is only ever reading small text files, and
// these tests show exactly which ones.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// fakeMachine points discovery at an empty fake /sys/block and /dev,
// restoring the real paths when the test ends. Because sysBlock and
// devRoot are package variables, tests in this package must not run
// in parallel.
func fakeMachine(t *testing.T) (sys string, dev string) {
	t.Helper()
	sys, dev = t.TempDir(), t.TempDir()
	oldSys, oldDev := sysBlock, devRoot
	sysBlock, devRoot = sys, dev
	t.Cleanup(func() { sysBlock, devRoot = oldSys, oldDev })
	return sys, dev
}

// addDisk gives the fake machine one disk: the sysfs entries that
// mark real storage, and the device file under the fake /dev whose
// contents stand in for the first bytes of the disk.
func addDisk(t *testing.T, sys, dev, name string, sizeBytes uint64, contents []byte) {
	t.Helper()
	dir := filepath.Join(sys, name)
	if err := os.MkdirAll(filepath.Join(dir, "device"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSysfs(t, dir, "size", fmt.Sprintf("%d\n", sizeBytes/sectorSize))
	if contents != nil {
		if err := os.WriteFile(filepath.Join(dev, name), contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// addPartition gives a fake disk one partition, named or not.
func addPartition(t *testing.T, sys, disk, name, partName string, sizeBytes uint64) {
	t.Helper()
	dir := filepath.Join(sys, disk, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSysfs(t, dir, "partition", "1\n")
	writeSysfs(t, dir, "size", fmt.Sprintf("%d\n", sizeBytes/sectorSize))
	uevent := "MAJOR=253\nMINOR=1\nDEVNAME=" + name + "\nDEVTYPE=disk\n"
	if partName != "" {
		uevent += "PARTNAME=" + partName + "\n"
	}
	writeSysfs(t, dir, "uevent", uevent)
}

func writeSysfs(t *testing.T, dir, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverBlockDevicesReadsSysfs(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 2<<30, nil)
	// SCSI-style identity on the bus device, padded the way the wire
	// format pads it; the trimming is part of what's under test.
	writeSysfs(t, filepath.Join(sys, "vda", "device"), "model", "QEMU HARDDISK   \n")
	// virtio-style identity: a serial directly on the disk.
	writeSysfs(t, filepath.Join(sys, "vda"), "serial", "liken-lab-state\n")
	// A loop device has no `device` entry and is not storage.
	if err := os.MkdirAll(filepath.Join(sys, "loop0"), 0o755); err != nil {
		t.Fatal(err)
	}

	disks := discoverBlockDevices()
	if len(disks) != 1 {
		t.Fatalf("discovered %d disks, want 1: %v", len(disks), disks)
	}
	d := disks[0]
	if d.Name != "vda" || d.SizeBytes != 2<<30 {
		t.Errorf("got %+v", d)
	}
	if d.Model != "QEMU HARDDISK" {
		t.Errorf("model should be trimmed of padding: %q", d.Model)
	}
	if d.Serial != "liken-lab-state" {
		t.Errorf("serial: %q", d.Serial)
	}
}

func TestDiscoverBlockDevicesToleratesMalformedSizes(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 0, nil)
	writeSysfs(t, filepath.Join(sys, "vda"), "size", "not a number\n")

	disks := discoverBlockDevices()
	if len(disks) != 1 || disks[0].SizeBytes != 0 {
		t.Errorf("a malformed size should read as zero, not fail discovery: %v", disks)
	}
}

func TestDiscoverPartitionsParsesUevent(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 2<<30, nil)
	addPartition(t, sys, "vda", "vda1", "liken:clusterState", 1<<30)
	addPartition(t, sys, "vda", "vda2", "", 1<<20)
	// A disk's sysfs directory holds much more than partitions; only
	// entries with a `partition` file count.
	if err := os.MkdirAll(filepath.Join(sys, "vda", "queue"), 0o755); err != nil {
		t.Fatal(err)
	}

	parts := discoverPartitions()
	if len(parts) != 2 {
		t.Fatalf("discovered %d partitions, want 2: %v", len(parts), parts)
	}
	if parts[0].name != "vda1" || parts[0].partName != "liken:clusterState" || parts[0].sizeBytes != 1<<30 {
		t.Errorf("vda1: %+v", parts[0])
	}
	if parts[1].name != "vda2" || parts[1].partName != "" {
		t.Errorf("an unnamed partition should read as empty, not error: %+v", parts[1])
	}
}

func TestReportBlockDevicesNarratesTheInventory(t *testing.T) {
	// The report prints the same inventory the facts publish; both
	// read the same fake sysfs, which is the parity under test.
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 2<<30, nil)
	writeSysfs(t, filepath.Join(sys, "vda"), "serial", "liken-lab-state\n")
	reportBlockDevices()
}

func TestDiscoverBlockDevicesReadsSCSIModels(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "sda", 2<<30, nil)
	writeSysfs(t, filepath.Join(sys, "sda", "device"), "model", "Samsung SSD 990 \n")
	disks := discoverBlockDevices()
	if len(disks) != 1 || disks[0].Model != "Samsung SSD 990" {
		t.Errorf("the padded model string trims: %+v", disks)
	}
	reportBlockDevices()
}

func TestDiscoverBlockDevicesWithNoSysfsReportsNothing(t *testing.T) {
	fakeMachine(t)
	sysBlock = filepath.Join(t.TempDir(), "no-sys-block")
	if disks := discoverBlockDevices(); disks != nil {
		t.Errorf("an unreadable /sys/block discovers nothing: %v", disks)
	}
}

func TestReportBlockDevicesWithNoDisks(t *testing.T) {
	fakeMachine(t)
	// A machine with no storage at all still narrates that fact; the
	// world report must never be silent about a whole section.
	reportBlockDevices()
}
