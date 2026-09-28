package main

// Patching GRUB's boot sectors: what grub-bios-setup does, done by
// hand, in the same way that this repo writes partition tables and
// filesystems by hand. This lets the machine verify and heal its own
// boot sectors instead of depending on a rescue boot and a person
// running dd.
//
// BIOS boot is a chain of disk addresses set at install time. The
// firmware loads sector 0 and jumps into it. Those 440 bytes
// (boot.img) hold just enough code to load one more sector: the
// first sector of the core image, whose address is patched into
// boot.img at a fixed offset. That first sector, which GRUB calls
// diskboot, ends with a blocklist: the disk address and length of
// the rest of the core image, patched in the same way. Only from
// that point does GRUB have real filesystem drivers, and only then
// does it stop needing literal sector numbers.
//
// liken keeps every one of those addresses derivable. The core image
// lives at the start of the biosBoot partition, contiguous, so the
// whole chain is a pure function of boot.img, core.img, and the
// partition's first sector. This is what makes healing reliable: any
// boot can recompute the expected bytes from the proven slot's
// artifacts and compare them against the disk. When a Linode image
// deploy zeroes the MBR, which has happened twice, this code repairs
// it at the next opportunity instead of leaving the machine unable to
// boot.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/liken-sh/liken/disks"
)

// The offsets that grub-bios-setup patches, fixed by boot.img's layout:
//
//	0x5c  GRUB_BOOT_MACHINE_KERNEL_SECTOR: the 64-bit LBA of the
//	      core image's first sector
//	0x66  GRUB_BOOT_MACHINE_DRIVE_CHECK: a workaround for BIOSes
//	      that pass a garbage boot drive in DL. On hard disks,
//	      grub-bios-setup disables the check by overwriting the two
//	      instruction bytes with NOPs.
//
// And the offsets in diskboot, fixed by the last 12 bytes of the core
// image's first sector: the blocklist entry that names where the rest
// of the image lives (start sector, sector count), and the real-mode
// segment it loads at (0x820, compiled in, asserted here and never
// written).
const (
	grubKernelSectorOffset = 0x5c
	grubDriveCheckOffset   = 0x66
	grubBlocklistStart     = 500
	grubBlocklistLength    = 508
	grubBlocklistSegment   = 510
	grubLoadSegment        = 0x820
	mbrBootCodeBytes       = 440
)

// grubBootSectors is one machine's expected boot chain: the bytes
// that belong at LBA 0 and at the biosBoot partition, computed from a
// release's artifacts and the partition's location. Installing and
// healing perform the same operation on this value: write what
// should be there.
type grubBootSectors struct {
	mbr     []byte // the 440 boot-code bytes for sector 0
	core    []byte // the patched core image for the partition
	coreLBA uint64 // the partition's first sector
}

// planGRUBBootSectors patches copies of the release's grub-boot.img
// and grub-core.img for a machine whose biosBoot partition starts at
// coreLBA. This function validates everything, so a write can only
// ever put a coherent chain on disk.
func planGRUBBootSectors(bootImg, coreImg []byte, part *slotPartition) (*grubBootSectors, error) {
	if len(bootImg) != disks.SectorSize {
		return nil, fmt.Errorf("grub-boot.img is %d bytes; boot.img is one %d-byte sector", len(bootImg), disks.SectorSize)
	}
	if len(coreImg) < disks.SectorSize {
		return nil, fmt.Errorf("grub-core.img is %d bytes, not even one sector; it cannot carry a blocklist", len(coreImg))
	}
	coreSectors := (uint64(len(coreImg)) + disks.SectorSize - 1) / disks.SectorSize
	if available := part.lastLBA - part.firstLBA + 1; coreSectors > available {
		return nil, fmt.Errorf("grub-core.img needs %d sectors but the biosBoot partition holds %d", coreSectors, available)
	}

	mbr := bytes.Clone(bootImg[:mbrBootCodeBytes])
	binary.LittleEndian.PutUint64(mbr[grubKernelSectorOffset:], part.firstLBA)
	// Two NOPs disable the buggy-BIOS drive check, as grub-bios-setup
	// does for any hard disk.
	mbr[grubDriveCheckOffset], mbr[grubDriveCheckOffset+1] = 0x90, 0x90

	core := bytes.Clone(coreImg)
	if seg := binary.LittleEndian.Uint16(core[grubBlocklistSegment:]); seg != grubLoadSegment {
		return nil, fmt.Errorf("grub-core.img's load segment is %#x, want %#x; this is not an i386-pc core image", seg, grubLoadSegment)
	}
	// The blocklist: the rest of the image follows its first sector
	// without gaps. The partition guarantees this.
	binary.LittleEndian.PutUint64(core[grubBlocklistStart:], part.firstLBA+1)
	binary.LittleEndian.PutUint16(core[grubBlocklistLength:], uint16(coreSectors-1))

	return &grubBootSectors{mbr: mbr, core: core, coreLBA: part.firstLBA}, nil
}

// inPlace reports whether the disk already carries this chain. This
// is the comparison half of healing, and it is cheap enough to run on
// every boot (440 bytes plus the core image).
func (s *grubBootSectors) inPlace(disk io.ReaderAt) (bool, error) {
	mbr := make([]byte, mbrBootCodeBytes)
	if _, err := disk.ReadAt(mbr, 0); err != nil {
		return false, fmt.Errorf("reading the boot code: %w", err)
	}
	if !bytes.Equal(mbr, s.mbr) {
		return false, nil
	}
	core := make([]byte, len(s.core))
	if _, err := disk.ReadAt(core, int64(s.coreLBA)*disks.SectorSize); err != nil {
		return false, fmt.Errorf("reading the core image: %w", err)
	}
	return bytes.Equal(core, s.core), nil
}

// write puts the chain on disk: the core image first and synced, then
// the MBR's boot code last. This order makes a torn write safe. The
// MBR points at the partition's first sector, which never moves, so
// an old MBR over a new core image still boots. A new MBR over a
// half-written core image would jump into garbage instead.
func (s *grubBootSectors) write(disk disks.Device) error {
	if _, err := disk.WriteAt(s.core, int64(s.coreLBA)*disks.SectorSize); err != nil {
		return fmt.Errorf("writing the core image: %w", err)
	}
	if err := disk.Sync(); err != nil {
		return err
	}
	// This writes only the 440 boot-code bytes. Everything after them
	// in sector 0 (the disk signature, the protective MBR entry, the
	// boot signature) belongs to the partition table's writer.
	if _, err := disk.WriteAt(s.mbr, 0); err != nil {
		return fmt.Errorf("writing the boot code: %w", err)
	}
	return disk.Sync()
}
