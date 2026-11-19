package disks

// This file tests the GPT machinery: the layout arithmetic that
// the partition planner builds on, and the reader/serializer pair
// that lets a table be edited in place. The round-trip tests matter
// most. ReadGPT must read back exactly what SerializeGPT writes,
// including every GUID, because the reader exists to preserve
// identity through edits.

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestGPTLastUsableLBA(t *testing.T) {
	// A 2 GiB disk has 4,194,304 sectors of 512 bytes. The table
	// reserves 34 sectors at each end: the MBR, header, and 32 entry
	// sectors in front, and the mirror at the tail. So the last
	// sector a partition may occupy is 35 sectors from the end.
	if got := LastUsableLBA(4_194_304); got != 4_194_269 {
		t.Errorf("LastUsableLBA(4194304) = %d, want 4194269", got)
	}
}

func TestAlignLBA(t *testing.T) {
	cases := []struct{ in, want uint64 }{
		{0, 0},         // already on a boundary
		{1, 2_048},     // anything inside the first MiB rounds up past the table
		{34, 2_048},    // the first sector after the primary table, likewise
		{2_048, 2_048}, // a boundary stays put
		{2_049, 4_096}, // one past a boundary rounds to the next
	}
	for _, c := range cases {
		if got := AlignLBA(c.in); got != c.want {
			t.Errorf("AlignLBA(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// sampleTable builds a two-partition table with distinct, fixed
// GUIDs, so that tests can check that every identity survives a
// round trip.
func sampleTable() *Table {
	return &Table{
		DiskGUID: MustGUID("11111111-2222-3333-4455-66778899AABB"),
		Entries: []Entry{
			{
				TypeGUID:   LinuxFilesystemData,
				UniqueGUID: MustGUID("AAAAAAAA-BBBB-CCCC-DDEE-FF0011223344"),
				FirstLBA:   2_048,
				LastLBA:    4_095,
				Name:       "liken:machineState",
			},
			{
				TypeGUID:   LinuxFilesystemData,
				UniqueGUID: MustGUID("99999999-8888-7777-6655-443322110000"),
				FirstLBA:   4_096,
				LastLBA:    100_000,
				Name:       "liken:clusterState",
			},
		},
	}
}

// diskFile writes a table's chunks into a sparse file of the given
// sector count. This is the closest thing to a block device that a
// test can use.
func diskFile(t *testing.T, table *Table, totalSectors uint64) *os.File {
	t.Helper()
	chunks, err := SerializeGPT(table, totalSectors)
	if err != nil {
		t.Fatalf("serializing the table: %v", err)
	}
	f, err := os.Create(filepath.Join(t.TempDir(), "disk"))
	if err != nil {
		t.Fatalf("creating the disk file: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	if err := f.Truncate(int64(totalSectors) * SectorSize); err != nil {
		t.Fatalf("sizing the disk file: %v", err)
	}
	for _, chunk := range chunks {
		if _, err := f.WriteAt(chunk.Data, int64(chunk.LBA)*SectorSize); err != nil {
			t.Fatalf("writing a table chunk: %v", err)
		}
	}
	return f
}

const testDiskSectors = 262_144 // 128 MiB

func TestGPTRoundTripPreservesEverything(t *testing.T) {
	want := sampleTable()
	f := diskFile(t, want, testDiskSectors)

	got, err := ReadGPT(f, testDiskSectors)
	if err != nil {
		t.Fatalf("reading the table: %v", err)
	}
	if got.DiskGUID != want.DiskGUID {
		t.Error("the disk GUID changed in the round trip")
	}
	if !slices.Equal(got.Entries, want.Entries) {
		t.Errorf("entries changed in the round trip:\ngot  %+v\nwant %+v", got.Entries, want.Entries)
	}
	if got.AlternateLBA != testDiskSectors-1 {
		t.Errorf("alternateLBA = %d, want %d", got.AlternateLBA, testDiskSectors-1)
	}
	if got.LastUsableLBA != LastUsableLBA(testDiskSectors) {
		t.Errorf("lastUsableLBA = %d, want %d", got.LastUsableLBA, LastUsableLBA(testDiskSectors))
	}
}

func TestGPTReaderRecoversFromACorruptPrimary(t *testing.T) {
	want := sampleTable()
	f := diskFile(t, want, testDiskSectors)
	// One flipped byte in the primary header breaks its checksum.
	if _, err := f.WriteAt([]byte{0xFF}, 1*SectorSize+40); err != nil {
		t.Fatalf("corrupting the table: %v", err)
	}

	got, err := ReadGPT(f, testDiskSectors)
	if err != nil {
		t.Fatalf("reading the table: %v", err)
	}
	if got.DiskGUID != want.DiskGUID || !slices.Equal(got.Entries, want.Entries) {
		t.Error("the table read with a corrupt primary differs from the written table, want the backup's copy")
	}
}

func TestGPTReaderRejectsACorruptEntryArray(t *testing.T) {
	f := diskFile(t, sampleTable(), testDiskSectors)
	// This corrupts the primary's entry array and the backup
	// header. The primary fails its entries CRC, the backup fails
	// its header CRC, and nothing is left to trust.
	if _, err := f.WriteAt([]byte{0xFF}, 2*SectorSize+8); err != nil {
		t.Fatalf("corrupting the table: %v", err)
	}
	if _, err := f.WriteAt([]byte{0xFF}, int64(testDiskSectors-1)*SectorSize+40); err != nil {
		t.Fatalf("corrupting the table: %v", err)
	}

	_, err := ReadGPT(f, testDiskSectors)
	if err == nil {
		t.Fatal("reading a disk with both copies corrupt returned no error, want one")
	}
	if !strings.Contains(err.Error(), "primary") || !strings.Contains(err.Error(), "backup") {
		t.Errorf("the error is %v, want it to name the primary and the backup", err)
	}
}

func TestGPTReaderPrefersThePrimaryWhenCopiesDisagree(t *testing.T) {
	want := sampleTable()
	f := diskFile(t, want, testDiskSectors)
	// This overwrites the backup region with a different,
	// internally valid table: same geometry, different disk GUID.
	other := sampleTable()
	other.DiskGUID = MustGUID("DEADBEEF-DEAD-BEEF-DEAD-BEEFDEADBEEF")
	chunks, err := SerializeGPT(other, testDiskSectors)
	if err != nil {
		t.Fatalf("serializing the table: %v", err)
	}
	for _, chunk := range chunks[3:] { // backup entries + backup header only
		if _, err := f.WriteAt(chunk.Data, int64(chunk.LBA)*SectorSize); err != nil {
			t.Fatalf("writing a table chunk: %v", err)
		}
	}

	got, err := ReadGPT(f, testDiskSectors)
	if err != nil {
		t.Fatalf("reading the table: %v", err)
	}
	if got.DiskGUID != want.DiskGUID {
		t.Error("the disk GUID is the backup's, want the primary's")
	}
}

func TestSerializeGPTRelocatesTheBackupWhenTheDiskGrows(t *testing.T) {
	table := sampleTable()
	small := diskFile(t, table, testDiskSectors)
	read, err := ReadGPT(small, testDiskSectors)
	if err != nil {
		t.Fatalf("reading the table: %v", err)
	}

	// This re-serializes the same table for a disk twice the size.
	// The backup must land at the new end, lastUsable must move
	// out, and nothing about the partitions themselves may change.
	const grownSectors = 2 * testDiskSectors
	grown := diskFile(t, read, grownSectors)
	reread, err := ReadGPT(grown, grownSectors)
	if err != nil {
		t.Fatalf("reading the table: %v", err)
	}
	if reread.AlternateLBA != grownSectors-1 {
		t.Errorf("backup header at %d, want %d", reread.AlternateLBA, grownSectors-1)
	}
	if reread.LastUsableLBA != LastUsableLBA(grownSectors) {
		t.Errorf("lastUsable = %d, want %d", reread.LastUsableLBA, LastUsableLBA(grownSectors))
	}
	if reread.DiskGUID != table.DiskGUID || !slices.Equal(reread.Entries, table.Entries) {
		t.Error("the GUIDs or partition extents changed when the table moved to a larger disk, want them unchanged")
	}

	// The stale backup at the old end still exists as bytes. This is
	// fine, because nothing looks there anymore. This code confirms
	// that ReadGPT consults the new end, by corrupting the primary
	// and checking that recovery still works.
	if _, err := grown.WriteAt([]byte{0xFF}, 1*SectorSize+40); err != nil {
		t.Fatalf("corrupting the table: %v", err)
	}
	recovered, err := ReadGPT(grown, grownSectors)
	if err != nil {
		t.Fatalf("reading the table: %v", err)
	}
	if !slices.Equal(recovered.Entries, table.Entries) {
		t.Error("the entries recovered from the relocated backup differ from the written entries")
	}
}

func TestSerializeGPTRejectsOversizedNames(t *testing.T) {
	table := sampleTable()
	table.Entries[0].Name = strings.Repeat("x", 37)
	if _, err := SerializeGPT(table, testDiskSectors); err == nil {
		t.Error("serializing a 37-character name returned no error, want one")
	}
}

func TestGPTNamesUseTheWholeField(t *testing.T) {
	table := sampleTable()
	table.Entries[0].Name = strings.Repeat("n", 36) // exactly the field width: no NUL terminator on disk
	f := diskFile(t, table, testDiskSectors)
	got, err := ReadGPT(f, testDiskSectors)
	if err != nil {
		t.Fatalf("reading the table: %v", err)
	}
	if got.Entries[0].Name != table.Entries[0].Name {
		t.Errorf("the name reads back as %q, want the 36-character name unchanged", got.Entries[0].Name)
	}
}

func TestWriteGPTWritesEverythingButNeedsARealDevice(t *testing.T) {
	// Writing against a regular file lays down every byte of the
	// table, and the reader can read it back. The write then fails
	// at the last step. BLKRRPART is a block-device ioctl, and a
	// file has no kernel partition view to re-read. This failure
	// marks the boundary between what a unit test can prove and
	// what the QEMU harness covers.
	path := filepath.Join(t.TempDir(), "disk")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating the disk file: %v", err)
	}
	if err := f.Truncate(testDiskSectors * SectorSize); err != nil {
		t.Fatalf("sizing the disk file: %v", err)
	}
	f.Close()

	parts := []Partition{{Name: "liken:machineState", FirstLBA: 2_048, LastLBA: 4_095, TypeGUID: LinuxFilesystemData}}
	err = Write(path, testDiskSectors, parts)
	if err == nil || !strings.Contains(err.Error(), "re-reading the partition table") {
		t.Fatalf("the write returned %v, want the failure of re-reading the partition table", err)
	}

	// The bytes still arrived: the reader sees a valid table.
	r, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening the disk file: %v", err)
	}
	defer r.Close()
	table, err := ReadGPT(r, testDiskSectors)
	if err != nil {
		t.Fatalf("reading the table: %v", err)
	}
	if len(table.Entries) != 1 || table.Entries[0].Name != "liken:machineState" {
		t.Errorf("the table read back is %+v, want one partition named liken:machineState", table.Entries)
	}
}

func TestWriteTableInPlaceSkipsTheKernelReread(t *testing.T) {
	// A regular file cannot answer BLKRRPART, so success here
	// proves that the in-place variant never asks. It writes the
	// bytes and stops. This is the whole reason for having it.
	// grow.go uses it when nothing about the kernel's view of the
	// partitions changes.
	path := filepath.Join(t.TempDir(), "disk")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating the disk file: %v", err)
	}
	if err := f.Truncate(testDiskSectors * SectorSize); err != nil {
		t.Fatalf("sizing the disk file: %v", err)
	}
	f.Close()

	if err := WriteTableInPlace(path, testDiskSectors, sampleTable()); err != nil {
		t.Fatalf("writing the table in place to a plain file: %v", err)
	}

	r, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening the disk file: %v", err)
	}
	defer r.Close()
	table, err := ReadGPT(r, testDiskSectors)
	if err != nil {
		t.Fatalf("reading the table: %v", err)
	}
	if table.AlternateLBA != testDiskSectors-1 {
		t.Errorf("the backup header is at sector %d, want the last sector", table.AlternateLBA)
	}
}

func TestTableWritesPreserveTheBootCode(t *testing.T) {
	// The first 446 bytes of sector 0 are the BIOS boot code, plus
	// the MBR disk signature. This is GRUB's first stage on a BIOS
	// machine. The table writer owns only what follows: the
	// protective 0xEE entry and the boot signature. A rewrite, such
	// as a claim over an old table or growth relocating the backup,
	// must carry the boot code through unchanged. Otherwise, liken's
	// own storage reconciliation would remove the ability to boot
	// the machine it runs on.
	path := filepath.Join(t.TempDir(), "disk")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating the disk file: %v", err)
	}
	if err := f.Truncate(testDiskSectors * SectorSize); err != nil {
		t.Fatalf("sizing the disk file: %v", err)
	}
	sentinel := bytes.Repeat([]byte{0xC3}, 446)
	if _, err := f.WriteAt(sentinel, 0); err != nil {
		t.Fatalf("writing the boot code: %v", err)
	}
	f.Close()

	if err := WriteTableInPlace(path, testDiskSectors, sampleTable()); err != nil {
		t.Fatalf("writing the table in place: %v", err)
	}

	sector := make([]byte, SectorSize)
	r, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening the disk file: %v", err)
	}
	defer r.Close()
	if _, err := r.ReadAt(sector, 0); err != nil {
		t.Fatalf("reading sector 0: %v", err)
	}
	if !bytes.Equal(sector[:446], sentinel) {
		t.Error("the boot code changed in the table write")
	}
	if sector[446+4] != 0xEE {
		t.Error("the protective entry at byte 450 is not 0xEE")
	}
	if sector[510] != 0x55 || sector[511] != 0xAA {
		t.Error("the boot signature is not 0x55 0xAA")
	}
}

