package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// placement is a read-only volume's directory in a store, and the
// source repository whose main the volume follows. Nothing is placed
// until the test calls place.
type placement struct {
	t         *testing.T
	source    string
	repo      *repository
	directory string
	tree      string
	entries   []string
}

func placementOf(t *testing.T, files map[string]string, entries ...string) *placement {
	t.Helper()
	source := repositoryWithACommit(t, files)
	_, repo := storeWith(t, fileURL(source))
	if err := repo.create(t.Context()); err != nil {
		t.Fatalf("creating the repository: %v", err)
	}
	directory := t.TempDir()
	return &placement{
		t:         t,
		source:    source,
		repo:      repo,
		directory: directory,
		tree:      filepath.Join(directory, "tree"),
		entries:   entries,
	}
}

// fetched fetches main into the store and returns its commit.
func (p *placement) fetched() string {
	p.t.Helper()
	if err := p.repo.fetch(p.t.Context(), nil, "main", 0); err != nil {
		p.t.Fatalf("fetching main: %v", err)
	}
	commit, err := p.repo.resolve(p.t.Context(), "main")
	if err != nil {
		p.t.Fatalf("resolving main: %v", err)
	}
	return commit
}

// place fetches main and places its commit in the tree.
func (p *placement) place() error {
	p.t.Helper()
	return p.repo.place(p.t.Context(), p.fetched(), p.directory, p.tree, p.entries)
}

// placed is place for a placement that the test needs to succeed.
func (p *placement) placed() {
	p.t.Helper()
	if err := p.place(); err != nil {
		p.t.Fatalf("placing main: %v", err)
	}
}

func TestAVolumeWithPathsChecksOutOnlyItsEntries(t *testing.T) {
	files := map[string]string{
		"a/b/x":      "x",
		"a/y":        "y",
		"c/z":        "z",
		"root.txt":   "r",
		"pages/i":    "i",
		"pages/[id]": "id",
		":odd":       "odd",
	}
	for _, c := range []struct {
		name    string
		entries []string
		want    map[string]string
	}{
		{
			name:    "a nested pair",
			entries: []string{"a", "a/b"},
			want:    map[string]string{"a/b/x": "x", "a/y": "y"},
		},
		{
			name:    "a file entry",
			entries: []string{"root.txt"},
			want:    map[string]string{"root.txt": "r"},
		},
		{
			name:    "a file inside a directory",
			entries: []string{"a/y"},
			want:    map[string]string{"a/y": "y"},
		},
		{
			name:    "a name with brackets, which is not a pattern",
			entries: []string{"pages/[id]"},
			want:    map[string]string{"pages/[id]": "id"},
		},
		{
			name:    "a name that starts with a colon, which is not magic",
			entries: []string{":odd"},
			want:    map[string]string{":odd": "odd"},
		},
		{
			name:    "no paths",
			entries: nil,
			want:    files,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := placementOf(t, files, c.entries...)
			p.placed()
			if got := readTree(t, p.tree); !sameTree(got, c.want) {
				t.Errorf("the tree has %v, want %v", got, c.want)
			}
		})
	}
}

// embedRepository makes a repository with one commit at name inside
// the source, and adds it to the source's index as a submodule. git
// keeps it as a submodule on each later git add, because the directory
// has a .git.
func embedRepository(t *testing.T, source, name string) {
	t.Helper()
	inner := filepath.Join(source, name)
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatalf("making %s: %v", inner, err)
	}
	git(t, inner, "init", "--quiet", "--initial-branch=main")
	commitFiles(t, inner, map[string]string{"inside.txt": "inside"})
	git(t, source, "add", name)
}

func TestASubmoduleEntryIsAnEmptyDirectory(t *testing.T) {
	p := placementOf(t, map[string]string{"a.txt": "one"}, "mod")
	embedRepository(t, p.source, "mod")
	commitFiles(t, p.source, nil)

	p.placed()
	inside, err := os.ReadDir(p.tree)
	if err != nil {
		t.Fatalf("reading the tree: %v", err)
	}
	if len(inside) != 1 || inside[0].Name() != "mod" || !inside[0].IsDir() {
		t.Fatalf("the tree has %v, want only the directory mod", inside)
	}
	if got := readTree(t, filepath.Join(p.tree, "mod")); len(got) != 0 {
		t.Errorf("the submodule has %v, want no files, because its commit is in another repository", got)
	}
}

func TestARootGitattributesAppliesToTheFilesOfAnEntry(t *testing.T) {
	p := placementOf(t, map[string]string{
		".gitattributes": "* text eol=crlf\n",
		"a/x.txt":        "one\ntwo\n",
	}, "a")
	p.placed()

	want := map[string]string{"a/x.txt": "one\r\ntwo\r\n"}
	if got := readTree(t, p.tree); !sameTree(got, want) {
		t.Errorf("the tree has %q, want %q, the bytes of a checkout of the whole tree", got, want)
	}
}

func TestAPlacementRefusesACommitThatLacksAnEntry(t *testing.T) {
	for _, c := range []struct {
		name    string
		entries []string
		missing string
	}{
		{name: "a missing directory", entries: []string{"a", "gone", "nope"}, missing: "gone"},
		{name: "a path under a file", entries: []string{"a/x.txt/inner"}, missing: "a/x.txt/inner"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := placementOf(t, map[string]string{"a/x.txt": "one"}, c.entries...)
			commit := p.fetched()

			err := p.repo.place(t.Context(), commit, p.directory, p.tree, p.entries)
			var missing *missingPath
			if !errors.As(err, &missing) || missing.entry != c.missing || missing.commit != commit {
				t.Fatalf("place returned %v, want the entry %q missing from %s", err, c.missing, short(commit))
			}
			want := `commit ` + short(commit) + ` has no "` + c.missing + `"`
			if err.Error() != want {
				t.Errorf("the error is %q, want %q", err, want)
			}
			if _, err := os.Stat(p.tree); err == nil {
				t.Error("a refused placement made the tree, want none")
			}
		})
	}
}

func TestTheEntryCheckReturnsGitsErrorForARepositoryItCannotRead(t *testing.T) {
	p := placementOf(t, map[string]string{"a/x.txt": "one"}, "a")
	commit := p.fetched()
	refusingGit(t, "rev-parse")

	err := p.repo.checkEntries(t.Context(), commit, p.entries)
	var missing *missingPath
	if err == nil || errors.As(err, &missing) {
		t.Fatalf("the check returned %v, want git's error and no missing entry", err)
	}
	if !strings.Contains(err.Error(), "the repository was not read") {
		t.Errorf("the error is %q, want git's own text", err)
	}
}

func TestACheckoutOfAnEntryTheIndexLacksFails(t *testing.T) {
	p := placementOf(t, map[string]string{"a/x.txt": "one"})
	commit := p.fetched()
	if err := p.repo.checkout(t.Context(), commit, p.tree, []string{"gone"}); err == nil {
		t.Error("checkout of an entry the commit does not have returned no error, want one")
	}
}

func TestAFirstPlacementReportsACheckoutItCannotMake(t *testing.T) {
	p := placementOf(t, map[string]string{"a/x.txt": "one"}, "a")
	readOnlyDir(t, p.directory)
	if err := p.place(); err == nil {
		t.Error("place returned no error in a volume directory it cannot write, want one")
	}
}
