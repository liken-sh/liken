package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestInsideMatchesAnEntryAndThePathsUnderIt(t *testing.T) {
	for _, c := range []struct {
		name    string
		entries []string
		path    string
		want    bool
	}{
		{name: "no paths", entries: nil, path: "anything/at/all", want: true},
		{name: "the entry itself", entries: []string{"app"}, path: "app", want: true},
		{name: "a file under the entry", entries: []string{"app"}, path: "app/sub/a.yaml", want: true},
		{name: "a sibling with the entry as a prefix", entries: []string{"app"}, path: "app-b/a.yaml", want: false},
		{name: "the parent of the entry", entries: []string{"apps/a"}, path: "apps", want: false},
		{name: "the root", entries: []string{"app"}, path: ".", want: false},
		{name: "the second entry", entries: []string{"app", "docs/one.md"}, path: "docs/one.md", want: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := inside(c.entries, c.path); got != c.want {
				t.Errorf("inside(%q, %q) = %v, want %v", c.entries, c.path, got, c.want)
			}
		})
	}
}

func TestTopEntriesDropsAnEntryUnderAnother(t *testing.T) {
	got := topEntries([]string{"a", "a-b", "a/b", "a/b/c", "b"})
	if want := []string{"a", "a-b", "b"}; !slices.Equal(got, want) {
		t.Errorf("topEntries = %q, want %q", got, want)
	}
}

func TestTheSparsePatternsReadBackAsTheEntries(t *testing.T) {
	entries := []string{"app", "docs/one.md", "pages/[id]", "st*r", "q?", `back\slash`, "!bang", "#hash"}
	patterns := sparsePatterns(entries)
	want := "/app\n/docs/one.md\n/pages/\\[id]\n/st\\*r\n/q\\?\n/back\\\\slash\n/!bang\n/#hash\n"
	if patterns != want {
		t.Errorf("sparsePatterns = %q, want %q", patterns, want)
	}
	if got := parseSparsePatterns(patterns); !slices.Equal(got, entries) {
		t.Errorf("parseSparsePatterns = %q, want %q", got, entries)
	}
}

func TestDescribePathsQuotesEachEntry(t *testing.T) {
	for _, c := range []struct {
		entries []string
		want    string
	}{
		{entries: nil, want: "the whole tree"},
		{entries: []string{"app", "my docs"}, want: `"app", "my docs"`},
	} {
		if got := describePaths(c.entries); got != c.want {
			t.Errorf("describePaths(%q) = %q, want %q", c.entries, got, c.want)
		}
	}
}

// sparseRepository has files at the root, a directory per application,
// a nested pair, and names with each of git's pattern characters, each
// next to a name that the pattern would match if it were not escaped.
var sparseRepository = map[string]string{
	"README.md":           "root",
	"app-a/config.yaml":   "a",
	"app-a/sub/x.yaml":    "x",
	"app-b/config.yaml":   "b",
	"docs/one.md":         "one",
	"docs/two.md":         "two",
	"nest/top.txt":        "top",
	"nest/deep/low.txt":   "low",
	"nest-b/x.txt":        "nest-b",
	"pages/[id]/page.tsx": "id",
	"pages/i/page.tsx":    "i",
	"st*r":                "literal star",
	"star":                "star",
	"q?":                  "literal question",
	"qa":                  "qa",
	`back\slash`:          "literal backslash",
	"backslash":           "backslash",
}

// sparseTree creates a work tree of a repository with one commit of
// files, checked out with the entries.
func sparseTree(t *testing.T, files map[string]string, entries []string) *workTree {
	t.Helper()
	work, commit := workTreeOf(t, repositoryWithACommit(t, files))
	work.paths = entries
	if err := work.create(t.Context(), "main", commit); err != nil {
		t.Fatalf("creating the work tree: %v", err)
	}
	return work
}

