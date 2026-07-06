package main

// Tests for the tree copy that carries the initramfs onto the new
// root, against real temporary directories. The switch_root dance
// itself (mounts, chroot, exec) is QEMU territory.

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestFsTypeNameKnowsTheBootFilesystems(t *testing.T) {
	if got := fsTypeName(unix.TMPFS_MAGIC); got != "tmpfs" {
		t.Errorf("got %q", got)
	}
	if got := fsTypeName(unix.RAMFS_MAGIC); got != "ramfs" {
		t.Errorf("got %q", got)
	}
	if got := fsTypeName(0x1234); got != "magic 0x1234" {
		t.Errorf("an unknown magic is reported raw: %q", got)
	}
}

func TestCopyTreeReplicatesDirsFilesAndSymlinks(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "sbin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sbin", "tool"), []byte("#!"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("tool", filepath.Join(src, "sbin", "alias")); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(dst, "sbin", "tool"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o750 {
		t.Errorf("permissions must survive bit for bit, got %v", info.Mode().Perm())
	}
	if link, err := os.Readlink(filepath.Join(dst, "sbin", "alias")); err != nil || link != "tool" {
		t.Errorf("symlinks copy as symlinks: %q, %v", link, err)
	}
}

func TestCopyTreeLeavesDevBehind(t *testing.T) {
	// /dev holds device nodes the kernel owns, not files the image
	// delivered; the copy takes the empty directory and nothing in it.
	src, dst := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "dev"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "dev", "stray"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "dev")); err != nil {
		t.Error("the empty /dev directory itself comes along")
	}
	if _, err := os.Stat(filepath.Join(dst, "dev", "stray")); !os.IsNotExist(err) {
		t.Error("nothing inside /dev should be copied")
	}
}

func TestCopyTreeRefusesUnexpectedFileTypes(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	if err := unix.Mkfifo(filepath.Join(src, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(src, dst); err == nil {
		t.Error("a fifo is not part of any initramfs we built; the copy must refuse it")
	}
}

func TestCopyFileRefusesToOverwrite(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	dst := filepath.Join(t.TempDir(), "dst")
	if err := os.WriteFile(src, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst, 0o644); err == nil {
		t.Error("the new root starts empty; an existing file is a bug to surface")
	}
}
