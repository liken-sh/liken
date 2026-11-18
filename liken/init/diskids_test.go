package main

// diskTransport looks only for a bus directory such as ata3 or nvme
// in the resolved path, so the fake device paths leave out the rest.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeDisk creates a device directory and links sys/<name> to it, as
// the kernel links /sys/block. It returns the device directory.
func fakeDisk(t *testing.T, sys, name string, pathSegments ...string) string {
	t.Helper()
	devicesRoot := filepath.Join(t.TempDir(), "devices")
	deviceDir := filepath.Join(append([]string{devicesRoot}, pathSegments...)...)
	if err := os.MkdirAll(filepath.Join(deviceDir, "device"), 0o755); err != nil {
		t.Fatalf("creating the device directory: %v", err)
	}
	if err := os.Symlink(deviceDir, filepath.Join(sys, name)); err != nil {
		t.Fatalf("linking the disk into sys: %v", err)
	}
	return deviceDir
}

// writeSysfsBytes writes a binary attribute such as a VPD page.
func writeSysfsBytes(t *testing.T, dir, name string, value []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), value, 0o644); err != nil {
		t.Fatalf("writing the sysfs attribute %s: %v", name, err)
	}
}

func TestDiskIDNamesSATADiskWithWWN(t *testing.T) {
	sys, _ := fakeMachine(t)
	dir := fakeDisk(t, sys, "sda", "pci0000:00", "0000:00:1f.2", "ata3", "host2",
		"target2:0:0", "2:0:0:0", "block", "sda")
	writeSysfs(t, filepath.Join(dir, "device"), "wwid", "naa.5002538d40a45c88\n")

	names := diskIDNames("sda")
	want := []string{"wwn-0x5002538d40a45c88", "scsi-35002538d40a45c88"}
	if !equalNames(names, want) {
		t.Errorf("the names are %v, want %v", names, want)
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
		t.Errorf("the names are %v, want %v", names, want)
	}
}

func TestDiskIDNamesVirtioSerial(t *testing.T) {
	sys, _ := fakeMachine(t)
	dir := fakeDisk(t, sys, "vda", "pci0000:00", "0000:00:05.0", "virtio2", "block", "vda")
	writeSysfs(t, dir, "serial", "liken-a\n")

	names := diskIDNames("vda")
	want := []string{"virtio-liken-a"}
	if !equalNames(names, want) {
		t.Errorf("the names are %v, want %v", names, want)
	}
}

func TestDiskIDNamesUSB(t *testing.T) {
	sys, _ := fakeMachine(t)
	usbDevice := filepath.Join(t.TempDir(), "devices", "pci0000:00", "0000:00:14.0", "usb1", "1-4")
	if err := os.MkdirAll(usbDevice, 0o755); err != nil {
		t.Fatalf("creating the USB device directory: %v", err)
	}
	writeSysfs(t, usbDevice, "idVendor", "0781\n")
	writeSysfs(t, usbDevice, "manufacturer", "SanDisk\n")
	writeSysfs(t, usbDevice, "product", "Cruzer Blade\n")
	writeSysfs(t, usbDevice, "serial", "4C530001\n")

	dir := filepath.Join(usbDevice, "1-4:1.0", "host6", "target6:0:0", "6:0:0:0", "block", "sdb")
	if err := os.MkdirAll(filepath.Join(dir, "device"), 0o755); err != nil {
		t.Fatalf("creating the disk directory: %v", err)
	}
	if err := os.Symlink(dir, filepath.Join(sys, "sdb")); err != nil {
		t.Fatalf("linking sdb into sys: %v", err)
	}

	names := diskIDNames("sdb")
	want := []string{"usb-SanDisk_Cruzer_Blade_4C530001-0:0"}
	if !equalNames(names, want) {
		t.Errorf("the names are %v, want %v", names, want)
	}
}

