package main

// A FAT32 formatter, from the specification.
//
// The system slots need FAT because the firmware is their first
// reader: UEFI promises to understand exactly one filesystem, and
// this is it. Like the GPT (gpt.go), FAT32 is a small, fixed binary
// format, simple enough to write directly rather than by shelling
// out to a tool. The whole filesystem is three structures:
//
//   - A boot sector describing the geometry: how big a sector is, how
//     many sectors make a cluster (the allocation unit), where the
//     tables live. Everything else is computed from these numbers, so
//     every reader (firmware, the kernel, this code) derives the same
//     layout from the same 90 bytes.
//
//   - The file allocation table itself, twice. The FAT is the
//     filesystem's entire notion of allocation: one 32-bit entry per
//     cluster, each holding the number of the next cluster in its
//     file (a linked list drawn in a table) or an end-of-chain mark.
//     The second copy is FAT's only durability measure: there is no
//     journal, just a spare copy of the table.
//
//   - A root directory, which under FAT32 is an ordinary cluster
//     chain like any file's, starting (by universal convention) at
//     cluster 2. Clusters 0 and 1 don't exist; their table entries
//     are repurposed as signature and health flags.
//
// What FAT lacks is worth noticing too: no permissions, no owners,
// no symlinks, no journal. It is a format for interchange, which is
// why firmware standardized on it. It is also why the slots hold
// nothing but the OS artifacts themselves, whose integrity is proven
// by digest rather than trusted to the filesystem.

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/chrisguidry/liken/machine"
)

const (
	// fat32SectorsPerCluster fixes the allocation unit at 4 KiB.
	// Cluster size is a real tradeoff on big volumes (slack space
	// against table size), but slots are half a gigabyte and hold a
	// handful of large files; 4 KiB keeps the math simple and the
	// cluster count comfortably in FAT32 territory.
	fat32SectorsPerCluster = 8

	// fat32ReservedSectors is how many sectors precede the first FAT:
	// the boot sector, FSInfo, their backups at sectors 6 and 7, and
	// padding. 32 is the value everything else writes, and matching
	// convention costs nothing.
	fat32ReservedSectors = 32

	fat32NumFATs = 2

	// fat32MinClusters is the line below which a volume is legally
	// FAT16. The FAT *type* is determined by cluster count alone;
	// nothing in the header declares the type authoritatively. A
	// volume below this line but formatted with FAT32 structures
	// would be misparsed as FAT16 by any spec-faithful reader.
	fat32MinClusters = 65_525
)

