package machine

// Fixtures shared across this package's test files. They create the
// filesystem failures that the package's error paths exist to
// handle: directories that refuse writes, and files that refuse
// reads.

import (
	"os"
	"path/filepath"
	"testing"
)

// readOnlyDir returns a directory that refuses new entries. This is
// how the package's writers fail: each writer needs a writable
// directory for its temp file, or for removal of that file. Cleanup
// restores write permission, so t.TempDir can remove the tree.
func readOnlyDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	return dir
}

// sealedStore returns a manifest store. Its directory already
// exists, holds the given files, and refuses all further writes.
// This is the shape of a machineState filesystem that has become
// read-only while a lifecycle still runs on it.
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

// unreadableFile creates a file that exists but cannot be read. This
// makes a loader take the "present but failing" branch, instead of
// the missing-file branch that the loader usually treats as a
// default.
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