func TestGPTReaderRejectsForeignGeometry(t *testing.T) {
	f := diskFile(t, sampleTable(), testDiskSectors)
	// This rewrites the primary header to claim a 64-entry array,
	// with a recomputed and valid header CRC. This makes the header
	// structurally sound, but not a table that liken ever writes.
	h := make([]byte, SectorSize)
	if _, err := f.ReadAt(h, 1*SectorSize); err != nil {
		t.Fatalf("reading the primary header: %v", err)
	}
	binary.LittleEndian.PutUint32(h[80:84], 64)
	clear(h[16:20])
	binary.LittleEndian.PutUint32(h[16:20], crc32.ChecksumIEEE(h[0:92]))
	if _, err := f.WriteAt(h, 1*SectorSize); err != nil {
		t.Fatalf("rewriting the primary header: %v", err)
	}

	// The backup still parses, so the read succeeds from there, and
	// the primary alone must be disqualified. This corrupts the
	// backup too, to see the primary's own error.
	if _, err := f.WriteAt([]byte{0xFF}, int64(testDiskSectors-1)*SectorSize+40); err != nil {
		t.Fatalf("corrupting the table: %v", err)
	}
	_, err := ReadGPT(f, testDiskSectors)
	if err == nil || !strings.Contains(err.Error(), "entries of") {
		t.Errorf("the error is %v, want a refusal of the entry array size", err)
	}
}