// formatFAT32 writes a fresh, empty FAT32 filesystem across the given
// device or file. The volume ID is FAT's only identity field (there
// are no UUIDs here). The label is cosmetic, up to 11 bytes, and
// shows up in directory listings on other machines, so it is worth
// setting to something that says whose partition this is.
//
// Ordering doesn't matter for crash safety: this only ever runs
// against a partition being claimed, where a torn write leaves the
// partition unformatted and the next boot simply formats it again
// (recognition is by GPT name, which went on first).
func formatFAT32(f *os.File, totalBytes uint64, label string, volumeID uint32) error {
	totalSectors := totalBytes / sectorSize

	// Microsoft's own FAT-size formula, verbatim from the
	// specification. The table's size depends on the cluster count,
	// which depends on the space left after the table; the formula
	// resolves that circularity by slightly overestimating, wasting
	// at most a sector or two of table that nothing will ever index.
	tmp1 := totalSectors - fat32ReservedSectors
	tmp2 := uint64(256*fat32SectorsPerCluster+fat32NumFATs) / 2
	fatSectors := (tmp1 + tmp2 - 1) / tmp2

	dataStart := uint64(fat32ReservedSectors) + fat32NumFATs*fatSectors
	if totalSectors < dataStart {
		return fmt.Errorf("partition is too small for FAT32: %d bytes leaves no room past the tables", totalBytes)
	}
	clusters := (totalSectors - dataStart) / fat32SectorsPerCluster
	if clusters < fat32MinClusters {
		return fmt.Errorf(
			"partition is too small for FAT32: %d bytes yields %d clusters and FAT32 requires %d (about 260Mi); the FAT type is determined by cluster count, so a smaller volume would misparse as FAT16",
			totalBytes, clusters, fat32MinClusters)
	}
	// The far limit: cluster numbers at and above 0x0FFFFFF5 are
	// reserved marks (bad cluster, end of chain), not addresses.
	if clusters >= 0x0FFFFFF5 {
		return fmt.Errorf("partition is too large for FAT32: %d clusters", clusters)
	}

	boot := buildFAT32BootSector(totalSectors, fatSectors, label, volumeID)
	// The FSInfo sector is a hint cache: how many clusters are free
	// and where to start looking. Readers are allowed to distrust it,
	// but a fresh count is exact: every cluster except the root
	// directory's one.
	info := buildFAT32FSInfo(uint32(clusters-1), 3)

	// Zero both tables and the root directory's cluster before
	// writing anything meaningful: a claimed partition inherits
	// whatever bytes the disk held before, and stale data where a
	// reader expects free entries is filesystem corruption.
	zeroStart := uint64(fat32ReservedSectors) * sectorSize
	zeroLen := (fat32NumFATs*fatSectors + fat32SectorsPerCluster) * sectorSize
	if err := zeroRange(f, int64(zeroStart), int64(zeroLen)); err != nil {
		return fmt.Errorf("zeroing the allocation tables: %w", err)
	}

	// The first three FAT entries are flags, not chains: entry 0
	// echoes the media byte, entry 1 carries the clean-shutdown and
	// no-errors bits, and entry 2 ends the root directory's one-
	// cluster chain.
	head := make([]byte, 12)
	binary.LittleEndian.PutUint32(head[0:4], 0x0FFFFFF8)
	binary.LittleEndian.PutUint32(head[4:8], 0x0FFFFFFF)
	binary.LittleEndian.PutUint32(head[8:12], 0x0FFFFFFF)

	// The label lives in two places: the boot sector field above, and
	// a special entry in the root directory, a 32-byte directory
	// entry whose attribute byte (0x08) marks it as the volume's name
	// rather than a file. Tools expect the pair to agree; fsck flags
	// a volume that has one without the other.
	labelEntry := make([]byte, 32)
	copy(labelEntry[0:11], "           ")
	copy(labelEntry[0:11], label)
	labelEntry[11] = 0x08

	// Primary and backup copies of everything. The backups are worth
	// writing: fsck tools really do recover boot sectors from
	// sector 6.
	writes := []struct {
		lba  uint64
		data []byte
	}{
		{0, boot},
		{1, info},
		{6, boot},
		{7, info},
		{fat32ReservedSectors, head},
		{fat32ReservedSectors + fatSectors, head},
		{dataStart, labelEntry},
	}
	for _, w := range writes {
		if _, err := f.WriteAt(w.data, int64(w.lba*sectorSize)); err != nil {
			return fmt.Errorf("writing sector %d: %w", w.lba, err)
		}
	}
	return f.Sync()
}

