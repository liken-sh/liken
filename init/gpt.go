package main

// Reading and writing GUID Partition Tables, from scratch.
//
// A partition table is not an artifact of some tool; it's a few
// hundred bytes at well-known offsets that the kernel (and firmware,
// and every OS) knows how to read. GPT, the modern format, is simple
// enough to handle directly, and doing so shows exactly what it is:
//
//	LBA 0        a "protective MBR": a legacy MBR whose single
//	             partition claims the whole disk, so old tools see
//	             "something's here" instead of "free space, help
//	             yourself"
//	LBA 1        the GPT header: where everything else is, plus CRC32
//	             checksums of itself and of the entry array
//	LBA 2..33    the partition entry array: 128 slots of 128 bytes,
//	             each naming a partition's type, unique GUID, extent,
//	             and 36-character name
//	...          the partitions themselves
//	end of disk  a mirror of the entries and header (in that order,
//	             header last at the very final sector), so a wrecked
//	             LBA 0/1 is survivable
//
// The 36-character partition *name* is the field liken cares most
// about: it carries a role's identity (liken:clusterState), which is
// what lets every boot after the first recognize a partition no
// matter what device name the kernel assigns the disk that day.
//
// Writing comes in two flavors. Claiming a blank disk creates a table
// from nothing, minting fresh GUIDs. Growing a partition *edits* the
// table that exists, which is why there's a reader here too: an edit
// must carry every identity (the disk's GUID, each partition's unique
// GUID) through unchanged. Those GUIDs are how other tools tell disks
// and partitions apart; they are not ours to refresh.

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"slices"
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

	// GPT names are UTF-16LE (UEFI predates the industry settling on
	// UTF-8) in a fixed 72-byte field: 36 code units.
	gptNameChars = 36
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

// A gptPartition is one entry as liken declares it when claiming; the
// type GUID and a fresh unique GUID are filled in at write time.
type gptPartition struct {
	name     string
	firstLBA uint64
	lastLBA  uint64
}

// A gptEntry is one occupied slot of the entry array, everything the
// disk records about a partition. An edit preserves each field
// byte-for-byte except the extent it means to change.
type gptEntry struct {
	typeGUID   [16]byte
	uniqueGUID [16]byte
	firstLBA   uint64
	lastLBA    uint64
	attributes uint64
	name       string
}

// A gptTable is a whole partition table in memory: what readGPT
// returns and serializeGPT lays out. The two header facts are kept
// because they're how a grown disk announces itself: a table whose
// alternate (backup) header is no longer at the disk's final sector
// was written when the disk was smaller.
type gptTable struct {
	diskGUID      [16]byte
	entries       []gptEntry
	lastUsableLBA uint64
	alternateLBA  uint64
}

