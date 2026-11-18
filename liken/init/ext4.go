package main

// Init grows a mounted ext4 filesystem without resize2fs.
//
// A filesystem records its size in its superblock, so growing the
// partition does not make the filesystem bigger. For a filesystem that
// stays mounted, resize2fs only sends the kernel one ioctl with the new
// block count. init sends that ioctl itself, so the image does not need
// the e2fsprogs resize binary.

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
)

// hasExt4 reports whether the device has ext4's superblock magic
// (0xEF53, little-endian) at offset 1080, which is 56 bytes into the
// superblock.
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

// ext4Geometry is the block size and block count that a grow needs.
type ext4Geometry struct {
	blockSize  uint64
	blockCount uint64
}

// The superblock starts 1024 bytes into the device for every block
// size.
const ext4SuperblockOffset = 1024

// parseExt4Superblock reads the geometry from the bytes of a
// superblock. These are the on-disk offsets of the fields it uses:
//
//	s_blocks_count_lo  u32 at 4    block count, low 32 bits
//	s_log_block_size   u32 at 24   block size = 1024 << this
//	s_magic            u16 at 56   0xEF53
//	s_feature_incompat u32 at 96   bit 0x80 = the 64bit feature
//	s_blocks_count_hi  u32 at 336  block count's high bits (64bit only)
func parseExt4Superblock(sb []byte) (ext4Geometry, error) {
	if len(sb) < 1024 {
		return ext4Geometry{}, fmt.Errorf("the superblock is %d bytes, want at least 1024", len(sb))
	}
	if sb[56] != 0x53 || sb[57] != 0xEF {
		return ext4Geometry{}, errors.New("the superblock does not hold the ext4 magic number 0xEF53")
	}

	logBlockSize := le32(sb[24:])
	// ext4's largest block size is 64KiB, which is 1024 << 6.
	if logBlockSize > 6 {
		return ext4Geometry{}, fmt.Errorf("the superblock's block size exponent is %d, and ext4 allows at most 6", logBlockSize)
	}
	g := ext4Geometry{
		blockSize:  1024 << logBlockSize,
		blockCount: uint64(le32(sb[4:])),
	}
	// Only a filesystem with the 64bit feature stores the high bits of
	// the block count at offset 336. Without it, those bytes belong to
	// other fields.
	if le32(sb[96:])&0x80 != 0 {
		g.blockCount |= uint64(le32(sb[336:])) << 32
	}
	return g, nil
}

func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

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

// ext4ResizeFS is EXT4_IOC_RESIZE_FS, which is _IOW('f', 16, __u64):
// direction write (1<<30), argument size (8<<16), type ('f'<<8), and
// command number 16.
const ext4ResizeFS = (1 << 30) | (8 << 16) | ('f' << 8) | 16 // 0x40086610

// growExt4 grows the filesystem mounted at mountpoint to newBlocks
// blocks. The ioctl acts on the filesystem of the file descriptor it
// receives, so a descriptor of the mountpoint is enough.
func growExt4(mountpoint string, newBlocks uint64) error {
	f, err := os.OpenFile(mountpoint, os.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), ext4ResizeFS, uintptr(unsafe.Pointer(&newBlocks))); errno != 0 {
		return fmt.Errorf("resizing the filesystem to %d blocks with EXT4_IOC_RESIZE_FS: %w", newBlocks, errno)
	}
	return nil
}

// maybeGrowFilesystem grows a mounted role's filesystem to the size of
// its partition when the partition is bigger. mke2fs reserves blocks
// in the resize_inode for a filesystem to grow to about 1024 times its
// size, so the grow is expected to succeed. When it fails, the error
// fails the role, as a failed mount does.
func maybeGrowFilesystem(role machine.DeclaredRole, p partition, mountpoint string) error {
	g, err := readExt4Geometry(devRoot + "/" + p.name)
	if err != nil {
		return fmt.Errorf("reading the filesystem geometry of the %s role: %w", role.Name, err)
	}
	newBlocks := p.sizeBytes / g.blockSize
	if newBlocks <= g.blockCount {
		return nil
	}
	fmt.Printf("liken: storage: growing the filesystem of the %s role from %s to %s\n",
		role.Name, gib(g.blockCount*g.blockSize), gib(newBlocks*g.blockSize))
	if err := growExt4(mountpoint, newBlocks); err != nil {
		return fmt.Errorf("growing the filesystem of the %s role: %w", role.Name, err)
	}
	return nil
}
