package main

// worktree.go holds the tree a writeable volume's pod writes, the git
// directory beside it, and the changes git finds in it.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// alternatesFile names the bare repository whose objects this work tree
// reads, so history is stored once per URL.
const alternatesFile = "objects/info/alternates"

// workTree is one volume's git directory and the checkout beside it.
// The checkout holds no .git of its own, so the pod cannot commit or
// push around the driver.
type workTree struct {
	repository *repository
	directory  string
	gitDir     string
	tree       string

	mu sync.Mutex
}

// workTree is the work tree of a volume, sharing the bare repository of
// its URL.
func (s *store) workTree(repo *repository, id string) *workTree {
	directory := s.volumeDir(id)
	return &workTree{
		repository: repo,
		directory:  directory,
		gitDir:     filepath.Join(directory, "git"),
		tree:       filepath.Join(directory, "tree"),
	}
}

// exists reports whether create finished. HEAD is what create writes
// last.
func (w *workTree) exists() bool {
	_, err := os.Stat(filepath.Join(w.gitDir, "HEAD"))
	return err == nil
}

// create makes the git directory beside the tree, shares the bare
// repository's objects through the alternates file, points HEAD at the
// ref, and resets the tree to the commit. reset sets HEAD, the index,
// and the tree in one call, so git status is meaningful from the first
// stage.
func (w *workTree) create(ctx context.Context, ref, commit string) error {
	if err := os.MkdirAll(w.tree, 0o755); err != nil {
		return err
	}
	if _, err := w.git(ctx, "init", "--quiet"); err != nil {
		return err
	}
	alternates := filepath.Join(w.gitDir, alternatesFile)
	if err := os.MkdirAll(filepath.Dir(alternates), 0o755); err != nil {
		return err
	}
	objects := filepath.Join(w.repository.dir, "objects")
	if err := os.WriteFile(alternates, []byte(objects+"\n"), 0o644); err != nil {
		return err
	}
	if _, err := w.git(ctx, "symbolic-ref", "HEAD", "refs/heads/"+ref); err != nil {
		return err
	}
	_, err := w.git(ctx, "reset", "--hard", "--quiet", commit)
	return err
}

// head is the commit the tree stands on.
func (w *workTree) head(ctx context.Context) (string, error) {
	output, err := w.git(ctx, "rev-parse", "--verify", "--end-of-options", "HEAD")
	if err != nil {
		return "", err
	}
	return trimLine(output.stdout), nil
}

// change is one path git reports and the bytes it holds now.
type change struct {
	path string
	size int64
}

// pending is what the pod wrote and the driver has not committed. Every
// untracked file is named, so three files under a new directory count
// as three paths and not one.
func (w *workTree) pending(ctx context.Context) ([]change, error) {
	output, err := w.git(ctx, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	return w.changes(output.stdout), nil
}

// changes reads git's -z report: two status letters, a space, the path,
// and a second path after a rename or a copy.
func (w *workTree) changes(report string) []change {
	entries := strings.Split(report, "\x00")
	found := []change{}
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) < 4 {
			continue
		}
		if entry[0] == 'R' || entry[0] == 'C' {
			i++
		}
		found = append(found, change{path: entry[3:], size: w.sizeOf(entry[3:])})
	}
	return found
}

// sizeOf is the size of a path git named, and zero for a path the pod
// deleted.
func (w *workTree) sizeOf(path string) int64 {
	info, err := os.Lstat(filepath.Join(w.tree, filepath.FromSlash(path)))
	if err != nil || !info.Mode().IsRegular() {
		return 0
	}
	return info.Size()
}

// git runs git against the work tree with the git directory beside it,
// so the pod never sees a .git. The lock keeps a stage and a status of
// the same tree apart.
func (w *workTree) git(ctx context.Context, args ...string) (gitOutput, error) {
	return w.gitWith(ctx, nil, args...)
}

// gitWith is git with an environment of its own, which is how the
// author, the committer, and a credential reach one invocation.
func (w *workTree) gitWith(ctx context.Context, env []string, args ...string) (gitOutput, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return runGit(ctx, w.directory, env,
		append([]string{"--git-dir=" + w.gitDir, "--work-tree=" + w.tree}, args...)...)
}

// refCommit is the commit the ref names, and the empty string
// where the git directory holds no such ref.
func (w *workTree) refCommit(ctx context.Context, ref string) string {
	output, err := w.git(ctx, "rev-parse", "--verify", "--quiet", "--end-of-options", ref)
	if err != nil {
		return ""
	}
	return trimLine(output.stdout)
}
