package main

// These tests run on temporary files, so they check the results of the
// calls and cannot check what a power cut does.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyFile(t *testing.T) {
	dir := t.TempDir()
	artifact := artifactFor(t, dir, "vmlinuz", []byte("the kernel"))
	if err := verifyFile(artifact, filepath.Join(dir, "vmlinuz")); err != nil {
		t.Errorf("verifyFile returned %v for matching bytes, want nil", err)
	}
	artifact.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	if err := verifyFile(artifact, filepath.Join(dir, "vmlinuz")); err == nil {
		t.Error("verifying a wrong digest returned no error, want one")
	}
	if err := verifyFile(artifact, filepath.Join(dir, "missing")); err == nil {
		t.Error("verifying a missing file returned no error, want one")
	}
}

func TestCopyDurably(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if err := os.WriteFile(source, []byte("payload"), 0o644); err != nil {
		t.Fatalf("writing the source: %v", err)
	}
	dest := filepath.Join(dir, "dest")
	if err := copyDurably(source, dest); err != nil {
		t.Fatalf("copying: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "payload" {
		t.Errorf("the copy has %q and error %v, want payload", got, err)
	}
	if _, err := os.Stat(dest + ".partial"); err == nil {
		t.Errorf("%s.partial still exists after the rename, want it gone", dest)
	}
	if err := copyDurably(filepath.Join(dir, "missing"), dest); err == nil {
		t.Error("copying a missing source returned no error, want one")
	}
}

func TestCopyDurablyReportsAnUnwritableDestination(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if err := os.WriteFile(source, []byte("payload"), 0o644); err != nil {
		t.Fatalf("writing the source: %v", err)
	}
	sealed := filepath.Join(dir, "sealed")
	if err := os.Mkdir(sealed, 0o555); err != nil {
		t.Fatalf("creating the read-only directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })
	if err := copyDurably(source, filepath.Join(sealed, "dest")); err == nil {
		t.Error("copying into a read-only directory returned no error, want one")
	}
}

func TestCopyDurablyReportsAMissingSource(t *testing.T) {
	dir := t.TempDir()
	err := copyDurably(filepath.Join(dir, "absent"), filepath.Join(dir, "dest"))
	if err == nil {
		t.Error("copying a missing source returned no error, want one")
	}
}

func TestSyncDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "vmlinuz"), []byte("the kernel"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	if err := syncDirectory(dir); err != nil {
		t.Errorf("syncDirectory returned %v, want nil", err)
	}
	if err := syncDirectory(filepath.Join(dir, "no-such-directory")); err == nil {
		t.Error("syncing a missing directory returned no error, want one")
	}
}
