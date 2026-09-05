package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// WorkTreeOf makes the bare repository of the source, fetches the ref, and
// answers the work tree of one volume on it.
func workTreeOf(t *testing.T, source string) (*workTree, string) {
	t.Helper()
	holder := newStore(t.TempDir())
	repo := holder.repository(fileURL(source))
	if err := repo.create(t.Context()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.fetch(t.Context(), nil, "main", 0); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	commit, err := repo.resolve(t.Context(), "main")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	work := holder.workTree(repo, "csi-1")
	if err := os.MkdirAll(work.directory, 0o700); err != nil {
		t.Fatalf("making the volume directory: %v", err)
	}
	return work, commit
}

func TestTheWorkTreeReadsTheBareRepositorysObjects(t *testing.T) {
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one", "docs/b.txt": "two"})
	work, commit := workTreeOf(t, source)
	if work.exists() {
		t.Fatal("the work tree exists before it is created")
	}
	if err := work.create(t.Context(), "main", commit); err != nil {
		t.Fatalf("create: %v", err)
	}

	if !work.exists() {
		t.Error("the work tree does not exist after it is created")
	}
	want := map[string]string{"a.txt": "one", "docs/b.txt": "two"}
	if got := readTree(t, work.tree); !sameTree(got, want) {
		t.Errorf("the tree holds %v, want %v", got, want)
	}
	if _, err := os.Stat(filepath.Join(work.tree, ".git")); err == nil {
		t.Error("the tree holds a .git, which the pod would see")
	}
	alternates, err := os.ReadFile(filepath.Join(work.gitDir, alternatesFile))
	if err != nil {
		t.Fatalf("reading the alternates: %v", err)
	}
	objects := filepath.Join(work.repository.dir, "objects")
	if strings.TrimSpace(string(alternates)) != objects {
		t.Errorf("the alternates name %q, want %q", alternates, objects)
	}
	head, err := work.head(t.Context())
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if head != commit {
		t.Errorf("the tree stands on %s, want %s", head, commit)
	}
	if got := strings.TrimSpace(git(t, work.gitDir, "symbolic-ref", "HEAD")); got != "refs/heads/main" {
		t.Errorf("HEAD names %q, want refs/heads/main", got)
	}
}

func TestThePendingSetIsWhatThePodWrote(t *testing.T) {
	for _, c := range []struct {
		name  string
		write func(t *testing.T, tree string)
		want  []change
	}{
		{
			name:  "a tree the pod has not touched",
			write: func(*testing.T, string) {},
			want:  []change{},
		},
		{
			name: "a file the pod changed",
			write: func(t *testing.T, tree string) {
				writeFiles(t, tree, map[string]string{"a.txt": "12345"})
			},
			want: []change{{path: "a.txt", size: 5}},
		},
		{
			name: "a file the pod added",
			write: func(t *testing.T, tree string) {
				writeFiles(t, tree, map[string]string{"new.txt": "123"})
			},
			want: []change{{path: "new.txt", size: 3}},
		},
		{
			name: "a file the pod deleted",
			write: func(t *testing.T, tree string) {
				if err := os.Remove(filepath.Join(tree, "a.txt")); err != nil {
					t.Fatalf("removing the file: %v", err)
				}
			},
			want: []change{{path: "a.txt", size: 0}},
		},
		{
			name: "every file under a directory the pod added",
			write: func(t *testing.T, tree string) {
				writeFiles(t, tree, map[string]string{"new/one.txt": "1", "new/two.txt": "22"})
			},
			want: []change{{path: "new/one.txt", size: 1}, {path: "new/two.txt", size: 2}},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
			work, commit := workTreeOf(t, source)
			if err := work.create(t.Context(), "main", commit); err != nil {
				t.Fatalf("create: %v", err)
			}
			c.write(t, work.tree)

			found, err := work.pending(t.Context())
			if err != nil {
				t.Fatalf("pending: %v", err)
			}
			if len(found) != len(c.want) {
				t.Fatalf("pending answered %v, want %v", found, c.want)
			}
			for i := range c.want {
				if found[i] != c.want[i] {
					t.Errorf("pending answered %v, want %v", found, c.want)
				}
			}
		})
	}
}

func TestTheChangesReadGitsOwnReport(t *testing.T) {
	work := &workTree{tree: t.TempDir()}
	writeFiles(t, work.tree, map[string]string{"new.txt": "12345"})
	// Git writes the new path first and the old one after it, with -z.
	report := "R  new.txt\x00old.txt\x00 M kept.txt\x00"
	want := []change{{path: "new.txt", size: 5}, {path: "kept.txt", size: 0}}

	got := work.changes(report)
	if len(got) != len(want) {
		t.Fatalf("changes answered %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("changes answered %v, want %v", got, want)
		}
	}
}

func TestTheWorkTreeReportsWhatGitCannotDo(t *testing.T) {
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	work, commit := workTreeOf(t, source)

	if _, err := work.pending(t.Context()); err == nil {
		t.Error("pending answered no error before the work tree was made")
	}
	if _, err := work.head(t.Context()); err == nil {
		t.Error("head answered no error before the work tree was made")
	}
	if err := work.create(t.Context(), "not a ref", commit); err == nil {
		t.Error("create answered no error for a ref git refuses")
	}
	if err := work.create(t.Context(), "main", "0000000000000000000000000000000000000000"); err == nil {
		t.Error("create answered no error for a commit the store does not hold")
	}
}

func TestTheWorkTreeReportsATreeItCannotMake(t *testing.T) {
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	work, commit := workTreeOf(t, source)
	writeFiles(t, work.directory, map[string]string{"tree": ""})
	if err := work.create(t.Context(), "main", commit); err == nil {
		t.Error("create answered no error where the tree is a file")
	}
}

func TestTheWorkTreeReportsAnAlternatesFileItCannotWrite(t *testing.T) {
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	work, commit := workTreeOf(t, source)
	if err := os.MkdirAll(filepath.Join(work.gitDir, "objects"), 0o755); err != nil {
		t.Fatalf("making the objects directory: %v", err)
	}
	writeFiles(t, filepath.Join(work.gitDir, "objects"), map[string]string{"info": ""})
	if err := work.create(t.Context(), "main", commit); err == nil {
		t.Error("create answered no error where the alternates cannot be written")
	}
}
