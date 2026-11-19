package main

// Tests for disk discovery against a fake machine: a sysfs tree and a
// /dev directory in temporary directories, with the files the kernel
// writes (a `device` entry, a `size` in 512-byte sectors, and a
// `uevent` of KEY=value lines).

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/liken/disks"
)

// fakeMachine points discovery at an empty fake /sys/block and /dev
// until the test ends. It sets package variables, so a test that calls
// it must not run in parallel.
func fakeMachine(t *testing.T) (sys string, dev string) {
	t.Helper()
	sys, dev = t.TempDir(), t.TempDir()
	oldSys, oldDev := sysBlock, devRoot
	sysBlock, devRoot = sys, dev
	t.Cleanup(func() { sysBlock, devRoot = oldSys, oldDev })
	return sys, dev
}

// addDisk adds a disk to the fake machine: its sysfs entries, and a
// file under the fake /dev that holds the first bytes of the disk.
func addDisk(t *testing.T, sys, dev, name string, sizeBytes uint64, contents []byte) {
	t.Helper()
	dir := filepath.Join(sys, name)
	if err := os.MkdirAll(filepath.Join(dir, "device"), 0o755); err != nil {
		t.Fatalf("creating the fake disk directory: %v", err)
	}
	writeSysfs(t, dir, "size", fmt.Sprintf("%d\n", sizeBytes/disks.SectorSize))
	if contents != nil {
		if err := os.WriteFile(filepath.Join(dev, name), contents, 0o600); err != nil {
			t.Fatalf("writing the fake disk node: %v", err)
		}
	}
}

// addPartition adds a partition to a fake disk, with a PARTNAME in its
// uevent when partName is not empty.
func addPartition(t *testing.T, sys, disk, name, partName string, sizeBytes uint64) {
	t.Helper()
	dir := filepath.Join(sys, disk, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating the fake partition directory: %v", err)
	}
	writeSysfs(t, dir, "partition", "1\n")
	writeSysfs(t, dir, "size", fmt.Sprintf("%d\n", sizeBytes/disks.SectorSize))
	uevent := "MAJOR=253\nMINOR=1\nDEVNAME=" + name + "\nDEVTYPE=disk\n"
	if partName != "" {
		uevent += "PARTNAME=" + partName + "\n"
	}
	writeSysfs(t, dir, "uevent", uevent)
}

// addPartitionNode creates the /dev node of a fake partition. On a
// real machine devtmpfs creates it after the sysfs entry appears.
func addPartitionNode(t *testing.T, dev, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dev, name), nil, 0o600); err != nil {
		t.Fatalf("writing the fake partition node: %v", err)
	}
}

func writeSysfs(t *testing.T, dir, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o644); err != nil {
		t.Fatalf("writing the sysfs file: %v", err)
	}
}

func TestDiscoverBlockDevicesReadsSysfs(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 2<<30, nil)
	// A SCSI model is on the bus device, padded with spaces.
	writeSysfs(t, filepath.Join(sys, "vda", "device"), "model", "QEMU HARDDISK   \n")
	// A virtio serial is on the disk itself.
	writeSysfs(t, filepath.Join(sys, "vda"), "serial", "liken-lab-state\n")
	// A loop device has no `device` entry, so it is not storage.
	if err := os.MkdirAll(filepath.Join(sys, "loop0"), 0o755); err != nil {
		t.Fatalf("creating the fake loop0 directory: %v", err)
	}

	disks := discoverBlockDevices()
	if len(disks) != 1 {
		t.Fatalf("discovered %d disks, want 1: %v", len(disks), disks)
	}
	d := disks[0]
	if d.Name != "vda" || d.SizeBytes != 2<<30 {
		t.Errorf("the disk is %+v, want vda with 2 GiB", d)
	}
	if d.Model != "QEMU HARDDISK" {
		t.Errorf("the model is %q, want QEMU HARDDISK without padding", d.Model)
	}
	if d.Serial != "liken-lab-state" {
		t.Errorf("the serial is %q, want liken-lab-state", d.Serial)
	}
}

