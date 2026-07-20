package disks

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// formatTestPartition formats a sparse file that stands in for a
// partition of the given size. It uses a fixed volume ID, so that
// the output is deterministic, and returns the file open for
// reading.
func formatTestPartition(t *testing.T, sizeBytes uint64) *os.File {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "slot"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	if err := f.Truncate(int64(sizeBytes)); err != nil {
		t.Fatal(err)
	}
	if err := FormatFAT32(f, sizeBytes, "LIKEN-SYS-A", 0x1234_5678); err != nil {
		t.Fatal(err)
	}
	return f
}

func readSector(t *testing.T, f *os.File, lba uint64) []byte {
	t.Helper()
	sector := make([]byte, SectorSize)
	if _, err := f.ReadAt(sector, int64(lba*SectorSize)); err != nil {
		t.Fatal(err)
	}
	return sector
}

const testSlotBytes = 512 << 20 // the lab's slot size, 512 MiB

func TestFAT32BootSector(t *testing.T) {
	f := formatTestPartition(t, testSlotBytes)
	bs := readSector(t, f, 0)

	// These are the fields that firmware and the kernel actually
	// read, at the offsets that Microsoft's specification fixes
	// permanently.
	if bs[0] != 0xEB || bs[2] != 0x90 {
		t.Errorf("jump instruction: got % X, want EB xx 90", bs[0:3])
	}
	if got := string(bs[3:11]); got != "liken   " {
		t.Errorf("OEM name: got %q", got)
	}
	if got := binary.LittleEndian.Uint16(bs[11:13]); got != 512 {
		t.Errorf("bytes per sector: got %d", got)
	}
	if got := bs[13]; got != 8 {
		t.Errorf("sectors per cluster: got %d, want 8", got)
	}
	if got := binary.LittleEndian.Uint16(bs[14:16]); got != 32 {
		t.Errorf("reserved sectors: got %d, want 32", got)
	}
	if got := bs[16]; got != 2 {
		t.Errorf("number of FATs: got %d, want 2", got)
	}
	// FAT32 declares itself partly by zeroing the FAT12/16 fields.
	if got := binary.LittleEndian.Uint16(bs[17:19]); got != 0 {
		t.Errorf("root entry count: got %d, want 0 on FAT32", got)
	}
	if got := binary.LittleEndian.Uint16(bs[19:21]); got != 0 {
		t.Errorf("16-bit total sectors: got %d, want 0 on FAT32", got)
	}
	if got := binary.LittleEndian.Uint16(bs[22:24]); got != 0 {
		t.Errorf("16-bit FAT size: got %d, want 0 on FAT32", got)
	}
	if got := bs[21]; got != 0xF8 {
		t.Errorf("media byte: got %#x, want 0xF8 (fixed disk)", got)
	}
	if got := binary.LittleEndian.Uint32(bs[32:36]); got != testSlotBytes/SectorSize {
		t.Errorf("32-bit total sectors: got %d, want %d", got, testSlotBytes/SectorSize)
	}
	// Microsoft's sizing formula for a 512 MiB volume at 8 sectors
	// per cluster: ceil((1048576-32) / ((256*8+2)/2)) = 1023.
	if got := binary.LittleEndian.Uint32(bs[36:40]); got != 1023 {
		t.Errorf("32-bit FAT size: got %d, want 1023", got)
	}
	if got := binary.LittleEndian.Uint32(bs[44:48]); got != 2 {
		t.Errorf("root directory cluster: got %d, want 2", got)
	}
	if got := binary.LittleEndian.Uint16(bs[48:50]); got != 1 {
		t.Errorf("FSInfo sector: got %d, want 1", got)
	}
	if got := binary.LittleEndian.Uint16(bs[50:52]); got != 6 {
		t.Errorf("backup boot sector: got %d, want 6", got)
	}
	if got := bs[66]; got != 0x29 {
		t.Errorf("extended boot signature: got %#x, want 0x29", got)
	}
	if got := binary.LittleEndian.Uint32(bs[67:71]); got != 0x1234_5678 {
		t.Errorf("volume ID: got %#x", got)
	}
	if got := string(bs[71:82]); got != "LIKEN-SYS-A" {
		t.Errorf("volume label: got %q", got)
	}
	if got := string(bs[82:90]); got != "FAT32   " {
		t.Errorf("filesystem type string: got %q", got)
	}
	if bs[510] != 0x55 || bs[511] != 0xAA {
		t.Errorf("boot signature: got % X", bs[510:512])
	}
}

