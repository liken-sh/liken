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

// dependabotFile is the path of the generated Dependabot configuration.
// GitHub reads the configuration only from this path.
const dependabotFile = ".github/dependabot.yml"

// dependabotEntry is one directory that Dependabot updates: a Go
// module, a Cargo workspace or standalone crate, the workflows
// directory, or one composite action.
type dependabotEntry struct {
	Ecosystem, Directory string
}

// Dependabot renders the Dependabot configuration, with one entry for
// each directory with a go.mod, each directory with a Cargo.lock, the
// repository root for .github/workflows, and each composite action in
// .github/actions. Dependabot opens a security update only for a
// directory that the configuration lists, so `generate -check` fails
// at a commit that adds a module, a crate, or an action without its
// entry. A member of a Cargo workspace has no Cargo.lock, and the
// workspace's entry covers it. The template, dependabot.yml.tmpl,
// explains the settings of each entry.
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
			// git can list a file that is deleted from the working tree.
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
