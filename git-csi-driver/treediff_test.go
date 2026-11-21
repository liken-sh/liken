package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
)

// inodesOf returns the inode of every path under dir, keyed by path. A
// path whose inode is the same after a placement was not written again.
func inodesOf(t *testing.T, dir string) map[string]uint64 {
	t.Helper()
	found := map[string]uint64{}
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		name, err := filepath.Rel(dir, path)
		found[filepath.ToSlash(name)] = info.Sys().(*syscall.Stat_t).Ino
		return err
	}); err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	return found
}

// commitEmpty makes a commit that changes no file.
func commitEmpty(t *testing.T, source string) {
	t.Helper()
	git(t, source, "-c", "user.name=lab", "-c", "user.email=lab@liken.sh",
		"commit", "--quiet", "--allow-empty", "-m", "empty")
}

func TestACommitThatChangesNoEntryWritesNoFile(t *testing.T) {
	for _, c := range []struct {
		name    string
		entries []string
		change  func(t *testing.T, source string)
	}{
		{
			name:    "a commit outside the entries",
			entries: []string{"a"},
			change: func(t *testing.T, source string) {
				commitFiles(t, source, map[string]string{"b/z": "changed"})
			},
		},
		{
			name:    "an empty commit, without paths",
			entries: nil,
			change:  commitEmpty,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := placementOf(t, map[string]string{"a/x": "x", "a/y": "y", "b/z": "z"}, c.entries...)
			p.placed()
			before := inodesOf(t, p.tree)

			c.change(t, p.source)
			p.placed()
			if after := inodesOf(t, p.tree); !reflect.DeepEqual(after, before) {
				t.Errorf("the tree's inodes are %v, want %v, because nothing in the entries changed", after, before)
			}
			if got, want := placedCommit(p.directory), p.fetched(); got != want {
				t.Errorf("the recorded commit is %s, want %s", got, want)
			}
		})
	}
}

func TestACommitThatChangesOneFileRenamesOnlyThatFile(t *testing.T) {
	for _, c := range []struct {
		name    string
		entries []string
	}{
		{name: "with paths", entries: []string{"a"}},
		{name: "without paths", entries: nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := placementOf(t, map[string]string{"a/x": "x", "a/y": "y", "b/z": "z"}, c.entries...)
			p.placed()
			before := inodesOf(t, p.tree)

			commitFiles(t, p.source, map[string]string{"a/y": "changed"})
			p.placed()
			after := inodesOf(t, p.tree)
			for name, inode := range before {
				if moved := after[name] != inode; moved != (name == "a/y") {
					t.Errorf("%s has inode %d, was %d, want only a/y written again", name, after[name], inode)
				}
			}
			if got := readTree(t, p.tree)["a/y"]; got != "changed" {
				t.Errorf("a/y has %q, want %q", got, "changed")
			}
		})
	}
}

// describeTree returns one line for each path under dir: a directory,
// a symlink and its target, or a file and its mode and content. Two
// equal descriptions show the same tree to a pod.
func describeTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	found := map[string]string{}
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		switch info, _ := entry.Info(); {
		case entry.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			found[name] = "symlink to " + target
			return err
		case entry.IsDir():
			found[name] = "directory"
		default:
			content, err := os.ReadFile(path)
			found[name] = fmt.Sprintf("file %v %q", info.Mode().Perm(), content)
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	return found
}

// replaceWith deletes the path in the source and runs make in its
// place, so a test can change a path's type.
func replaceWith(t *testing.T, source, name string, make func(path string) error) {
	t.Helper()
	path := filepath.Join(source, name)
	if err := os.RemoveAll(path); err != nil {
		t.Fatalf("removing %s: %v", name, err)
	}
	if err := make(path); err != nil {
		t.Fatalf("making %s: %v", name, err)
	}
}

func symlinkTo(target string) func(string) error {
	return func(path string) error { return os.Symlink(target, path) }
}

func fileWith(content string) func(string) error {
	return func(path string) error { return os.WriteFile(path, []byte(content), 0o644) }
}

func nothing(string) error { return nil }

