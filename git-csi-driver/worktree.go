package main

// worktree.go manages a writeable volume's checkout and git directory,
// and reads the changes the pod has made in the checkout.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// alternatesFile lists other object stores that git reads from. A work
// tree points it at the bare repository of its URL, so each URL's
// history is stored once on the node.
const alternatesFile = "objects/info/alternates"

// workTree is one volume's git directory and checkout. The git
// directory is outside the checkout, so the pod sees no .git and cannot
// commit or push on its own.
type workTree struct {
	repository *repository
	directory  string
	gitDir     string
	tree       string
	// paths is the volume's entries, or nil for a volume that checks out
	// the whole tree. sparse.go describes how a tree keeps to them.
	paths []string

	mu sync.Mutex
}

func (s *store) workTree(repo *repository, id string, paths []string) *workTree {
	work := s.tree(id)
	work.repository = repo
	work.paths = paths
	return work
}

// tree returns a volume's work tree with no repository set, for
// callers that do not know the volume's URL.
func (s *store) tree(id string) *workTree {
	directory := s.volumeDir(id)
	return &workTree{
		directory: directory,
		gitDir:    filepath.Join(directory, "git"),
		tree:      filepath.Join(directory, "tree"),
	}
}

// alternate returns the bare repository that the alternates file points
// to, or an empty string when there is no alternates file. It returns an
// error when the file is there and cannot be read, so the sweep does not
// take a tree it cannot read for a tree that uses no repository.
func (w *workTree) alternate() (string, error) {
	content, err := os.ReadFile(filepath.Join(w.gitDir, alternatesFile))
	if absent(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return filepath.Dir(trimLine(string(content))), nil
}

// exists reports whether the git directory has a HEAD file. git init
// writes HEAD, so exists is true from the first step of create, before
// the alternates file and the checkout are written.
func (w *workTree) exists() bool {
	_, err := os.Stat(filepath.Join(w.gitDir, "HEAD"))
	return err == nil
}

// create initializes the git directory, points HEAD at the ref, and
// resets the checkout to the commit. The reset writes the index along
// with the files, so git status is correct from the first stage. For a
// volume with paths, the patterns are written before the reset, so the
// reset writes only the files of the entries. An entry that the commit
// does not have checks out nothing, and the stage still succeeds,
// because a new application has no directory until its first commit.
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
	if w.paths != nil {
		if err := w.sparse(ctx, sparsePatterns(w.paths)); err != nil {
			return err
		}
	}
	_, err := w.git(ctx, "reset", "--hard", "--quiet", commit)
	return err
}

func (w *workTree) head(ctx context.Context) (string, error) {
	output, err := w.git(ctx, "rev-parse", "--verify", "--end-of-options", "HEAD")
	if err != nil {
		return "", err
	}
	return trimLine(output.stdout), nil
}

// change is one path that git status reports, with its size in bytes.
type change struct {
	path string
	size int64
}

// pending returns the paths inside the volume's paths that the pod
// changed and the driver has not committed. Each untracked file is its
// own change, not its directory. A file that the pod writes outside the
// paths is not a pending change, because the driver never commits it.
func (w *workTree) pending(ctx context.Context) ([]change, error) {
	return w.pendingIn(ctx, w.paths)
}

// pendingIn is pending limited to the entries, or the whole tree when
// entries is nil. --literal-pathspecs makes git read each entry as a
// file name, so an entry such as pages/[id] is not a pattern.
//
// Without --no-optional-locks, git status refreshes the index and holds
// index.lock while it does. The watch runs pending in the background,
// and unwatch kills it when the pod goes away. A kill during the
// refresh leaves index.lock behind, and every later git add on the
// volume fails.
func (w *workTree) pendingIn(ctx context.Context, entries []string) ([]change, error) {
	args := []string{"--no-optional-locks", "status",
		"--porcelain", "-z", "--untracked-files=all"}
	if entries != nil {
		args = append(append([]string{"--literal-pathspecs"}, args...), "--")
		args = append(args, entries...)
	}
	output, err := w.git(ctx, args...)
	if err != nil {
		return nil, err
	}
	return w.changes(output.stdout), nil
}

// changes parses the output of git status --porcelain -z. Each entry is
// two status letters, a space, and the path. A rename or a copy has a
// second entry with the old path, which changes skips.
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

