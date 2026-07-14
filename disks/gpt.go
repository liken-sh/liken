// Package disks writes and reads the on-disk formats liken deals in
// directly: GUID Partition Tables and FAT32 filesystems.
//
// These are the two formats firmware itself understands, which is why
// liken handles them by hand instead of shelling out to tools: a
// machine's first boot has no tools, and an install image is built on
// a workstation that shouldn't need any either. Both formats are a
// few hundred bytes at well-known offsets, simple enough that writing
// them directly shows exactly what they are.
//
// Two consumers share this package: init, which claims and grows a
// machine's real disks from PID 1, and the image package, which lays
// the same structures into a plain file when it builds bootable
// install media.
package disks

// Reading and writing GUID Partition Tables, from scratch.
//
// A partition table is not an artifact of some tool; it's a few
// hundred bytes at well-known offsets that the kernel (and firmware,
// and every OS) knows how to read. GPT, the modern format, is simple
// enough to handle directly, and doing so shows exactly what it is:
//
//	LBA 0        a "protective MBR": a legacy MBR whose single
//	             partition claims the whole disk, so tools that don't
//	             understand GPT see an occupied disk rather than free
//	             space
//	LBA 1        the GPT header: where everything else is, plus CRC32
//	             checksums of itself and of the entry array
//	LBA 2..33    the partition entry array: 128 slots of 128 bytes,
//	             each naming a partition's type, unique GUID, extent,
//	             and 36-character name
//	...          the partitions themselves
//	end of disk  a mirror of the entries and header (in that order,
//	             header last at the very final sector), so the table
//	             survives damage to LBA 0 and 1
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
// GUID) through unchanged. Other tools rely on those GUIDs to tell
// disks and partitions apart, so an edit must never regenerate them.

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
	SectorSize = 512

	// The entry array's size is part of the format: 128 entries of 128
	// bytes = 32 sectors. With the MBR and header ahead of it, the
	// first sector a partition may use is 34; mirrored at the tail, the
	// last usable sector is 34 from the end.
	entryCount   = 128
	entrySize    = 128
	entrySectors = entryCount * entrySize / SectorSize
	ReservedLBAs = 2 + entrySectors

	// Partitions start on 1MiB boundaries (2048 sectors), the
	// alignment every modern partitioner uses, chosen so partitions
	// align with any plausible physical block or RAID stripe beneath.
	PartitionAlignment = 2048

	// GPT names are UTF-16LE (UEFI predates the industry settling on
	// UTF-8) in a fixed 72-byte field: 36 code units.
	NameChars = 36
)

// LastUsableLBA is the last sector a partition may occupy: the
// backup entry array and header claim the tail of the disk.
func LastUsableLBA(totalSectors uint64) uint64 {
	return totalSectors - ReservedLBAs - 1
}

// AlignLBA rounds a sector number up to the next 1MiB boundary.
func AlignLBA(lba uint64) uint64 {
	return (lba + PartitionAlignment - 1) / PartitionAlignment * PartitionAlignment
}

// A Partition is one entry as liken declares it when claiming; a
// fresh unique GUID is filled in at write time. The type GUID is part
// of the plan: most roles are ordinary Linux data, but the system
// slots must be typed as EFI system partitions or the firmware will
// never look at them.
type Partition struct {
	Name     string
	FirstLBA uint64
	LastLBA  uint64
	TypeGUID [16]byte
}

// An Entry is one occupied slot of the entry array, everything the
// disk records about a partition. An edit preserves each field
// byte-for-byte except the extent it means to change.
type Entry struct {
	TypeGUID   [16]byte
	UniqueGUID [16]byte
	FirstLBA   uint64
	LastLBA    uint64
	Attributes uint64
	Name       string
}

// A Table is a whole partition table in memory: what ReadGPT returns
// and SerializeGPT lays out. The two header fields are kept because
// they are how a grown disk is detected: a table whose alternate
// (backup) header is no longer at the disk's final sector was written
// when the disk was smaller.
type Table struct {
	DiskGUID      [16]byte
	Entries       []Entry
	LastUsableLBA uint64
	AlternateLBA  uint64
}

// A Chunk is one run of bytes at one location: the unit serialization
// produces and writing consumes.
type Chunk struct {
	LBA  uint64
	Data []byte
}

// mbrBootCodeSize is how much of sector 0 precedes the MBR's own
// fields: 440 bytes of x86 boot code, then the 4-byte disk signature
// and 2 reserved bytes. Everything before the partition entries at
// 446 belongs to the bootloader, not the partition table.
const mbrBootCodeSize = 446