// A gptChunk is one run of bytes at one location: the unit
// serialization produces and writing consumes.
type gptChunk struct {
	lba  uint64
	data []byte
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

// serializeGPT is the pure half of writing: a table in, the five
// on-disk chunks out (protective MBR, primary header, entries, backup
// entries, backup header), with both CRCs computed. totalSectors
// decides where the backup lands and what lastUsable becomes, which
// is exactly how serializing a table read from a smaller disk
// relocates its backup to the new end.
func serializeGPT(t *gptTable, totalSectors uint64) ([]gptChunk, error) {
	if len(t.entries) > gptEntryCount {
		return nil, fmt.Errorf("%d partitions won't fit a %d-entry GPT", len(t.entries), gptEntryCount)
	}

	// The entry array first, because both headers embed its checksum.
	entries := make([]byte, gptEntryCount*gptEntrySize)
	for i, p := range t.entries {
		e := entries[i*gptEntrySize:]
		copy(e[0:16], p.typeGUID[:])
		copy(e[16:32], p.uniqueGUID[:])
		binary.LittleEndian.PutUint64(e[32:40], p.firstLBA)
		binary.LittleEndian.PutUint64(e[40:48], p.lastLBA)
		binary.LittleEndian.PutUint64(e[48:56], p.attributes)

		name := utf16.Encode([]rune(p.name))
		if len(name) > gptNameChars {
			return nil, fmt.Errorf("partition name %q exceeds GPT's %d characters", p.name, gptNameChars)
		}
		for j, r := range name {
			binary.LittleEndian.PutUint16(e[56+2*j:], r)
		}
	}
	entriesCRC := crc32.ChecksumIEEE(entries)

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
		copy(h[56:72], t.diskGUID[:])
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

	return []gptChunk{
		{0, mbr},
		{1, header(1, backupHeaderLBA, 2)},
		{2, entries},
		{backupEntriesLBA, entries},
		{backupHeaderLBA, header(backupHeaderLBA, 1, backupEntriesLBA)},
	}, nil
}

// readGPTCopy parses one copy of the table from its header sector,
// verifying both checksums, or explains what disqualified it.
func readGPTCopy(r io.ReaderAt, headerLBA uint64) (*gptTable, error) {
	h := make([]byte, sectorSize)
	if _, err := r.ReadAt(h, int64(headerLBA)*sectorSize); err != nil {
		return nil, fmt.Errorf("reading the header at sector %d: %w", headerLBA, err)
	}
	if string(h[0:8]) != "EFI PART" {
		return nil, fmt.Errorf("no GPT signature at sector %d", headerLBA)
	}

	// The header's CRC covers headerSize bytes with the CRC field
	// itself zeroed; the size is read from the header (92 today, but
	// the format allows more) so the checksum covers what the writer
	// covered.
	headerSize := binary.LittleEndian.Uint32(h[12:16])
	if headerSize < 92 || headerSize > sectorSize {
		return nil, fmt.Errorf("implausible header size %d at sector %d", headerSize, headerLBA)
	}
	scratch := slices.Clone(h[:headerSize])
	clear(scratch[16:20])
	if crc32.ChecksumIEEE(scratch) != binary.LittleEndian.Uint32(h[16:20]) {
		return nil, fmt.Errorf("header checksum mismatch at sector %d", headerLBA)
	}

	// liken only ever writes the standard 128×128 array; a table with
	// other geometry wasn't ours, and a foreign disk was already
	// refused at claim time, so refuse the surprise here too.
	entryCount := binary.LittleEndian.Uint32(h[80:84])
	entrySize := binary.LittleEndian.Uint32(h[84:88])
	if entryCount != gptEntryCount || entrySize != gptEntrySize {
		return nil, fmt.Errorf("unexpected entry geometry %d×%d at sector %d", entryCount, entrySize, headerLBA)
	}

	entriesLBA := binary.LittleEndian.Uint64(h[72:80])
	entries := make([]byte, entryCount*entrySize)
	if _, err := r.ReadAt(entries, int64(entriesLBA)*sectorSize); err != nil {
		return nil, fmt.Errorf("reading the entry array at sector %d: %w", entriesLBA, err)
	}
	if crc32.ChecksumIEEE(entries) != binary.LittleEndian.Uint32(h[88:92]) {
		return nil, fmt.Errorf("entry array checksum mismatch (header at sector %d)", headerLBA)
	}

	t := &gptTable{
		lastUsableLBA: binary.LittleEndian.Uint64(h[48:56]),
		alternateLBA:  binary.LittleEndian.Uint64(h[32:40]),
	}
	copy(t.diskGUID[:], h[56:72])

	for i := range int(entryCount) {
		e := entries[i*int(entrySize):]
		var typeGUID [16]byte
		copy(typeGUID[:], e[0:16])
		if typeGUID == ([16]byte{}) {
			continue // an all-zero type GUID marks an unused slot
		}
		p := gptEntry{
			typeGUID:   typeGUID,
			firstLBA:   binary.LittleEndian.Uint64(e[32:40]),
			lastLBA:    binary.LittleEndian.Uint64(e[40:48]),
			attributes: binary.LittleEndian.Uint64(e[48:56]),
		}
		copy(p.uniqueGUID[:], e[16:32])
		var units []uint16
		for j := range gptNameChars {
			u := binary.LittleEndian.Uint16(e[56+2*j:])
			if u == 0 {
				break
			}
			units = append(units, u)
		}
		p.name = string(utf16.Decode(units))
		t.entries = append(t.entries, p)
	}
	return t, nil
}

// readGPT parses a device's partition table, trying the primary copy
// first and falling back to the backup. Disagreements resolve in the
// primary's favor: the kernel read the primary too, so recognition
// already trusted it, and the next rewrite reconciles the pair.
func readGPT(r io.ReaderAt, totalSectors uint64) (*gptTable, error) {
	primary, perr := readGPTCopy(r, 1)
	backup, berr := readGPTCopy(r, totalSectors-1)

	switch {
	case perr == nil && berr == nil:
		if primary.diskGUID != backup.diskGUID || !slices.Equal(primary.entries, backup.entries) {
			fmt.Println("liken: storage: the primary and backup partition tables disagree; trusting the primary (so did the kernel)")
		}
		return primary, nil
	case perr == nil:
		fmt.Printf("liken: storage: the backup partition table is unreadable (%v); the next rewrite restores it\n", berr)
		return primary, nil
	case berr == nil:
		fmt.Printf("liken: storage: the primary partition table is unreadable (%v); recovered from the backup\n", perr)
		return backup, nil
	default:
		// The one corner both copies can't save you from: if the disk
		// was grown while the primary was already dead, the backup is
		// stranded mid-disk where nothing can find it.
		return nil, fmt.Errorf("neither partition table copy is readable: primary: %v; backup at sector %d: %v (a grown disk's backup is no longer at the end)",
			perr, totalSectors-1, berr)
	}
}

// writeGPTTable is the I/O half of writing: serialize the table for
// this disk size, put the chunks where they go, then ask the kernel
// to re-read the result. Deliberately thin; everything interesting
// happened in serializeGPT.
func writeGPTTable(devPath string, totalSectors uint64, t *gptTable) error {
	chunks, err := serializeGPT(t, totalSectors)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(devPath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, chunk := range chunks {
		if _, err := f.WriteAt(chunk.data, int64(chunk.lba)*sectorSize); err != nil {
			return fmt.Errorf("writing at LBA %d: %w", chunk.lba, err)
		}
	}
	if err := f.Sync(); err != nil {
		return err
	}

	// The bytes are on disk, but the kernel's view of the device
	// predates them; this ioctl asks it to re-read the table, which
	// is what makes the vda1, vda2, ... devices appear (or grow).
	if _, err := unix.IoctlRetInt(int(f.Fd()), unix.BLKRRPART); err != nil {
		return fmt.Errorf("re-reading partition table: %w", err)
	}
	return nil
}

// writeGPT lays a brand-new partition table onto a blank disk being
// claimed: fresh GUIDs for the disk and every partition, because a
// claim is the moment these identities are born.
func writeGPT(devPath string, totalSectors uint64, parts []gptPartition) error {
	t := &gptTable{diskGUID: randomGUID()}
	for _, p := range parts {
		t.entries = append(t.entries, gptEntry{
			typeGUID:   linuxFilesystemData,
			uniqueGUID: randomGUID(),
			firstLBA:   p.firstLBA,
			lastLBA:    p.lastLBA,
			name:       p.name,
		})
	}
	return writeGPTTable(devPath, totalSectors, t)
}
