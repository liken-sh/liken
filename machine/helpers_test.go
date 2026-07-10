package machine

// Fixtures shared across this package's test files. They manufacture
// the filesystem misfortunes the package's error paths exist for:
// directories that refuse writes and files that refuse reads.

import (
	"os"
	"path/filepath"
	"testing"
)

// readOnlyDir is a directory that refuses new entries, which is how
// the package's writers fail: every one of them needs a writable
// directory for its temp file or its removal. Cleanup restores write
// permission so t.TempDir can remove the tree.
func readOnlyDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	return dir
}

// sealedStore is a manifest store whose directory already exists,
// holds the given files, and refuses all further writes: the shape of
// a machineState filesystem that has gone read-only underneath a
// running lifecycle.
func sealedStore(t *testing.T, files map[string]string) ManifestStore {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "manifests")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	return MachineManifests(root)
}

// unreadableFile plants a file that exists but cannot be read, so a
// loader hits the "present but failing" branch rather than the
// missing-file one it usually treats as a default.
func unreadableFile(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	return path
}
