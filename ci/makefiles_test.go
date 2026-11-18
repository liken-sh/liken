package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// pinnedMakefiles are part of the recipes of pinned components, whose
// published tags never change. Editing one needs a new revision of each
// pinned component built from it, so the test skips them until a commit
// that raises those revisions adds .DELETE_ON_ERROR.
var pinnedMakefiles = []string{"toolchains/manifest/Makefile", "trust/Makefile"}

// When a recipe fails or is killed, its target is left partly written
// and newer than its inputs, so the next make treats it as up to date
// and ships a truncated file. .DELETE_ON_ERROR makes make delete the
// target of a failed recipe.
func TestEveryMakefileDeletesTheTargetOfAFailedRecipe(t *testing.T) {
	listed, err := exec.Command("git", "-C", "..", "ls-files", "Makefile", "*/Makefile").Output()
	if err != nil {
		t.Fatalf("listing the Makefiles with git: %v", err)
	}
	makefiles := strings.Fields(string(listed))
	if len(makefiles) == 0 {
		t.Fatal("git lists no Makefile, want at least one")
	}
	for _, makefile := range makefiles {
		if slices.Contains(pinnedMakefiles, makefile) {
			continue
		}
		text, err := os.ReadFile(filepath.Join("..", makefile))
		if err != nil {
			t.Fatalf("reading %s: %v", makefile, err)
		}
		if !strings.Contains("\n"+string(text), "\n.DELETE_ON_ERROR:") {
			t.Errorf("%s does not declare .DELETE_ON_ERROR", makefile)
		}
	}
}
