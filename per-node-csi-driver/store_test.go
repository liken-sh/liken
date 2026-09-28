package main

import (
	"os"
	"path/filepath"
	"testing"
)

// aStore is a store in a directory of the test's own.
func aStore(t *testing.T) *store {
	t.Helper()
	return newStore(t.TempDir())
}

func TestAFreshCopyIsWriteableByAnyUser(t *testing.T) {
	held := aStore(t)
	if err := held.makeCopy("example-store"); err != nil {
		t.Fatalf("makeCopy: %v", err)
	}
	info, err := os.Stat(held.copyPath("example-store"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != copyMode {
		t.Errorf("the copy has mode %o, want %o", info.Mode().Perm(), copyMode)
	}
}

func TestAStoreThatIsAFileMakesNoCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store")
	if err := os.WriteFile(path, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	if err := newStore(path).makeCopy("example-store"); err == nil {
		t.Error("makeCopy answered no error, want one")
	}
}

func TestTheStoreNamesEveryCopyItHolds(t *testing.T) {
	held := aStore(t)
	if got := held.handles(); len(got) != 0 {
		t.Errorf("a store with no copies named %v, want none", got)
	}
	for _, handle := range []string{"example-store", "some-cache"} {
		if err := held.makeCopy(handle); err != nil {
			t.Fatalf("makeCopy: %v", err)
		}
	}
	got := held.handles()
	if len(got) != 2 || got[0] != "example-store" || got[1] != "some-cache" {
		t.Errorf("the store named %v, want example-store and some-cache", got)
	}
}

func TestRemovingACopyLeavesTheOtherCopiesAlone(t *testing.T) {
	held := aStore(t)
	for _, handle := range []string{"example-store", "some-cache"} {
		if err := held.makeCopy(handle); err != nil {
			t.Fatalf("makeCopy: %v", err)
		}
	}
	if err := held.removeCopy("some-cache"); err != nil {
		t.Fatalf("removeCopy: %v", err)
	}
	got := held.handles()
	if len(got) != 1 || got[0] != "example-store" {
		t.Errorf("the store named %v, want example-store alone", got)
	}
}

func TestACopysSizeIsTheBytesItsFilesHold(t *testing.T) {
	held := aStore(t)
	if err := held.makeCopy("example-store"); err != nil {
		t.Fatalf("makeCopy: %v", err)
	}
	copyPath := held.copyPath("example-store")
	if err := os.MkdirAll(filepath.Join(copyPath, "inner"), 0o755); err != nil {
		t.Fatalf("making the inner directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(copyPath, "one"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("writing a file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(copyPath, "inner", "two"), []byte("!"), 0o644); err != nil {
		t.Fatalf("writing a file: %v", err)
	}
	size, err := copySize(copyPath)
	if err != nil {
		t.Fatalf("copySize: %v", err)
	}
	if size != 6 {
		t.Errorf("copySize answered %d, want 6", size)
	}
}

func TestACopyThatIsNotThereHasNoSize(t *testing.T) {
	if _, err := copySize(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("copySize answered no error, want one")
	}
}

func TestTheStoresFilesystemReportsFreeAndTotalBytes(t *testing.T) {
	available, total, err := spaceOf(t.TempDir())
	if err != nil {
		t.Fatalf("spaceOf: %v", err)
	}
	if total <= 0 {
		t.Errorf("spaceOf answered a total of %d, want more than zero", total)
	}
	if available > total {
		t.Errorf("spaceOf answered %d available of %d total", available, total)
	}
}

func TestAPathThatIsNotThereHasNoFilesystem(t *testing.T) {
	if _, _, err := spaceOf(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("spaceOf answered no error, want one")
	}
}
