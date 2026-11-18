package main

// The installer writes files so that they survive a power cut.
//
// The system slots are FAT32, because firmware can read FAT32 and
// little else. FAT has no journal, so after a power cut nothing in the
// filesystem shows that a write was incomplete. So each write goes to a
// temporary name ending in .partial, calls fsync on the file, and then
// renames it. A power cut before the rename leaves a .partial file that
// nothing reads.
//
// The call must be fsync. A drive with a volatile write cache can
// report a write as complete while the data is still in the drive's
// RAM, and fsync is the call that asks the drive to flush that cache.
//
// The fsync makes the file's data durable, but not the rename. On FAT,
// the directory entry that the rename writes stays in the block
// device's buffers until something writes them out. A caller that needs
// the rename on the disk calls unix.Sync and then syncDirectory, as
// flushSlot does.
//
// The same method is used for the other FAT filesystems of the machine:
// the boot home that holds GRUB's configuration, and the installation
// stick that the hardware report writes its proposal to.

import (
	"os"

	"github.com/liken-sh/liken/liken/machine"
)

// verifyFile checks one file on disk against its release artifact.
func verifyFile(artifact machine.ReleaseArtifact, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return artifact.Verify(f)
}

// copyDurably copies source to a .partial file, calls fsync on it, and
// renames it to dest. Without the fsync, the file's bytes can still be
// in the page cache when the rename reaches the disk, and a power cut
// then leaves dest with its final name and partial contents.
func copyDurably(source, dest string) error {
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp := dest + ".partial"
	dst, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := dst.ReadFrom(src); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Sync(); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

// syncDirectory calls fsync on a directory, which flushes the drive's
// write cache. On FAT it does not write out the directory entries that
// are still in the block device's buffers, so call unix.Sync first when
// a rename in the directory must reach the disk.
func syncDirectory(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// writeFileDurably writes data to a .partial file, calls fsync on it,
// and renames it to path. It is copyDurably for data that is already in
// memory.
func writeFileDurably(path string, data []byte) error {
	tmp := path + ".partial"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
