package plugins

import (
	"path/filepath"
	"testing"
)

func TestNameFollowsTheKubectlConvention(t *testing.T) {
	if got := Name("audio"); got != "kubectl-liken-audio" {
		t.Fatalf("the plugin name is %q, want kubectl-liken-audio", got)
	}
}

func TestBinDirLivesUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := BinDir()
	if err != nil {
		t.Fatalf("finding the plugin directory: %v", err)
	}
	if got != filepath.Join(home, ".liken", "plugins", "bin") {
		t.Fatalf("the plugin directory is %q, want a path under %s", got, home)
	}
}
