package disks

// This file tests the FAT file writer, reading the result back
// through the package's own reader (fat32_read.go). The consumers
// that matter are firmware and the kernel's vfat driver, and neither
// can run in a unit test. What the reader checks (geometry, chains,
// long names, checksums, and the agreement of the two FAT copies) is
// exactly what those real consumers rely on. A drill confirmed that
// the kernel agrees, using fsck and a loop mount of a writer-built
// volume.

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// formattedVolume creates a sparse file just past FAT32's minimum
// size, and formats it. This is the smallest valid volume that
// these tests can use.
func formattedVolume(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "volume"))
	if err != nil {
		t.Fatalf("creating the volume file: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	const size = 300 << 20
	if err := f.Truncate(size); err != nil {
		t.Fatalf("sizing the volume file: %v", err)
	}
	if err := FormatFAT32(f, size, "LIKEN-TEST", 42); err != nil {
		t.Fatalf("formatting the volume: %v", err)
	}
	return f
}

// openVolume opens a just-written volume for assertions.
func openVolume(t *testing.T, f *os.File) *FATVolume {
	t.Helper()
	v, err := OpenFATVolume(f)
	if err != nil {
		t.Fatalf("opening the volume: %v", err)
	}
	return v
}

// mustRead reads one file's bytes back or fails the test.
func mustRead(t *testing.T, v *FATVolume, path string) []byte {
	t.Helper()
	data, err := v.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the file back: %v", err)
	}
	return data
}

func TestFATWriterRoundTripsATree(t *testing.T) {
	vol := formattedVolume(t)
	w, err := NewFATWriter(vol)
	if err != nil {
		t.Fatalf("opening the volume for writing: %v", err)
	}
	for _, dir := range []string{"EFI", "EFI/BOOT", "loader", "loader/entries"} {
		if err := w.Mkdir(dir); err != nil {
			t.Fatalf("creating the directory: %v", err)
		}
	}
	files := map[string]string{
		"EFI/BOOT/BOOTX64.EFI":         "pretend boot program",
		"loader/loader.conf":           "timeout menu-force\n",
		"loader/entries/node-1.conf":   "title install as node-1\n",
		"loader/entries/node-2.conf":   "title install as node-2\n",
		"vmlinuz":                      "pretend kernel",
		"liken.cpio":                   "pretend generic image",
		"deployment.cpio":              "pretend layer",
		"deployment.cpio.sha256":       "the collision case: same 8 chars, and fsck demands unique short names",
		"a name with spaces and é.txt": "long names survive",
	}
	for name, content := range files {
		if err := w.WriteFile(name, strings.NewReader(content), int64(len(content))); err != nil {
			t.Fatalf("writing the file: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing the writer: %v", err)
	}

	v := openVolume(t, vol)
	for name, want := range files {
		if got := string(mustRead(t, v, name)); got != want {
			t.Errorf("%s read back %q, want %q", name, got, want)
		}
	}
	e, err := v.Find("EFI/BOOT")
	if err != nil || !e.IsDir {
		t.Errorf("EFI/BOOT is %+v (error %v), want a directory", e, err)
	}
}

func TestFATWriterSpansClusters(t *testing.T) {
	vol := formattedVolume(t)
	w, err := NewFATWriter(vol)
	if err != nil {
		t.Fatalf("opening the volume for writing: %v", err)
	}
	// This file spans three clusters and part of a fourth. The
	// chain, not the record, carries the shape, and the size field
	// trims the extra bytes at the end.
	big := bytes.Repeat([]byte("0123456789abcdef"), (3*4096+700)/16)
	if err := w.WriteFile("big.bin", bytes.NewReader(big), int64(len(big))); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing the writer: %v", err)
	}

	if got := mustRead(t, openVolume(t, vol), "big.bin"); !bytes.Equal(got, big) {
		t.Errorf("the multi-cluster file reads back with different bytes (%d bytes read, %d written)", len(got), len(big))
	}
}

