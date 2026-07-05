package main

// Writing GUID Partition Tables, from scratch.
//
// A partition table is not an artifact of some tool; it's a few
// hundred bytes at well-known offsets that the kernel (and firmware,
// and every OS) knows how to read. GPT, the modern format, is simple
// enough to write directly, and writing it shows exactly what it is:
//
//   LBA 0        a "protective MBR": a legacy MBR whose single
//                partition claims the whole disk, so old tools see
//                "something's here" instead of "free space, help
//                yourself"
//   LBA 1        the GPT header: where everything else is, plus CRC32
//                checksums of itself and of the entry array
//   LBA 2..33    the partition entry array: 128 slots of 128 bytes,
//                each naming a partition's type, unique GUID, extent,
//                and 36-character name
//   ...          the partitions themselves
//   end of disk  a mirror of the entries and header (in that order,
//                header last at the very final sector), so a wrecked
//                LBA 0/1 is survivable
//
// The 36-character partition *name* is the field liken cares most
// about: it carries a role's identity (liken:clusterState), which is
// what lets every boot after the first recognize a partition no
// matter what device name the kernel assigns the disk that day.

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"unicode/utf16"

	"golang.org/x/sys/unix"
)

const (
	sectorSize = 512

	// The entry array's size is part of the format: 128 entries of 128
	// bytes = 32 sectors. With the MBR and header ahead of it, the
	// first sector a partition may use is 34; mirrored at the tail, the
	// last usable sector is 34 from the end.
	gptEntryCount   = 128
	gptEntrySize    = 128
	gptEntrySectors = gptEntryCount * gptEntrySize / sectorSize
	gptReservedLBAs = 2 + gptEntrySectors

	// Partitions start on 1MiB boundaries (2048 sectors), the
	// alignment every modern partitioner uses, chosen so partitions
	// align with any plausible physical block or RAID stripe beneath.
	partitionAlignment = 2048
)

// gptLastUsableLBA is the last sector a partition may occupy: the
// backup entry array and header claim the tail of the disk.
func gptLastUsableLBA(totalSectors uint64) uint64 {
	return totalSectors - gptReservedLBAs - 1
}

// alignLBA rounds a sector number up to the next 1MiB boundary.
func alignLBA(lba uint64) uint64 {
	return (lba + partitionAlignment - 1) / partitionAlignment * partitionAlignment
}

// A gptPartition is one entry as liken declares it; the type GUID and
// a fresh unique GUID are filled in at write time.
type gptPartition struct {
	name     string
	firstLBA uint64
	lastLBA  uint64
}

// linuxFilesystemData is the partition type GUID meaning "an ordinary
// Linux filesystem". Types are well-known constants, not invented:
// this exact GUID is what lsblk, blkid, and installers everywhere
// recognize as a Linux data partition.
var linuxFilesystemData = mustGUID("0FC63DAF-8483-4772-8E79-3D69D8477DE4")

// mustGUID turns a GUID's canonical text into its 16 on-disk bytes.
// The encoding is a historical wart: the first three fields are
// little-endian (GUIDs come from Microsoft, via UEFI) while the rest
// is byte-for-byte, so the text and the bytes read differently, and
// getting this wrong makes every tool misread the type.
func mustGUID(s string) [16]byte {
	var canonical [16]byte
	n, err := fmt.Sscanf(s,
		"%02X%02X%02X%02X-%02X%02X-%02X%02X-%02X%02X-%02X%02X%02X%02X%02X%02X",
		&canonical[0], &canonical[1], &canonical[2], &canonical[3],
		&canonical[4], &canonical[5], &canonical[6], &canonical[7],
		&canonical[8], &canonical[9], &canonical[10], &canonical[11],
		&canonical[12], &canonical[13], &canonical[14], &canonical[15])
	if n != 16 || err != nil {
		panic("bad GUID literal: " + s)
	}
	return guidToDisk(canonical)
}

func guidToDisk(canonical [16]byte) [16]byte {
	return [16]byte{
		canonical[3], canonical[2], canonical[1], canonical[0],
		canonical[5], canonical[4],
		canonical[7], canonical[6],
		canonical[8], canonical[9],
		canonical[10], canonical[11], canonical[12], canonical[13], canonical[14], canonical[15],
	}
}

// randomGUID generates a version-4 (random) GUID in on-disk encoding,
// for the disk itself and each partition, so every one is globally
// distinguishable from every other disk and partition in the world.
func randomGUID() [16]byte {
	var canonical [16]byte
	if _, err := rand.Read(canonical[:]); err != nil {
		// getrandom failing on this kernel would have already hung
		// the boot at DHCP, so this can't be reached; panic if it
		// somehow is.
		panic(err)
	}
	canonical[6] = canonical[6]&0x0F | 0x40 // version 4: random
	canonical[8] = canonical[8]&0x3F | 0x80 // variant: RFC 4122
	return guidToDisk(canonical)
}

