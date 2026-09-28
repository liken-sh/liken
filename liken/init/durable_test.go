package main

// Tests for the durability discipline: verification against the release
// document, the write-fsync-rename copy, and the directory flush that
// makes a rename outlive a power cut. These run against temp files, so
// what they pin is the sequence of operations, not the hardware
// behavior underneath it.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyFile(t *testing.T) {
	dir := t.TempDir()
	artifact := artifactFor(t, dir, "vmlinuz", []byte("the kernel"))
	if err := verifyFile(artifact, filepath.Join(dir, "vmlinuz")); err != nil {
		t.Errorf("matching bytes verify: %v", err)
	}
	artifact.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	if err := verifyFile(artifact, filepath.Join(dir, "vmlinuz")); err == nil {
		t.Error("a wrong digest must fail verification")
	}
	if err := verifyFile(artifact, filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing file must fail verification")
	}
}

func TestCopyDurably(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if err := os.WriteFile(source, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "dest")
	if err := copyDurably(source, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "payload" {
		t.Errorf("the copy carries the bytes: %q, %v", got, err)
	}
	if _, err := os.Stat(dest + ".partial"); err == nil {
		t.Error("no .partial file may remain after the rename")
	}
	if err := copyDurably(filepath.Join(dir, "missing"), dest); err == nil {
		t.Error("a missing source is an error")
	}
}

func TestCopyDurablyReportsAnUnwritableDestination(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if err := os.WriteFile(source, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	sealed := filepath.Join(dir, "sealed")
	if err := os.Mkdir(sealed, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })
	if err := copyDurably(source, filepath.Join(sealed, "dest")); err == nil {
		t.Error("an unwritable slot is an error the install must surface")
	}
}

func TestCopyDurablyReportsAMissingSource(t *testing.T) {
	dir := t.TempDir()
	err := copyDurably(filepath.Join(dir, "absent"), filepath.Join(dir, "dest"))
	if err == nil {
		t.Error("a source that doesn't exist can't be copied")
	}
}

func TestSyncDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "vmlinuz"), []byte("the kernel"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := syncDirectory(dir); err != nil {
		t.Errorf("a directory's own entries must be flushable: %v", err)
	}
	if err := syncDirectory(filepath.Join(dir, "no-such-directory")); err == nil {
		t.Error("a directory that isn't there can't be flushed, and the install must hear about it")
	}
}