func TestAnUpdateWritesTheSameTreeAsAFullCheckout(t *testing.T) {
	files := map[string]string{
		"a/x": "x", "a/y": "y", "a/d/one": "1", "a/d/two": "2",
		"b/z": "z", "pages/i": "i", "pages/[id]": "id",
	}
	for _, c := range []struct {
		name    string
		entries []string
		setup   func(t *testing.T, source string)
		change  func(t *testing.T, source string)
		// kept is a path that the change leaves alone, so its inode must
		// stay.
		kept string
	}{
		{
			name:    "a mode change",
			entries: []string{"a"},
			change: func(t *testing.T, source string) {
				if err := os.Chmod(filepath.Join(source, "a/x"), 0o755); err != nil {
					t.Fatalf("making a/x executable: %v", err)
				}
			},
			kept: "a/y",
		},
		{
			name:    "a file becomes a directory",
			entries: []string{"a"},
			change: func(t *testing.T, source string) {
				replaceWith(t, source, "a/x", nothing)
				writeFiles(t, source, map[string]string{"a/x/inner": "inner"})
			},
			kept: "a/y",
		},
		{
			name:    "a directory becomes a file",
			entries: []string{"a"},
			change: func(t *testing.T, source string) {
				replaceWith(t, source, "a/d", fileWith("d"))
			},
			kept: "a/y",
		},
		{
			name:    "a file becomes a symlink",
			entries: nil,
			change: func(t *testing.T, source string) {
				replaceWith(t, source, "a/x", symlinkTo("y"))
			},
			kept: "b/z",
		},
		{
			name:    "a symlink becomes a file",
			entries: []string{"a"},
			setup: func(t *testing.T, source string) {
				replaceWith(t, source, "a/x", symlinkTo("y"))
			},
			change: func(t *testing.T, source string) {
				replaceWith(t, source, "a/x", fileWith("x again"))
			},
			kept: "a/y",
		},
		{
			name:    "a symlink changes its target",
			entries: []string{"a"},
			setup: func(t *testing.T, source string) {
				replaceWith(t, source, "a/x", symlinkTo("y"))
			},
			change: func(t *testing.T, source string) {
				replaceWith(t, source, "a/x", symlinkTo("d"))
			},
			kept: "a/y",
		},
		{
			name:    "a directory empties",
			entries: nil,
			change: func(t *testing.T, source string) {
				replaceWith(t, source, "a/d", nothing)
			},
			kept: "a/y",
		},
		{
			name:    "a file moves to a new directory",
			entries: []string{"a"},
			change: func(t *testing.T, source string) {
				replaceWith(t, source, "a/x", nothing)
				writeFiles(t, source, map[string]string{"a/new/deep/x": "x"})
			},
			kept: "a/y",
		},
		{
			name:    "a submodule moves to another commit",
			entries: nil,
			setup: func(t *testing.T, source string) {
				embedRepository(t, source, "mod")
			},
			change: func(t *testing.T, source string) {
				commitFiles(t, filepath.Join(source, "mod"), map[string]string{"inside.txt": "more"})
			},
			kept: "a/y",
		},
		{
			name:    "a submodule becomes a file",
			entries: nil,
			setup: func(t *testing.T, source string) {
				embedRepository(t, source, "mod")
			},
			change: func(t *testing.T, source string) {
				replaceWith(t, source, "mod", fileWith("mod"))
			},
			kept: "a/y",
		},
		{
			name:    "a file becomes a submodule",
			entries: []string{"a"},
			change: func(t *testing.T, source string) {
				replaceWith(t, source, "a/x", nothing)
				embedRepository(t, source, "a/x")
			},
			kept: "a/y",
		},
		{
			name:    "a name with brackets keeps its sibling out",
			entries: []string{"pages/[id]"},
			change: func(t *testing.T, source string) {
				writeFiles(t, source, map[string]string{"pages/[id]": "id again", "pages/i": "i again"})
			},
		},
		{
			name:    "a nested pair of entries",
			entries: []string{"a", "a/d"},
			change: func(t *testing.T, source string) {
				replaceWith(t, source, "a/d/two", nothing)
				writeFiles(t, source, map[string]string{"a/d/three": "3", "b/z": "z again"})
			},
			kept: "a/d/one",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := placementOf(t, files, c.entries...)
			if c.setup != nil {
				c.setup(t, p.source)
				commitFiles(t, p.source, nil)
			}
			p.placed()
			before := inodesOf(t, p.tree)

			c.change(t, p.source)
			commitFiles(t, p.source, nil)
			commit := p.fetched()
			if err := p.repo.place(t.Context(), commit, p.directory, p.tree, p.entries); err != nil {
				t.Fatalf("placing the change: %v", err)
			}

			full := filepath.Join(t.TempDir(), "full")
			if err := p.repo.checkout(t.Context(), commit, full, p.entries); err != nil {
				t.Fatalf("checking out the whole commit: %v", err)
			}
			if got, want := describeTree(t, p.tree), describeTree(t, full); !reflect.DeepEqual(got, want) {
				t.Errorf("the updated tree is\n%v\nwant the full checkout\n%v", got, want)
			}
			if after := inodesOf(t, p.tree); c.kept != "" && after[c.kept] != before[c.kept] {
				t.Errorf("%s has inode %d, was %d, want it kept because the change left it alone",
					c.kept, after[c.kept], before[c.kept])
			}
			if got := placedCommit(p.directory); got != commit {
				t.Errorf("the recorded commit is %s, want %s", got, commit)
			}
		})
	}
}

