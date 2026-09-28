package main

// Growing ext4 filesystems, without resize2fs.
//
// A filesystem's size lives in its superblock, not in its partition.
// Growing the partition changes nothing until the code tells the
// filesystem that there is more room. The usual tool for that is
// resize2fs, but for online growth, meaning the filesystem stays
// mounted, which is the only state liken needs, resize2fs is only a
// thin wrapper. The kernel has done the actual work since ext3, and
// asking it takes one ioctl carrying the new block count. liken
// issues the ioctl directly. This spares the image a second
// e2fsprogs binary and shows what "resizing a filesystem" actually
// is.
//
// The superblock sits 1024 bytes into the device, whatever the block
// size is. The first KiB is left alone for boot sectors, a
// convention older than ext itself. Everything growth needs is in
// the superblock's first few hundred bytes.

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/machine"
)

// hasExt4 checks a device for ext4's superblock magic: two bytes,
// 0xEF53 little-endian, at offset 1080. The superblock starts at
// 1024, and the magic sits 56 bytes into it. This is the same check
// that blkid makes; identifying a filesystem takes nothing more than
// this.
func hasExt4(devPath string) bool {
	f, err := os.Open(devPath)
	if err != nil {
		return false
	}
	defer f.Close()
	magic := make([]byte, 2)
	if _, err := f.ReadAt(magic, 1080); err != nil {
		return false
	}
	return magic[0] == 0x53 && magic[1] == 0xEF
}

// ext4Geometry is the two superblock facts resizing needs: how big a
// block is, and how many blocks the filesystem's superblock records.
type ext4Geometry struct {
	blockSize  uint64
	blockCount uint64
}

const ext4SuperblockOffset = 1024

// parseExt4Superblock reads geometry from a superblock's bytes. The
// offsets are the on-disk format, fixed permanently:
//
//	s_blocks_count_lo  u32 at 4    block count, low 32 bits
//	s_log_block_size   u32 at 24   block size = 1024 << this
//	s_magic            u16 at 56   0xEF53
//	s_feature_incompat u32 at 96   bit 0x80 = the 64bit feature
//	s_blocks_count_hi  u32 at 336  block count's high bits (64bit only)
func parseExt4Superblock(sb []byte) (ext4Geometry, error) {
	if len(sb) < 1024 {
		return ext4Geometry{}, fmt.Errorf("superblock truncated at %d bytes", len(sb))
	}
	if sb[56] != 0x53 || sb[57] != 0xEF {
		return ext4Geometry{}, fmt.Errorf("no ext4 magic in the superblock")
	}

	logBlockSize := le32(sb[24:])
	// 1024 << 6 = 64KiB, ext4's ceiling. Anything above this value is
	// corruption.
	if logBlockSize > 6 {
		return ext4Geometry{}, fmt.Errorf("implausible block size exponent %d", logBlockSize)
	}
	g := ext4Geometry{
		blockSize:  1024 << logBlockSize,
		blockCount: uint64(le32(sb[4:])),
	}
	// With the 64bit feature, the block count has high bits in a
	// second field. Without the feature, those bytes belong to other
	// fields, and the code must not read them as a count.
	if le32(sb[96:])&0x80 != 0 {
		g.blockCount |= uint64(le32(sb[336:])) << 32
	}
	return g, nil
}

func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

// readExt4Geometry reads the superblock from a device node.
func readExt4Geometry(devPath string) (ext4Geometry, error) {
	f, err := os.Open(devPath)
	if err != nil {
		return ext4Geometry{}, err
	}
	defer f.Close()
	sb := make([]byte, 1024)
	if _, err := f.ReadAt(sb, ext4SuperblockOffset); err != nil {
		return ext4Geometry{}, fmt.Errorf("reading the superblock: %w", err)
	}
	return parseExt4Superblock(sb)
}

// ext4ResizeFS is EXT4_IOC_RESIZE_FS: _IOW('f', 16, __u64), assembled
// the same way the kernel's ioctl macros assemble it: direction
// "write" (1<<30), argument size (8<<16), type ('f'<<8), and command
// number (16).
const ext4ResizeFS = (1 << 30) | (8 << 16) | ('f' << 8) | 16 // 0x40086610

// growExt4 asks the kernel to grow the mounted filesystem to
// newBlocks. Any fd inside the mount identifies the filesystem; the
// mountpoint itself is the simplest one to use.
func growExt4(mountpoint string, newBlocks uint64) error {
	f, err := os.OpenFile(mountpoint, os.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), ext4ResizeFS, uintptr(unsafe.Pointer(&newBlocks))); errno != 0 {
		return fmt.Errorf("EXT4_IOC_RESIZE_FS to %d blocks: %w", newBlocks, errno)
	}
	return nil
}

// maybeGrowFilesystem brings a mounted role's filesystem up to its
// partition's size, if the partition outgrew the filesystem.
// mke2fs's defaults reserve resize headroom (the resize_inode) for
// growth of roughly a thousandfold, far beyond anything a disk will
// actually do, so a grow that fits the partition is expected to
// succeed. Failure means the code cannot satisfy the declared
// capacity, and that fails reconciliation, the same as any other
// unsatisfiable role.
func maybeGrowFilesystem(role machine.DeclaredRole, p partition, mountpoint string) error {
	g, err := readExt4Geometry(devRoot + "/" + p.name)
	if err != nil {
		return fmt.Errorf("reading %s's filesystem geometry: %w", role.Name, err)
	}
	newBlocks := p.sizeBytes / g.blockSize
	if newBlocks <= g.blockCount {
		return nil
	}
	fmt.Printf("liken: storage: growing %s's filesystem from %s to %s\n",
		role.Name, gib(g.blockCount*g.blockSize), gib(newBlocks*g.blockSize))
	if err := growExt4(mountpoint, newBlocks); err != nil {
		return fmt.Errorf("growing %s's filesystem: %w", role.Name, err)
	}
	return nil
}
