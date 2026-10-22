package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"syscall"
)

func TestAppendOnceAppendsToTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), dbxFile)
	writeFile(t, path, dbxBefore)

	if err := appendOnce(path, authAppend); err != nil {
		t.Fatal(err)
	}

	if held, _ := os.ReadFile(path); !bytes.Equal(held, append(bytes.Clone(dbxBefore), authAppend...)) {
		t.Errorf("the file holds %x, want the variable and then the append", held)
	}
}

func TestAppendOnceNeedsTheFile(t *testing.T) {
	if err := appendOnce(filepath.Join(t.TempDir(), "absent"), authAppend); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("appendOnce = %v, want ENOENT", err)
	}
}

// The flags of a file read back as they were set. A filesystem with no
// inode flags, such as an overlay in a container, answers ENOTTY, and
// the test skips there. Setting the flags it read changes no
// immutable flag, so the test needs no capability.
func TestInodeFlagsReadBackWhatWasSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), dbxFile)
	writeFile(t, path, dbxBefore)
	flags, err := inodeFlags(path)
	if errors.Is(err, syscall.ENOTTY) || errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.EINVAL) {
		t.Skipf("the filesystem of %s has no inode flags: %v", path, err)
	}
	if err != nil {
		t.Fatal(err)
	}

	if err := setInodeFlags(path, flags); err != nil {
		t.Fatal(err)
	}

	if again, err := inodeFlags(path); err != nil || again != flags {
		t.Errorf("inodeFlags = 0x%x, %v, want 0x%x", again, err, flags)
	}
}

func TestInodeFlagsNeedTheFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	if _, err := inodeFlags(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("inodeFlags = %v, want a missing file", err)
	}
	if err := setInodeFlags(missing, 0); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("setInodeFlags = %v, want a missing file", err)
	}
}
