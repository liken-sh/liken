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
