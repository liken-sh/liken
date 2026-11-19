package image

// Tests for the cpio writer. The consumer that matters is the
// kernel's initramfs unpacker, which cannot run in a unit test. So
// these tests parse the newc format back out of the archive and
// check the properties the unpacker relies on: magic numbers, header
// arithmetic, 4-byte alignment, root ownership, and the trailer.

import (
	"bytes"
	"fmt"
	"testing"
)

// readArchive parses one archive with the production reader
// (cpio_read.go) and asserts that it stands alone: exactly one
// archive, nothing after the trailer. Tests that expect concatenation
// call readCPIO themselves.
func readArchive(t *testing.T, raw []byte) []cpioEntry {
	t.Helper()
	entries, rest, err := readCPIO(raw)
	if err != nil {
		t.Fatalf("reading the archive: %v", err)
	}
	if len(rest) != 0 {
		t.Errorf("%d bytes follow the trailer, want 0", len(rest))
	}
	return entries
}

func TestArchiveRoundTrips(t *testing.T) {
	var buf bytes.Buffer
	w := newArchive(&buf)
	if err := w.dir("etc", 0o755); err != nil {
		t.Fatalf("writing the directory etc: %v", err)
	}
	if err := w.file("etc/token", []byte("secret\n"), 0o600); err != nil {
		t.Fatalf("writing the file etc/token: %v", err)
	}
	if err := w.close(); err != nil {
		t.Fatalf("closing the archive: %v", err)
	}

	entries := readArchive(t, buf.Bytes())
	if len(entries) != 2 {
		t.Fatalf("the archive has %d entries, want 2", len(entries))
	}
	if entries[0].name != "etc" || entries[0].mode&0o170000 != 0o040000 {
		t.Errorf("the first entry is %+v, want the directory etc", entries[0])
	}
	if entries[1].name != "etc/token" || string(entries[1].data) != "secret\n" {
		t.Errorf("the second entry is %+v, want the file etc/token with the content \"secret\\n\"", entries[1])
	}
	if entries[1].mode&0o777 != 0o600 {
		t.Errorf("etc/token has mode %o, want 600", entries[1].mode&0o777)
	}
}

func TestArchiveEntriesBelongToRoot(t *testing.T) {
	var buf bytes.Buffer
	w := newArchive(&buf)
	if err := w.file("f", []byte("x"), 0o644); err != nil {
		t.Fatalf("writing the file f: %v", err)
	}
	if err := w.close(); err != nil {
		t.Fatalf("closing the archive: %v", err)
	}
	for _, e := range readArchive(t, buf.Bytes()) {
		if e.uid != 0 || e.gid != 0 {
			t.Errorf("%s owned by %d:%d, want root", e.name, e.uid, e.gid)
		}
	}
}

func TestArchiveAlignsOddSizes(t *testing.T) {
	// Names and file bodies of every length must keep the next header
	// on a 4-byte boundary. Otherwise the kernel reads incorrect data.
	var buf bytes.Buffer
	w := newArchive(&buf)
	for i := range 5 {
		name := fmt.Sprintf("f%d", i)
		if err := w.file(name, bytes.Repeat([]byte("x"), i), 0o644); err != nil {
			t.Fatalf("writing the file %s: %v", name, err)
		}
	}
	if err := w.close(); err != nil {
		t.Fatalf("closing the archive: %v", err)
	}
	if got := len(readArchive(t, buf.Bytes())); got != 5 {
		t.Errorf("the archive has %d entries, want 5", got)
	}
}