// sizeOf returns the size of a file in the checkout, or zero for a
// deleted path or anything that is not a regular file.
func (w *workTree) sizeOf(path string) int64 {
	info, err := os.Lstat(filepath.Join(w.tree, filepath.FromSlash(path)))
	if err != nil || !info.Mode().IsRegular() {
		return 0
	}
	return info.Size()
}

// scratchTree is the directory of a second checkout where a rebase
// runs. A rebase in the pod's own checkout would rewrite the pod's
// files while the pod has them open.
const scratchTree = "scratch"

// addScratchTree adds a detached git worktree at HEAD and returns its
// directory and a function that removes it.
func (w *workTree) addScratchTree(ctx context.Context) (string, func(), error) {
	dir := filepath.Join(w.directory, scratchTree)
	// A driver killed during a rebase leaves its scratch tree, and git
	// refuses to add a worktree where one already exists.
	w.removeScratch(ctx, dir)
	if _, err := w.git(ctx, "worktree", "add", "--detach", "--quiet",
		"--end-of-options", dir, "HEAD"); err != nil {
		return "", nil, err
	}
	return dir, func() { w.removeScratch(ctx, dir) }, nil
}

// removeScratch deletes the scratch tree and git's record of it. It
// ignores errors, because the scratch tree is often already gone.
func (w *workTree) removeScratch(ctx context.Context, dir string) {
	_, _ = w.git(ctx, "worktree", "remove", "--force", "--end-of-options", dir)
	_ = os.RemoveAll(dir)
	_, _ = w.git(ctx, "worktree", "prune")
}

// moveCheckout moves the checkout and the followed branch from commit old to
// commit new. git read-tree -m -u rewrites only the paths that differ
// between the two commits, so the pod's other files are not touched. It
// fails when the pod has uncommitted changes to a path that also
// differs between the commits.
func (w *workTree) moveCheckout(ctx context.Context, old, new string) error {
	if _, err := w.git(ctx, "read-tree", "-m", "-u", "--end-of-options", old, new); err != nil {
		return err
	}
	branch, err := w.followedRef(ctx)
	if err != nil {
		return err
	}
	_, err = w.git(ctx, "update-ref", "refs/heads/"+branch, new)
	return err
}

// changedPaths returns the paths that differ between two commits, which
// are the paths that moveCheckout rewrites. It returns none when git cannot
// read the commits.
func (w *workTree) changedPaths(ctx context.Context, old, new string) []string {
	output, _ := w.git(ctx, "diff-tree", "-r", "-z", "--name-only", "--end-of-options", old, new)
	return splitZero(output.stdout)
}

// git runs git on this work tree. The mutex in gitWith stops two git
// commands from running on the same tree at once, such as a stage and
// a status.
func (w *workTree) git(ctx context.Context, args ...string) (gitOutput, error) {
	return w.gitWith(ctx, nil, args...)
}

// gitWith is git with extra environment variables, such as the author,
// the committer, and credentials.
func (w *workTree) gitWith(ctx context.Context, env []string, args ...string) (gitOutput, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return runGit(ctx, w.directory, env,
		append([]string{"--git-dir=" + w.gitDir, "--work-tree=" + w.tree}, args...)...)
}

// followedRef returns the branch that HEAD points to, or an empty
// string when HEAD names a commit and no branch. With --quiet, git
// symbolic-ref exits with 1 for that case and with 128 when it cannot
// read the git directory, which followedRef returns as an error.
func (w *workTree) followedRef(ctx context.Context) (string, error) {
	output, err := w.git(ctx, "symbolic-ref", "--quiet", "HEAD")
	if err != nil && output.code == 1 {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(trimLine(output.stdout), "refs/heads/"), nil
}

// absent reports whether err says that a path is not there. A path
// under an entry that is not a directory cannot be there either, so
// ENOTDIR counts as ENOENT does: a file in volumes/ holds no work tree
// and no record.
func absent(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// refCommit returns the commit the ref points to, or an empty string
// when the git directory has no such ref.
func (w *workTree) refCommit(ctx context.Context, ref string) string {
	output, err := w.git(ctx, "rev-parse", "--verify", "--quiet", "--end-of-options", ref)
	if err != nil {
		return ""
	}
	return trimLine(output.stdout)
}
