package disks

// This file implements a FAT32 formatter, built from the
// specification.
//
// The system slots need FAT because the firmware reads them first.
// UEFI promises to understand exactly one filesystem, and this is
// it. Like the GPT (gpt.go), FAT32 is a small, fixed binary format,
// simple enough to write directly instead of shelling out to a tool.
// The whole filesystem is three structures:
//
//   - A boot sector that describes the geometry: how big a sector
//     is, how many sectors make a cluster (the allocation unit), and
//     where the tables live. Everything else is computed from these
//     numbers, so every reader (firmware, the kernel, this code)
//     derives the same layout from the same 90 bytes.
//
//   - The file allocation table itself, stored twice. The FAT is the
//     filesystem's entire record of allocation. It has one 32-bit
//     entry for each cluster. Each entry holds the number of the
//     next cluster in its file, forming a linked list stored in a
//     table, or holds an end-of-chain mark. The second copy is FAT's
//     only durability measure. There is no journal, only a spare
//     copy of the table.
//
//   - A root directory, which under FAT32 is an ordinary cluster
//     chain like any file's. By universal convention, it starts at
//     cluster 2. Clusters 0 and 1 do not exist. Their table entries
//     are repurposed as signature and health flags.
//
// What FAT lacks is also worth noting: no permissions, no owners, no
// symlinks, no journal. It is a format for data exchange between
// systems, which is why firmware standardized on it. This is also
// why the slots hold nothing but the OS artifacts themselves. Their
// integrity is proven by a digest, not trusted to the filesystem.

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

const (
	// fat32ReservedSectors is the number of sectors that precede the
	// first FAT. These sectors hold the boot sector, FSInfo, their
	// backups at sectors 6 and 7, and padding. 32 is the value that
	// every other formatter writes, and matching this convention
	// costs nothing.
	fat32ReservedSectors = 32

	fat32NumFATs = 2

	// fat32MinClusters is the cluster count below which a volume is
	// legally FAT16. Cluster count alone determines the FAT type.
	// Nothing in the header declares the type directly. A volume
	// below this count, even if formatted with FAT32 structures,
	// would be misread as FAT16 by any reader that follows the
	// specification.
	fat32MinClusters = 65_525
)

// fat32SectorsPerCluster picks the allocation unit from the volume's
// size, following the table in Microsoft's specification. The first
// line matters most. At one sector per cluster, a volume barely past
// the 65,525-cluster minimum fits in about 33 MiB. This is what lets
// small partitions, such as the GRUB boot home, be FAT32 at
// all. Above 260 MB, the specification increases the cluster size,
// to keep the allocation table from growing without limit. liken's
// half-gigabyte slots land on 8 sectors (4 KiB), the same choice
// that every other formatter makes.
func fat32SectorsPerCluster(totalSectors uint64) uint64 {
	switch {
	case totalSectors <= 532_480: // ≤ 260 MB
		return 1
	case totalSectors <= 16_777_216: // ≤ 8 GB
		return 8
	case totalSectors <= 33_554_432: // ≤ 16 GB
		return 16
	case totalSectors <= 67_108_864: // ≤ 32 GB
		return 32
	default:
		return 64
	}
}

// A Device is the surface that a format writes onto: positioned
// writes, plus a durability barrier. An *os.File is one example,
// either a partition device or a plain image file. A Section is
// another example: a partition's window inside an image file.
type Device interface {
	io.WriterAt
	Sync() error
}

