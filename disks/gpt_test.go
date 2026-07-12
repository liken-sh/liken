package disks

// Tests for the GPT machinery: the layout arithmetic the partition
// planner builds on, and the reader/serializer pair that lets a
// table be edited in place. The round-trip tests matter most: what
// SerializeGPT writes, ReadGPT must read back identically, GUIDs and
// all, because the reader exists to preserve identity through edits.

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
	// A 2 GiB disk is 4,194,304 sectors of 512 bytes. The table
	// reserves 34 sectors at each end (MBR + header + 32 entry
	// sectors in front; the mirror at the tail), so the last sector a
	// partition may occupy is 35 from the end.
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
// GUIDs, so tests can assert every identity survives a round trip.
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
// sector count: the closest thing to a block device a test can have.
func diskFile(t *testing.T, table *Table, totalSectors uint64) *os.File {
	t.Helper()
	chunks, err := SerializeGPT(table, totalSectors)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(t.TempDir(), "disk"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	if err := f.Truncate(int64(totalSectors) * SectorSize); err != nil {
		t.Fatal(err)
	}
	for _, chunk := range chunks {
		if _, err := f.WriteAt(chunk.Data, int64(chunk.LBA)*SectorSize); err != nil {
			t.Fatal(err)
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
		t.Fatal(err)
	}
	if got.DiskGUID != want.DiskGUID {
		t.Error("the disk GUID did not survive the round trip")
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
		t.Fatal(err)
	}

	got, err := ReadGPT(f, testDiskSectors)
	if err != nil {
		t.Fatal(err)
	}
	if got.DiskGUID != want.DiskGUID || !slices.Equal(got.Entries, want.Entries) {
		t.Error("the backup copy should have supplied the whole table")
	}
}

func TestGPTReaderRejectsACorruptEntryArray(t *testing.T) {
	f := diskFile(t, sampleTable(), testDiskSectors)
	// Corrupt the primary's entry array AND the backup header: the
	// primary fails its entries CRC, the backup fails its header CRC,
	// and there is nothing left to trust.
	if _, err := f.WriteAt([]byte{0xFF}, 2*SectorSize+8); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0xFF}, int64(testDiskSectors-1)*SectorSize+40); err != nil {
		t.Fatal(err)
	}

	_, err := ReadGPT(f, testDiskSectors)
	if err == nil {
		t.Fatal("expected an error when both copies are corrupt")
	}
	if !strings.Contains(err.Error(), "primary") || !strings.Contains(err.Error(), "backup") {
		t.Errorf("error should account for both copies: %v", err)
	}
}

func TestGPTReaderPrefersThePrimaryWhenCopiesDisagree(t *testing.T) {
	want := sampleTable()
	f := diskFile(t, want, testDiskSectors)
	// Overwrite the backup region with a different, internally-valid
	// table: same geometry, different disk GUID.
	other := sampleTable()
	other.DiskGUID = MustGUID("DEADBEEF-DEAD-BEEF-DEAD-BEEFDEADBEEF")
	chunks, err := SerializeGPT(other, testDiskSectors)
	if err != nil {
		t.Fatal(err)
	}
	for _, chunk := range chunks[3:] { // backup entries + backup header only
		if _, err := f.WriteAt(chunk.Data, int64(chunk.LBA)*SectorSize); err != nil {
			t.Fatal(err)
		}
	}

	got, err := ReadGPT(f, testDiskSectors)
	if err != nil {
		t.Fatal(err)
	}
	if got.DiskGUID != want.DiskGUID {
		t.Error("disagreement should resolve in the primary's favor")
	}
}

func TestSerializeGPTRelocatesTheBackupWhenTheDiskGrows(t *testing.T) {
	table := sampleTable()
	small := diskFile(t, table, testDiskSectors)
	read, err := ReadGPT(small, testDiskSectors)
	if err != nil {
		t.Fatal(err)
	}

	// Re-serialize the same table for a disk twice the size: the
	// backup must land at the new end, lastUsable must move out, and
	// nothing about the partitions themselves may change.
	const grownSectors = 2 * testDiskSectors
	grown := diskFile(t, read, grownSectors)
	reread, err := ReadGPT(grown, grownSectors)
	if err != nil {
		t.Fatal(err)
	}
	if reread.AlternateLBA != grownSectors-1 {
		t.Errorf("backup header at %d, want %d", reread.AlternateLBA, grownSectors-1)
	}
	if reread.LastUsableLBA != LastUsableLBA(grownSectors) {
		t.Errorf("lastUsable = %d, want %d", reread.LastUsableLBA, LastUsableLBA(grownSectors))
	}
	if reread.DiskGUID != table.DiskGUID || !slices.Equal(reread.Entries, table.Entries) {
		t.Error("growing the disk must not change any identity or extent")
	}

	// The stale backup at the old end still exists as bytes, which is
	// fine, because nothing looks there anymore. Confirm the *new*
	// end is what ReadGPT consults by corrupting the primary and
	// checking that recovery still works.
	if _, err := grown.WriteAt([]byte{0xFF}, 1*SectorSize+40); err != nil {
		t.Fatal(err)
	}
	recovered, err := ReadGPT(grown, grownSectors)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(recovered.Entries, table.Entries) {
		t.Error("recovery should come from the relocated backup")
	}
}

func TestSerializeGPTRejectsOversizedNames(t *testing.T) {
	table := sampleTable()
	table.Entries[0].Name = strings.Repeat("x", 37)
	if _, err := SerializeGPT(table, testDiskSectors); err == nil {
		t.Error("expected an error for a 37-character name")
	}
}

