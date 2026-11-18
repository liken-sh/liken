package main

// The switch itself needs root, loop devices, and a real boot, so
// these tests cover only the file copy, in temporary directories.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestNoRoleMountsOverTheBootMounts(t *testing.T) {
	// A role mounted on bootMountsDir or a parent of it would hide the
	// slot and the image that the machine booted from.
	for name, role := range roleMounts {
		if role.path == bootMountsDir || strings.HasPrefix(bootMountsDir, role.path+"/") {
			t.Errorf("role %s mounts at %s, which covers %s, want no role over the boot mounts", name, role.path, bootMountsDir)
		}
	}
}

func TestCopyTreeReplicatesDirsFilesAndSymlinks(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "sbin"), 0o755); err != nil {
		t.Fatalf("creating the sbin directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "sbin", "tool"), []byte("#!"), 0o750); err != nil {
		t.Fatalf("writing the tool: %v", err)
	}
	if err := os.Symlink("tool", filepath.Join(src, "sbin", "alias")); err != nil {
		t.Fatalf("creating the alias symlink: %v", err)
	}
	if err := copyTree(src, dst, nil); err != nil {
		t.Fatalf("copying the tree: %v", err)
	}

	info, err := os.Stat(filepath.Join(dst, "sbin", "tool"))
	if err != nil {
		t.Fatalf("reading the copied tool: %v", err)
	}
	if info.Mode().Perm() != 0o750 {
		t.Errorf("the copied tool has mode %v, want 0750", info.Mode().Perm())
	}
	if link, err := os.Readlink(filepath.Join(dst, "sbin", "alias")); err != nil || link != "tool" {
		t.Errorf("the copied alias is %q with error %v, want a symlink to tool", link, err)
	}
}

func TestCopyTreeSkipsTheBootArchivesOwnSubtrees(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "lib", "modules", "boot"), 0o755); err != nil {
		t.Fatalf("creating the boot module directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "lib", "modules", "boot", "overlay.ko"), nil, 0o644); err != nil {
		t.Fatalf("writing the boot module: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "liken"), []byte("init"), 0o755); err != nil {
		t.Fatalf("writing init: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(src, "etc", "liken"), 0o755); err != nil {
		t.Fatalf("creating the etc directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "etc", "liken", "cluster.yaml"), []byte("kind: Cluster"), 0o644); err != nil {
		t.Fatalf("writing cluster.yaml: %v", err)
	}

	skip := []string{
		filepath.Join(src, "lib", "modules", "boot"),
		filepath.Join(src, "liken"),
	}
	if err := copyTree(src, dst, skip); err != nil {
		t.Fatalf("copying the tree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "etc", "liken", "cluster.yaml")); err != nil {
		t.Error("etc/liken/cluster.yaml is missing from the destination, want it copied")
	}
	if _, err := os.Stat(filepath.Join(dst, "liken")); !os.IsNotExist(err) {
		t.Error("the skipped file liken is in the destination, want it left behind")
	}
	if _, err := os.Stat(filepath.Join(dst, "lib", "modules", "boot")); !os.IsNotExist(err) {
		t.Error("the skipped directory lib/modules/boot is in the destination, want it left behind")
	}
	if _, err := os.Stat(filepath.Join(dst, "lib", "modules")); err != nil {
		t.Error("lib/modules is missing from the destination, want the parent of a skipped directory copied")
	}
}

func TestCopyTreeOverwritesWhatTheDestinationAlreadyHas(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "lib", "modules", "r1"), 0o755); err != nil {
		t.Fatalf("creating the source module directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "lib", "modules", "r1", "modules.dep"), []byte("complete"), 0o644); err != nil {
		t.Fatalf("writing the source modules.dep: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dst, "lib", "modules", "r1"), 0o700); err != nil {
		t.Fatalf("creating the destination module directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dst, "lib", "modules", "r1", "modules.dep"), []byte("partial"), 0o644); err != nil {
		t.Fatalf("writing the destination modules.dep: %v", err)
	}

	if err := copyTree(src, dst, nil); err != nil {
		t.Fatalf("copying the tree: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "lib", "modules", "r1", "modules.dep"))
	if err != nil {
		t.Fatalf("reading the copied modules.dep: %v", err)
	}
	if string(got) != "complete" {
		t.Errorf("modules.dep holds %q, want the copied \"complete\"", got)
	}
	info, err := os.Stat(filepath.Join(dst, "lib", "modules", "r1"))
	if err != nil {
		t.Fatalf("reading the destination directory: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("the existing directory has mode %v, want 0700", info.Mode().Perm())
	}
}

func TestCopyTreeRefusesUnexpectedFileTypes(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	if err := unix.Mkfifo(filepath.Join(src, "pipe"), 0o644); err != nil {
		t.Fatalf("creating the fifo: %v", err)
	}
	if err := copyTree(src, dst, nil); err == nil {
		t.Error("copying a tree with a fifo returned no error, want one")
	}
}

func TestCopyTreeToleratesAnExistingSymlink(t *testing.T) {
	// The image has symlinks of its own, such as mtab.
	src, dst := t.TempDir(), t.TempDir()
	if err := os.Symlink("target", filepath.Join(src, "alias")); err != nil {
		t.Fatalf("creating the source symlink: %v", err)
	}
	if err := os.Symlink("target", filepath.Join(dst, "alias")); err != nil {
		t.Fatalf("creating the destination symlink: %v", err)
	}
	if err := copyTree(src, dst, nil); err != nil {
		t.Errorf("copying over an existing symlink: %v, want no error", err)
	}
}

func TestCopyFileReportsAMissingSource(t *testing.T) {
	if err := copyFile(filepath.Join(t.TempDir(), "absent"), filepath.Join(t.TempDir(), "out"), 0o644); err == nil {
		t.Error("copying a missing source returned no error, want one")
	}
}

func TestCopyFileReportsAnUnwritableDestination(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(src, []byte("bytes"), 0o644); err != nil {
		t.Fatalf("writing the source file: %v", err)
	}
	if err := copyFile(src, filepath.Join(t.TempDir(), "no-such-dir", "out"), 0o644); err == nil {
		t.Error("copying to a missing directory returned no error, want one")
	}
}
