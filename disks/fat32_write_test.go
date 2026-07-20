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
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	const size = 300 << 20
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if err := FormatFAT32(f, size, "LIKEN-TEST", 42); err != nil {
		t.Fatal(err)
	}
	return f
}

// openVolume opens a just-written volume for assertions.
func openVolume(t *testing.T, f *os.File) *FATVolume {
	t.Helper()
	v, err := OpenFATVolume(f)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// mustRead reads one file's bytes back or fails the test.
func mustRead(t *testing.T, v *FATVolume, path string) []byte {
	t.Helper()
	data, err := v.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestFATWriterRoundTripsATree(t *testing.T) {
	vol := formattedVolume(t)
	w, err := NewFATWriter(vol)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"EFI", "EFI/BOOT", "loader", "loader/entries"} {
		if err := w.Mkdir(dir); err != nil {
			t.Fatal(err)
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
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	v := openVolume(t, vol)
	for name, want := range files {
		if got := string(mustRead(t, v, name)); got != want {
			t.Errorf("%s read back %q, want %q", name, got, want)
		}
	}
	e, err := v.Find("EFI/BOOT")
	if err != nil || !e.IsDir {
		t.Errorf("EFI/BOOT must be a directory: %+v, %v", e, err)
	}
}

func TestFATWriterSpansClusters(t *testing.T) {
	vol := formattedVolume(t)
	w, err := NewFATWriter(vol)
	if err != nil {
		t.Fatal(err)
	}
	// This file spans three clusters and part of a fourth. The
	// chain, not the record, carries the shape, and the size field
	// trims the extra bytes at the end.
	big := bytes.Repeat([]byte("0123456789abcdef"), (3*4096+700)/16)
	if err := w.WriteFile("big.bin", bytes.NewReader(big), int64(len(big))); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	if got := mustRead(t, openVolume(t, vol), "big.bin"); !bytes.Equal(got, big) {
		t.Errorf("a multi-cluster file must read back byte-identical (%d vs %d bytes)", len(got), len(big))
	}
}

func TestFATWriterAccountsForFreeSpace(t *testing.T) {
	vol := formattedVolume(t)
	w, err := NewFATWriter(vol)
	if err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat("x", 5000) // two clusters
	if err := w.WriteFile("file.bin", strings.NewReader(content), int64(len(content))); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	v := openVolume(t, vol)
	used := v.UsedClusters()
	if total := uint32(len(v.fat)) - 2; v.FreeClusters != total-used {
		t.Errorf("FSInfo says %d free; the table says %d", v.FreeClusters, total-used)
	}
	if v.NextFree != used+2 {
		t.Errorf("FSInfo's next-free hint %d; the bump allocator stopped at %d", v.NextFree, used+2)
	}
}

func TestFATWriterKeepsTheVolumeLabelFirst(t *testing.T) {
	vol := formattedVolume(t)
	w, err := NewFATWriter(vol)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteFile("hello.txt", strings.NewReader("hi"), 2); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// fsck expects the boot-sector label and the root's label
	// record to agree. The writer must carry the format's record
	// through unchanged.
	v := openVolume(t, vol)
	raw, err := v.chain(2)
	if err != nil {
		t.Fatal(err)
	}
	if raw[11] != 0x08 || !strings.HasPrefix(string(raw[0:11]), "LIKEN-TEST") {
		t.Errorf("the volume label must stay the root's first record: % x", raw[:12])
	}
}

func TestFATWriterRefusesOrphanPaths(t *testing.T) {
	vol := formattedVolume(t)
	w, err := NewFATWriter(vol)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteFile("no/such/dir.txt", strings.NewReader("x"), 1); err == nil {
		t.Error("a file needs its directory first")
	}
	if err := w.Mkdir("a/b"); err == nil {
		t.Error("a directory needs its parent first")
	}
}

func TestFATWriterRefusesAnUnformattedVolume(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "blank"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(300 << 20); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFATWriter(f); err == nil {
		t.Error("a blank file is not a FAT32 volume")
	}
	if _, err := OpenFATVolume(f); err == nil {
		t.Error("a blank file is not a FAT32 volume to read either")
	}
}