func TestAWriteableTreeChecksOutOnlyItsPaths(t *testing.T) {
	for _, c := range []struct {
		name    string
		entries []string
		want    map[string]string
	}{
		{
			name:    "a directory",
			entries: []string{"app-a"},
			want:    map[string]string{"app-a/config.yaml": "a", "app-a/sub/x.yaml": "x"},
		},
		{
			name:    "a file",
			entries: []string{"docs/one.md"},
			want:    map[string]string{"docs/one.md": "one"},
		},
		{
			name:    "a nested pair",
			entries: []string{"nest", "nest/deep"},
			want:    map[string]string{"nest/top.txt": "top", "nest/deep/low.txt": "low"},
		},
		{
			name:    "brackets",
			entries: []string{"pages/[id]"},
			want:    map[string]string{"pages/[id]/page.tsx": "id"},
		},
		{
			name:    "a star",
			entries: []string{"st*r"},
			want:    map[string]string{"st*r": "literal star"},
		},
		{
			name:    "a question mark",
			entries: []string{"q?"},
			want:    map[string]string{"q?": "literal question"},
		},
		{
			name:    "a backslash",
			entries: []string{`back\slash`},
			want:    map[string]string{`back\slash`: "literal backslash"},
		},
		{
			name:    "an entry the commit does not have",
			entries: []string{"apps/new"},
			want:    map[string]string{},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			work := sparseTree(t, sparseRepository, c.entries)

			if got := readTree(t, work.tree); !sameTree(got, c.want) {
				t.Errorf("the tree has %v, want %v", got, c.want)
			}
			found, err := work.pending(t.Context())
			if err != nil {
				t.Fatalf("reading the pending paths: %v", err)
			}
			if len(found) != 0 {
				t.Errorf("the new tree has %v pending, want nothing", found)
			}
		})
	}
}

func TestAWriteOutsideThePathsIsNotPending(t *testing.T) {
	work := sparseTree(t, sparseRepository, []string{"app-a"})
	writeFiles(t, work.tree, map[string]string{
		"README.md":         "a tracked file the pod wrote",
		"app-b/config.yaml": "another tracked file",
		"log.txt":           "an untracked file",
		"app-a/new.yaml":    "inside",
	})

	found, err := work.pending(t.Context())
	if err != nil {
		t.Fatalf("reading the pending paths: %v", err)
	}
	if len(found) != 1 || found[0].path != "app-a/new.yaml" {
		t.Errorf("the tree has %v pending, want only app-a/new.yaml", found)
	}
	if got := gitIn(t, work, "status", "--porcelain", "--untracked-files=no"); got != "" {
		t.Errorf("git status reports %q for the tracked files, want a clean tree", got)
	}
}

func TestCreateReportsPatternsItCannotWrite(t *testing.T) {
	refusingGit(t, "config")
	work, commit := workTreeOf(t, repositoryWithACommit(t, map[string]string{"app/a.txt": "a"}))
	work.paths = []string{"app"}
	if err := work.create(t.Context(), "main", commit); err == nil {
		t.Error("create returned no error when git config failed, want one")
	}
}

func TestAKeptTreeChecksOutTheNewPaths(t *testing.T) {
	for _, c := range []struct {
		name string
		old  []string
		new  []string
		want map[string]string
	}{
		{
			name: "other entries",
			old:  []string{"app-a"},
			new:  []string{"app-b", "docs/one.md"},
			want: map[string]string{"app-b/config.yaml": "b", "docs/one.md": "one"},
		},
		{
			name: "from entries to the whole tree",
			old:  []string{"app-a"},
			new:  nil,
			want: sparseRepository,
		},
		{
			name: "from the whole tree to entries",
			old:  nil,
			new:  []string{"pages/[id]"},
			want: map[string]string{"pages/[id]/page.tsx": "id"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			work := sparseTree(t, sparseRepository, c.old)
			work.paths = c.new

			if err := work.changePaths(t.Context()); err != nil {
				t.Fatalf("changing the paths: %v", err)
			}
			if got := readTree(t, work.tree); !sameTree(got, c.want) {
				t.Errorf("the tree has %v, want %v", got, c.want)
			}
			if got, err := work.checkedOut(); err != nil || !slices.Equal(got, c.new) {
				t.Errorf("the tree checks out %q (%v), want %q", got, err, c.new)
			}
			if got := gitIn(t, work, "status", "--porcelain"); got != "" {
				t.Errorf("git status reports %q, want a clean tree", got)
			}
		})
	}
}

func TestAKeptTreeWithTheSamePathsRunsNoGit(t *testing.T) {
	work := sparseTree(t, sparseRepository, []string{"app-a"})
	refusingGit(t, "status")
	if err := work.changePaths(t.Context()); err != nil {
		t.Errorf("changing to the same paths returned %v, want nothing to do", err)
	}
}

func TestAKeptTreeKeepsItsPathsWhileAChangeInsideThemIsPending(t *testing.T) {
	work := sparseTree(t, sparseRepository, []string{"app-a", "docs/one.md"})
	writeFiles(t, work.tree, map[string]string{"app-a/new.yaml": "pending", "log.txt": "outside"})
	work.paths = []string{"app-b"}

	err := work.changePaths(t.Context())
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Fatalf("changePaths returned %v, want FailedPrecondition", err)
	}
	want := `paths: the work tree on this node checks out "app-a", "docs/one.md", and the volume names "app-b". ` +
		`The tree has 1 uncommitted paths inside "app-a", "docs/one.md". ` +
		`The driver never commits a path outside the volume's paths, so the stage leaves the tree as it is`
	if got := status.Convert(err).Message(); got != want {
		t.Errorf("the message is %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(work.tree, "app-b")); err == nil {
		t.Error("the tree has app-b, want the old paths kept")
	}
}