// writeGPT lays a complete partition table onto a device: protective
// MBR, primary header and entries, backup entries and header, then
// asks the kernel to re-read the result.
func writeGPT(devPath string, totalSectors uint64, parts []gptPartition) error {
	if len(parts) > gptEntryCount {
		return fmt.Errorf("%d partitions won't fit a %d-entry GPT", len(parts), gptEntryCount)
	}

	// The entry array first, because both headers embed its checksum.
	entries := make([]byte, gptEntryCount*gptEntrySize)
	for i, p := range parts {
		e := entries[i*gptEntrySize:]
		copy(e[0:16], linuxFilesystemData[:])
		unique := randomGUID()
		copy(e[16:32], unique[:])
		binary.LittleEndian.PutUint64(e[32:40], p.firstLBA)
		binary.LittleEndian.PutUint64(e[40:48], p.lastLBA)
		// e[48:56] attributes: none apply to plain data partitions.

		// The name is UTF-16LE (UEFI predates the industry settling
		// on UTF-8) in a fixed 72-byte (36-character) field.
		name := utf16.Encode([]rune(p.name))
		if len(name) > 36 {
			return fmt.Errorf("partition name %q exceeds GPT's 36 characters", p.name)
		}
		for j, r := range name {
			binary.LittleEndian.PutUint16(e[56+2*j:], r)
		}
	}
	entriesCRC := crc32.ChecksumIEEE(entries)

	diskGUID := randomGUID()
	backupHeaderLBA := totalSectors - 1
	backupEntriesLBA := totalSectors - 1 - gptEntrySectors

	// Each header names its own location and its twin's; the backup is
	// not a byte copy but the same facts from the other end of the disk.
	header := func(currentLBA, otherLBA, entriesLBA uint64) []byte {
		h := make([]byte, sectorSize)
		copy(h[0:8], "EFI PART")
		binary.LittleEndian.PutUint32(h[8:12], 0x0001_0000) // revision 1.0
		binary.LittleEndian.PutUint32(h[12:16], 92)         // header size
		// h[16:20] is the header's own CRC, computed over the 92 bytes
		// with this field zeroed, then patched in.
		binary.LittleEndian.PutUint64(h[24:32], currentLBA)
		binary.LittleEndian.PutUint64(h[32:40], otherLBA)
		binary.LittleEndian.PutUint64(h[40:48], gptReservedLBAs)
		binary.LittleEndian.PutUint64(h[48:56], gptLastUsableLBA(totalSectors))
		copy(h[56:72], diskGUID[:])
		binary.LittleEndian.PutUint64(h[72:80], entriesLBA)
		binary.LittleEndian.PutUint32(h[80:84], gptEntryCount)
		binary.LittleEndian.PutUint32(h[84:88], gptEntrySize)
		binary.LittleEndian.PutUint32(h[88:92], entriesCRC)
		binary.LittleEndian.PutUint32(h[16:20], crc32.ChecksumIEEE(h[0:92]))
		return h
	}

	// The protective MBR: one legacy partition of type 0xEE spanning
	// the disk (capped at the 32-bit sector count MBR can express), so
	// GPT-unaware tools refuse to touch it rather than see empty space.
	mbr := make([]byte, sectorSize)
	span := min(totalSectors-1, 0xFFFF_FFFF)
	entry := mbr[446:]
	entry[1], entry[2], entry[3] = 0x00, 0x02, 0x00 // CHS start: legacy filler
	entry[4] = 0xEE                                 // type: "GPT protective"
	entry[5], entry[6], entry[7] = 0xFF, 0xFF, 0xFF // CHS end: "beyond CHS"
	binary.LittleEndian.PutUint32(entry[8:12], 1)
	binary.LittleEndian.PutUint32(entry[12:16], uint32(span))
	mbr[510], mbr[511] = 0x55, 0xAA

	f, err := os.OpenFile(devPath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, chunk := range []struct {
		lba  uint64
		data []byte
	}{
		{0, mbr},
		{1, header(1, backupHeaderLBA, 2)},
		{2, entries},
		{backupEntriesLBA, entries},
		{backupHeaderLBA, header(backupHeaderLBA, 1, backupEntriesLBA)},
	} {
		if _, err := f.WriteAt(chunk.data, int64(chunk.lba)*sectorSize); err != nil {
			return fmt.Errorf("writing at LBA %d: %w", chunk.lba, err)
		}
	}
	if err := f.Sync(); err != nil {
		return err
	}

	// The bytes are on disk, but the kernel's view of the device
	// predates them; this ioctl asks it to re-read the table, which
	// is what makes the vda1, vda2, ... devices appear.
	if _, err := unix.IoctlRetInt(int(f.Fd()), unix.BLKRRPART); err != nil {
		return fmt.Errorf("re-reading partition table: %w", err)
	}
	return nil
}
