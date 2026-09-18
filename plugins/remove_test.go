package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveDeletesTheInstalledCLI(t *testing.T) {
	binDir := t.TempDir()
	path := filepath.Join(binDir, Name("audio"))
	if err := os.WriteFile(path, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Remove(binDir, "audio"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the CLI must be gone: %v", err)
	}
}

func TestRemoveReportsACLIThatIsNotInstalled(t *testing.T) {
	if err := Remove(t.TempDir(), "audio"); err == nil {
		t.Fatal("removing a CLI that was never installed must be an error")
	}
}