func TestWriteGPTTableReportsAMissingDevice(t *testing.T) {
	err := WriteTable(filepath.Join(t.TempDir(), "absent"), testDiskSectors, sampleTable())
	if err == nil {
		t.Error("writing to a device that does not exist returned no error, want one")
	}
}

func TestReadGPTWithBothCopiesUnreadable(t *testing.T) {
	// A blank device has neither table. The error names both
	// failures, and the grown-disk exception, because that is the
	// one case the backup cannot cover.
	blank := bytes.NewReader(make([]byte, 4_096*SectorSize))
	_, err := ReadGPT(blank, 4_096)
	if err == nil || !strings.Contains(err.Error(), "neither partition table copy is readable") {
		t.Errorf("the error is %v, want it to say that neither copy is readable", err)
	}
}

func TestWriteGPTTableRefusesAnUnserializableTable(t *testing.T) {
	// Serialization is validated before the device is even opened,
	// so a table that cannot be laid out never touches the disk.
	t1 := &Table{Entries: []Entry{{Name: strings.Repeat("x", NameChars+1), TypeGUID: LinuxFilesystemData}}}
	err := WriteTable(filepath.Join(t.TempDir(), "disk"), 4_096, t1)
	if err == nil || !strings.Contains(err.Error(), "characters") {
		t.Errorf("the error is %v, want a refusal of the partition name", err)
	}
}

func TestSerializeGPTRefusesTooManyEntries(t *testing.T) {
	t1 := &Table{Entries: make([]Entry, entryCount+1)}
	_, err := SerializeGPT(t1, 1<<20)
	if err == nil || !strings.Contains(err.Error(), "partitions do not fit") {
		t.Errorf("the error is %v, want a refusal of the entry count", err)
	}
}

func TestMustGUIDPanicsOnAGarbageLiteral(t *testing.T) {
	// MustGUID guards the package's own constants. A bad literal is
	// a programming error, and it must fail at first use. It must
	// not decode into a wrong type GUID that every tool then
	// misreads.
	defer func() {
		if recover() == nil {
			t.Error("MustGUID of a malformed literal did not panic, want a panic")
		}
	}()
	MustGUID("not-a-guid")
}