func TestFATWriterAccountsForFreeSpace(t *testing.T) {
	vol := formattedVolume(t)
	w, err := NewFATWriter(vol)
	if err != nil {
		t.Fatalf("opening the volume for writing: %v", err)
	}
	content := strings.Repeat("x", 5000) // two clusters
	if err := w.WriteFile("file.bin", strings.NewReader(content), int64(len(content))); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing the writer: %v", err)
	}

	v := openVolume(t, vol)
	used := v.UsedClusters()
	if total := uint32(len(v.fat)) - 2; v.FreeClusters != total-used {
		t.Errorf("the FSInfo free count is %d, want %d from the table", v.FreeClusters, total-used)
	}
	if v.NextFree != used+2 {
		t.Errorf("the FSInfo next-free hint is %d, want %d where the allocator stopped", v.NextFree, used+2)
	}
}

func TestFATWriterKeepsTheVolumeLabelFirst(t *testing.T) {
	vol := formattedVolume(t)
	w, err := NewFATWriter(vol)
	if err != nil {
		t.Fatalf("opening the volume for writing: %v", err)
	}
	if err := w.WriteFile("hello.txt", strings.NewReader("hi"), 2); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing the writer: %v", err)
	}

	// fsck expects the boot-sector label and the root's label
	// record to agree. The writer must carry the format's record
	// through unchanged.
	v := openVolume(t, vol)
	raw, err := v.chain(2)
	if err != nil {
		t.Fatalf("reading the root directory chain: %v", err)
	}
	if raw[11] != 0x08 || !strings.HasPrefix(string(raw[0:11]), "LIKEN-TEST") {
		t.Errorf("the first record of the root is % x, want the volume label", raw[:12])
	}
}

func TestFATWriterRefusesOrphanPaths(t *testing.T) {
	vol := formattedVolume(t)
	w, err := NewFATWriter(vol)
	if err != nil {
		t.Fatalf("opening the volume for writing: %v", err)
	}
	if err := w.WriteFile("no/such/dir.txt", strings.NewReader("x"), 1); err == nil {
		t.Error("WriteFile accepted a file whose directory does not exist")
	}
	if err := w.Mkdir("a/b"); err == nil {
		t.Error("Mkdir accepted a directory whose parent does not exist")
	}
}

func TestFATWriterRefusesAnUnformattedVolume(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "blank"))
	if err != nil {
		t.Fatalf("creating the volume file: %v", err)
	}
	defer f.Close()
	if err := f.Truncate(300 << 20); err != nil {
		t.Fatalf("sizing the volume file: %v", err)
	}
	if _, err := NewFATWriter(f); err == nil {
		t.Error("NewFATWriter accepted a blank file as a FAT32 volume")
	}
	if _, err := OpenFATVolume(f); err == nil {
		t.Error("OpenFATVolume accepted a blank file as a FAT32 volume")
	}
}

// withBootSector formats a volume and then changes its boot sector.
func withBootSector(t *testing.T, change func(boot []byte)) *os.File {
	t.Helper()
	f := formattedVolume(t)
	boot := make([]byte, SectorSize)
	if _, err := f.ReadAt(boot, 0); err != nil {
		t.Fatalf("reading the boot sector: %v", err)
	}
	change(boot)
	if _, err := f.WriteAt(boot, 0); err != nil {
		t.Fatalf("writing the boot sector: %v", err)
	}
	return f
}

// A boot sector whose numbers leave no data region, or give clusters of
// no sectors, describes a volume that the writer cannot lay out. The
// writer must refuse it, not wrap the cluster count or divide by zero.
// Each case keeps 128 sectors per cluster where it can, so that a
// wrapped count asks for about 128 MiB and not 16 GiB.
func TestFATWriterRefusesAGeometryItCannotUse(t *testing.T) {
	cases := []struct {
		name   string
		change func(boot []byte)
	}{
		{"total sectors before the data region", func(boot []byte) {
			boot[13] = 128
			binary.LittleEndian.PutUint32(boot[32:36], 1)
		}},
		{"clusters of no sectors", func(boot []byte) {
			boot[13] = 0
		}},
		{"less than one cluster after the data region", func(boot []byte) {
			boot[13] = 128
			reserved := uint32(binary.LittleEndian.Uint16(boot[14:16]))
			fatSectors := binary.LittleEndian.Uint32(boot[36:40])
			binary.LittleEndian.PutUint32(boot[32:36], reserved+2*fatSectors+1)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := withBootSector(t, c.change)
			if _, err := NewFATWriter(f); err == nil {
				t.Error("NewFATWriter accepted the volume, want an error that gives the geometry")
			}
		})
	}
}
