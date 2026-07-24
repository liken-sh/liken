package main

// Durability: how the installer makes a write survive a power cut.
//
// The system slots are FAT32, because firmware can read FAT32 and can
// read almost nothing else. FAT has no journal, so nothing in the
// filesystem promises that a half-finished write is recognizable as
// half-finished. The discipline that makes a write safe therefore
// lives here, in the code that performs it, and it has three parts:
// write to a temporary name, force the bytes out, then rename. A
// power cut before the rename leaves a .partial file that no later
// boot looks at; a power cut after it leaves a complete file.
//
// "Force the bytes out" means fsync, not sync. sync writes dirty pages
// back to the driver and returns. A drive with a volatile write cache
// reports those writes complete while the bytes are still in the
// drive's own RAM. fsync is the call that asks the drive to empty that
// cache, and it is the difference between a file that exists after a
// power cut and one that does not.
//
// The same discipline serves the machine's other FAT filesystems: the
// boot home that carries GRUB's configuration, and the installation
// stick that the hardware report writes its proposal to.

import (
	"os"

	"github.com/liken-sh/liken/machine"
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

// copyDurably copies through a temporary name, runs fsync, and
// renames the file, so the slot never holds a file that looks final
// but is not. FAT has no journal, so durability here depends entirely
// on this discipline. Without the explicit sync before the rename,
// the page cache may still hold the file's bytes when the rename
// happens, and a power cut then leaves a final-looking file with
// incomplete contents.
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

// syncDirectory pushes one directory's own contents, the names of the
// files in it, all the way to the medium.
//
// The two functions above make each file's bytes durable, and stop
// there. The rename that gives a file its final name is a directory
// update, and nothing has forced that update out yet. On FAT, an fsync
// of a directory takes the same path as an fsync of a file
// (fat_file_fsync), which ends in a flush of the whole block device, so
// one call covers every rename made on the filesystem it names.
//
// The directory opens read-only, which is what fsync on a directory
// needs; writing to a directory is the kernel's job, not ours.
func syncDirectory(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// writeFileDurably writes bytes through a temporary name, then it
// runs fsync and renames the file. This is the same method that
// copyDurably applies to slot artifacts, for the same reason: FAT has
// no journal, so the code itself must enforce durability.
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
