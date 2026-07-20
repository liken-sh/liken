// Package image assembles the pieces of liken into the archives a
// machine boots. build.sh produces the generic operating system.
// This package produces the deployment layer that rides on top of
// it (layer.go explains the split and why concatenation joins them).
package image

// This file implements a cpio writer, for the one format that
// matters here: "newc", the only archive format the kernel's
// initramfs unpacker accepts.
//
// The format dates from 1990 and is simple, which is exactly why the
// kernel adopted it. Every entry is a fixed 110-byte ASCII header (a
// magic number and thirteen 8-digit hexadecimal fields), then the
// file name, then the file's bytes, each padded to a 4-byte
// boundary. A special final entry named TRAILER!!! marks the end.
// There is no index, no compression, and no seeking. The kernel
// reads the archive from start to finish, creating each node as it
// appears. This is why parent directories must be written before
// their contents.
//
// Two properties matter for an initramfs, so this writer fixes them
// rather than making them configurable. Every entry belongs to root
// (uid/gid 0), no matter who ran the build. And hardlink handling
// goes unused (nlink is 1 for files, 2 for directories, and every
// data length is real), so the unpacker's deduplication paths never
// run.

import (
	"fmt"
	"io"
)

type archive struct {
	w   io.Writer
	off int // the number of bytes written so far, used for 4-byte alignment
	ino int // a fresh inode number for each entry. The unpacker treats
	// (dev, ino) pairs as hardlink identity, so each entry's number must differ.
}

func newArchive(w io.Writer) *archive {
	return &archive{w: w}
}

// header writes one newc header, plus the entry's name, padded so
// the data that follows starts on a 4-byte boundary. The header
// holds thirteen fields, in this order: inode, mode, uid, gid,
// nlink, mtime, filesize, devmajor, devminor, rdevmajor, rdevminor,
// namesize, and check. check is always zero here; newc's sibling
// format, "crc", uses it, but newc does not.
func (a *archive) header(name string, mode, nlink, filesize int) error {
	a.ino++
	// mtime is zero on purpose. This way, the archive's bytes depend
	// only on its contents, not on when it was built. This keeps a
	// deployment layer reproducible and its digest stable.
	if err := a.write(fmt.Appendf(nil,
		"070701%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X%08X",
		a.ino, mode, 0, 0, nlink, 0, filesize, 0, 0, 0, 0, len(name)+1, 0)); err != nil {
		return err
	}
	if err := a.write(append([]byte(name), 0)); err != nil {
		return err
	}
	return a.pad()
}

// dir writes one directory entry. Directories carry no data. The
// S_IFDIR bits in the mode tell the unpacker to create the directory.
func (a *archive) dir(name string, perm int) error {
	return a.header(name, 0o040000|perm, 2, 0)
}

// file writes one regular file entry: the header, then the bytes,
// then padding to keep the next header aligned.
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
