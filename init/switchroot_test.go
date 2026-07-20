package main

// Tests for the tree carry that brings the boot loader's extra files
// (the deployment layer) onto the overlay root, run against real
// temporary directories. Tests for the switch_root procedure itself
// (loop devices, mounts, chroot, exec) run separately, under QEMU.

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCarryTreeReplicatesDirsFilesAndSymlinks(t *testing.T) {
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
	if err := carryTree(src, dst, nil); err != nil {
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

func TestCarryTreeSkipsTheBootArchivesOwnSubtrees(t *testing.T) {
	// The boot archive's module tree, and /dev, and the staging
	// mounts, must never land on the real root. The skip list names
	// them by path.
	src, dst := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "lib", "modules", "boot"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "lib", "modules", "boot", "overlay.ko"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "liken"), []byte("init"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "etc", "liken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "etc", "liken", "cluster.yaml"), []byte("kind: Cluster"), 0o644); err != nil {
		t.Fatal(err)
	}

	skip := []string{
		filepath.Join(src, "lib", "modules", "boot"),
		filepath.Join(src, "liken"),
	}
	if err := carryTree(src, dst, skip); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "etc", "liken", "cluster.yaml")); err != nil {
		t.Error("the layer's files come along")
	}
	if _, err := os.Stat(filepath.Join(dst, "liken")); !os.IsNotExist(err) {
		t.Error("a skipped file must stay behind")
	}
	if _, err := os.Stat(filepath.Join(dst, "lib", "modules", "boot")); !os.IsNotExist(err) {
		t.Error("a skipped subtree must stay behind entirely")
	}
	if _, err := os.Stat(filepath.Join(dst, "lib", "modules")); err != nil {
		t.Error("a skipped subtree's parents still come along")
	}
}

func TestCarryTreeOverwritesWhatTheDestinationAlreadyHas(t *testing.T) {
	// The destination is the overlay, whose lower layer already
	// carries the system's copy of anything that the layer overrides.
	// The depmod index is the most important example. Later archives
	// win: that is the initramfs contract that the carry preserves.
	src, dst := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "lib", "modules", "r1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "lib", "modules", "r1", "modules.dep"), []byte("complete"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dst, "lib", "modules", "r1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "lib", "modules", "r1", "modules.dep"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := carryTree(src, dst, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "lib", "modules", "r1", "modules.dep"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "complete" {
		t.Errorf("the carried file must win, got %q", got)
	}
	info, err := os.Stat(filepath.Join(dst, "lib", "modules", "r1"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("an existing directory keeps its permissions, got %v", info.Mode().Perm())
	}
}

func TestCarryTreeRefusesUnexpectedFileTypes(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	if err := unix.Mkfifo(filepath.Join(src, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := carryTree(src, dst, nil); err == nil {
		t.Error("a fifo is not part of any initramfs we built; the carry must refuse it")
	}
}

func TestCarryTreeToleratesAnExistingSymlink(t *testing.T) {
	// The overlay's lower layer ships symlinks of its own, such as
	// mtab and the iptables aliases. Carrying the same link again does
	// nothing, and is not a failure.
	src, dst := t.TempDir(), t.TempDir()
	if err := os.Symlink("target", filepath.Join(src, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(dst, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := carryTree(src, dst, nil); err != nil {
		t.Errorf("an existing symlink must not fail the carry: %v", err)
	}
}

func TestCopyFileReportsAMissingSource(t *testing.T) {
	if err := copyFile(filepath.Join(t.TempDir(), "absent"), filepath.Join(t.TempDir(), "out"), 0o644); err == nil {
		t.Error("a missing source must be reported")
	}
}

func TestCopyFileReportsAnUnwritableDestination(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(src, []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, filepath.Join(t.TempDir(), "no-such-dir", "out"), 0o644); err == nil {
		t.Error("an unwritable destination must be reported")
	}
}
