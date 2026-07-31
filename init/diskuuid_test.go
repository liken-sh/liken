package main

// Tests for the by-uuid readers. An ext4 case pins the superblock
// offsets against a hand-built superblock, the same way ext4_test.go
// does. A FAT32 case formats a real volume with disks.FormatFAT32, the
// way fatstate_test.go does, so the test proves the reader against the
// exact bytes liken itself writes.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/disks"
)

// ext4DeviceWithUUID builds a device image carrying ext4's magic and a
// given 16-byte UUID at the offsets the by-uuid reader parses: the
// superblock starts 1024 bytes into the device, so the image pads that
// much before it.
func ext4DeviceWithUUID(uuid []byte) []byte {
	dev := make([]byte, 2048)
	dev[1024+56], dev[1024+57] = 0x53, 0xEF
	copy(dev[1024+104:1024+120], uuid)
	return dev
}

// deviceFile writes bytes to a tempdir path and returns the path, standing
// in for a partition's device node.
func deviceFile(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "partition")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
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
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFilesystemUUIDReadsAFAT32VolumeID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "volume.img")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(64 << 20); err != nil {
		t.Fatal(err)
	}
	// Little-endian bytes 0x34 0x12 0x78 0x56 at offset 67 assemble into
	// this volume ID, and the reader must render them back as the high
	// half, a dash, then the low half: 5678-1234.
	if err := disks.FormatFAT32(f, 64<<20, "TEST", 0x5678_1234); err != nil {
		t.Fatal(err)
	}
	want := "5678-1234"
	if got := filesystemUUID(path); got != want {
		t.Errorf("got %q, want %q", got, want)
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
				t.Errorf("got %q, want no UUID", got)
			}
		})
	}
}
