package plugins

import (
	"path/filepath"
	"testing"
)

func TestNameFollowsTheKubectlConvention(t *testing.T) {
	if got := Name("audio"); got != "kubectl-liken-audio" {
		t.Fatalf("got %q", got)
	}
}

func TestBinDirLivesUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := BinDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(home, ".liken", "plugins", "bin") {
		t.Fatalf("got %q", got)
	}
}