// BIOSBootPartition is the type GUID for GRUB's BIOS boot partition:
// a small raw partition, no filesystem, holding the core image that
// the MBR's 440 bytes of boot code jump into. The MBR has no room
// for a real program, and GPT (unlike the old MBR layout) leaves no
// dependable gap after sector 0, so GRUB's own convention is a typed
// partition it can trust nothing else will claim. The GUID spells
// "Hah!IdontNeedEFI" in ASCII — GRUB's little joke, and a genuinely
// well-known constant every partitioning tool recognizes.
var BIOSBootPartition = MustGUID("21686148-6449-6E6F-744E-656564454649")

// LinuxFilesystemData is the partition type GUID meaning "an ordinary
// Linux filesystem". Types are well-known constants, not invented:
// this exact GUID is what lsblk, blkid, and installers everywhere
// recognize as a Linux data partition.
var LinuxFilesystemData = MustGUID("0FC63DAF-8483-4772-8E79-3D69D8477DE4")

// EFISystemPartition is the type GUID that makes a partition an ESP:
// the one partition type UEFI firmware itself reads. The type GUID is
// the entire signal: firmware doesn't inspect contents to find boot
// candidates, it looks for this GUID and expects FAT inside. That is
// why the type is planned per role rather than assumed.
var EFISystemPartition = MustGUID("C12A7328-F81F-11D2-BA4B-00A0C93EC93B")