// FormatFAT32 writes a fresh, empty FAT32 filesystem across the
// given device. The volume ID is FAT's only identity field; FAT has
// no UUIDs. The label is cosmetic, up to 11 bytes, and appears in
// directory listings on other machines. Set it to something that
// says whose partition this is.
//
// Write order does not matter for crash safety. FormatFAT32 only
// ever runs against a partition being claimed or an image being
// built. If a write is torn, the volume stays unformatted, and the
// job simply runs again.
func FormatFAT32(f Device, totalBytes uint64, label string, volumeID uint32) error {
	totalSectors := totalBytes / SectorSize
	sectorsPerCluster := fat32SectorsPerCluster(totalSectors)

	// This is Microsoft's own FAT-size formula, taken directly from
	// the specification. The table's size depends on the cluster
	// count, which depends on the space left after the table. The
	// formula resolves this circular dependency by slightly
	// overestimating. This wastes at most a sector or two of table
	// space that nothing will ever index.
	tmp1 := totalSectors - fat32ReservedSectors
	tmp2 := (256*sectorsPerCluster + fat32NumFATs) / 2
	fatSectors := (tmp1 + tmp2 - 1) / tmp2

	dataStart := uint64(fat32ReservedSectors) + fat32NumFATs*fatSectors
	if totalSectors < dataStart {
		return fmt.Errorf("partition is too small for FAT32: %d bytes leaves no room past the tables", totalBytes)
	}
	clusters := (totalSectors - dataStart) / sectorsPerCluster
	if clusters < fat32MinClusters {
		return fmt.Errorf(
			"partition is too small for FAT32: %d bytes yields %d clusters and FAT32 requires %d (about 33Mi at one sector per cluster); the FAT type is determined by cluster count, so a smaller volume would misparse as FAT16",
			totalBytes, clusters, fat32MinClusters)
	}
	// This is the upper limit. Cluster numbers at and above
	// 0x0FFFFFF5 are reserved marks, such as bad cluster or end of
	// chain, and are not addresses.
	if clusters >= 0x0FFFFFF5 {
		return fmt.Errorf("partition is too large for FAT32: %d clusters", clusters)
	}

	boot := buildFAT32BootSector(totalSectors, fatSectors, sectorsPerCluster, label, volumeID)
	// The FSInfo sector caches two hints: how many clusters are free,
	// and where to start looking for one. Readers are allowed to
	// distrust it, but a fresh count is exact: every cluster except
	// the root directory's one cluster.
	info := buildFAT32FSInfo(uint32(clusters-1), 3)

	// This code zeros both tables and the root directory's cluster
	// before it writes anything meaningful. A claimed partition
	// inherits whatever bytes the disk held before. Stale data where
	// a reader expects free entries is filesystem corruption.
	zeroStart := uint64(fat32ReservedSectors) * SectorSize
	zeroLen := (fat32NumFATs*fatSectors + sectorsPerCluster) * SectorSize
	if err := zeroRange(f, int64(zeroStart), int64(zeroLen)); err != nil {
		return fmt.Errorf("zeroing the allocation tables: %w", err)
	}

	// The first three FAT entries are flags, not chains. Entry 0
	// repeats the media byte. Entry 1 carries the clean-shutdown and
	// no-errors bits. Entry 2 ends the root directory's one-cluster
	// chain.
	head := make([]byte, 12)
	binary.LittleEndian.PutUint32(head[0:4], 0x0FFFFFF8)
	binary.LittleEndian.PutUint32(head[4:8], 0x0FFFFFFF)
	binary.LittleEndian.PutUint32(head[8:12], 0x0FFFFFFF)

	// The label lives in two places: the boot sector field above, and
	// a special entry in the root directory. This is a 32-byte
	// directory entry whose attribute byte (0x08) marks it as the
	// volume's name, not a file. Tools expect the two labels to
	// match. fsck flags a volume that has one without the other.
	labelEntry := make([]byte, 32)
	copy(labelEntry[0:11], "           ")
	copy(labelEntry[0:11], label)
	labelEntry[11] = 0x08

	// This writes primary and backup copies of everything. Writing
	// the backups is worth the cost. fsck tools do recover boot
	// sectors from sector 6.
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
		if _, err := f.WriteAt(w.data, int64(w.lba*SectorSize)); err != nil {
			return fmt.Errorf("writing sector %d: %w", w.lba, err)
		}
	}
	return f.Sync()
}