// addMMCCard adds the block devices of an eMMC card. The data area's
// `device` link leads to the mmc card, and each other area's link
// leads to the data area.
func addMMCCard(t *testing.T, sys string) {
	t.Helper()
	root := t.TempDir()
	blockClass := filepath.Join(root, "class", "block")
	card := filepath.Join(root, "devices", "pci0000:00", "0000:00:1c.0",
		"mmc_host", "mmc0", "mmc0:0001")
	for _, dir := range []string{blockClass, filepath.Join(root, "bus", "mmc"), card} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("creating the fake sysfs directory: %v", err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "bus", "mmc"), filepath.Join(card, "subsystem")); err != nil {
		t.Fatalf("linking the card subsystem: %v", err)
	}
	writeSysfs(t, card, "name", "SEM64G\n")
	writeSysfs(t, card, "serial", "0x4a2b5c8e\n")

	data := addMMCArea(t, sys, blockClass, "mmcblk0", card, 58<<30)
	addMMCArea(t, sys, blockClass, "mmcblk0boot0", data, 4<<20)
	addMMCArea(t, sys, blockClass, "mmcblk0boot1", data, 4<<20)
	addMMCArea(t, sys, blockClass, "mmcblk0gp0", data, 1<<30)
	addMMCArea(t, sys, blockClass, "mmcblk0rpmb", data, 512<<10)
}

// addMMCArea adds one mmc block device with its size, a `subsystem`
// link, and a `device` link to parent. It returns the device's
// directory.
func addMMCArea(t *testing.T, sys, blockClass, name, parent string, sizeBytes uint64) string {
	t.Helper()
	dir := filepath.Join(sys, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating the fake disk directory: %v", err)
	}
	writeSysfs(t, dir, "size", fmt.Sprintf("%d\n", sizeBytes/disks.SectorSize))
	if err := os.Symlink(blockClass, filepath.Join(dir, "subsystem")); err != nil {
		t.Fatalf("linking the disk subsystem: %v", err)
	}
	if err := os.Symlink(parent, filepath.Join(dir, "device")); err != nil {
		t.Fatalf("linking the disk device: %v", err)
	}
	return dir
}

func TestDiscoverBlockDevicesSkipsMMCHardwareAreas(t *testing.T) {
	sys, _ := fakeMachine(t)
	addMMCCard(t, sys)

	found := discoverBlockDevices()
	if len(found) != 1 {
		t.Fatalf("discovered %d disks, want only the card's data area: %v", len(found), found)
	}
	if found[0].Name != "mmcblk0" || found[0].SizeBytes != 58<<30 {
		t.Errorf("the disk is %+v, want the mmcblk0 data area", found[0])
	}
}

// The mmc check must not skip disks on other buses.
func TestDiscoverBlockDevicesKeepsEveryOtherDisk(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "nvme0n1", 512<<30, nil)
	addDisk(t, sys, dev, "sda", 8<<30, nil)
	addDisk(t, sys, dev, "vda", 2<<30, nil)

	var names []string
	for _, d := range discoverBlockDevices() {
		names = append(names, d.Name)
	}
	if !equalNames(names, []string{"nvme0n1", "sda", "vda"}) {
		t.Errorf("the disks are %v, want nvme0n1, sda, and vda", names)
	}
}

func TestDiscoverBlockDevicesToleratesMalformedSizes(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 0, nil)
	writeSysfs(t, filepath.Join(sys, "vda"), "size", "not a number\n")

	disks := discoverBlockDevices()
	if len(disks) != 1 || disks[0].SizeBytes != 0 {
		t.Errorf("the disks are %v, want one disk with size 0 because the size file is malformed", disks)
	}
}

func TestDiscoverPartitionsParsesUevent(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 2<<30, nil)
	addPartition(t, sys, "vda", "vda1", "liken:clusterState", 1<<30)
	addPartition(t, sys, "vda", "vda2", "", 1<<20)
	// Only entries with a `partition` file are partitions.
	if err := os.MkdirAll(filepath.Join(sys, "vda", "queue"), 0o755); err != nil {
		t.Fatalf("creating the fake queue directory: %v", err)
	}

	parts := discoverPartitions()
	if len(parts) != 2 {
		t.Fatalf("discovered %d partitions, want 2: %v", len(parts), parts)
	}
	if parts[0].name != "vda1" || parts[0].partName != "liken:clusterState" || parts[0].sizeBytes != 1<<30 {
		t.Errorf("vda1 is %+v, want partName liken:clusterState and 1 GiB", parts[0])
	}
	if parts[1].name != "vda2" || parts[1].partName != "" {
		t.Errorf("vda2 is %+v, want an empty partName", parts[1])
	}
}

