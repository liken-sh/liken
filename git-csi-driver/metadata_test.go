package main

import (
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// createdWorkTree is a work tree of the source, checked out on main.
func createdWorkTree(t *testing.T, files map[string]string) *workTree {
	t.Helper()
	work, commit := workTreeOf(t, repositoryWithACommit(t, files))
	if err := work.create(t.Context(), "main", commit); err != nil {
		t.Fatalf("create: %v", err)
	}
	return work
}

func TestTheWalkRecordsWhatACheckoutCannotCarry(t *testing.T) {
	tree := t.TempDir()
	writeFiles(t, tree, map[string]string{
		"plain.yaml":        "one",
		"secret.yaml":       "two",
		"docs/plain.md":     "three",
		"scripts/run.sh":    "four",
		"named\npipe.yaml":  "five",
		"docs/deeper/a.txt": "six",
	})
	if err := os.Chmod(filepath.Join(tree, "secret.yaml"), 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := os.Chmod(filepath.Join(tree, "scripts/run.sh"), 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tree, ".storage"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(filepath.Join(tree, "docs"), 0o750); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	records, err := walkTreeMetadata(tree)
	if err != nil {
		t.Fatalf("walkTreeMetadata: %v", err)
	}
	got := []string{}
	for _, one := range records {
		got = append(got, one.path)
	}
	want := ".storage|docs|scripts/run.sh|secret.yaml"
	if strings.Join(got, "|") != want {
		t.Errorf("the walk recorded %v, want %s", got, want)
	}
}

func TestTheWalkReportsATreeItCannotRead(t *testing.T) {
	if _, err := walkTreeMetadata(filepath.Join(t.TempDir(), "gone")); err == nil {
		t.Error("walkTreeMetadata answered no error for a tree that is not there")
	}
}

func TestASymbolicLinkIsRecordedNowhere(t *testing.T) {
	tree := t.TempDir()
	if err := os.Symlink("elsewhere", filepath.Join(tree, "link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	records, err := walkTreeMetadata(tree)
	if err != nil {
		t.Fatalf("walkTreeMetadata: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("the walk recorded %v, want nothing", records)
	}
}

func TestARecordIsOneLine(t *testing.T) {
	content := metadataContent([]metadataRecord{
		{path: "secret.yaml", mode: 0o600, uid: 0, gid: 0},
		{path: ".storage", directory: true, mode: 0o755, uid: 1000, gid: 1000},
	})
	want := "0600 0 0 secret.yaml\n0755 1000 1000 .storage/\n"
	if content != want {
		t.Errorf("metadataContent answered %q, want %q", content, want)
	}
	read := parseMetadataContent(content)
	if len(read) != 2 {
		t.Fatalf("parseMetadataContent answered %v, want two records", read)
	}
	if read[0].path != "secret.yaml" || read[0].mode != fs.FileMode(0o600) || read[0].directory {
		t.Errorf("the first record is %+v", read[0])
	}
	if read[1].path != ".storage" || !read[1].directory || read[1].uid != 1000 || read[1].gid != 1000 {
		t.Errorf("the second record is %+v", read[1])
	}
}

func TestALineTheDriverCannotReadIsNoRecord(t *testing.T) {
	for _, c := range []struct {
		name string
		line string
	}{
		{name: "an empty line", line: ""},
		{name: "too few fields", line: "0600 0 secret.yaml"},
		{name: "a mode that is not octal", line: "0900 0 0 secret.yaml"},
		{name: "an owner that is not a number", line: "0600 root 0 secret.yaml"},
		{name: "a group that is not a number", line: "0600 0 wheel secret.yaml"},
		{name: "a path that climbs out of the tree", line: "0600 0 0 ../escape.yaml"},
		{name: "an absolute path", line: "0600 0 0 /etc/shadow"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if one, ok := parseMetadataLine(c.line); ok {
				t.Errorf("parseMetadataLine(%q) answered %+v, want no record", c.line, one)
			}
		})
	}
}

func TestTheRecordIsCommittedOnlyWhenItChanges(t *testing.T) {
	work := createdWorkTree(t, map[string]string{"a.txt": "one"})
	rules := defaultPolicy()
	if err := os.Chmod(filepath.Join(work.tree, "a.txt"), 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	if err := work.recordMetadata(t.Context(), rules); err != nil {
		t.Fatalf("recordMetadata: %v", err)
	}
	first := work.refCommit(t.Context(), metadataRef)
	if first == "" {
		t.Fatal("the metadata ref names no commit")
	}
	content, err := work.metadataRecord(t.Context())
	if err != nil {
		t.Fatalf("metadataRecord: %v", err)
	}
	if !strings.HasPrefix(content, "0600 ") || !strings.HasSuffix(content, " a.txt\n") {
		t.Errorf("the record is %q, want the mode of a.txt", content)
	}

	if err := work.recordMetadata(t.Context(), rules); err != nil {
		t.Fatalf("recordMetadata: %v", err)
	}
	if again := work.refCommit(t.Context(), metadataRef); again != first {
		t.Errorf("a record that did not change made the commit %s, want %s", again, first)
	}

	if err := os.Chmod(filepath.Join(work.tree, "a.txt"), 0o640); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := work.recordMetadata(t.Context(), rules); err != nil {
		t.Fatalf("recordMetadata: %v", err)
	}
	third := work.refCommit(t.Context(), metadataRef)
	if third == first {
		t.Error("a record that changed made no commit")
	}
	parents := gitIn(t, work, "rev-list", "--count", metadataRef)
	if strings.TrimSpace(parents) != "2" {
		t.Errorf("the metadata ref holds %s commits, want 2", strings.TrimSpace(parents))
	}
}

// gitIn runs the tests' own git against the work tree's git directory.
func gitIn(t *testing.T, work *workTree, args ...string) string {
	t.Helper()
	return git(t, work.directory,
		append([]string{"--git-dir=" + work.gitDir, "--work-tree=" + work.tree}, args...)...)
}

func TestTheRecordIsNotCommittedWhenGitCannotHoldIt(t *testing.T) {
	for _, c := range []struct {
		name  string
		stand func(t *testing.T, work *workTree)
	}{
		{
			name: "a record file the driver cannot write",
			stand: func(t *testing.T, work *workTree) {
				if err := os.MkdirAll(
					filepath.Join(work.directory, metadataRecordFile), 0o755); err != nil {
					t.Fatalf("making the record directory: %v", err)
				}
			},
		},
		{
			name: "a git directory that is gone",
			stand: func(t *testing.T, work *workTree) {
				if err := os.RemoveAll(work.gitDir); err != nil {
					t.Fatalf("removing the git directory: %v", err)
				}
			},
		},
		{
			name: "an index the driver cannot write",
			stand: func(t *testing.T, work *workTree) {
				if err := os.MkdirAll(
					filepath.Join(work.directory, metadataIndexFile), 0o755); err != nil {
					t.Fatalf("making the index directory: %v", err)
				}
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			work := createdWorkTree(t, map[string]string{"a.txt": "one"})
			if err := os.Chmod(filepath.Join(work.tree, "a.txt"), 0o600); err != nil {
				t.Fatalf("chmod: %v", err)
			}
			c.stand(t, work)
			if err := work.recordMetadata(t.Context(), defaultPolicy()); err == nil {
				t.Error("recordMetadata answered no error")
			}
		})
	}
}

func TestTheReplayGivesTheTreeItsModesAndEmptyDirectories(t *testing.T) {
	work := createdWorkTree(t, map[string]string{"a.txt": "one", "docs/b.txt": "two"})
	rules := defaultPolicy()
	if err := os.Chmod(filepath.Join(work.tree, "a.txt"), 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(work.tree, ".storage"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := work.recordMetadata(t.Context(), rules); err != nil {
		t.Fatalf("recordMetadata: %v", err)
	}

	// The checkout a restore starts from carries neither.
	if err := os.Chmod(filepath.Join(work.tree, "a.txt"), 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(work.tree, ".storage")); err != nil {
		t.Fatalf("removing the empty directory: %v", err)
	}

	if err := work.replayMetadata(t.Context(), slog.Default(), false); err != nil {
		t.Fatalf("replayMetadata: %v", err)
	}
	mode, err := os.Stat(filepath.Join(work.tree, "a.txt"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode.Mode().Perm() != 0o600 {
		t.Errorf("a.txt is %s, want -rw-------", mode.Mode())
	}
	storage, err := os.Stat(filepath.Join(work.tree, ".storage"))
	if err != nil {
		t.Fatalf("the empty directory did not return: %v", err)
	}
	if !storage.IsDir() || storage.Mode().Perm() != 0o700 {
		t.Errorf(".storage is %s, want drwx------", storage.Mode())
	}
}

func TestTheReplayTakesTheOwnerWhereTheDriverMay(t *testing.T) {
	work := createdWorkTree(t, map[string]string{"a.txt": "one"})
	if err := os.Chmod(filepath.Join(work.tree, "a.txt"), 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := work.recordMetadata(t.Context(), defaultPolicy()); err != nil {
		t.Fatalf("recordMetadata: %v", err)
	}
	logs := &logbook{}
	if err := work.replayMetadata(t.Context(),
		slog.New(slog.NewTextHandler(logs, nil)), true); err != nil {
		t.Fatalf("replayMetadata: %v", err)
	}
	if strings.Contains(logs.String(), "the owner was not replayed") {
		t.Errorf("the driver could not take its own file: %s", logs)
	}
}

func TestTheReplayReportsWhatItCannotGive(t *testing.T) {
	for _, c := range []struct {
		name    string
		content string
		owners  bool
		says    string
	}{
		{
			name:    "a directory a file stands in the way of",
			content: "0755 0 0 a.txt/\n",
			says:    "the directory was not made",
		},
		{
			name:    "a path the checkout does not hold",
			content: "0600 0 0 gone.yaml\n",
			says:    "the mode was not replayed",
		},
		{
			name:    "an owner the driver may not take",
			content: "0644 65534 65534 a.txt\n",
			owners:  true,
			says:    "the owner was not replayed",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			work := createdWorkTree(t, map[string]string{"a.txt": "one"})
			recordOnRef(t, work, c.content)
			logs := &logbook{}
			if err := work.replayMetadata(t.Context(),
				slog.New(slog.NewTextHandler(logs, nil)), c.owners); err != nil {
				t.Fatalf("replayMetadata: %v", err)
			}
			if !strings.Contains(logs.String(), c.says) {
				t.Errorf("the log is %q, want %q in it", logs, c.says)
			}
		})
	}
}

// recordOnRef puts the content on the metadata ref, which is what a
// restore reads.
func recordOnRef(t *testing.T, work *workTree, content string) {
	t.Helper()
	if err := work.commitMetadata(t.Context(), defaultPolicy(), content); err != nil {
		t.Fatalf("commitMetadata: %v", err)
	}
}

func TestTheReplayReportsARefItCannotRead(t *testing.T) {
	work := createdWorkTree(t, map[string]string{"a.txt": "one"})
	if err := work.replayMetadata(t.Context(), slog.Default(), false); err == nil {
		t.Error("replayMetadata answered no error for a tree with no metadata ref")
	}
}

func TestTheRecordIsNotMadeFromATreeThatIsGone(t *testing.T) {
	work := createdWorkTree(t, map[string]string{"a.txt": "one"})
	if err := os.RemoveAll(work.tree); err != nil {
		t.Fatalf("removing the tree: %v", err)
	}
	if err := work.recordMetadata(t.Context(), defaultPolicy()); err == nil {
		t.Error("recordMetadata answered no error for a tree that is not there")
	}
}
