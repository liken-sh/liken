package main

// Tests for by-id name computation, run against a fake sysfs: a
// device tree under a fake /sys/devices, and a symlink from the fake
// /sys/block that stands in for the one the kernel makes. The
// transport tests only need the resolved path to pass through a
// segment naming the bus (ataN, nvme, usbN, virtioN), because that is
// all diskTransport reads, so the fixture keeps the rest of the path
// short.

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeDisk builds a device directory under a fake /sys/devices and
// symlinks the fake /sys/block/<name> to it, the way the kernel links
// a real disk's block entry to its place in the device tree. It
// returns the device directory, so a test can write the sysfs
// attributes the disk publishes there.
func fakeDisk(t *testing.T, sys, name string, pathSegments ...string) string {
	t.Helper()
	devicesRoot := filepath.Join(t.TempDir(), "devices")
	deviceDir := filepath.Join(append([]string{devicesRoot}, pathSegments...)...)
	if err := os.MkdirAll(filepath.Join(deviceDir, "device"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(deviceDir, filepath.Join(sys, name)); err != nil {
		t.Fatal(err)
	}
	return deviceDir
}

func TestDiskIDNamesSATADiskWithWWN(t *testing.T) {
	sys, _ := fakeMachine(t)
	dir := fakeDisk(t, sys, "sda", "pci0000:00", "0000:00:1f.2", "ata3", "host2",
		"target2:0:0", "2:0:0:0", "block", "sda")
	writeSysfs(t, filepath.Join(dir, "device"), "wwid", "naa.5002538d40a45c88\n")

	names := diskIDNames("sda")
	want := []string{"wwn-0x5002538d40a45c88", "scsi-35002538d40a45c88"}
	if !equalNames(names, want) {
		t.Errorf("got %v, want %v", names, want)
	}
}

func TestDiskIDNamesNVME(t *testing.T) {
	sys, _ := fakeMachine(t)
	dir := fakeDisk(t, sys, "nvme0n1", "pci0000:00", "0000:00:1c.0", "0000:01:00.0",
		"nvme", "nvme0", "nvme0n1")
	writeSysfs(t, dir, "wwid", "eui.0025385b21406566\n")
	writeSysfs(t, filepath.Join(dir, "device"), "model", "Samsung SSD 980 500GB\n")
	writeSysfs(t, filepath.Join(dir, "device"), "serial", "S5GXNX0T123456\n")

	names := diskIDNames("nvme0n1")
	want := []string{"nvme-Samsung_SSD_980_500GB_S5GXNX0T123456", "nvme-eui.0025385b21406566"}
	if !equalNames(names, want) {
		t.Errorf("got %v, want %v", names, want)
	}
}

func TestDiskIDNamesVirtioSerial(t *testing.T) {
	sys, _ := fakeMachine(t)
	dir := fakeDisk(t, sys, "vda", "pci0000:00", "0000:00:05.0", "virtio2", "block", "vda")
	writeSysfs(t, dir, "serial", "liken-a\n")

	names := diskIDNames("vda")
	want := []string{"virtio-liken-a"}
	if !equalNames(names, want) {
		t.Errorf("got %v, want %v", names, want)
	}
}

func TestDiskIDNamesUSB(t *testing.T) {
	sys, _ := fakeMachine(t)
	// The USB device sits above the interface, the host, the target,
	// and the LUN in the device tree. Its own directory, not any of
	// theirs, carries manufacturer, product, and serial.
	usbDevice := filepath.Join(t.TempDir(), "devices", "pci0000:00", "0000:00:14.0", "usb1", "1-4")
	if err := os.MkdirAll(usbDevice, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSysfs(t, usbDevice, "idVendor", "0781\n")
	writeSysfs(t, usbDevice, "manufacturer", "SanDisk\n")
	writeSysfs(t, usbDevice, "product", "Cruzer Blade\n")
	writeSysfs(t, usbDevice, "serial", "4C530001\n")

	dir := filepath.Join(usbDevice, "1-4:1.0", "host6", "target6:0:0", "6:0:0:0", "block", "sdb")
	if err := os.MkdirAll(filepath.Join(dir, "device"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, filepath.Join(sys, "sdb")); err != nil {
		t.Fatal(err)
	}

	names := diskIDNames("sdb")
	want := []string{"usb-SanDisk_Cruzer_Blade_4C530001-0:0"}
	if !equalNames(names, want) {
		t.Errorf("got %v, want %v", names, want)
	}
}

func TestDiskIDNamesSATAWithoutWWNHasNone(t *testing.T) {
	sys, _ := fakeMachine(t)
	fakeDisk(t, sys, "sda", "pci0000:00", "0000:00:1f.2", "ata3", "host2",
		"target2:0:0", "2:0:0:0", "block", "sda")

	if names := diskIDNames("sda"); names != nil {
		t.Errorf("a SATA disk with no WWN got %v, want no by-id names", names)
	}
}

func TestScsiVPD80Serial(t *testing.T) {
	dir := t.TempDir()
	page := []byte{0x00, 0x80, 0x00, 0x08, 'S', '3', 'Z', '1', ' ', ' ', ' ', ' '}
	if err := os.MkdirAll(filepath.Join(dir, "device"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "device", "vpd_pg80"), page, 0o644); err != nil {
		t.Fatal(err)
	}

	if got := scsiVPD80Serial(dir); got != "S3Z1" {
		t.Errorf("got %q, want S3Z1", got)
	}
}

func TestScsiVPD80SerialAbsent(t *testing.T) {
	if got := scsiVPD80Serial(t.TempDir()); got != "" {
		t.Errorf("a disk with no vpd_pg80 page reported %q, want empty", got)
	}
}

func TestSanitizeIDPart(t *testing.T) {
	if got := sanitizeIDPart("WDC WD40EFRX-68N"); got != "WDC_WD40EFRX-68N" {
		t.Errorf("got %q, want WDC_WD40EFRX-68N", got)
	}
}

func TestSanitizeIDPartReplacesSlashAndNUL(t *testing.T) {
	if got := sanitizeIDPart("abc/def\x00ghi"); got != "abc_def_ghi" {
		t.Errorf("got %q, want abc_def_ghi", got)
	}
}

// equalNames compares two name lists in order, the same order
// diskIDNames promises its callers: wwn, scsi, then the names
// specific to the disk's transport.
func equalNames(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