func TestReportBlockDevicesPrintsDisks(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 2<<30, nil)
	writeSysfs(t, filepath.Join(sys, "vda"), "serial", "liken-lab-state\n")
	got := capturedStdout(t, reportBlockDevices)
	if want := "liken: disk " + filepath.Join(dev, "vda") + ": 2.0 GiB, serial liken-lab-state\n"; got != want {
		t.Errorf("reportBlockDevices printed %q, want %q", got, want)
	}
}

func TestDiscoverBlockDevicesReadsSCSIModels(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "sda", 2<<30, nil)
	writeSysfs(t, filepath.Join(sys, "sda", "device"), "model", "Samsung SSD 990 \n")
	disks := discoverBlockDevices()
	if len(disks) != 1 || disks[0].Model != "Samsung SSD 990" {
		t.Errorf("the disks are %+v, want the model Samsung SSD 990 without padding", disks)
	}
	reportBlockDevices()
}

func TestDiscoverBlockDevicesTrimsNULPaddedSerials(t *testing.T) {
	// An iSCSI LUN can report its serial padded with NUL bytes.
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "sda", 8<<30, nil)
	writeSysfs(t, filepath.Join(sys, "sda", "device"), "serial",
		"64117ed8-bf4f-4164-8bd4-cd6d70d854e5\x00\n")

	found := discoverBlockDevices()
	if len(found) != 1 {
		t.Fatalf("discovered %d disks, want 1: %v", len(found), found)
	}
	if found[0].Serial != "64117ed8-bf4f-4164-8bd4-cd6d70d854e5" {
		t.Errorf("the serial is %q, want it without NUL padding", found[0].Serial)
	}
}

func TestDiscoverBlockDevicesFillsStableNames(t *testing.T) {
	sys, _ := fakeMachine(t)
	dir := fakeDisk(t, sys, "sda", "pci0000:00", "0000:00:1f.2", "ata3", "host2",
		"target2:0:0", "2:0:0:0", "block", "sda")
	writeSysfs(t, filepath.Join(dir, "device"), "wwid", "naa.5002538d40a45c88\n")

	disks := discoverBlockDevices()
	if len(disks) != 1 {
		t.Fatalf("discovered %d disks, want 1: %v", len(disks), disks)
	}
	want := []string{
		"/dev/disk/by-id/wwn-0x5002538d40a45c88",
		"/dev/disk/by-id/scsi-35002538d40a45c88",
		"/dev/disk/by-path/pci-0000:00:1f.2-ata-3",
	}
	if !equalNames(disks[0].StableNames, want) {
		t.Errorf("the stable names are %v, want %v", disks[0].StableNames, want)
	}
	reportBlockDevices()
}

func TestDiscoverBlockDevicesWithNoIdentityHasNoStableNames(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 2<<30, nil)

	disks := discoverBlockDevices()
	if len(disks) != 1 || disks[0].StableNames != nil {
		t.Errorf("the stable names are %v, want none for a disk with no identifying attributes", disks[0].StableNames)
	}
}

func TestDiscoverBlockDevicesFillsSerialFromVPD80(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "sda", 8<<30, nil)
	page := []byte{0x00, 0x80, 0x00, 0x08, 'S', '3', 'Z', '1', ' ', ' ', ' ', ' '}
	if err := os.WriteFile(filepath.Join(sys, "sda", "device", "vpd_pg80"), page, 0o644); err != nil {
		t.Fatalf("writing the vpd_pg80 page: %v", err)
	}

	disks := discoverBlockDevices()
	if len(disks) != 1 || disks[0].Serial != "S3Z1" {
		t.Errorf("the disks are %+v, want serial S3Z1 for a SATA disk with no serial attribute", disks)
	}
}

func TestDiscoverBlockDevicesWithNoSysfsReportsNothing(t *testing.T) {
	fakeMachine(t)
	sysBlock = filepath.Join(t.TempDir(), "no-sys-block")
	if disks := discoverBlockDevices(); disks != nil {
		t.Errorf("the disks are %v, want none when /sys/block cannot be read", disks)
	}
}

func TestReportBlockDevicesWithNoDisks(t *testing.T) {
	fakeMachine(t)
	if got := capturedStdout(t, reportBlockDevices); got != "liken: no disks attached\n" {
		t.Errorf("reportBlockDevices printed %q, want the line that says no disks are attached", got)
	}
}
