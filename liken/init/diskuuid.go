package main

// Init reads the filesystem UUID of each partition for the by-uuid
// links.
//
// A filesystem UUID identifies the filesystem, not the disk. mke2fs
// assigns the UUID of an ext4 filesystem, and disks.FormatFAT32 assigns
// the volume ID of a FAT32 filesystem. A reformatted disk gets a new
// UUID, and a filesystem copied to another disk keeps its UUID.
//
// The links are for programs that do not know liken's role names. A CSI
// driver or an /etc/fstab line mounts a volume by the UUID of its
// filesystem and expects to find it under /dev/disk/by-uuid, where
// udev puts it on other Linux distributions.

import (
	"encoding/binary"
	"fmt"
	"os"

	"github.com/liken-sh/liken/liken/disks"
)

// filesystemUUID returns the UUID of an ext4 filesystem or the volume
// ID of a FAT32 filesystem on devPath. It reads the device's bytes, as
// hasExt4 and disks.HasFAT32 do, so it needs no mount and no blkid. It
// returns "" for a missing device, a device too short to hold the
// field, and a device with neither filesystem, because an unformatted
// disk or a blank partition is an ordinary case and not an error.
func filesystemUUID(devPath string) string {
	if hasExt4(devPath) {
		return ext4UUID(devPath)
	}
	if disks.HasFAT32(devPath) {
		return fat32VolumeID(devPath)
	}
	return ""
}

// s_uuid is a 16-byte field at offset 0x68 of the superblock.
const ext4SuperblockUUIDOffset = ext4SuperblockOffset + 0x68

// ext4UUID returns s_uuid in the 8-4-4-4-12 form that blkid prints, in
// lowercase hex.
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

// The FAT32 boot sector has the volume ID at byte 67, after the
// extended boot signature.
const fat32VolumeIDOffset = 67

// fat32VolumeID returns the 4-byte volume ID as the high 16 bits, a
// dash, and the low 16 bits, in uppercase hex, which is how other FAT
// tools print it.
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