func TestAKeptTreeChangesItsPathsWhenOnlyAFileOutsideThemIsPending(t *testing.T) {
	work := sparseTree(t, sparseRepository, []string{"app-a"})
	writeFiles(t, work.tree, map[string]string{"log.txt": "outside"})
	work.paths = []string{"app-b"}

	if err := work.changePaths(t.Context()); err != nil {
		t.Fatalf("changing the paths: %v", err)
	}
	want := map[string]string{"app-b/config.yaml": "b", "log.txt": "outside"}
	if got := readTree(t, work.tree); !sameTree(got, want) {
		t.Errorf("the tree has %v, want %v", got, want)
	}
}

func TestChangePathsReportsWhatItCannotDo(t *testing.T) {
	for _, c := range []struct {
		name  string
		old   []string
		new   []string
		fault func(t *testing.T, work *workTree)
	}{
		{
			name: "unreadable patterns",
			old:  []string{"app-a"},
			new:  []string{"app-b"},
			fault: func(t *testing.T, work *workTree) {
				replaceWithDirectory(t, filepath.Join(work.gitDir, sparseCheckoutFile))
			},
		},
		{
			name:  "a status git refuses",
			old:   []string{"app-a"},
			new:   []string{"app-b"},
			fault: func(t *testing.T, work *workTree) { refusingGit(t, "status") },
		},
		{
			name:  "a configuration git refuses",
			old:   []string{"app-a"},
			new:   []string{"app-b"},
			fault: func(t *testing.T, work *workTree) { refusingGit(t, "config") },
		},
		{
			name: "a git directory with no room for the patterns",
			old:  nil,
			new:  []string{"app-b"},
			fault: func(t *testing.T, work *workTree) {
				info := filepath.Join(work.gitDir, "info")
				if err := os.RemoveAll(info); err != nil {
					t.Fatalf("removing %s: %v", info, err)
				}
				writeFiles(t, work.gitDir, map[string]string{"info": "a file"})
			},
		},
		{
			name: "a read-only git directory",
			old:  nil,
			new:  []string{"app-b"},
			fault: func(t *testing.T, work *workTree) {
				readOnlyDir(t, filepath.Join(work.gitDir, "info"))
			},
		},
		{
			name:  "a read-tree git refuses",
			old:   []string{"app-a"},
			new:   []string{"app-b"},
			fault: func(t *testing.T, work *workTree) { refusingGit(t, "read-tree") },
		},
		{
			name:  "a sparse checkout git cannot turn off",
			old:   []string{"app-a"},
			new:   nil,
			fault: func(t *testing.T, work *workTree) { refusingGit(t, "core.sparseCheckout false") },
		},
		{
			name: "patterns that cannot be removed",
			old:  []string{"app-a"},
			new:  nil,
			fault: func(t *testing.T, work *workTree) {
				readOnlyDir(t, filepath.Join(work.gitDir, "info"))
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			work := sparseTree(t, sparseRepository, c.old)
			work.paths = c.new
			c.fault(t, work)

			if got := status.Code(work.changePaths(t.Context())); got != codes.Internal {
				t.Errorf("changePaths returned %v, want Internal", got)
			}
		})
	}
}

func TestPresentReportsAPathInTheCheckout(t *testing.T) {
	work := sparseTree(t, sparseRepository, []string{"app-a"})
	for _, c := range []struct {
		path string
		want bool
	}{
		{path: "app-a/config.yaml", want: true},
		{path: "app-b/config.yaml", want: false},
		{path: "app-a/config.yaml/under", want: false},
	} {
		if got := work.present(c.path); got != c.want {
			t.Errorf("present(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// The scratch tree is where a rebase runs. git worktree add copies the
// patterns into its git directory, so it checks out the same paths.
func TestTheScratchTreeChecksOutOnlyThePaths(t *testing.T) {
	work := sparseTree(t, sparseRepository, []string{"docs/one.md"})
	dir, remove, err := work.addScratchTree(t.Context())
	if err != nil {
		t.Fatalf("adding the scratch tree: %v", err)
	}
	defer remove()

	got := readTree(t, dir)
	delete(got, ".git")
	if want := map[string]string{"docs/one.md": "one"}; !sameTree(got, want) {
		t.Errorf("the scratch tree has %v, want %v", got, want)
	}
	if strings.Contains(git(t, dir, "status", "--porcelain"), "README") {
		t.Error("the scratch tree reports README.md, want the files outside the paths left out")
	}
}