// buildFAT32BootSector lays out the 90 bytes of geometry that every
// FAT reader parses, padded to a full sector. The sector ends with
// the 0x55AA mark that distinguishes a structured sector from a
// blank one. An MBR ends with the same two bytes, which is why
// init's blank-disk check needs no special case for FAT.
func buildFAT32BootSector(totalSectors, fatSectors, sectorsPerCluster uint64, label string, volumeID uint32) []byte {
	bs := make([]byte, SectorSize)

	// This is a jump instruction over the parameter block, from the
	// era when the BIOS executed the boot sector's code. Nothing
	// executes it here, but readers check its shape as a sanity
	// check.
	bs[0], bs[1], bs[2] = 0xEB, 0x58, 0x90
	copy(bs[3:11], "liken   ")

	binary.LittleEndian.PutUint16(bs[11:13], SectorSize)
	bs[13] = byte(sectorsPerCluster)
	binary.LittleEndian.PutUint16(bs[14:16], fat32ReservedSectors)
	bs[16] = fat32NumFATs
	// Offsets 17-23 are the FAT12/16 fields: root entry count,
	// 16-bit totals, and 16-bit FAT size. All are zero on FAT32.
	// Zeroing them is one of the ways readers tell the FAT variants
	// apart. The media byte at offset 21 survives from the diskette
	// era. 0xF8 means "fixed disk" and must match FAT entry 0.
	bs[21] = 0xF8
	// These are sectors-per-track and head counts for CHS
	// addressing, which nothing has used in this century. These
	// values are conventional filler.
	binary.LittleEndian.PutUint16(bs[24:26], 32)
	binary.LittleEndian.PutUint16(bs[26:28], 64)
	// "Hidden sectors" records the partition's offset on its disk,
	// and only old CHS arithmetic reads it. Modern formatters write
	// zero for partitions addressed by LBA.
	binary.LittleEndian.PutUint32(bs[28:32], 0)
	binary.LittleEndian.PutUint32(bs[32:36], uint32(totalSectors))

	binary.LittleEndian.PutUint32(bs[36:40], uint32(fatSectors))
	// This sets the extension flags (mirroring on: writes go to both
	// FATs) and version 0.0, the only version that exists.
	binary.LittleEndian.PutUint16(bs[40:42], 0)
	binary.LittleEndian.PutUint16(bs[42:44], 0)
	// The root directory starts at cluster 2, because clusters 0 and
	// 1 do not exist. Their FAT entries are the flag words above.
	binary.LittleEndian.PutUint32(bs[44:48], 2)
	binary.LittleEndian.PutUint16(bs[48:50], 1) // FSInfo lives at sector 1
	binary.LittleEndian.PutUint16(bs[50:52], 6) // its backup, and the boot sector's, at 6
	// This writes 12 reserved bytes, then the extended boot signature
	// block: a BIOS drive number (0x80, first fixed disk), the 0x29
	// mark that says the three fields after it are present, the
	// volume's ID and label, and the type string. The type string is
	// informational only, but every formatter writes it, and some
	// tools read it.
	bs[64] = 0x80
	bs[66] = 0x29
	binary.LittleEndian.PutUint32(bs[67:71], volumeID)
	copy(bs[71:82], "           ")
	copy(bs[71:82], label)
	copy(bs[82:90], "FAT32   ")

	bs[510], bs[511] = 0x55, 0xAA
	return bs
}

// buildFAT32FSInfo lays out the free-space hint sector. It has three
// signature words, with the free-cluster count and the next-free
// hint between them.
func buildFAT32FSInfo(freeClusters, nextFree uint32) []byte {
	info := make([]byte, SectorSize)
	binary.LittleEndian.PutUint32(info[0:4], 0x41615252)
	binary.LittleEndian.PutUint32(info[484:488], 0x61417272)
	binary.LittleEndian.PutUint32(info[488:492], freeClusters)
	binary.LittleEndian.PutUint32(info[492:496], nextFree)
	info[510], info[511] = 0x55, 0xAA
	return info
}

// zeroRange writes zeros across a byte range in bounded chunks. This
// lets it zero a megabyte of allocation table without needing a
// megabyte of memory.
func zeroRange(f io.WriterAt, offset, length int64) error {
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

// HasFAT32 checks a device for a FAT32 filesystem that liken wrote:
// the boot signature plus the type string. Microsoft's specification
// warns that the type string does not determine the FAT type;
// cluster count does that. But the question here is narrower: has
// liken already formatted this volume? liken writes both marks
// itself, so checking for them is enough.
func HasFAT32(devPath string) bool {
	f, err := os.Open(devPath)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, SectorSize)
	if _, err := io.ReadFull(io.NewSectionReader(f, 0, SectorSize), head); err != nil {
		return false
	}
	return head[510] == 0x55 && head[511] == 0xAA && string(head[82:90]) == "FAT32   "
}
