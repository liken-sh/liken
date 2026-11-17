package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A recipe that fails, or a build that is killed during one, leaves its
// target partly written and newer than its inputs, and the next make
// takes that file as current. A truncated squashfs or a half-rendered
// page then goes into an image or onto a site. So every Makefile of the
// repository declares .DELETE_ON_ERROR, and make deletes the target of a
// recipe that fails.
// pinnedMakefiles are the Makefiles that are a part of a pinned
// component's recipe: trust's own, and the one of toolchains/manifest,
// which the mpv and weston images take as a build context. A published
// tag never changes, so an edit to one of them needs a new revision of
// each component whose recipe holds it, and of every component that
// builds on those. Each one takes the declaration in the commit that
// next raises those revisions.
var pinnedMakefiles = []string{"toolchains/manifest/Makefile", "trust/Makefile"}

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
		if slices.Contains(pinnedMakefiles, makefile) {
			continue
		}
		text, err := os.ReadFile(filepath.Join("..", makefile))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains("\n"+string(text), "\n.DELETE_ON_ERROR:") {
			t.Errorf("%s does not declare .DELETE_ON_ERROR", makefile)
		}
	}
}
