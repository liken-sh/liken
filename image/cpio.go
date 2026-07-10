// Package image assembles the pieces of liken into the archives a
// machine boots. build.sh produces the generic operating system;
// this package produces the deployment layer that rides on top of it
// (layer.go explains the split and why concatenation joins them).
package image

// A cpio writer, for the one format that matters here: "newc", the
// only archive format the kernel's initramfs unpacker accepts.
//
// The format is from 1990 and it is delightfully simple, which is
// exactly why the kernel adopted it: every entry is a fixed 110-byte
// ASCII header (a magic number and thirteen 8-digit hexadecimal
// fields), then the file name, then the file's bytes, each padded to
// a 4-byte boundary. A special final entry named TRAILER!!! marks the
// end. There is no index, no compression, and no seeking: the kernel
// reads it start to finish, creating each node as it appears, which
// is why parent directories must be written before their contents.
//
// Two properties matter for an initramfs and are fixed here rather
// than configurable: every entry belongs to root (uid/gid 0),
// whoever ran the build; and hardlink handling is unused (nlink is 1
// for files, 2 for directories, and every data length is real), so
// the unpacker's deduplication paths never engage.

import (
	"fmt"
	"io"
)

type archive struct {
	w   io.Writer
	off int // bytes written, for the 4-byte alignment arithmetic
	ino int // a fresh inode number per entry; the unpacker treats
	// (dev, ino) pairs as hardlink identity, so they must differ
}

func newArchive(w io.Writer) *archive {
	return &archive{w: w}
}

// header writes one newc header plus the entry's name, padded so the
// data that follows starts 4-byte aligned. The thirteen fields, in
// order: inode, mode, uid, gid, nlink, mtime, filesize, devmajor,
// devminor, rdevmajor, rdevminor, namesize, check (always zero;
// newc's sibling format "crc" uses it, newc does not).
func (a *archive) header(name string, mode, nlink, filesize int) error {
	a.ino++
	// mtime is zero deliberately: the archive's bytes then depend
	// only on its contents, not on when it was built, which keeps a
	// deployment layer reproducible and its digest stable.
	if err := a.write([]byte(fmt.Sprintf(
		"070701%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X",
		a.ino, mode, 0, 0, nlink, 0, filesize, 0, 0, 0, 0, len(name)+1, 0))); err != nil {
		return err
	}
	if err := a.write(append([]byte(name), 0)); err != nil {
		return err
	}
	return a.pad()
}

// dir writes one directory entry. Directories carry no data; the
// S_IFDIR bits in the mode are what make the unpacker mkdir.
func (a *archive) dir(name string, perm int) error {
	return a.header(name, 0o040000|perm, 2, 0)
}

// file writes one regular file entry: header, then the bytes, then
// padding to keep the next header aligned.
func (a *archive) file(name string, data []byte, perm int) error {
	if err := a.header(name, 0o100000|perm, 1, len(data)); err != nil {
		return err
	}
	if err := a.write(data); err != nil {
		return err
	}
	return a.pad()
}

// close writes the trailer entry that tells the unpacker the archive
// is complete.
func (a *archive) close() error {
	return a.header("TRAILER!!!", 0, 1, 0)
}

func (a *archive) write(b []byte) error {
	n, err := a.w.Write(b)
	a.off += n
	return err
}

func (a *archive) pad() error {
	if rem := a.off % 4; rem != 0 {
		return a.write(make([]byte, 4-rem))
	}
	return nil
}
