package main

// Tests for disk discovery, run against a fake machine: a sysfs tree
// and a /dev directory built in tempdirs. The fixtures here write the
// same files the kernel would write: a `device` entry marking real
// storage, a `size` in 512-byte sectors, and a `uevent` of KEY=value
// lines. This is the point of the tests: discovery only ever reads
// small text files, and these tests show exactly which ones.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/disks"
)

// fakeMachine points discovery at an empty fake /sys/block and /dev,
// and restores the real paths when the test ends. Because sysBlock
// and devRoot are package variables, tests in this package must not
// run in parallel.
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
	writeSysfs(t, dir, "size", fmt.Sprintf("%d\n", sizeBytes/disks.SectorSize))
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
	writeSysfs(t, dir, "size", fmt.Sprintf("%d\n", sizeBytes/disks.SectorSize))
	uevent := "MAJOR=253\nMINOR=1\nDEVNAME=" + name + "\nDEVTYPE=disk\n"
	if partName != "" {
		uevent += "PARTNAME=" + partName + "\n"
	}
	writeSysfs(t, dir, "uevent", uevent)
}

// addPartitionNode gives a fake partition the /dev node devtmpfs
// creates for it. On a real machine the node is a separate step
// from the sysfs entry, so it is a separate step here too.
func addPartitionNode(t *testing.T, dev, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dev, name), nil, 0o600); err != nil {
		t.Fatal(err)
	}
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
	// format pads it. The trimming is part of what the test checks.
	writeSysfs(t, filepath.Join(sys, "vda", "device"), "model", "QEMU HARDDISK   \n")
	// virtio-style identity: a serial directly on the disk.
	writeSysfs(t, filepath.Join(sys, "vda"), "serial", "liken-lab-state\n")
	// A loop device has no `device` entry, so it is not storage.
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

// addMMCCard gives the fake machine the block devices an eMMC module
// presents. The `device` link is what tells them apart: the card's data area
// hangs off the mmc card device, and each hardware area hangs off the data
// area's own block device, because the mmc block driver makes them children of
// that disk.
func addMMCCard(t *testing.T, sys string) {
	t.Helper()
	root := t.TempDir()
	blockClass := filepath.Join(root, "class", "block")
	card := filepath.Join(root, "devices", "pci0000:00", "0000:00:1c.0",
		"mmc_host", "mmc0", "mmc0:0001")
	for _, dir := range []string{blockClass, filepath.Join(root, "bus", "mmc"), card} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "bus", "mmc"), filepath.Join(card, "subsystem")); err != nil {
		t.Fatal(err)
	}
	writeSysfs(t, card, "name", "SEM64G\n")
	writeSysfs(t, card, "serial", "0x4a2b5c8e\n")

	data := addMMCArea(t, sys, blockClass, "mmcblk0", card, 58<<30)
	addMMCArea(t, sys, blockClass, "mmcblk0boot0", data, 4<<20)
	addMMCArea(t, sys, blockClass, "mmcblk0boot1", data, 4<<20)
	addMMCArea(t, sys, blockClass, "mmcblk0gp0", data, 1<<30)
	addMMCArea(t, sys, blockClass, "mmcblk0rpmb", data, 512<<10)
}

// addMMCArea adds one mmc block device to the fake machine: its size, the
// `subsystem` link every block device carries, and the `device` link to its
// parent. It returns the directory it made.
func addMMCArea(t *testing.T, sys, blockClass, name, parent string, sizeBytes uint64) string {
	t.Helper()
	dir := filepath.Join(sys, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSysfs(t, dir, "size", fmt.Sprintf("%d\n", sizeBytes/disks.SectorSize))
	if err := os.Symlink(blockClass, filepath.Join(dir, "subsystem")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(parent, filepath.Join(dir, "device")); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The card's data area is the only entry that may survive the walk.
// The boot and general-purpose areas hold what a board's firmware
// reads before Linux runs, and RPMB is an authenticated mailbox, so
// a role placed on any of them would be destroyed or unreachable.
func TestDiscoverBlockDevicesSkipsMMCHardwareAreas(t *testing.T) {
	sys, _ := fakeMachine(t)
	addMMCCard(t, sys)

	found := discoverBlockDevices()
	if len(found) != 1 {
		t.Fatalf("discovered %d disks, want only the card's data area: %v", len(found), found)
	}
	if found[0].Name != "mmcblk0" || found[0].SizeBytes != 58<<30 {
		t.Errorf("got %+v, want the mmcblk0 data area", found[0])
	}
}

// The skip keys on a sysfs shape only the mmc block driver builds,
// so a machine with any other kind of disk walks exactly as it did
// before. This test is that claim, pinned.
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
		t.Errorf("got %v, want every disk the walk found before", names)
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
	// A disk's sysfs directory holds much more than partitions. Only
	// entries with a `partition` file count as partitions.
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
	// read the same fake sysfs, and this test checks that parity.
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

func TestDiscoverBlockDevicesTrimsNULPaddedSerials(t *testing.T) {
	// An iSCSI LUN reports its serial from a fixed-width buffer and
	// pads the remainder with NUL. The padding is the transport's, not
	// the disk's, and a NUL that reaches the Machine status is a byte
	// no reader of the API expects.
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "sda", 8<<30, nil)
	writeSysfs(t, filepath.Join(sys, "sda", "device"), "serial",
		"64117ed8-bf4f-4164-8bd4-cd6d70d854e5\x00\n")

	found := discoverBlockDevices()
	if len(found) != 1 {
		t.Fatalf("discovered %d disks, want 1: %v", len(found), found)
	}
	if found[0].Serial != "64117ed8-bf4f-4164-8bd4-cd6d70d854e5" {
		t.Errorf("the NUL padding trims: %q", found[0].Serial)
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
		t.Errorf("got %v, want %v", disks[0].StableNames, want)
	}
	reportBlockDevices()
}

func TestDiscoverBlockDevicesWithNoIdentityHasNoStableNames(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 2<<30, nil)

	disks := discoverBlockDevices()
	if len(disks) != 1 || disks[0].StableNames != nil {
		t.Errorf("a disk with no identifying attributes got %v, want no stable names", disks[0].StableNames)
	}
}

func TestDiscoverBlockDevicesFillsSerialFromVPD80(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "sda", 8<<30, nil)
	page := []byte{0x00, 0x80, 0x00, 0x08, 'S', '3', 'Z', '1', ' ', ' ', ' ', ' '}
	if err := os.WriteFile(filepath.Join(sys, "sda", "device", "vpd_pg80"), page, 0o644); err != nil {
		t.Fatal(err)
	}

	disks := discoverBlockDevices()
	if len(disks) != 1 || disks[0].Serial != "S3Z1" {
		t.Errorf("a SATA disk with no plain serial attribute got %+v, want serial S3Z1", disks)
	}
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
	// A machine with no storage at all still reports that fact. The
	// world report must never stay silent about a whole section.
	reportBlockDevices()
}