// A card without a name or a serial gets no by-id name, because a
// partial name would not match udev's.
func TestDiskIDNamesMMC(t *testing.T) {
	cases := []struct {
		name   string
		card   string
		serial string
		want   []string
	}{
		{"name and serial", "SEM64G", "0x4a2b5c8e", []string{"mmc-SEM64G_0x4a2b5c8e"}},
		{"spaces in the card name", "SD 32G", "0x00112233", []string{"mmc-SD_32G_0x00112233"}},
		{"no serial", "SEM64G", "", nil},
		{"no name", "", "0x4a2b5c8e", nil},
		{"neither", "", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sys, _ := fakeMachine(t)
			dir := fakeDisk(t, sys, "mmcblk0", "pci0000:00", "0000:00:1c.0",
				"mmc_host", "mmc0", "mmc0:0001", "block", "mmcblk0")
			writeSysfs(t, filepath.Join(dir, "device"), "name", tc.card+"\n")
			writeSysfs(t, filepath.Join(dir, "device"), "serial", tc.serial+"\n")

			if names := diskIDNames("mmcblk0"); !equalNames(names, tc.want) {
				t.Errorf("the names are %v, want %v", names, tc.want)
			}
		})
	}
}

func TestDiskIDNamesSATAWithoutWWNHasNone(t *testing.T) {
	sys, _ := fakeMachine(t)
	fakeDisk(t, sys, "sda", "pci0000:00", "0000:00:1f.2", "ata3", "host2",
		"target2:0:0", "2:0:0:0", "block", "sda")

	if names := diskIDNames("sda"); names != nil {
		t.Errorf("a SATA disk with no WWN has the names %v, want none", names)
	}
}

func TestDiskIDNamesSATAWithATAIdentity(t *testing.T) {
	sys, _ := fakeMachine(t)
	dir := fakeDisk(t, sys, "sda", "pci0000:00", "0000:00:1f.2", "ata3", "host2",
		"target2:0:0", "2:0:0:0", "block", "sda")
	writeSysfsBytes(t, filepath.Join(dir, "device"), "vpd_pg89",
		vpd89Page(t, "QEMU HARDDISK", "liken-node-1-state"))

	names := diskIDNames("sda")
	want := []string{"ata-QEMU_HARDDISK_liken-node-1-state"}
	if !equalNames(names, want) {
		t.Errorf("the names are %v, want %v", names, want)
	}
}

func TestDiskIDNamesSATAWithWWNAndATAIdentity(t *testing.T) {
	sys, _ := fakeMachine(t)
	dir := fakeDisk(t, sys, "sda", "pci0000:00", "0000:00:1f.2", "ata3", "host2",
		"target2:0:0", "2:0:0:0", "block", "sda")
	writeSysfs(t, filepath.Join(dir, "device"), "wwid", "naa.5002538d40a45c88\n")
	writeSysfsBytes(t, filepath.Join(dir, "device"), "vpd_pg89",
		vpd89Page(t, "QEMU HARDDISK", "liken-node-1-state"))

	names := diskIDNames("sda")
	want := []string{
		"wwn-0x5002538d40a45c88",
		"scsi-35002538d40a45c88",
		"ata-QEMU_HARDDISK_liken-node-1-state",
	}
	if !equalNames(names, want) {
		t.Errorf("the names are %v, want %v", names, want)
	}
}

// vpd89Page pads and byte-swaps model and serial itself, so the tests
// check the swap in ataIdentifyString.
func vpd89Page(t *testing.T, model, serial string) []byte {
	t.Helper()
	page := make([]byte, 572)
	page[1] = 0x89
	page[2], page[3] = 0x02, 0x38
	copy(page[60+20:60+40], swapBytePairs(padField(serial, 20)))
	copy(page[60+54:60+94], swapBytePairs(padField(model, 40)))
	return page
}

func padField(s string, width int) string {
	if len(s) >= width {
		return s[:width]
	}
	return s + strings.Repeat(" ", width-len(s))
}

func swapBytePairs(s string) []byte {
	b := []byte(s)
	for i := 0; i+1 < len(b); i += 2 {
		b[i], b[i+1] = b[i+1], b[i]
	}
	return b
}

