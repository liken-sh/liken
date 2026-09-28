package main

// Reading a partition's filesystem UUID, for the by-uuid link tree.
//
// A filesystem UUID names the contents of a partition, not the disk that
// holds it. mke2fs assigns ext4's UUID, and liken assigns FAT32's volume
// ID (FormatFAT32, in the disks package), the moment each filesystem is
// created. Reformat the same disk and it gets a new identity; move a
// filesystem's bytes to a different disk and the identity follows the
// bytes. This is why a role's own device can never appear under
// by-uuid: claiming a role (claim.go) takes a disk that carries no
// partition table at all, and a disk with no partitions has no
// filesystem yet to carry a UUID.
//
// The tree exists for consumers that have no notion of liken's role
// names. A CSI driver, or a line in /etc/fstab, names a volume to mount
// by the UUID its filesystem carries, and expects to find it under
// /dev/disk/by-uuid, the way every other Linux distribution's udev
// publishes it.
//
// filesystemUUID reads a device's raw bytes, the same way hasExt4 and
// disks.HasFAT32 already do, rather than mounting the filesystem or
// shelling out to blkid. It returns "" for anything it does not
// recognize: a missing device, a read shorter than the structure it
// expects, or bytes that carry neither filesystem's magic. This is the
// ordinary answer for a disk a role has claimed but not yet formatted,
// and for a raw or blank partition generally, not a failure to report.

import (
	"encoding/binary"
	"fmt"
	"os"

	"github.com/liken-sh/liken/disks"
)

func filesystemUUID(devPath string) string {
	if hasExt4(devPath) {
		return ext4UUID(devPath)
	}
	if disks.HasFAT32(devPath) {
		return fat32VolumeID(devPath)
	}
	return ""
}

// ext4SuperblockUUIDOffset is s_uuid's position within the superblock:
// 16 bytes at 0x68, right after the 4-byte fields the resize code reads
// (ext4.go). The layout is ext4's on-disk format, fixed permanently.
const ext4SuperblockUUIDOffset = ext4SuperblockOffset + 0x68

// ext4UUID reads s_uuid and renders it in the canonical 8-4-4-4-12 form
// that blkid and every other UUID-aware tool uses: lowercase hex, split
// by the fields the UUID standard defines, joined with dashes.
func ext4UUID(devPath string) string {
	f, err := os.Open(devPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	uuid := make([]byte, 16)
	if _, err := f.ReadAt(uuid, ext4SuperblockUUIDOffset); err != nil {
		return ""
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
}

// fat32VolumeIDOffset is where the boot sector carries the volume ID:
// byte 67, right after the extended boot signature (fat32.go).
const fat32VolumeIDOffset = 67

// fat32VolumeID reads the 4-byte volume ID FAT keeps in place of a UUID
// and renders it the way Windows and every FAT tool display it: the
// high 16 bits, a dash, the low 16 bits, in uppercase hex.
func fat32VolumeID(devPath string) string {
	f, err := os.Open(devPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	id := make([]byte, 4)
	if _, err := f.ReadAt(id, fat32VolumeIDOffset); err != nil {
		return ""
	}
	v := binary.LittleEndian.Uint32(id)
	return fmt.Sprintf("%04X-%04X", v>>16, v&0xFFFF)
}
