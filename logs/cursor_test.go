package main

// The cursor's only job is to be available after a container restart,
// and to fail without causing harm in every other case. Because of
// this, these tests mostly cover the failure cases: missing,
// corrupt, and torn writes.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCursorRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := saveCursor(dir, kmsgCursor{Seq: 812}); err != nil {
		t.Fatal(err)
	}
	var cur kmsgCursor
	if !loadCursor(dir, &cur) {
		t.Fatal("saved cursor did not load")
	}
	if cur.Seq != 812 {
		t.Errorf("seq: got %d, want 812", cur.Seq)
	}
}

func TestCursorOverwrites(t *testing.T) {
	dir := t.TempDir()
	if err := saveCursor(dir, tailCursor{Dev: 1, Ino: 2, Offset: 100}); err != nil {
		t.Fatal(err)
	}
	if err := saveCursor(dir, tailCursor{Dev: 1, Ino: 2, Offset: 250}); err != nil {
		t.Fatal(err)
	}
	var cur tailCursor
	if !loadCursor(dir, &cur) {
		t.Fatal("cursor did not load")
	}
	if cur.Offset != 250 {
		t.Errorf("offset: got %d, want the later save's 250", cur.Offset)
	}
}

func TestMissingCursorIsAFreshStart(t *testing.T) {
	var cur kmsgCursor
	if loadCursor(t.TempDir(), &cur) {
		t.Error("an empty dir should not produce a cursor")
	}
}

func TestCorruptCursorIsAFreshStart(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, cursorFile), []byte("{torn"), 0o600); err != nil {
		t.Fatal(err)
	}
	var cur kmsgCursor
	if loadCursor(dir, &cur) {
		t.Error("a corrupt cursor should read as no cursor")
	}
}

// The temp file must not remain after a save. A rename that completed
// leaves only cursor.json. A crash before the rename leaves only the
// temp file, which the loader ignores.
func TestSaveLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	if err := saveCursor(dir, kmsgCursor{Seq: 1}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != cursorFile {
		t.Errorf("dir should hold exactly %s: %v", cursorFile, entries)
	}
}
