package disks

import (
	"bytes"
	"testing"
)

// cutDisk is a disk image in memory that models a power cut. A write
// reaches the medium only at the next Sync. Until then the drive can
// put the writes on the medium in any order, so a cut can leave any
// subset of them there.
type cutDisk struct {
	medium  []byte
	pending []Chunk
	// cuts holds, for each moment between two operations, the medium
	// and the writes that a cut at that moment can leave on it.
	cuts []cutPoint
}

type cutPoint struct {
	medium  []byte
	pending []Chunk
}

func (d *cutDisk) WriteAt(p []byte, off int64) (int, error) {
	d.pending = append(d.pending, Chunk{LBA: uint64(off / SectorSize), Data: bytes.Clone(p)})
	d.record()
	return len(p), nil
}

func (d *cutDisk) Sync() error {
	for _, w := range d.pending {
		copy(d.medium[w.LBA*SectorSize:], w.Data)
	}
	d.pending = nil
	d.record()
	return nil
}

func (d *cutDisk) record() {
	d.cuts = append(d.cuts, cutPoint{medium: bytes.Clone(d.medium), pending: append([]Chunk(nil), d.pending...)})
}

// afterCut returns the medium with the writes that the bits of mask
// select from the pending writes.
func (c cutPoint) afterCut(mask int) []byte {
	medium := bytes.Clone(c.medium)
	for i, w := range c.pending {
		if mask&(1<<i) != 0 {
			copy(medium[w.LBA*SectorSize:], w.Data)
		}
	}
	return medium
}

// grownTable is one partition that fills a disk of the given size.
func grownTable(totalSectors uint64) *Table {
	return &Table{
		DiskGUID: MustGUID("11111111-2222-3333-4455-66778899AABB"),
		Entries: []Entry{{
			TypeGUID:   LinuxFilesystemData,
			UniqueGUID: MustGUID("AAAAAAAA-BBBB-CCCC-DDEE-FF0011223344"),
			FirstLBA:   PartitionAlignment,
			LastLBA:    LastUsableLBA(totalSectors),
			Name:       "liken:podStorage",
		}},
	}
}

// A disk that grew gets a table with a larger partition and its backup
// at the new end. A power cut at any moment of that write must leave
// one readable copy, the old table or the new one, because a disk
// with neither does not boot.
func TestAPowerCutDuringATableWriteLeavesOneReadableCopy(t *testing.T) {
	const smallSectors, grownSectors = 4096, 8192
	old, err := SerializeGPT(grownTable(smallSectors), smallSectors)
	if err != nil {
		t.Fatalf("serializing the old table: %v", err)
	}
	disk := &cutDisk{medium: make([]byte, grownSectors*SectorSize)}
	for _, chunk := range old {
		copy(disk.medium[chunk.LBA*SectorSize:], chunk.Data)
	}
	disk.record()
	chunks, err := SerializeGPT(grownTable(grownSectors), grownSectors)
	if err != nil {
		t.Fatalf("serializing the grown table: %v", err)
	}
	if err := writeChunks(disk, chunks); err != nil {
		t.Fatalf("writing the grown table: %v", err)
	}

	for moment, cut := range disk.cuts {
		for mask := range 1 << len(cut.pending) {
			if _, err := ReadGPT(bytes.NewReader(cut.afterCut(mask)), grownSectors); err != nil {
				t.Errorf("a cut after operation %d, with the pending writes %05b on the medium, leaves no readable table: %v", moment, mask, err)
			}
		}
	}
}
