package disks

// The mark a FAT volume carries between mounts.
//
// FAT has no journal. The one thing it records about its own health is
// a single bit in the boot sector, and the driver manages it: the bit
// goes on when the volume is mounted for writing, and off when the
// volume is released. A volume found with the bit on was mounted and
// never released, so its directory entries and its allocation table
// may disagree with each other.
//
// Reading that bit is how a machine learns it did not stop cleanly.
// Clearing it is how a machine says the volume has been dealt with.
// Both belong here, next to the formatter that lays the boot sector
// out, because both depend on the same field offsets.
//
// The driver will not clear the bit itself once it has found it set.
// That is deliberate: the bit is meant to survive until a repair, so a
// warning cannot be lost by a reboot. It also means a volume marked
// once stays marked forever unless something outside the driver clears
// it, which is why ClearFAT32Dirty exists at all.

import (
	"fmt"
	"os"
)

// fat32StateOffset is where the boot sector keeps the state byte on a
// FAT32 volume. FAT12 and FAT16 keep it at offset 0x25 instead,
// because their extended boot signature block starts earlier. liken
// formats its volumes as FAT32 and nothing else, so only this offset
// is needed here, and the callers below check the volume's type before
// they trust the byte.
const fat32StateOffset = 0x41

// fatStateDirty is the bit that says the volume was not released.
const fatStateDirty = 0x01

// readBootSector reads the first sector and confirms that it is a
// FAT32 boot sector liken would recognize. Confirming the type first
// matters: on a FAT16 volume the same offset falls inside the count of
// sectors per FAT, so a byte read there would be a number, not a flag.
func readBootSector(devPath string) ([]byte, error) {
	f, err := os.Open(devPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sector := make([]byte, SectorSize)
	if _, err := f.ReadAt(sector, 0); err != nil {
		return nil, fmt.Errorf("reading the boot sector of %s: %w", devPath, err)
	}
	if sector[510] != 0x55 || sector[511] != 0xAA || string(sector[82:90]) != "FAT32   " {
		return nil, fmt.Errorf("%s does not carry a FAT32 boot sector", devPath)
	}
	return sector, nil
}

// FAT32Dirty reports whether a volume still carries the mark that says
// it was mounted and never released.
//
// Read this before mounting the volume. Mounting it for writing sets
// the mark, so a read afterwards says only that the volume is in use
// now, which is true of every mounted volume and answers nothing.
func FAT32Dirty(devPath string) (bool, error) {
	sector, err := readBootSector(devPath)
	if err != nil {
		return false, err
	}
	return sector[fat32StateOffset]&fatStateDirty != 0, nil
}

// ClearFAT32Dirty clears the mark and puts the change on the platter.
//
// The volume must not be mounted. A mounted volume's boot sector lives
// in the kernel's own buffer, and the driver writes that buffer back
// on its own schedule, so a write underneath it is either lost or
// wins by accident. Neither outcome is a fix.
//
// The caller decides whether clearing the mark is honest. This
// function only performs it, and it refuses a volume that is not
// FAT32, because on any other layout this byte means something else.
func ClearFAT32Dirty(devPath string) error {
	sector, err := readBootSector(devPath)
	if err != nil {
		return err
	}
	if sector[fat32StateOffset]&fatStateDirty == 0 {
		return nil
	}
	f, err := os.OpenFile(devPath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	sector[fat32StateOffset] &^= fatStateDirty
	// One byte carries the change, and writing only that byte leaves
	// every other field exactly as it was found. A volume this machine
	// did not format may hold fields liken does not know about.
	if _, err := f.WriteAt(sector[fat32StateOffset:fat32StateOffset+1], fat32StateOffset); err != nil {
		return fmt.Errorf("clearing the mark on %s: %w", devPath, err)
	}
	// FAT32 keeps a backup boot sector, named by the field at offset
	// 50. The driver reads the primary and never consults the backup,
	// so the backup is left alone: a repair tool compares the two, and
	// a difference here is a true record of what happened.
	if err := f.Sync(); err != nil {
		return fmt.Errorf("flushing the mark on %s: %w", devPath, err)
	}
	return nil
}
