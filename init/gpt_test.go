package main

// Tests for the GPT machinery: the layout arithmetic the partition
// planner builds on, and the reader/serializer pair that lets a
// table be edited in place. The round-trip tests are the backbone:
// what serializeGPT writes, readGPT must read back identically,
// GUIDs and all, because preserved identity is the whole point of
// having a reader.

import (
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
	if got := gptLastUsableLBA(4_194_304); got != 4_194_269 {
		t.Errorf("gptLastUsableLBA(4194304) = %d, want 4194269", got)
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
		if got := alignLBA(c.in); got != c.want {
			t.Errorf("alignLBA(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// sampleTable builds a two-partition table with distinct, fixed
// GUIDs, so tests can assert every identity survives a round trip.
func sampleTable() *gptTable {
	return &gptTable{
		diskGUID: mustGUID("11111111-2222-3333-4455-66778899AABB"),
		entries: []gptEntry{
			{
				typeGUID:   linuxFilesystemData,
				uniqueGUID: mustGUID("AAAAAAAA-BBBB-CCCC-DDEE-FF0011223344"),
				firstLBA:   2_048,
				lastLBA:    4_095,
				name:       "liken:machineState",
			},
			{
				typeGUID:   linuxFilesystemData,
				uniqueGUID: mustGUID("99999999-8888-7777-6655-443322110000"),
				firstLBA:   4_096,
				lastLBA:    100_000,
				name:       "liken:clusterState",
			},
		},
	}
}

// diskFile writes a table's chunks into a sparse file of the given
// sector count: the closest thing to a block device a test can have.
func diskFile(t *testing.T, table *gptTable, totalSectors uint64) *os.File {
	t.Helper()
	chunks, err := serializeGPT(table, totalSectors)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(t.TempDir(), "disk"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	if err := f.Truncate(int64(totalSectors) * sectorSize); err != nil {
		t.Fatal(err)
	}
	for _, chunk := range chunks {
		if _, err := f.WriteAt(chunk.data, int64(chunk.lba)*sectorSize); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

const testDiskSectors = 262_144 // 128 MiB

func TestGPTRoundTripPreservesEverything(t *testing.T) {
	want := sampleTable()
	f := diskFile(t, want, testDiskSectors)

	got, err := readGPT(f, testDiskSectors)
	if err != nil {
		t.Fatal(err)
	}
	if got.diskGUID != want.diskGUID {
		t.Error("the disk GUID did not survive the round trip")
	}
	if !slices.Equal(got.entries, want.entries) {
		t.Errorf("entries changed in the round trip:\ngot  %+v\nwant %+v", got.entries, want.entries)
	}
	if got.alternateLBA != testDiskSectors-1 {
		t.Errorf("alternateLBA = %d, want %d", got.alternateLBA, testDiskSectors-1)
	}
	if got.lastUsableLBA != gptLastUsableLBA(testDiskSectors) {
		t.Errorf("lastUsableLBA = %d, want %d", got.lastUsableLBA, gptLastUsableLBA(testDiskSectors))
	}
}

func TestGPTReaderRecoversFromACorruptPrimary(t *testing.T) {
	want := sampleTable()
	f := diskFile(t, want, testDiskSectors)
	// One flipped byte in the primary header breaks its checksum.
	if _, err := f.WriteAt([]byte{0xFF}, 1*sectorSize+40); err != nil {
		t.Fatal(err)
	}

	got, err := readGPT(f, testDiskSectors)
	if err != nil {
		t.Fatal(err)
	}
	if got.diskGUID != want.diskGUID || !slices.Equal(got.entries, want.entries) {
		t.Error("the backup copy should have supplied the whole table")
	}
}

func TestGPTReaderRejectsACorruptEntryArray(t *testing.T) {
	f := diskFile(t, sampleTable(), testDiskSectors)
	// Corrupt the primary's entry array AND the backup header: the
	// primary fails its entries CRC, the backup fails its header CRC,
	// and there is nothing left to trust.
	if _, err := f.WriteAt([]byte{0xFF}, 2*sectorSize+8); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0xFF}, int64(testDiskSectors-1)*sectorSize+40); err != nil {
		t.Fatal(err)
	}

	_, err := readGPT(f, testDiskSectors)
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
	other.diskGUID = mustGUID("DEADBEEF-DEAD-BEEF-DEAD-BEEFDEADBEEF")
	chunks, err := serializeGPT(other, testDiskSectors)
	if err != nil {
		t.Fatal(err)
	}
	for _, chunk := range chunks[3:] { // backup entries + backup header only
		if _, err := f.WriteAt(chunk.data, int64(chunk.lba)*sectorSize); err != nil {
			t.Fatal(err)
		}
	}

	got, err := readGPT(f, testDiskSectors)
	if err != nil {
		t.Fatal(err)
	}
	if got.diskGUID != want.diskGUID {
		t.Error("disagreement should resolve in the primary's favor")
	}
}

func TestSerializeGPTRelocatesTheBackupWhenTheDiskGrows(t *testing.T) {
	table := sampleTable()
	small := diskFile(t, table, testDiskSectors)
	read, err := readGPT(small, testDiskSectors)
	if err != nil {
		t.Fatal(err)
	}

	// Re-serialize the same table for a disk twice the size: the
	// backup must land at the new end, lastUsable must move out, and
	// nothing about the partitions themselves may change.
	const grownSectors = 2 * testDiskSectors
	grown := diskFile(t, read, grownSectors)
	reread, err := readGPT(grown, grownSectors)
	if err != nil {
		t.Fatal(err)
	}
	if reread.alternateLBA != grownSectors-1 {
		t.Errorf("backup header at %d, want %d", reread.alternateLBA, grownSectors-1)
	}
	if reread.lastUsableLBA != gptLastUsableLBA(grownSectors) {
		t.Errorf("lastUsable = %d, want %d", reread.lastUsableLBA, gptLastUsableLBA(grownSectors))
	}
	if reread.diskGUID != table.diskGUID || !slices.Equal(reread.entries, table.entries) {
		t.Error("growing the disk must not change any identity or extent")
	}

	// And the old backup location must not still parse as a header:
	// the stale copy at the old end is dead bytes... which is fine,
	// because nothing looks there anymore. Confirm the *new* end is
	// what readGPT consults by corrupting the primary.
	if _, err := grown.WriteAt([]byte{0xFF}, 1*sectorSize+40); err != nil {
		t.Fatal(err)
	}
	recovered, err := readGPT(grown, grownSectors)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(recovered.entries, table.entries) {
		t.Error("recovery should come from the relocated backup")
	}
}

func TestSerializeGPTRejectsOversizedNames(t *testing.T) {
	table := sampleTable()
	table.entries[0].name = strings.Repeat("x", 37)
	if _, err := serializeGPT(table, testDiskSectors); err == nil {
		t.Error("expected an error for a 37-character name")
	}
}

func TestGPTNamesUseTheWholeField(t *testing.T) {
	table := sampleTable()
	table.entries[0].name = strings.Repeat("n", 36) // exactly the field width: no NUL terminator on disk
	f := diskFile(t, table, testDiskSectors)
	got, err := readGPT(f, testDiskSectors)
	if err != nil {
		t.Fatal(err)
	}
	if got.entries[0].name != table.entries[0].name {
		t.Errorf("a 36-character name should survive: %q", got.entries[0].name)
	}
}

func TestWriteGPTWritesEverythingButNeedsARealDevice(t *testing.T) {
	// writeGPT against a regular file lays down every byte of the
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
	if err := f.Truncate(testDiskSectors * sectorSize); err != nil {
		t.Fatal(err)
	}
	f.Close()

	parts := []gptPartition{{name: "liken:machineState", firstLBA: 2_048, lastLBA: 4_095, typeGUID: linuxFilesystemData}}
	err = writeGPT(path, testDiskSectors, parts)
	if err == nil || !strings.Contains(err.Error(), "re-reading partition table") {
		t.Fatalf("expected the ioctl boundary failure: %v", err)
	}

	// The bytes made it regardless: the reader sees a valid table.
	r, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	table, err := readGPT(r, testDiskSectors)
	if err != nil {
		t.Fatal(err)
	}
	if len(table.entries) != 1 || table.entries[0].name != "liken:machineState" {
		t.Errorf("the written table should read back: %+v", table.entries)
	}
}

func TestGPTReaderRejectsForeignGeometry(t *testing.T) {
	f := diskFile(t, sampleTable(), testDiskSectors)
	// Rewrite the primary header claiming a 64-entry array, with a
	// recomputed (valid!) header CRC: structurally sound, but not a
	// table liken ever writes.
	h := make([]byte, sectorSize)
	if _, err := f.ReadAt(h, 1*sectorSize); err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(h[80:84], 64)
	clear(h[16:20])
	binary.LittleEndian.PutUint32(h[16:20], crc32.ChecksumIEEE(h[0:92]))
	if _, err := f.WriteAt(h, 1*sectorSize); err != nil {
		t.Fatal(err)
	}

	// The backup still parses, so the read succeeds from there; the
	// primary alone must be disqualified. Kill the backup to see the
	// primary's own error.
	if _, err := f.WriteAt([]byte{0xFF}, int64(testDiskSectors-1)*sectorSize+40); err != nil {
		t.Fatal(err)
	}
	_, err := readGPT(f, testDiskSectors)
	if err == nil || !strings.Contains(err.Error(), "geometry") {
		t.Errorf("expected a geometry refusal: %v", err)
	}
}
