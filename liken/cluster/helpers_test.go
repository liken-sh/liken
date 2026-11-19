package cluster

// Fixtures shared across this package's test files.

import (
	"os"
	"path/filepath"
	"testing"
)

// unreadableFile plants a file that exists but cannot be read. This
// makes a loader hit the "present but failing" branch, instead of
// the missing-file branch it usually treats as a default.
func unreadableFile(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating the directory of %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte("secret"), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("removing the permissions of %s: %v", path, err)
	}
	return path
}
