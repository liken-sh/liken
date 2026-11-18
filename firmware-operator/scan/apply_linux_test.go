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
		t.Fatalf("appending to %s: %v", path, err)
	}

	if got, _ := os.ReadFile(path); !bytes.Equal(got, append(bytes.Clone(dbxBefore), authAppend...)) {
		t.Errorf("the file contains %x, want the variable and then the append", got)
	}
}

func TestAppendOnceNeedsTheFile(t *testing.T) {
	if err := appendOnce(filepath.Join(t.TempDir(), "absent"), authAppend); !errors.Is(err, syscall.ENOENT) {
		t.Errorf("appendOnce on a missing file returned %v, want ENOENT", err)
	}
}

// The test skips on a filesystem without inode flags, such as an
// overlay in a container. It sets the flags that it just read, so it
// changes no immutable flag and needs no capability.
func TestInodeFlagsReadBackWhatWasSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), dbxFile)
	writeFile(t, path, dbxBefore)
	flags, err := inodeFlags(path)
	if errors.Is(err, syscall.ENOTTY) || errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.EINVAL) {
		t.Skipf("the filesystem of %s has no inode flags: %v", path, err)
	}
	if err != nil {
		t.Fatalf("reading the inode flags of %s: %v", path, err)
	}

	if err := setInodeFlags(path, flags); err != nil {
		t.Fatalf("setting the inode flags of %s: %v", path, err)
	}

	if again, err := inodeFlags(path); err != nil || again != flags {
		t.Errorf("after setInodeFlags, inodeFlags returned 0x%x and %v, want 0x%x and no error", again, err, flags)
	}
}

func TestInodeFlagsNeedTheFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	if _, err := inodeFlags(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("inodeFlags on a missing file returned %v, want a missing-file error", err)
	}
	if err := setInodeFlags(missing, 0); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("setInodeFlags on a missing file returned %v, want a missing-file error", err)
	}
}