func TestGPTNamesUseTheWholeField(t *testing.T) {
	table := sampleTable()
	table.Entries[0].Name = strings.Repeat("n", 36) // exactly the field width: no NUL terminator on disk
	f := diskFile(t, table, testDiskSectors)
	got, err := ReadGPT(f, testDiskSectors)
	if err != nil {
		t.Fatal(err)
	}
	if got.Entries[0].Name != table.Entries[0].Name {
		t.Errorf("a 36-character name should survive: %q", got.Entries[0].Name)
	}
}

func TestWriteGPTWritesEverythingButNeedsARealDevice(t *testing.T) {
	// Write against a regular file lays down every byte of the
	// table (round-trippable by the reader), and then fails at the
	// last step: BLKRRPART is a block-device ioctl, and a file has no
	// kernel partition view to re-read. That failure is the boundary
	// between what a unit test can prove and what the QEMU harness
	// owns.
	path := filepath.Join(t.TempDir(), "disk")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(testDiskSectors * SectorSize); err != nil {
		t.Fatal(err)
	}
	f.Close()

	parts := []Partition{{Name: "liken:machineState", FirstLBA: 2_048, LastLBA: 4_095, TypeGUID: LinuxFilesystemData}}
	err = Write(path, testDiskSectors, parts)
	if err == nil || !strings.Contains(err.Error(), "re-reading partition table") {
		t.Fatalf("expected the ioctl boundary failure: %v", err)
	}

	// The bytes made it regardless: the reader sees a valid table.
	r, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	table, err := ReadGPT(r, testDiskSectors)
	if err != nil {
		t.Fatal(err)
	}
	if len(table.Entries) != 1 || table.Entries[0].Name != "liken:machineState" {
		t.Errorf("the written table should read back: %+v", table.Entries)
	}
}

func TestWriteTableInPlaceSkipsTheKernelReread(t *testing.T) {
	// A regular file can't answer BLKRRPART, so success here proves
	// the in-place variant never asks: it writes the bytes and stops,
	// which is the whole point of having it (grow.go uses it when
	// nothing about the kernel's view of the partitions changes).
	path := filepath.Join(t.TempDir(), "disk")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(testDiskSectors * SectorSize); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if err := WriteTableInPlace(path, testDiskSectors, sampleTable()); err != nil {
		t.Fatalf("in-place write should succeed on a plain file: %v", err)
	}

	r, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	table, err := ReadGPT(r, testDiskSectors)
	if err != nil {
		t.Fatal(err)
	}
	if table.AlternateLBA != testDiskSectors-1 {
		t.Errorf("the backup should land on the last sector: %d", table.AlternateLBA)
	}
}

func TestGPTReaderRejectsForeignGeometry(t *testing.T) {
	f := diskFile(t, sampleTable(), testDiskSectors)
	// Rewrite the primary header claiming a 64-entry array, with a
	// recomputed (valid!) header CRC: structurally sound, but not a
	// table liken ever writes.
	h := make([]byte, SectorSize)
	if _, err := f.ReadAt(h, 1*SectorSize); err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(h[80:84], 64)
	clear(h[16:20])
	binary.LittleEndian.PutUint32(h[16:20], crc32.ChecksumIEEE(h[0:92]))
	if _, err := f.WriteAt(h, 1*SectorSize); err != nil {
		t.Fatal(err)
	}

	// The backup still parses, so the read succeeds from there; the
	// primary alone must be disqualified. Kill the backup to see the
	// primary's own error.
	if _, err := f.WriteAt([]byte{0xFF}, int64(testDiskSectors-1)*SectorSize+40); err != nil {
		t.Fatal(err)
	}
	_, err := ReadGPT(f, testDiskSectors)
	if err == nil || !strings.Contains(err.Error(), "geometry") {
		t.Errorf("expected a geometry refusal: %v", err)
	}
}

func TestWriteGPTTableReportsAMissingDevice(t *testing.T) {
	err := WriteTable(filepath.Join(t.TempDir(), "absent"), testDiskSectors, sampleTable())
	if err == nil {
		t.Error("a device that won't open is an error")
	}
}

func TestReadGPTWithBothCopiesUnreadable(t *testing.T) {
	// A blank device has neither table; the error names both failures
	// and the grown-disk caveat, because that is the one case the
	// backup can't cover.
	blank := bytes.NewReader(make([]byte, 4_096*SectorSize))
	_, err := ReadGPT(blank, 4_096)
	if err == nil || !strings.Contains(err.Error(), "neither partition table copy is readable") {
		t.Errorf("expected both copies to be reported: %v", err)
	}
}

func TestWriteGPTTableRefusesAnUnserializableTable(t *testing.T) {
	// Serialization is validated before the device is even opened, so
	// a table that can't be laid out never touches the disk.
	t1 := &Table{Entries: []Entry{{Name: strings.Repeat("x", NameChars+1), TypeGUID: LinuxFilesystemData}}}
	err := WriteTable(filepath.Join(t.TempDir(), "disk"), 4_096, t1)
	if err == nil || !strings.Contains(err.Error(), "exceeds GPT") {
		t.Errorf("expected the serialization refusal: %v", err)
	}
}

func TestSerializeGPTRefusesTooManyEntries(t *testing.T) {
	t1 := &Table{Entries: make([]Entry, entryCount+1)}
	_, err := SerializeGPT(t1, 1<<20)
	if err == nil || !strings.Contains(err.Error(), "won't fit") {
		t.Errorf("expected the entry-count refusal: %v", err)
	}
}

func TestMustGUIDPanicsOnAGarbageLiteral(t *testing.T) {
	// MustGUID guards the package's own constants: a bad literal is a
	// programming error that must fail at first use, not decode into
	// a wrong type GUID that every tool then misreads.
	defer func() {
		if recover() == nil {
			t.Error("a garbage literal must panic")
		}
	}()
	MustGUID("not-a-guid")
}