// storeHas reports whether the store's repository has the object. It
// runs git without the driver's code, as git does.
func storeHas(t *testing.T, repo *repository, object string) bool {
	t.Helper()
	command := exec.Command("git", "cat-file", "-e", object)
	command.Dir = repo.dir
	command.Env = gitEnvironment()
	return command.Run() == nil
}

func TestAForcedPushWithTheOldCommitGoneChecksOutEverything(t *testing.T) {
	p := placementOf(t, map[string]string{"a/x": "x", "a/same": "same"}, "a")
	p.placed()
	old := placedCommit(p.directory)
	before := inodesOf(t, p.tree)

	// A history with no commit in common with the old one replaces main,
	// and a sweep of the store deletes the old commit.
	git(t, p.source, "checkout", "--quiet", "--orphan", "rewritten")
	commitFiles(t, p.source, map[string]string{"a/x": "rewritten"})
	git(t, p.source, "branch", "--quiet", "--force", "main")
	p.fetched()
	git(t, p.repo.dir, "gc", "--quiet", "--prune=now")
	if storeHas(t, p.repo, old) {
		t.Fatalf("the store still has the old commit %s after the sweep", short(old))
	}

	p.placed()
	if got, want := readTree(t, p.tree), map[string]string{"a/x": "rewritten", "a/same": "same"}; !sameTree(got, want) {
		t.Errorf("the tree has %v, want %v", got, want)
	}
	if after := inodesOf(t, p.tree); after["a/same"] == before["a/same"] {
		t.Error("a/same kept its inode, want it written again by a full checkout, because no diff from the old commit exists")
	}
}

func TestAChangedGitattributesChecksOutEveryEntry(t *testing.T) {
	for _, c := range []struct {
		name       string
		entries    []string
		attributes string
	}{
		{name: "a root file above the entry", entries: []string{"a"}, attributes: ".gitattributes"},
		{name: "a file inside the entry", entries: []string{"a"}, attributes: "a/.gitattributes"},
		{name: "a nested file, without paths", entries: nil, attributes: "a/.gitattributes"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := placementOf(t, map[string]string{"a/x.txt": "one\n", "b.txt": "b\n"}, c.entries...)
			p.placed()

			// a/x.txt does not change in git, but its bytes on disk do.
			commitFiles(t, p.source, map[string]string{c.attributes: "*.txt text eol=crlf\n"})
			p.placed()
			if got := readTree(t, p.tree)["a/x.txt"]; got != "one\r\n" {
				t.Errorf("a/x.txt has %q, want %q, the bytes the new attributes give it", got, "one\r\n")
			}
		})
	}
}

func TestAStopDuringAnUpdateLeavesATreeWithNoRecordedCommit(t *testing.T) {
	p := placementOf(t, map[string]string{"a/x": "one", "b/y": "one"})
	p.placed()
	commitFiles(t, p.source, map[string]string{"a/x": "two", "b/y": "two"})

	// The rename into b fails after the rename into a, which is where a
	// driver that stopped during the update would leave the tree.
	closed := filepath.Join(p.tree, "b")
	readOnlyDir(t, closed)
	if err := p.place(); err == nil {
		t.Fatal("place returned no error with b closed, want one")
	}
	if got := placedCommit(p.directory); got != "" {
		t.Errorf("the recorded commit is %s after a stopped update, want none", short(got))
	}

	if err := os.Chmod(closed, 0o755); err != nil {
		t.Fatalf("opening b again: %v", err)
	}
	p.placed()
	if got, want := readTree(t, p.tree), map[string]string{"a/x": "two", "b/y": "two"}; !sameTree(got, want) {
		t.Errorf("the tree has %v, want %v", got, want)
	}
	if got, want := placedCommit(p.directory), p.fetched(); got != want {
		t.Errorf("the recorded commit is %s, want %s", got, want)
	}
}

