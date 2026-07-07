package main

// Tests for the ext4 superblock parser. Hand-built superblocks pin
// the offsets in ext4.go's comment; the resize ioctl itself needs a
// mounted filesystem and belongs to the QEMU harness.

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/chrisguidry/liken/machine"
)

// superblock builds 1024 bytes with the fields growth reads.
func superblock(blocksLo uint32, logBlockSize uint32, incompat uint32, blocksHi uint32) []byte {
	sb := make([]byte, 1024)
	binary.LittleEndian.PutUint32(sb[4:], blocksLo)
	binary.LittleEndian.PutUint32(sb[24:], logBlockSize)
	sb[56], sb[57] = 0x53, 0xEF
	binary.LittleEndian.PutUint32(sb[96:], incompat)
	binary.LittleEndian.PutUint32(sb[336:], blocksHi)
	return sb
}

func TestParseExt4Superblock(t *testing.T) {
	cases := []struct {
		name string
		sb   []byte
		want ext4Geometry
	}{
		{
			// mke2fs's default for small filesystems: 1 KiB blocks.
			"1KiB-blocks",
			superblock(65_536, 0, 0, 0),
			ext4Geometry{blockSize: 1_024, blockCount: 65_536},
		},
		{
			// The common case on real disks: 4 KiB blocks.
			"4KiB-blocks",
			superblock(262_144, 2, 0, 0),
			ext4Geometry{blockSize: 4_096, blockCount: 262_144},
		},
		{
			// With the 64bit feature, the count's high bits live in a
			// second field and must be assembled.
			"64bit-count",
			superblock(1, 2, 0x80, 1),
			ext4Geometry{blockSize: 4_096, blockCount: 1<<32 | 1},
		},
		{
			// Without the feature, those same bytes belong to other
			// fields and must be ignored.
			"high-bits-ignored-without-64bit",
			superblock(100, 2, 0, 1),
			ext4Geometry{blockSize: 4_096, blockCount: 100},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseExt4Superblock(c.sb)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestParseExt4SuperblockRejectsBadMagic(t *testing.T) {
	sb := superblock(100, 2, 0, 0)
	sb[56] = 0x00
	if _, err := parseExt4Superblock(sb); err == nil {
		t.Error("expected an error without the ext4 magic")
	}
}

func TestParseExt4SuperblockRejectsAbsurdBlockSizes(t *testing.T) {
	if _, err := parseExt4Superblock(superblock(100, 7, 0, 0)); err == nil {
		t.Error("expected an error for a block size exponent past ext4's ceiling")
	}
}

func TestParseExt4SuperblockRejectsTruncation(t *testing.T) {
	if _, err := parseExt4Superblock(make([]byte, 100)); err == nil {
		t.Error("expected an error for a truncated superblock")
	}
}

func TestReadExt4GeometryFromADevice(t *testing.T) {
	// A device node is just something to ReadAt; a file stands in.
	dev := filepath.Join(t.TempDir(), "vda1")
	image := make([]byte, 2048)
	sb := image[ext4SuperblockOffset:]
	sb[56], sb[57] = 0x53, 0xEF // s_magic
	sb[4] = 100                 // s_blocks_count_lo
	sb[24] = 2                  // s_log_block_size: 1024 << 2 = 4096
	if err := os.WriteFile(dev, image, 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := readExt4Geometry(dev)
	if err != nil {
		t.Fatal(err)
	}
	if g.blockSize != 4096 || g.blockCount != 100 {
		t.Errorf("got %+v", g)
	}
}

func TestReadExt4GeometryReportsMissingDevices(t *testing.T) {
	if _, err := readExt4Geometry(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("a missing device is an error, not empty geometry")
	}
}

func TestReadExt4GeometryReportsTruncatedDevices(t *testing.T) {
	dev := filepath.Join(t.TempDir(), "tiny")
	if err := os.WriteFile(dev, make([]byte, 512), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readExt4Geometry(dev); err == nil {
		t.Error("a device too small for a superblock is an error")
	}
}

func TestMaybeGrowFilesystemLeavesAFullFilesystemAlone(t *testing.T) {
	// The superblock says 100 blocks of 4096 bytes; the partition is
	// exactly that size, so there is nothing to grow and no ioctl to
	// make. (An actual grow needs a mounted filesystem: QEMU's drill.)
	sys, dev := fakeMachine(t)
	_ = sys
	image := make([]byte, 2048)
	sb := image[ext4SuperblockOffset:]
	sb[56], sb[57] = 0x53, 0xEF
	sb[4] = 100
	sb[24] = 2
	if err := os.WriteFile(filepath.Join(dev, "vda1"), image, 0o644); err != nil {
		t.Fatal(err)
	}
	role := declared(machine.MachineStateRole, "/dev/vda", "")
	p := partition{name: "vda1", disk: "vda", sizeBytes: 100 * 4096}
	if err := maybeGrowFilesystem(role, p, t.TempDir()); err != nil {
		t.Errorf("a filesystem already filling its partition needs nothing: %v", err)
	}
}

func TestMaybeGrowFilesystemReportsAnUnreadableDevice(t *testing.T) {
	fakeMachine(t)
	role := declared(machine.MachineStateRole, "/dev/vda", "")
	p := partition{name: "vda1", disk: "vda", sizeBytes: 1 << 30}
	if err := maybeGrowFilesystem(role, p, t.TempDir()); err == nil {
		t.Error("no device, no geometry: the error must surface")
	}
}
