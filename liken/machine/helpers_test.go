package machine

import (
	"os"
	"path/filepath"
	"testing"
)

// readOnlyDir returns a directory where nothing can be created, so a
// store rooted there cannot create its manifests directory and
// WriteDurable cannot create its temp file. Cleanup restores write
// permission so that t.TempDir can remove the tree.
func readOnlyDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("making %s read-only: %v", dir, err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	return dir
}

// sealedStore returns a manifest store whose manifests directory holds
// the given files and refuses new entries, as when the machineState
// filesystem turns read-only.
func sealedStore(t *testing.T, files map[string]string) ManifestStore {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "manifests")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating the manifests directory: %v", err)
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
			t.Fatalf("writing the manifest %s: %v", name, err)
		}
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("making the manifests directory read-only: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	return MachineManifests(root)
}

// unreadableFile creates a file that exists but cannot be read, so a
// loader returns its read error and does not fall back to the default
// it uses for a missing file.
func unreadableFile(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating the directory of %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte("secret"), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("making %s unreadable: %v", path, err)
	}
	return path
}