func TestDiskIDNamesSATAShortVPD89HasNoATAName(t *testing.T) {
	sys, _ := fakeMachine(t)
	dir := fakeDisk(t, sys, "sda", "pci0000:00", "0000:00:1f.2", "ata3", "host2",
		"target2:0:0", "2:0:0:0", "block", "sda")
	writeSysfs(t, filepath.Join(dir, "device"), "wwid", "naa.5002538d40a45c88\n")
	writeSysfsBytes(t, filepath.Join(dir, "device"), "vpd_pg89", make([]byte, 571))

	names := diskIDNames("sda")
	want := []string{"wwn-0x5002538d40a45c88", "scsi-35002538d40a45c88"}
	if !equalNames(names, want) {
		t.Errorf("the names are %v, want %v", names, want)
	}
}

func TestDiskIDNamesSATABlankModelHasNoATAName(t *testing.T) {
	sys, _ := fakeMachine(t)
	dir := fakeDisk(t, sys, "sda", "pci0000:00", "0000:00:1f.2", "ata3", "host2",
		"target2:0:0", "2:0:0:0", "block", "sda")
	writeSysfsBytes(t, filepath.Join(dir, "device"), "vpd_pg89",
		vpd89Page(t, strings.Repeat(" ", 40), "liken-node-1-state"))

	if names := diskIDNames("sda"); names != nil {
		t.Errorf("a page with an all-space model has the names %v, want none", names)
	}
}

func TestScsiVPD80Serial(t *testing.T) {
	dir := t.TempDir()
	page := []byte{0x00, 0x80, 0x00, 0x08, 'S', '3', 'Z', '1', ' ', ' ', ' ', ' '}
	if err := os.MkdirAll(filepath.Join(dir, "device"), 0o755); err != nil {
		t.Fatalf("creating the device directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "device", "vpd_pg80"), page, 0o644); err != nil {
		t.Fatalf("writing vpd_pg80: %v", err)
	}

	if got := scsiVPD80Serial(dir); got != "S3Z1" {
		t.Errorf("the serial is %q, want S3Z1", got)
	}
}

func TestScsiVPD80SerialAbsent(t *testing.T) {
	if got := scsiVPD80Serial(t.TempDir()); got != "" {
		t.Errorf("a disk with no vpd_pg80 page has the serial %q, want empty", got)
	}
}

func TestScsiVPD89ATAIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "device"), 0o755); err != nil {
		t.Fatalf("creating the device directory: %v", err)
	}
	writeSysfsBytes(t, filepath.Join(dir, "device"), "vpd_pg89",
		vpd89Page(t, "QEMU HARDDISK", "liken-node-1-state"))

	model, serial := scsiVPD89ATAIdentity(dir)
	if model != "QEMU HARDDISK" || serial != "liken-node-1-state" {
		t.Errorf("the model is %q and the serial is %q, want %q and %q",
			model, serial, "QEMU HARDDISK", "liken-node-1-state")
	}
}

func TestScsiVPD89ATAIdentityAbsent(t *testing.T) {
	model, serial := scsiVPD89ATAIdentity(t.TempDir())
	if model != "" || serial != "" {
		t.Errorf("a disk with no vpd_pg89 page has the model %q and the serial %q, want both empty", model, serial)
	}
}

func TestSanitizeIDPart(t *testing.T) {
	if got := sanitizeIDPart("WDC WD40EFRX-68N"); got != "WDC_WD40EFRX-68N" {
		t.Errorf("the string is %q, want WDC_WD40EFRX-68N", got)
	}
}

func TestSanitizeIDPartReplacesSlashAndNUL(t *testing.T) {
	if got := sanitizeIDPart("abc/def\x00ghi"); got != "abc_def_ghi" {
		t.Errorf("the string is %q, want abc_def_ghi", got)
	}
}

// equalNames compares in order, because the order of diskIDNames is
// part of its result.
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
