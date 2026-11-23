package main

import (
	"os"
	"path/filepath"
	"testing"
)

// An evidenceChange changes one file of the fixture's evidence. It
// removes the file, replaces its content with text, or appends text to
// it.
type evidenceChange struct {
	file, text      string
	remove, replace bool
}

// evidenceWith copies the fixture's evidence into a directory of the
// test, applies the change, and returns the directory.
func evidenceWith(t *testing.T, change evidenceChange) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS("testdata/evidence")); err != nil {
		t.Fatalf("copying the evidence: %v", err)
	}
	target := filepath.Join(dir, change.file)
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading %s: %v", change.file, err)
	}
	switch {
	case change.remove:
		err = os.Remove(target)
	case change.replace:
		err = os.WriteFile(target, []byte(change.text), 0o644)
	default:
		err = os.WriteFile(target, append(data, change.text...), 0o644)
	}
	if err != nil {
		t.Fatalf("changing %s: %v", change.file, err)
	}
	return dir
}

func TestMergeRefusesEvidenceThatItCannotExplain(t *testing.T) {
	cases := []struct {
		name   string
		change evidenceChange
	}{
		{"a shipped program with no link map", evidenceChange{file: "maps/mke2fs.map", remove: true}},
		{"no record of the shipped files", evidenceChange{file: "shipped", remove: true}},
		{"a shipped file that no package installed", evidenceChange{file: "shipped", text: "data zoneinfo/Mars /target/usr/share/zoneinfo/posix/Mars\n"}},
		{"a shipped file of no kind", evidenceChange{file: "shipped", text: "firmware sbin/mke2fs /target/sbin/mke2fs\n"}},
		{"a shipped file with a relative source", evidenceChange{file: "shipped", text: "data zoneinfo/UTC usr/share/zoneinfo/posix/UTC\n"}},
		{"a file that two unrelated packages installed", evidenceChange{file: "packages-file-list.txt", text: "libevent,./sbin/mke2fs\n"}},
		{"a file of a package that buildroot's SBOM does not list", evidenceChange{file: "packages-file-list.txt", replace: true, text: "toolchain,./sbin/mke2fs\ntzdata,./usr/share/zoneinfo/zone1970.tab\ntzdata,./usr/share/zoneinfo/posix/UTC\n"}},
		{"a line of a file list with no package", evidenceChange{file: "packages-file-list-host.txt", text: "./bin/autoheader\n"}},
		{"a missing file list", evidenceChange{file: "packages-file-list-staging.txt", remove: true}},
		{"an archive that no package installed", evidenceChange{file: "maps/mke2fs.map", text: "/staging/usr/lib/libnone.a(none.o)  mke2fs.o (none)\n"}},
		{"an archive in the build directory of no package", evidenceChange{file: "maps/mke2fs.map", text: "/build/none-1.0/libnone.a(none.o)  mke2fs.o (none)\n"}},
		{"a member of a thin archive that no package installed", evidenceChange{file: "maps/mke2fs.map", text: "/staging/usr/lib/libnone.a.p/none.c.o  mke2fs.o (none)\n"}},
		{"a map that names only archives and ld's own stubs", evidenceChange{file: "maps/mke2fs.map", text: "LOAD linker stubs\nLOAD /staging/lib/libc.a\n", replace: true}},
		{"a map with no section of archive members", evidenceChange{file: "maps/mke2fs.map", text: "LOAD /staging/lib/crt1.o\nLOAD mke2fs.o\nLOAD /staging/usr/lib/libblkid.a\nLOAD /staging/lib/libc.a\n", replace: true}},
		{"a map whose section of archive members names no member", evidenceChange{file: "maps/mke2fs.map", text: "Archive member included to satisfy reference by file (symbol)\n\nLOAD /staging/lib/crt1.o\nLOAD mke2fs.o\nLOAD /staging/lib/libc.a\n", replace: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := fixtureInputs()
			in.evidence = evidenceWith(t, c.change)
			if _, err := merge(in); err == nil {
				t.Error("merge accepted the evidence")
			}
		})
	}
}

// A file under a package's build directory belongs to that package,
// as an archive that a program's link takes from its own source tree
// does. ld names a member of a thin archive, which meson writes, by the
// member's own path.
func TestAFileInABuildDirectoryBelongsToItsPackage(t *testing.T) {
	cases := []struct{ name, line string }{
		{"a member of an archive", "/build/libevent-2.1.13/.libs/libevent.a(event.o)  mke2fs.o (event_base_new)\n"},
		{"a member of a thin archive", "/build/libevent-2.1.13/libevent.a.p/event.c.o\n                              mke2fs.o (event_base_new)\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := fixtureInputs()
			in.evidence = evidenceWith(t, evidenceChange{file: "maps/mke2fs.map", text: c.line})
			b, err := merge(in)
			if err != nil {
				t.Fatalf("merging: %v", err)
			}
			if got := component(t, b, "buildroot:libevent")["scope"]; got != nil {
				t.Errorf("libevent has the scope %v, want none, because mke2fs links a file of its build directory", got)
			}
		})
	}
}
