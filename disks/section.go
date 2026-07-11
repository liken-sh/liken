package disks

// A Section addresses one partition inside a larger file, which is
// what building a disk image requires: the image file is the whole
// disk, and a filesystem must be written into the partition's window
// of it, at an offset, without being able to reach anything outside.
// On a running machine the kernel provides this windowing itself (the
// /dev/vda1 device *is* a section of /dev/vda); a build tool writing
// a plain file has to bring its own.

import (
	"fmt"
	"io"
	"os"
)

// A Section is an offset, bounded window into a file, usable anywhere
// the formats here want a Device or a reader.
type Section struct {
	f      *os.File
	offset int64
	size   int64
}

// NewSection windows size bytes of f starting at offset.
func NewSection(f *os.File, offset, size int64) *Section {
	return &Section{f: f, offset: offset, size: size}
}

// Size returns the window's length in bytes.
func (s *Section) Size() int64 { return s.size }

// WriteAt writes within the window, refusing to reach past its end:
// a filesystem that tries to write outside its partition is a bug
// worth stopping at the first byte.
func (s *Section) WriteAt(p []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(p)) > s.size {
		return 0, fmt.Errorf("write of %d bytes at %d reaches outside the %d-byte section", len(p), off, s.size)
	}
	return s.f.WriteAt(p, s.offset+off)
}

// ReadAt reads within the window, with the same bounds discipline.
func (s *Section) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= s.size {
		return 0, io.EOF
	}
	if off+int64(len(p)) > s.size {
		p = p[:s.size-off]
		n, err := s.f.ReadAt(p, s.offset+off)
		if err == nil {
			err = io.EOF
		}
		return n, err
	}
	return s.f.ReadAt(p, s.offset+off)
}

// Sync flushes the underlying file: durability is a property of the
// whole file, not the window.
func (s *Section) Sync() error { return s.f.Sync() }