// MustGUID turns a GUID's canonical text into its 16 on-disk bytes.
// The encoding is a historical quirk: the first three fields are
// little-endian (GUIDs come from Microsoft, via UEFI) while the rest
// is byte-for-byte, so the text and the bytes read differently.
// Getting this wrong makes every tool misread the type.
func MustGUID(s string) [16]byte {
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

// RandomGUID generates a version-4 (random) GUID in on-disk encoding,
// for the disk itself and each partition, so every one is globally
// distinguishable from every other disk and partition in the world.
func RandomGUID() [16]byte {
	var canonical [16]byte
	if _, err := rand.Read(canonical[:]); err != nil {
		// crypto/rand failing means the kernel could not supply
		// random bytes at all. These GUIDs are identities other tools
		// trust to tell disks apart forever, and minting them without
		// randomness risks collisions, so there is no proceeding: in
		// init this panic panics the kernel and panic=10 reboots the
		// machine for another try; in the toolkit it ends the build.
		panic(err)
	}
	canonical[6] = canonical[6]&0x0F | 0x40 // version 4: random
	canonical[8] = canonical[8]&0x3F | 0x80 // variant: RFC 4122
	return guidToDisk(canonical)
}

// SerializeGPT is the pure half of writing: a table in, the five
// on-disk chunks out (protective MBR, primary header, entries, backup
// entries, backup header), with both CRCs computed. totalSectors
// decides where the backup lands and what lastUsable becomes, which
// is exactly how serializing a table read from a smaller disk
// relocates its backup to the new end.
func SerializeGPT(t *Table, totalSectors uint64) ([]Chunk, error) {
	if len(t.Entries) > entryCount {
		return nil, fmt.Errorf("%d partitions won't fit a %d-entry GPT", len(t.Entries), entryCount)
	}

	// The entry array first, because both headers embed its checksum.
	entries := make([]byte, entryCount*entrySize)
	for i, p := range t.Entries {
		e := entries[i*entrySize:]
		copy(e[0:16], p.TypeGUID[:])
		copy(e[16:32], p.UniqueGUID[:])
		binary.LittleEndian.PutUint64(e[32:40], p.FirstLBA)
		binary.LittleEndian.PutUint64(e[40:48], p.LastLBA)
		binary.LittleEndian.PutUint64(e[48:56], p.Attributes)

		name := utf16.Encode([]rune(p.Name))
		if len(name) > NameChars {
			return nil, fmt.Errorf("partition name %q exceeds GPT's %d characters", p.Name, NameChars)
		}
		for j, r := range name {
			binary.LittleEndian.PutUint16(e[56+2*j:], r)
		}
	}
	entriesCRC := crc32.ChecksumIEEE(entries)

	backupHeaderLBA := totalSectors - 1
	backupEntriesLBA := totalSectors - 1 - entrySectors

	// Each header records its own location and the other copy's. The
	// backup is not a byte-for-byte copy of the primary; it holds the
	// same facts written from the other end of the disk.
	header := func(currentLBA, otherLBA, entriesLBA uint64) []byte {
		h := make([]byte, SectorSize)
		copy(h[0:8], "EFI PART")
		binary.LittleEndian.PutUint32(h[8:12], 0x0001_0000) // revision 1.0
		binary.LittleEndian.PutUint32(h[12:16], 92)         // header size
		// h[16:20] is the header's own CRC, computed over the 92 bytes
		// with this field zeroed, then patched in.
		binary.LittleEndian.PutUint64(h[24:32], currentLBA)
		binary.LittleEndian.PutUint64(h[32:40], otherLBA)
		binary.LittleEndian.PutUint64(h[40:48], ReservedLBAs)
		binary.LittleEndian.PutUint64(h[48:56], LastUsableLBA(totalSectors))
		copy(h[56:72], t.DiskGUID[:])
		binary.LittleEndian.PutUint64(h[72:80], entriesLBA)
		binary.LittleEndian.PutUint32(h[80:84], entryCount)
		binary.LittleEndian.PutUint32(h[84:88], entrySize)
		binary.LittleEndian.PutUint32(h[88:92], entriesCRC)
		binary.LittleEndian.PutUint32(h[16:20], crc32.ChecksumIEEE(h[0:92]))
		return h
	}

	// The protective MBR: one legacy partition of type 0xEE spanning
	// the disk (capped at the 32-bit sector count MBR can express), so
	// GPT-unaware tools refuse to touch it rather than see empty space.
	//
	// The table owns only the tail of sector 0: the partition entries
	// at byte 446 and the boot signature at 510. The 446 bytes ahead
	// of them are BIOS boot code, which belongs to whoever owns
	// booting — on a BIOS machine that is GRUB's first stage, planted
	// at install time and healed by init. This serialization emits
	// zeros there (a freshly claimed disk boots nothing yet), and
	// writeTableBytes preserves whatever the disk already carries, so
	// rewriting the partition table never un-boots a machine.
	mbr := make([]byte, SectorSize)
	span := min(totalSectors-1, 0xFFFF_FFFF)
	entry := mbr[446:]
	entry[1], entry[2], entry[3] = 0x00, 0x02, 0x00 // CHS start: legacy filler
	entry[4] = 0xEE                                 // type: "GPT protective"
	entry[5], entry[6], entry[7] = 0xFF, 0xFF, 0xFF // CHS end: "beyond CHS"
	binary.LittleEndian.PutUint32(entry[8:12], 1)
	binary.LittleEndian.PutUint32(entry[12:16], uint32(span))
	mbr[510], mbr[511] = 0x55, 0xAA

	return []Chunk{
		{0, mbr},
		{1, header(1, backupHeaderLBA, 2)},
		{2, entries},
		{backupEntriesLBA, entries},
		{backupHeaderLBA, header(backupHeaderLBA, 1, backupEntriesLBA)},
	}, nil
}

// readGPTCopy parses one copy of the table from its header sector,
// verifying both checksums, or explains what disqualified it.
func readGPTCopy(r io.ReaderAt, headerLBA uint64) (*Table, error) {
	h := make([]byte, SectorSize)
	if _, err := r.ReadAt(h, int64(headerLBA)*SectorSize); err != nil {
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
	if headerSize < 92 || headerSize > SectorSize {
		return nil, fmt.Errorf("implausible header size %d at sector %d", headerSize, headerLBA)
	}
	scratch := slices.Clone(h[:headerSize])
	clear(scratch[16:20])
	if crc32.ChecksumIEEE(scratch) != binary.LittleEndian.Uint32(h[16:20]) {
		return nil, fmt.Errorf("header checksum mismatch at sector %d", headerLBA)
	}

	// liken only ever writes the standard 128×128 array. A table with
	// any other geometry is not one liken wrote, and foreign disks
	// are refused at claim time, so refuse this one too.
	gotCount := binary.LittleEndian.Uint32(h[80:84])
	gotSize := binary.LittleEndian.Uint32(h[84:88])
	if gotCount != entryCount || gotSize != entrySize {
		return nil, fmt.Errorf("unexpected entry geometry %d×%d at sector %d", gotCount, gotSize, headerLBA)
	}

	entriesLBA := binary.LittleEndian.Uint64(h[72:80])
	entries := make([]byte, gotCount*gotSize)
	if _, err := r.ReadAt(entries, int64(entriesLBA)*SectorSize); err != nil {
		return nil, fmt.Errorf("reading the entry array at sector %d: %w", entriesLBA, err)
	}
	if crc32.ChecksumIEEE(entries) != binary.LittleEndian.Uint32(h[88:92]) {
		return nil, fmt.Errorf("entry array checksum mismatch (header at sector %d)", headerLBA)
	}

	t := &Table{
		LastUsableLBA: binary.LittleEndian.Uint64(h[48:56]),
		AlternateLBA:  binary.LittleEndian.Uint64(h[32:40]),
	}
	copy(t.DiskGUID[:], h[56:72])

	for i := range int(gotCount) {
		e := entries[i*int(gotSize):]
		var typeGUID [16]byte
		copy(typeGUID[:], e[0:16])
		if typeGUID == ([16]byte{}) {
			continue // an all-zero type GUID marks an unused slot
		}
		p := Entry{
			TypeGUID:   typeGUID,
			FirstLBA:   binary.LittleEndian.Uint64(e[32:40]),
			LastLBA:    binary.LittleEndian.Uint64(e[40:48]),
			Attributes: binary.LittleEndian.Uint64(e[48:56]),
		}
		copy(p.UniqueGUID[:], e[16:32])
		var units []uint16
		for j := range NameChars {
			u := binary.LittleEndian.Uint16(e[56+2*j:])
			if u == 0 {
				break
			}
			units = append(units, u)
		}
		p.Name = string(utf16.Decode(units))
		t.Entries = append(t.Entries, p)
	}
	return t, nil
}

// ReadGPT parses a device's partition table, trying the primary copy
// first and falling back to the backup. Disagreements resolve in the
// primary's favor: the kernel read the primary too, so recognition
// already trusted it, and the next rewrite reconciles the pair.
func ReadGPT(r io.ReaderAt, totalSectors uint64) (*Table, error) {
	primary, perr := readGPTCopy(r, 1)
	backup, berr := readGPTCopy(r, totalSectors-1)

	switch {
	case perr == nil && berr == nil:
		if primary.DiskGUID != backup.DiskGUID || !slices.Equal(primary.Entries, backup.Entries) {
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
		// Redundancy can't cover one case: if the disk was grown while
		// the primary was already unreadable, the backup is still at
		// the old end of the disk, where nothing looks for it.
		return nil, fmt.Errorf("neither partition table copy is readable: primary: %v; backup at sector %d: %v (a grown disk's backup is no longer at the end)",
			perr, totalSectors-1, berr)
	}
}

// WriteTable is the I/O half of writing: serialize the table for this
// disk size, put the chunks where they go, then ask the kernel to
// re-read the result. It is deliberately thin; everything interesting
// happens in SerializeGPT.
func WriteTable(devPath string, totalSectors uint64, t *Table) error {
	f, err := writeTableBytes(devPath, totalSectors, t)
	if err != nil {
		return err
	}
	defer f.Close()

	// The bytes are on disk, but the kernel's view of the device
	// predates them; this ioctl asks it to re-read the table, which
	// is what makes the vda1, vda2, ... devices appear (or grow).
	if _, err := unix.IoctlRetInt(int(f.Fd()), unix.BLKRRPART); err != nil {
		return fmt.Errorf("re-reading partition table: %w", err)
	}
	return nil
}

// WriteTableInPlace writes a table's bytes without asking the kernel
// to re-read them. That is only correct when no partition's extent
// changed — relocating the backup copy to the end of a grown disk is
// the case — because then the kernel's existing view of the device
// already matches the new table. It exists because the kernel refuses
// to re-read a disk with any partition mounted, and the disk that
// carries the running system always has one: the boot slot the OS
// mounted its own root image from.
func WriteTableInPlace(devPath string, totalSectors uint64, t *Table) error {
	f, err := writeTableBytes(devPath, totalSectors, t)
	if err != nil {
		return err
	}
	return f.Close()
}

// writeTableBytes serializes and writes a table's chunks, returning
// the still-open device for whatever the caller wants to ask of it.
func writeTableBytes(devPath string, totalSectors uint64, t *Table) (*os.File, error) {
	chunks, err := SerializeGPT(t, totalSectors)
	if err != nil {
		return nil, err
	}

	f, err := os.OpenFile(devPath, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}

	// Sector 0 is shared ground: SerializeGPT owns the protective
	// entry and the boot signature, but the boot code ahead of them
	// (446 bytes: code plus the MBR disk signature) is not the
	// table's to write. Carry the disk's existing bytes through, so a
	// GPT rewrite — growth relocating the backup, most commonly —
	// never zeroes the machine's own bootloader out from under it.
	bootCode := make([]byte, mbrBootCodeSize)
	if _, err := f.ReadAt(bootCode, 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("reading the existing boot code: %w", err)
	}
	for _, chunk := range chunks {
		if chunk.LBA == 0 {
			copy(chunk.Data[:mbrBootCodeSize], bootCode)
		}
	}

	for _, chunk := range chunks {
		if _, err := f.WriteAt(chunk.Data, int64(chunk.LBA)*SectorSize); err != nil {
			f.Close()
			return nil, fmt.Errorf("writing at LBA %d: %w", chunk.LBA, err)
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// Write lays a brand-new partition table onto a blank disk being
// claimed: fresh GUIDs for the disk and every partition, because
// claiming is when these identities are created.
func Write(devPath string, totalSectors uint64, parts []Partition) error {
	t := &Table{DiskGUID: RandomGUID()}
	for _, p := range parts {
		t.Entries = append(t.Entries, Entry{
			TypeGUID:   p.TypeGUID,
			UniqueGUID: RandomGUID(),
			FirstLBA:   p.FirstLBA,
			LastLBA:    p.LastLBA,
			Name:       p.Name,
		})
	}
	return WriteTable(devPath, totalSectors, t)
}