func TestFAT32FSInfo(t *testing.T) {
	f := formatTestPartition(t, testSlotBytes)
	info := readSector(t, f, 1)

	if got := binary.LittleEndian.Uint32(info[0:4]); got != 0x41615252 {
		t.Errorf("lead signature: got %#x", got)
	}
	if got := binary.LittleEndian.Uint32(info[484:488]); got != 0x61417272 {
		t.Errorf("structure signature: got %#x", got)
	}
	// 1,048,576 sectors - 32 reserved - 2*1023 FAT = 1,046,498 data
	// sectors = 130,812 clusters, minus the one the root directory
	// occupies.
	if got := binary.LittleEndian.Uint32(info[488:492]); got != 130_811 {
		t.Errorf("free cluster count: got %d, want 130811", got)
	}
	if got := binary.LittleEndian.Uint32(info[492:496]); got != 3 {
		t.Errorf("next-free hint: got %d, want 3 (2 is the root)", got)
	}
	if info[510] != 0x55 || info[511] != 0xAA {
		t.Errorf("trailing signature: got % X", info[510:512])
	}
}

func TestFAT32BackupSectors(t *testing.T) {
	f := formatTestPartition(t, testSlotBytes)
	// The backup copies at sectors 6 and 7 must be byte-for-byte
	// identical to the primaries. This is their entire purpose.
	if string(readSector(t, f, 0)) != string(readSector(t, f, 6)) {
		t.Error("backup boot sector differs from the primary")
	}
	if string(readSector(t, f, 1)) != string(readSector(t, f, 7)) {
		t.Error("backup FSInfo differs from the primary")
	}
}

func TestFAT32AllocationTables(t *testing.T) {
	f := formatTestPartition(t, testSlotBytes)
	const fatSize = 1023

	for name, start := range map[string]uint64{"first FAT": 32, "second FAT": 32 + fatSize} {
		fat := readSector(t, f, start)
		if got := binary.LittleEndian.Uint32(fat[0:4]); got != 0x0FFFFFF8 {
			t.Errorf("%s entry 0: got %#08x, want 0x0FFFFFF8 (media byte)", name, got)
		}
		if got := binary.LittleEndian.Uint32(fat[4:8]); got != 0x0FFFFFFF {
			t.Errorf("%s entry 1: got %#08x, want 0x0FFFFFFF (clean shutdown)", name, got)
		}
		if got := binary.LittleEndian.Uint32(fat[8:12]); got != 0x0FFFFFFF {
			t.Errorf("%s entry 2: got %#08x, want 0x0FFFFFFF (root directory's chain ends)", name, got)
		}
		for i := 12; i < SectorSize; i += 4 {
			if got := binary.LittleEndian.Uint32(fat[i : i+4]); got != 0 {
				t.Fatalf("%s entry %d: got %#08x, want free", name, i/4, got)
			}
		}
	}
}

func TestFAT32RootDirectoryCarriesTheLabel(t *testing.T) {
	f := formatTestPartition(t, testSlotBytes)
	// Cluster 2 begins right after the reserved region and both
	// FATs. A fresh root directory holds exactly one entry: the
	// volume-label entry (attribute 0x08), which matches the boot
	// sector's label field.
	root := readSector(t, f, 32+2*1023)
	if got := string(root[0:11]); got != "LIKEN-SYS-A" {
		t.Errorf("volume label entry: got %q", got)
	}
	if root[11] != 0x08 {
		t.Errorf("label entry attribute: got %#x, want 0x08 (volume ID)", root[11])
	}
	for i, b := range root[32:] {
		if b != 0 {
			t.Fatalf("root directory byte %d: got %#x, want 0", 32+i, b)
		}
	}
}

