package main

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// dependabotFile is the path of the generated Dependabot configuration,
// from the repository root. GitHub reads it under this name.
const dependabotFile = ".github/dependabot.yml"

// dependabotEntry is one directory that Dependabot reads: a Go module,
// a Cargo workspace or a standalone crate, or a directory of workflows
// or of one composite action.
type dependabotEntry struct {
	Ecosystem, Directory string
}

// Dependabot renders the Dependabot configuration: one entry for each
// directory with a go.mod, and one for each directory with a
// Cargo.lock. Dependabot opens a security update only for a directory
// that the configuration lists, so the generator lists every one that
// git tracks or would track, and `generate -check` fails at the commit
// that adds a module or a crate without its entry. A member of a Cargo
// workspace has no Cargo.lock of its own, and the workspace's entry
// covers it.
//
// The github-actions entries cover the workflows, which Dependabot
// reads from .github/workflows under the entry of the root, and each
// composite action under .github/actions, which needs an entry of its
// own directory. The root's entry is always there, because the
// generator always writes ci.yaml. A security update of an action rewrites the commit and
// the release comment of each uses: line, and `generate -check` fails
// until `ci/latest.sh --bump <repository>` writes the same pin into
// actionPins in ci/actions.go.
func Dependabot(root string) ([]byte, error) {
	files, err := sourceFiles(root)
	if err != nil {
		return nil, err
	}
	var modules, crates []dependabotEntry
	actions := []dependabotEntry{{Ecosystem: "github-actions", Directory: "/"}}
	for _, file := range files {
		name := path.Base(file)
		if matched, _ := path.Match(".github/actions/*/action.y*ml", file); matched {
			actions = append(actions, dependabotEntry{Ecosystem: "github-actions", Directory: path.Join("/", path.Dir(file))})
			continue
		}
		if name != "go.mod" && name != "Cargo.lock" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, file)); errors.Is(err, fs.ErrNotExist) {
			// git still tracks a file that the tree deleted.
			continue
		} else if err != nil {
			return nil, err
		}
		dir := path.Join("/", path.Dir(file))
		if name == "go.mod" {
			modules = append(modules, dependabotEntry{Ecosystem: "gomod", Directory: dir})
		} else {
			crates = append(crates, dependabotEntry{Ecosystem: "cargo", Directory: dir})
		}
	}
	byDirectory := func(a, b dependabotEntry) int { return strings.Compare(a.Directory, b.Directory) }
	slices.SortFunc(modules, byDirectory)
	slices.SortFunc(crates, byDirectory)
	slices.SortFunc(actions, byDirectory)
	return render("dependabot.yml.tmpl", slices.Concat(modules, crates, actions))
}
