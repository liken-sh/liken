package image

// Tests for the cpio writer. The consumer that matters is the
// kernel's initramfs unpacker, which can't run in a unit test, so
// these tests parse the newc format back out of the archive and check
// the properties the unpacker relies on: magic numbers, header
// arithmetic, 4-byte alignment, root ownership, and the trailer.

import (
	"bytes"
	"fmt"
	"strconv"
	"testing"
)

// A parsed newc entry, just enough to verify what the writer emits.
type entry struct {
	name string
	mode uint32
	uid  uint32
	gid  uint32
	data []byte
}

// readArchive parses a newc archive the way the kernel's unpacker
// does: fixed 110-byte ASCII headers, names and data each padded to
// four bytes, ending at the TRAILER!!! record.
func readArchive(t *testing.T, raw []byte) []entry {
	t.Helper()
	var entries []entry
	off := 0
	field := func(i int) uint32 {
		start := off + 6 + i*8
		v, err := strconv.ParseUint(string(raw[start:start+8]), 16, 32)
		if err != nil {
			t.Fatalf("header field %d at %d: %v", i, off, err)
		}
		return uint32(v)
	}
	pad4 := func(n int) int { return (n + 3) &^ 3 }
	for {
		if string(raw[off:off+6]) != "070701" {
			t.Fatalf("bad magic at %d: %q", off, raw[off:off+6])
		}
		mode, uid, gid := field(1), field(2), field(3)
		filesize, namesize := int(field(6)), int(field(11))
		name := string(raw[off+110 : off+110+namesize-1])
		dataStart := pad4(off + 110 + namesize)
		data := raw[dataStart : dataStart+filesize]
		off = pad4(dataStart + filesize)
		if name == "TRAILER!!!" {
			if off != len(raw) {
				t.Errorf("%d bytes after the trailer", len(raw)-off)
			}
			return entries
		}
		entries = append(entries, entry{name, mode, uid, gid, data})
	}
}

func TestArchiveRoundTrips(t *testing.T) {
	var buf bytes.Buffer
	w := newArchive(&buf)
	if err := w.dir("etc", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := w.file("etc/token", []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := w.close(); err != nil {
		t.Fatal(err)
	}

	entries := readArchive(t, buf.Bytes())
	if len(entries) != 2 {
		t.Fatalf("got %d entries", len(entries))
	}
	if entries[0].name != "etc" || entries[0].mode&0o170000 != 0o040000 {
		t.Errorf("dir entry: %+v", entries[0])
	}
	if entries[1].name != "etc/token" || string(entries[1].data) != "secret\n" {
		t.Errorf("file entry: %+v", entries[1])
	}
	if entries[1].mode&0o777 != 0o600 {
		t.Errorf("token mode: %o", entries[1].mode&0o777)
	}
}

func TestArchiveEntriesBelongToRoot(t *testing.T) {
	var buf bytes.Buffer
	w := newArchive(&buf)
	if err := w.file("f", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.close(); err != nil {
		t.Fatal(err)
	}
	for _, e := range readArchive(t, buf.Bytes()) {
		if e.uid != 0 || e.gid != 0 {
			t.Errorf("%s owned by %d:%d, want root", e.name, e.uid, e.gid)
		}
	}
}

func TestArchiveAlignsOddSizes(t *testing.T) {
	// Names and file bodies of every length must keep the following
	// header on a 4-byte boundary, or the kernel reads garbage.
	var buf bytes.Buffer
	w := newArchive(&buf)
	for i := range 5 {
		name := fmt.Sprintf("f%d", i)
		if err := w.file(name, bytes.Repeat([]byte("x"), i), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.close(); err != nil {
		t.Fatal(err)
	}
	if got := len(readArchive(t, buf.Bytes())); got != 5 {
		t.Errorf("got %d entries", got)
	}
}
