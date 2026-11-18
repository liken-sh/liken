package main

// These tests use fakeDisk from diskids_test.go, which builds a device
// tree under a fake /sys/devices and a symlink to it in a fake
// /sys/block.

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

// TestDiskPathNameNVME covers a kernel with no nsid attribute, where
// the name takes the namespace number from the block name.
func TestDiskPathNameNVME(t *testing.T) {
	sys, _ := fakeMachine(t)
	fakeDisk(t, sys, "nvme0n1", "pci0000:00", "0000:00:1c.0", "0000:03:00.0",
		"nvme", "nvme0", "nvme0n1")

	if got, want := diskPathName("nvme0n1"), "pci-0000:03:00.0-nvme-1"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestDiskPathNameNVMEReadsNsidAttribute covers a controller whose
// namespaces have gaps, so the disk nvme0n2 is namespace 5.
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

// fakeMMCDisk builds the sysfs tree of an mmc card: a host controller
// whose subsystem link points to the named bus, the mmc_host and card
// directories below it, and the card's block device.
func fakeMMCDisk(t *testing.T, sys, name, bus string, hostSegments ...string) {
	t.Helper()
	root := t.TempDir()
	host := filepath.Join(append([]string{root, "devices"}, hostSegments...)...)
	busDir := filepath.Join(root, "bus", bus)
	if err := os.MkdirAll(busDir, 0o755); err != nil {
		t.Fatalf("creating the bus directory: %v", err)
	}
	block := filepath.Join(host, "mmc_host", "mmc0", "mmc0:0001", "block", name)
	if err := os.MkdirAll(filepath.Join(block, "device"), 0o755); err != nil {
		t.Fatalf("creating the card's block device: %v", err)
	}
	if err := os.Symlink(busDir, filepath.Join(host, "subsystem")); err != nil {
		t.Fatalf("linking the host controller to its bus: %v", err)
	}
	if err := os.Symlink(block, filepath.Join(sys, name)); err != nil {
		t.Fatalf("linking /sys/block/%s: %v", name, err)
	}
}

// An eMMC on a platform host controller, which ACPI or a device tree
// describes.
func TestDiskPathNameMMCOnAPlatformHost(t *testing.T) {
	sys, _ := fakeMachine(t)
	fakeMMCDisk(t, sys, "mmcblk0", "platform", "platform", "80860F14:00")

	if got, want := diskPathName("mmcblk0"), "platform-80860F14:00"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A card reader whose driver registers a platform device below a PCI
// function.
func TestDiskPathNameMMCOnAPlatformHostBehindPCI(t *testing.T) {
	sys, _ := fakeMachine(t)
	fakeMMCDisk(t, sys, "mmcblk0", "platform", "pci0000:00", "0000:3f:00.0", "rtsx_pci_sdmmc.0")

	want := "pci-0000:3f:00.0-platform-rtsx_pci_sdmmc.0"
	if got := diskPathName("mmcblk0"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A card on an sdhci-pci host controller has no by-path name, because
// udev gives it none.
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

// TestDiskPathNameBrokenSymlinkHasNone covers a /sys/block entry whose
// link target does not exist.
func TestDiskPathNameBrokenSymlinkHasNone(t *testing.T) {
	sys, _ := fakeMachine(t)
	if err := os.Symlink(filepath.Join(sys, "nowhere"), filepath.Join(sys, "sdd")); err != nil {
		t.Fatalf("linking /sys/block/sdd to a missing target: %v", err)
	}

	if got := diskPathName("sdd"); got != "" {
		t.Errorf("a disk with a broken symlink got %q, want empty", got)
	}
}
