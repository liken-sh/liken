package disks

// A Section addresses one partition inside a larger file. Building a
// disk image requires this: the image file is the whole disk, and a
// build tool must write a filesystem into the partition's window of
// it, at an offset, without reaching anything outside that window.
// On a running machine, the kernel provides this windowing itself;
// the /dev/vda1 device is a section of /dev/vda. A build tool that
// writes a plain file must implement this windowing itself.

import (
	"fmt"
	"io"
	"os"
)

// A Section is a bounded window into a file, at an offset. The
// formats in this package can use a Section anywhere they want a
// Device or a reader.
type Section struct {
	f      *os.File
	offset int64
	size   int64
}

// NewSection creates a window of size bytes into f, starting at
// offset.
func NewSection(f *os.File, offset, size int64) *Section {
	return &Section{f: f, offset: offset, size: size}
}

// Size returns the window's length in bytes.
func (s *Section) Size() int64 { return s.size }

// WriteAt writes within the window. WriteAt refuses to write past
// the window's end. A filesystem that tries to write outside its
// partition has a bug, and this code stops it at the first byte.
func (s *Section) WriteAt(p []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(p)) > s.size {
		return 0, fmt.Errorf("write of %d bytes at %d reaches outside the %d-byte section", len(p), off, s.size)
	}
	return s.f.WriteAt(p, s.offset+off)
}

// ReadAt reads within the window, with the same bounds check as
// WriteAt.
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

// Sync flushes the underlying file. Durability is a property of the
// whole file, not of the window.
func (s *Section) Sync() error { return s.f.Sync() }