func TestFAT32RefusesTinyPartitions(t *testing.T) {
	// Below 65,525 clusters, the volume would legally be FAT16.
	// Cluster count alone determines the FAT type. A volume labeled
	// FAT32 but below this line would be misread everywhere. Even at
	// one sector per cluster, 16 MiB is well under this line.
	f, err := os.Create(filepath.Join(t.TempDir(), "tiny"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	err = FormatFAT32(f, 16<<20, "TOO-SMALL", 1)
	if err == nil {
		t.Fatal("expected an error for a partition too small for FAT32")
	}
	for _, want := range []string{"too small", "FAT32"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

func TestFAT32SectorsPerClusterFollowsTheSpecTable(t *testing.T) {
	// This checks Microsoft's size table, at each step and just
	// past each boundary. The 512 MiB slots stay at 8 sectors, so
	// nothing already formatted changes shape.
	cases := []struct {
		bytes uint64
		want  uint64
	}{
		{64 << 20, 1},  // near the GRUB boot home's size
		{260 << 20, 1}, // still at or under the 260 MB line
		{512 << 20, 8}, // the system slots
		{8 << 30, 8},   // the 8 GB line itself
		{(8 << 30) + 512, 16},
		{16 << 30, 16},
		{(16 << 30) + 512, 32},
		{32 << 30, 32},
		{(32 << 30) + 512, 64},
	}
	for _, c := range cases {
		if got := fat32SectorsPerCluster(c.bytes / SectorSize); got != c.want {
			t.Errorf("fat32SectorsPerCluster(%d bytes) = %d, want %d", c.bytes, got, c.want)
		}
	}
}

func TestFAT32FormatsASmallVolume(t *testing.T) {
	// 64 MiB is in the GRUB boot home's size class, far below the
	// old 4 KiB-cluster floor of ~260 MiB. At one sector per
	// cluster, the cluster count clears FAT32's minimum with room to
	// spare.
	f := formatTestPartition(t, 64<<20)
	bs := readSector(t, f, 0)

	if got := bs[13]; got != 1 {
		t.Errorf("sectors per cluster: got %d, want 1", got)
	}
	// The sizing formula at one sector per cluster:
	// ceil((131072-32) / ((256*1+2)/2)) = 1016 sectors of FAT.
	const fatSize = 1016
	if got := binary.LittleEndian.Uint32(bs[36:40]); got != fatSize {
		t.Errorf("32-bit FAT size: got %d, want %d", got, fatSize)
	}
	clusters := (64<<20/SectorSize - 32 - 2*fatSize) / 1
	if clusters < 65_525 {
		t.Fatalf("test arithmetic is off: %d clusters would misparse as FAT16", clusters)
	}
	// The layout is consistent: the root directory's label entry
	// sits where the geometry says cluster 2 begins.
	root := readSector(t, f, 32+2*fatSize)
	if got := string(root[0:11]); got != "LIKEN-SYS-A" {
		t.Errorf("volume label entry: got %q", got)
	}
	if !HasFAT32(f.Name()) {
		t.Error("HasFAT32 should recognize the small volume")
	}
}

func TestHasFAT32(t *testing.T) {
	f := formatTestPartition(t, testSlotBytes)
	if !HasFAT32(f.Name()) {
		t.Error("HasFAT32 should recognize a freshly formatted slot")
	}

	blank, err := os.Create(filepath.Join(t.TempDir(), "blank"))
	if err != nil {
		t.Fatal(err)
	}
	defer blank.Close()
	if err := blank.Truncate(1 << 20); err != nil {
		t.Fatal(err)
	}
	if HasFAT32(blank.Name()) {
		t.Error("HasFAT32 should not recognize a blank device")
	}
}

func TestHasFAT32ReportsAMissingDevice(t *testing.T) {
	if HasFAT32(filepath.Join(t.TempDir(), "absent")) {
		t.Error("no device, no filesystem")
	}
}
