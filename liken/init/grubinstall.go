package main

// This file writes GRUB's BIOS boot sectors in Go, which is the work of
// grub-bios-setup. Init writes partition tables and filesystems itself
// in the same way. With this code, a machine can check its own boot
// sectors and rewrite them when they are wrong, with no rescue boot and
// no person running dd.
//
// BIOS boot is a chain of disk addresses that the installer fills in.
// The firmware loads sector 0 and runs it. Its 440 bytes of code
// (boot.img) load one more sector, the first sector of the core
// image, at an address that is patched into boot.img at a fixed
// offset. That first sector (diskboot in GRUB's source) ends with a
// blocklist, which is the start sector and sector count of the rest
// of the core image, patched in the same way. After that GRUB has its
// own filesystem drivers and needs no more sector numbers.
//
// The core image is stored contiguously at the start of the biosBoot
// partition, so every address in the chain follows from boot.img,
// core.img, and the first sector of the partition. Because of that,
// any boot can compute the bytes that belong on the disk from the
// proven slot's files and compare them with the disk. A Linode image
// deploy can zero the MBR, and this code then rewrites it at the next
// boot, so the machine can boot again.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/liken-sh/liken/liken/disks"
)

// These are the offsets that grub-bios-setup patches. The first two
// are in boot.img:
//
//	0x5c  GRUB_BOOT_MACHINE_KERNEL_SECTOR: the 64-bit LBA of the
//	      core image's first sector
//	0x66  GRUB_BOOT_MACHINE_DRIVE_CHECK: a workaround for BIOSes
//	      that pass a wrong boot drive in DL. On hard disks,
//	      grub-bios-setup disables the check by overwriting its two
//	      instruction bytes with NOPs.
//
// The rest are in the last 12 bytes of the core image's first sector
// (diskboot): the start sector and sector count of the rest of the
// image, and the real-mode segment that it loads at. The segment
// (0x820) is part of the compiled code, so this code checks it and
// never writes it.
const (
	grubKernelSectorOffset = 0x5c
	grubDriveCheckOffset   = 0x66
	grubBlocklistStart     = 500
	grubBlocklistLength    = 508
	grubBlocklistSegment   = 510
	grubLoadSegment        = 0x820
	mbrBootCodeBytes       = 440
)

// grubBootSectors holds the bytes that belong at LBA 0 and in the
// biosBoot partition of one machine, computed from the release's
// artifacts and the partition's location. An install and a repair
// both write this value to the disk.
type grubBootSectors struct {
	mbr     []byte // the 440 boot-code bytes for sector 0
	core    []byte // the patched core image for the partition
	coreLBA uint64 // the partition's first sector
}

// planGRUBBootSectors patches copies of grub-boot.img and
// grub-core.img with the location of part, the machine's biosBoot
// partition. It refuses images of the wrong size, a core image that
// does not fit in the partition, and a core image with the wrong load
// segment, so write never puts a broken chain on the disk.
func planGRUBBootSectors(bootImg, coreImg []byte, part *slotPartition) (*grubBootSectors, error) {
	if len(bootImg) != disks.SectorSize {
		return nil, fmt.Errorf("grub-boot.img is %d bytes, want one %d-byte sector", len(bootImg), disks.SectorSize)
	}
	if len(coreImg) < disks.SectorSize {
		return nil, fmt.Errorf("grub-core.img is %d bytes, less than one %d-byte sector", len(coreImg), disks.SectorSize)
	}
	coreSectors := (uint64(len(coreImg)) + disks.SectorSize - 1) / disks.SectorSize
	if available := part.lastLBA - part.firstLBA + 1; coreSectors > available {
		return nil, fmt.Errorf("grub-core.img needs %d sectors and the biosBoot partition has %d", coreSectors, available)
	}

	mbr := bytes.Clone(bootImg[:mbrBootCodeBytes])
	binary.LittleEndian.PutUint64(mbr[grubKernelSectorOffset:], part.firstLBA)
	// Two NOPs disable the drive check, as grub-bios-setup does on
	// hard disks.
	mbr[grubDriveCheckOffset], mbr[grubDriveCheckOffset+1] = 0x90, 0x90

	core := bytes.Clone(coreImg)
	if seg := binary.LittleEndian.Uint16(core[grubBlocklistSegment:]); seg != grubLoadSegment {
		return nil, fmt.Errorf("the load segment of grub-core.img is %#x, want %#x, so it is not an i386-pc core image", seg, grubLoadSegment)
	}
	// The blocklist is one entry, because the rest of the image
	// follows its first sector in the partition without gaps.
	binary.LittleEndian.PutUint64(core[grubBlocklistStart:], part.firstLBA+1)
	binary.LittleEndian.PutUint16(core[grubBlocklistLength:], uint16(coreSectors-1))

	return &grubBootSectors{mbr: mbr, core: core, coreLBA: part.firstLBA}, nil
}

// inPlace reports whether the disk already has these bytes in sector
// 0 and in the core image area. It reads only 440 bytes and the core
// image, so it can run on every boot.
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

// write writes and syncs the core image before the boot code in the
// MBR. The boot code holds only the partition's first sector, which
// does not move, so an old MBR still boots the new core image. A new
// MBR written before the core image would run a half-written core
// image after a power loss.
func (s *grubBootSectors) write(disk disks.Device) error {
	if _, err := disk.WriteAt(s.core, int64(s.coreLBA)*disks.SectorSize); err != nil {
		return fmt.Errorf("writing the core image: %w", err)
	}
	if err := disk.Sync(); err != nil {
		return err
	}
	// Only the 440 bytes of boot code. The rest of sector 0 (the disk
	// signature, the protective MBR entry, and the boot signature)
	// belongs to the code that writes the partition table.
	if _, err := disk.WriteAt(s.mbr, 0); err != nil {
		return fmt.Errorf("writing the boot code: %w", err)
	}
	return disk.Sync()
}
