package main

// Tests for by-path name computation, run against the same fake sysfs
// fakeDisk builds for the by-id tests in diskids_test.go: a device
// tree under a fake /sys/devices, and a symlink from the fake
// /sys/block that stands in for the one the kernel makes.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiskPathNameSATA(t *testing.T) {
	sys, _ := fakeMachine(t)
	fakeDisk(t, sys, "sda", "pci0000:00", "0000:00:1f.2", "ata3", "host2",
		"target2:0:0", "2:0:0:0", "block", "sda")

	if got, want := diskPathName("sda"), "pci-0000:00:1f.2-ata-3"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestDiskPathNameNVME covers a kernel old enough to publish no nsid
// attribute: diskPathName falls back to the instance number parsed off
// the block name, which for a controller's first namespace equals the
// nsid anyway.
func TestDiskPathNameNVME(t *testing.T) {
	sys, _ := fakeMachine(t)
	fakeDisk(t, sys, "nvme0n1", "pci0000:00", "0000:00:1c.0", "0000:03:00.0",
		"nvme", "nvme0", "nvme0n1")

	if got, want := diskPathName("nvme0n1"), "pci-0000:03:00.0-nvme-1"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestDiskPathNameNVMEReadsNsidAttribute covers a controller whose
// namespaces are not contiguous from 1: the block name's trailing
// digit counts probe order, not the namespace's own number, so a disk
// named nvme0n2 can still be namespace 5.
func TestDiskPathNameNVMEReadsNsidAttribute(t *testing.T) {
	sys, _ := fakeMachine(t)
	fakeDisk(t, sys, "nvme0n2", "pci0000:00", "0000:00:1c.0", "0000:03:00.0",
		"nvme", "nvme0", "nvme0n2")
	writeSysfs(t, filepath.Join(sys, "nvme0n2"), "nsid", "5\n")

	if got, want := diskPathName("nvme0n2"), "pci-0000:03:00.0-nvme-5"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDiskPathNameVirtio(t *testing.T) {
	sys, _ := fakeMachine(t)
	fakeDisk(t, sys, "vda", "pci0000:00", "0000:00:0a.0", "virtio2", "block", "vda")

	if got, want := diskPathName("vda"), "pci-0000:00:0a.0"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDiskPathNameUSB(t *testing.T) {
	sys, _ := fakeMachine(t)
	fakeDisk(t, sys, "sdb", "pci0000:00", "0000:00:14.0", "usb1", "1-4", "1-4:1.0",
		"host0", "target0:0:0", "0:0:0:0", "block", "sdb")

	want := "pci-0000:00:14.0-usb-0:4:1.0-scsi-0:0:0:0"
	if got := diskPathName("sdb"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// fakeMMCDisk builds the sysfs tree an mmc card presents: a host
// controller on the named bus, the mmc_host and card directories the
// mmc core creates under it, and the card's block device at the bottom.
// The `subsystem` link on the host is what tells a platform host from a
// PCI one, and it is the fact the by-path name turns on.
func fakeMMCDisk(t *testing.T, sys, name, bus string, hostSegments ...string) {
	t.Helper()
	root := t.TempDir()
	host := filepath.Join(append([]string{root, "devices"}, hostSegments...)...)
	busDir := filepath.Join(root, "bus", bus)
	if err := os.MkdirAll(busDir, 0o755); err != nil {
		t.Fatal(err)
	}
	block := filepath.Join(host, "mmc_host", "mmc0", "mmc0:0001", "block", name)
	if err := os.MkdirAll(filepath.Join(block, "device"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(busDir, filepath.Join(host, "subsystem")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(block, filepath.Join(sys, name)); err != nil {
		t.Fatal(err)
	}
}

// An eMMC on a host the firmware enumerates through ACPI or a
// device tree: udev names such a disk after the platform device,
// and it is the only mmc disk udev names at all.
func TestDiskPathNameMMCOnAPlatformHost(t *testing.T) {
	sys, _ := fakeMachine(t)
	fakeMMCDisk(t, sys, "mmcblk0", "platform", "platform", "80860F14:00")

	if got, want := diskPathName("mmcblk0"), "platform-80860F14:00"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A card reader whose driver registers a platform device under a
// PCI function: udev prepends the PCI slot to the platform name.
func TestDiskPathNameMMCOnAPlatformHostBehindPCI(t *testing.T) {
	sys, _ := fakeMachine(t)
	fakeMMCDisk(t, sys, "mmcblk0", "platform", "pci0000:00", "0000:3f:00.0", "rtsx_pci_sdmmc.0")

	want := "pci-0000:3f:00.0-platform-rtsx_pci_sdmmc.0"
	if got := diskPathName("mmcblk0"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A card on a bare sdhci-pci host has no by-path name. udev's
// path_id has no mmc handler, and a PCI slot alone is a parent and
// not a transport, so path_id publishes no ID_PATH and udev builds
// no by-path link. liken must publish no name where udev publishes
// none.
func TestDiskPathNameMMCOnAPCIHostHasNone(t *testing.T) {
	sys, _ := fakeMachine(t)
	fakeMMCDisk(t, sys, "mmcblk0", "pci", "pci0000:00", "0000:00:1c.0")

	if got := diskPathName("mmcblk0"); got != "" {
		t.Errorf("got %q, want no by-path name", got)
	}
}

func TestDiskPathNameISCSIHasNone(t *testing.T) {
	sys, _ := fakeMachine(t)
	fakeDisk(t, sys, "sdc", "platform", "host6", "session2", "target6:0:0",
		"6:0:0:0", "block", "sdc")

	if got := diskPathName("sdc"); got != "" {
		t.Errorf("an iSCSI disk got %q, want no local by-path name", got)
	}
}

func TestDiskPathNameMissingDiskHasNone(t *testing.T) {
	fakeMachine(t)

	if got := diskPathName("nonexistent"); got != "" {
		t.Errorf("a disk with no sysfs entry got %q, want empty", got)
	}
}

// fakeDiskWithBrokenSymlink covers the disk whose /sys/block entry
// points nowhere, the state a device leaves behind for the instant
// between its uevent and the kernel finishing the symlink.
func TestDiskPathNameBrokenSymlinkHasNone(t *testing.T) {
	sys, _ := fakeMachine(t)
	if err := os.Symlink(filepath.Join(sys, "nowhere"), filepath.Join(sys, "sdd")); err != nil {
		t.Fatal(err)
	}

	if got := diskPathName("sdd"); got != "" {
		t.Errorf("a disk with a broken symlink got %q, want empty", got)
	}
}