func TestARestartDuringAnUpdateChecksOutEveryFileNext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		source := repositoryWithACommit(t, map[string]string{"a/x": "one", "b/y": "one"})
		vol := publishedVolume(t, n, "csi-1", fileURL(source), map[string]string{"pull": "1h"})
		replaceWith(t, source, "a/x", nothing)
		want := commitFiles(t, source, map[string]string{"b/y": "two"})
		// The rename into b fails after the delete of a/x, which is where
		// a driver that stopped during the update would leave the tree. A
		// second update from the old commit would fail on the delete.
		closed := filepath.Join(vol.tree, "b")
		readOnlyDir(t, closed)
		if followerOf(n, fileURL(source)).refresh(t.Context(), vol) {
			t.Fatal("refresh returned true with b closed, want false")
		}
		if err := os.Chmod(closed, 0o755); err != nil {
			t.Fatalf("opening b again: %v", err)
		}

		// The restarted driver pulls each resumed volume once.
		again := restarted(t, n, true)
		resumed := resumedVolume(again, "csi-1")
		waitForCommit(t, resumed, want)
		if got, want := readTree(t, resumed.tree), map[string]string{"b/y": "two"}; !sameTree(got, want) {
			t.Errorf("the tree has %v, want %v", got, want)
		}
	})
}

func TestAPlacementWithNoTreeChecksOutEverything(t *testing.T) {
	p := placementOf(t, map[string]string{"a/x": "one", "b/y": "one"})
	p.placed()
	commitFiles(t, p.source, map[string]string{"a/x": "two"})
	if err := os.RemoveAll(p.tree); err != nil {
		t.Fatalf("removing the tree: %v", err)
	}

	p.placed()
	if got, want := readTree(t, p.tree), map[string]string{"a/x": "two", "b/y": "one"}; !sameTree(got, want) {
		t.Errorf("the tree has %v, want %v", got, want)
	}
}

func TestAnUpdateReportsWhatItCannotChange(t *testing.T) {
	for _, c := range []struct {
		name   string
		change func(t *testing.T, source string)
		closed func(p *placement) string
	}{
		{
			name: "a file it cannot rename over",
			change: func(t *testing.T, source string) {
				writeFiles(t, source, map[string]string{"a/x": "changed"})
			},
			closed: func(p *placement) string { return filepath.Join(p.tree, "a") },
		},
		{
			name: "a file it cannot delete",
			change: func(t *testing.T, source string) {
				replaceWith(t, source, "a/x", nothing)
			},
			closed: func(p *placement) string { return filepath.Join(p.tree, "a") },
		},
		{
			name: "a directory it cannot make",
			change: func(t *testing.T, source string) {
				writeFiles(t, source, map[string]string{"c/new": "new"})
			},
			closed: func(p *placement) string { return p.tree },
		},
		{
			name: "a directory it cannot replace with a file",
			change: func(t *testing.T, source string) {
				replaceWith(t, source, "a/d", fileWith("d"))
			},
			closed: func(p *placement) string { return filepath.Join(p.tree, "a") },
		},
		{
			name: "an empty directory it cannot remove",
			change: func(t *testing.T, source string) {
				replaceWith(t, source, "a/d", nothing)
			},
			closed: func(p *placement) string { return filepath.Join(p.tree, "a") },
		},
		{
			name: "a volume directory it cannot write, with a file to check out",
			change: func(t *testing.T, source string) {
				writeFiles(t, source, map[string]string{"a/x": "changed"})
			},
			closed: func(p *placement) string { return p.directory },
		},
		{
			name: "a volume directory it cannot write, with only a delete",
			change: func(t *testing.T, source string) {
				replaceWith(t, source, "a/x", nothing)
			},
			closed: func(p *placement) string { return p.directory },
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := placementOf(t, map[string]string{"a/x": "x", "a/y": "y", "a/d/one": "1"})
			p.placed()
			c.change(t, p.source)
			commitFiles(t, p.source, nil)
			readOnlyDir(t, c.closed(p))

			if err := p.place(); err == nil {
				t.Errorf("place returned no error with %s closed, want one",
					strings.TrimPrefix(c.closed(p), p.directory))
			}
		})
	}
}
