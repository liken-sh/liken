package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A recipe that fails, or a build that is killed during one, leaves its
// target partly written and newer than its inputs, and the next make
// takes that file as current. A truncated squashfs or a half-rendered
// page then goes into an image or onto a site. So every Makefile of the
// repository declares .DELETE_ON_ERROR, and make deletes the target of a
// recipe that fails.
func TestEveryMakefileDeletesTheTargetOfAFailedRecipe(t *testing.T) {
	listed, err := exec.Command("git", "-C", "..", "ls-files", "Makefile", "*/Makefile").Output()
	if err != nil {
		t.Fatal(err)
	}
	makefiles := strings.Fields(string(listed))
	if len(makefiles) == 0 {
		t.Fatal("git lists no Makefile")
	}
	for _, makefile := range makefiles {
		text, err := os.ReadFile(filepath.Join("..", makefile))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains("\n"+string(text), "\n.DELETE_ON_ERROR:") {
			t.Errorf("%s does not declare .DELETE_ON_ERROR", makefile)
		}
	}
}
