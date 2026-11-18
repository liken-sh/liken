package main

// The ext4 test builds a superblock by hand to check the offsets. The
// FAT32 test formats a real volume with disks.FormatFAT32, as
// fatstate_test.go does, so the reader is tested against the bytes that
// liken writes.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/liken/disks"
)

// ext4DeviceWithUUID returns a device image with ext4's magic and the
// given 16-byte UUID in a superblock at offset 1024.
func ext4DeviceWithUUID(uuid []byte) []byte {
	dev := make([]byte, 2048)
	dev[1024+56], dev[1024+57] = 0x53, 0xEF
	copy(dev[1024+104:1024+120], uuid)
	return dev
}

// deviceFile writes content to a file in a temporary directory, which
// stands in for a partition's device node, and returns its path.
func deviceFile(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "partition")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("writing the device file: %v", err)
	}
	return path
}

func TestFilesystemUUIDReadsAnExt4Superblock(t *testing.T) {
	uuid := []byte{
		0x01, 0x02, 0x03, 0x04,
		0x05, 0x06,
		0x07, 0x08,
		0x09, 0x0a,
		0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
	}
	dev := deviceFile(t, ext4DeviceWithUUID(uuid))
	want := "01020304-0506-0708-090a-0b0c0d0e0f10"
	if got := filesystemUUID(dev); got != want {
		t.Errorf("filesystemUUID is %q, want %q", got, want)
	}
}

func TestFilesystemUUIDReadsAFAT32VolumeID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "volume.img")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating the volume image: %v", err)
	}
	defer f.Close()
	if err := f.Truncate(64 << 20); err != nil {
		t.Fatalf("sizing the volume image: %v", err)
	}
	// FormatFAT32 stores the ID little-endian at offset 67, as the bytes
	// 0x34 0x12 0x78 0x56. The reader prints the high half first.
	if err := disks.FormatFAT32(f, 64<<20, "TEST", 0x5678_1234); err != nil {
		t.Fatalf("formatting the volume image: %v", err)
	}
	want := "5678-1234"
	if got := filesystemUUID(path); got != want {
		t.Errorf("filesystemUUID is %q, want %q", got, want)
	}
}

func TestFilesystemUUIDIsEmptyWithoutAKnownFilesystem(t *testing.T) {
	cases := []struct {
		name string
		dev  string
	}{
		{"zeroed disk", deviceFile(t, make([]byte, 2<<20))},
		{"short read", deviceFile(t, []byte("too small for a superblock"))},
		{"missing device", filepath.Join(t.TempDir(), "absent")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := filesystemUUID(c.dev); got != "" {
				t.Errorf("filesystemUUID is %q, want no UUID", got)
			}
		})
	}
}