// buildFAT32BootSector lays out the 90 bytes of geometry every FAT
// reader parses, padded to a full sector ending in the 0x55AA mark
// that distinguishes a structured sector from a blank one. Those are
// the same two bytes an MBR ends with, which is why isBlank needs no
// special case for FAT.
func buildFAT32BootSector(totalSectors, fatSectors uint64, label string, volumeID uint32) []byte {
	bs := make([]byte, sectorSize)

	// A jump instruction over the parameter block, from the era when
	// the boot sector's code was executed by the BIOS. Nothing
	// executes it here, but readers check its shape as a sanity mark.
	bs[0], bs[1], bs[2] = 0xEB, 0x58, 0x90
	copy(bs[3:11], "liken   ")

	binary.LittleEndian.PutUint16(bs[11:13], sectorSize)
	bs[13] = fat32SectorsPerCluster
	binary.LittleEndian.PutUint16(bs[14:16], fat32ReservedSectors)
	bs[16] = fat32NumFATs
	// Offsets 17-23 are the FAT12/16 fields (root entry count,
	// 16-bit totals, 16-bit FAT size), all zero on FAT32; zeroing
	// them is one of the ways readers tell the FAT variants apart.
	// The media byte at 21 survives from the diskette era; 0xF8 means
	// "fixed disk" and must match FAT entry 0.
	bs[21] = 0xF8
	// Sectors-per-track and head counts for CHS addressing, which
	// nothing has used this century; these are conventional filler
	// values.
	binary.LittleEndian.PutUint16(bs[24:26], 32)
	binary.LittleEndian.PutUint16(bs[26:28], 64)
	// "Hidden sectors": the partition's offset on its disk, consulted
	// only by ancient CHS arithmetic. Zero is what modern formatters
	// write for partitions addressed by LBA.
	binary.LittleEndian.PutUint32(bs[28:32], 0)
	binary.LittleEndian.PutUint32(bs[32:36], uint32(totalSectors))

	binary.LittleEndian.PutUint32(bs[36:40], uint32(fatSectors))
	// Extension flags (mirroring on: writes go to both FATs) and
	// version 0.0, the only version that exists.
	binary.LittleEndian.PutUint16(bs[40:42], 0)
	binary.LittleEndian.PutUint16(bs[42:44], 0)
	// The root directory starts at cluster 2 because clusters 0 and 1
	// don't exist; their FAT entries are the flag words above.
	binary.LittleEndian.PutUint32(bs[44:48], 2)
	binary.LittleEndian.PutUint16(bs[48:50], 1) // FSInfo lives at sector 1
	binary.LittleEndian.PutUint16(bs[50:52], 6) // its backup, and the boot sector's, at 6
	// 12 reserved bytes, then the extended boot signature block: a
	// BIOS drive number (0x80, first fixed disk), the 0x29 mark that
	// says the three fields after it are present, the volume's ID and
	// label, and the type string. The type string is informational
	// only, but everything writes it and some tools read it.
	bs[64] = 0x80
	bs[66] = 0x29
	binary.LittleEndian.PutUint32(bs[67:71], volumeID)
	copy(bs[71:82], "           ")
	copy(bs[71:82], label)
	copy(bs[82:90], "FAT32   ")

	bs[510], bs[511] = 0x55, 0xAA
	return bs
}

// buildFAT32FSInfo lays out the free-space hint sector: three
// signature words with the free-cluster count and the next-free hint
// between them.
func buildFAT32FSInfo(freeClusters, nextFree uint32) []byte {
	info := make([]byte, sectorSize)
	binary.LittleEndian.PutUint32(info[0:4], 0x41615252)
	binary.LittleEndian.PutUint32(info[484:488], 0x61417272)
	binary.LittleEndian.PutUint32(info[488:492], freeClusters)
	binary.LittleEndian.PutUint32(info[492:496], nextFree)
	info[510], info[511] = 0x55, 0xAA
	return info
}

// zeroRange writes zeroes across a byte range in bounded chunks, so
// zeroing a megabyte of allocation table doesn't ask for a megabyte
// of memory.
func zeroRange(f *os.File, offset, length int64) error {
	zeros := make([]byte, 256<<10)
	for length > 0 {
		n := min(length, int64(len(zeros)))
		if _, err := f.WriteAt(zeros[:n], offset); err != nil {
			return err
		}
		offset += n
		length -= n
	}
	return nil
}

// formatSlot formats a system slot's partition, labeled for the role
// it serves so the slot identifies itself in any directory listing.
// The volume ID (FAT's only identity field; there are no UUIDs) is
// derived from a timestamp, the traditional choice.
func formatSlot(devPath string, sizeBytes uint64, role machine.StorageRoleName) error {
	f, err := os.OpenFile(devPath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	label := "LIKEN-SYS-A"
	if role == machine.SystemBRole {
		label = "LIKEN-SYS-B"
	}
	return formatFAT32(f, sizeBytes, label, uint32(time.Now().Unix()))
}

// hasFAT32 checks a device for a FAT32 filesystem liken wrote: the
// boot signature plus the type string. Microsoft's specification
// warns that the type string is not how the FAT *type* is determined
// (cluster count is), but the question here is narrower: has liken
// already formatted this slot? liken writes both marks itself, so
// checking for them is enough.
func hasFAT32(devPath string) bool {
	f, err := os.Open(devPath)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, sectorSize)
	if _, err := io.ReadFull(io.NewSectionReader(f, 0, sectorSize), head); err != nil {
		return false
	}
	return head[510] == 0x55 && head[511] == 0xAA && string(head[82:90]) == "FAT32   "
}
