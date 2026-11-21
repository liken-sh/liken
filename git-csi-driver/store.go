package main

// store.go lays out the driver's directories on the node: one bare
// repository per URL under repos/, and one directory per volume under
// volumes/.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// refPrefix is the ref namespace where a store repository keeps the refs
// it fetches. A separate namespace cannot collide with the remote's own
// ref names, and one bare repository can serve volumes that follow
// different refs.
const refPrefix = "refs/git-csi/"

// store is the root directory of the driver's data on the node.
//
// Each repository's lock is a channel with a buffer of one in place of
// a `sync.Mutex`. synctest counts a goroutine blocked on a channel send
// as durably blocked, so the fake clock keeps moving while a publish
// waits for a fetch of the same repository. A goroutine blocked on a
// `sync.Mutex` stops the clock.
type store struct {
	root string

	mu    sync.Mutex
	locks map[string]chan struct{}
}

func newStore(root string) *store {
	return &store{root: root, locks: map[string]chan struct{}{}}
}

// repositoryURLFile holds the remote URL in a bare repository's
// directory, so a person can tell which remote a hashed directory name
// belongs to.
const repositoryURLFile = "url"

// repository is the bare repository that every volume of the same URL on
// this node shares.
type repository struct {
	store *store
	url   string
	name  string
	dir   string
}

// repository names the directory by the sha256 of the URL, because a
// URL is not a valid file name.
func (s *store) repository(url string) *repository {
	sum := sha256.Sum256([]byte(url))
	name := hex.EncodeToString(sum[:])
	return &repository{
		store: s,
		url:   url,
		name:  name,
		dir:   filepath.Join(s.root, "repos", name),
	}
}

// volumeDir is the directory of one volume. It holds the volume's tree,
// its record, and the credential files that exist while git runs.
func (s *store) volumeDir(id string) string {
	return filepath.Join(s.root, "volumes", id)
}

// lock waits for this repository's lock and returns the function that
// releases it. Every caller that fetches into the bare repository takes
// it, and so does the sweep, so two fetches never write the repository
// at once and the sweep never deletes it during a fetch.
func (r *repository) lock() func() {
	r.store.mu.Lock()
	lock, found := r.store.locks[r.name]
	if !found {
		lock = make(chan struct{}, 1)
		r.store.locks[r.name] = lock
	}
	r.store.mu.Unlock()

	lock <- struct{}{}
	return func() { <-lock }
}

func (r *repository) exists() bool {
	_, err := os.Stat(filepath.Join(r.dir, "HEAD"))
	return err == nil
}

func (r *repository) create(ctx context.Context) error {
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return err
	}
	if _, err := runGit(ctx, r.dir, nil, "init", "--quiet", "--bare"); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.dir, repositoryURLFile), []byte(r.url+"\n"), 0o644)
}

// fetch writes the remote's ref to refPrefix+ref. The leading + of the
// refspec forces the update when the remote's history was rewritten. A
// depth of zero fetches the full history.
func (r *repository) fetch(ctx context.Context, env []string, ref string, depth int) error {
	args := []string{"fetch", "--quiet", "--no-tags"}
	if depth > 0 {
		args = append(args, "--depth="+strconv.Itoa(depth))
	}
	args = append(args, r.url, "+"+ref+":"+refPrefix+ref)
	_, err := runGit(ctx, r.dir, env, args...)
	return err
}

// resolve returns the commit at refPrefix+ref, or an error when the
// repository has not fetched the ref.
func (r *repository) resolve(ctx context.Context, ref string) (string, error) {
	output, err := runGit(ctx, r.dir, nil, "rev-parse", "--verify", "--end-of-options",
		refPrefix+ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return trimLine(output.stdout), nil
}

// checkout writes the files of the commit at the entries into dir, or
// the whole tree when entries is empty.
func (r *repository) checkout(ctx context.Context, commit, dir string, entries []string) error {
	if len(entries) == 0 {
		entries = []string{"."}
	}
	return r.checkoutFrom(ctx, commit, dir, append([]string{"--"}, entries...))
}

// checkoutFrom reads the whole tree of the commit into a temporary
// index, and then checks out the paths that pathspec names from that
// index into dir. The whole tree is in the index so that a
// .gitattributes outside the paths, such as a root `* text eol=crlf`,
// applies to the files it checks out, and a file has the same bytes
// whatever the volume's paths are. The index file is written beside dir
// and deleted afterward, so a pod sees no git metadata in dir.
func (r *repository) checkoutFrom(ctx context.Context, commit, dir string, pathspec []string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	index := dir + ".index"
	defer os.Remove(index)
	env := append([]string{"GIT_INDEX_FILE=" + index}, literalPathspecs...)

	if _, err := runGit(ctx, r.dir, env, "read-tree", commit); err != nil {
		return err
	}
	args := []string{"--work-tree=" + dir, "-c", "core.bare=false", "checkout", "--force"}
	_, err := runGit(ctx, r.dir, env, append(args, pathspec...)...)
	return err
}

// nextTree is the directory where place checks out a commit before it
// moves the files into the published tree.
const nextTree = "next"

// place moves the published tree to the commit. A commit that lacks an
// entry of paths changes nothing and returns a *missingPath.
//
// When the tree is already at a commit that the repository has, place
// writes only the files that changed between the two commits, which
// update describes. Otherwise it checks out every entry into nextTree
// and moves the files into tree. When tree does not exist yet, one
// rename moves the checkout into place. When tree exists, a pod can
// have it bind-mounted, and a bind mount follows the directory and not
// its name. Renaming a new directory over tree would leave the pod
// reading the old files, so replaceTree moves the files into the
// existing directory.
//
// place deletes placedFile before it changes the published tree, and
// writes it again after. A driver that stops or fails during the change
// leaves a tree with no recorded commit, so the next place checks out
// every entry again.
func (r *repository) place(ctx context.Context, commit, directory, tree string, entries []string) error {
	if err := r.checkEntries(ctx, commit, entries); err != nil {
		return err
	}
	fresh := filepath.Join(directory, nextTree)
	if err := os.RemoveAll(fresh); err != nil {
		return err
	}
	if changes, found := r.changesSince(ctx, directory, tree, commit, entries); found {
		return r.update(ctx, commit, directory, tree, changes)
	}
	if err := r.checkout(ctx, commit, fresh, entries); err != nil {
		return err
	}
	if err := forgetPlaced(directory); err != nil {
		return err
	}
	if err := installTree(fresh, tree); err != nil {
		return err
	}
	return recordPlaced(directory, commit)
}

func installTree(fresh, tree string) error {
	if _, err := os.Stat(tree); err != nil {
		return os.Rename(fresh, tree)
	}
	if err := replaceTree(fresh, tree); err != nil {
		return err
	}
	// The next call to place removes fresh before it checks out, so a
	// failed removal here is harmless.
	_ = os.RemoveAll(fresh)
	return nil
}

// placedFile, beside a read-only volume's tree, holds the commit that
// the tree was checked out from. The tree has no git directory, so a
// restarted driver reads the commit from this file and reports it
// before the next fetch, and place compares it with the next commit to
// find the files that changed.
const placedFile = "commit"

// recordPlaced writes a temporary file and renames it over placedFile,
// so a driver that stops partway leaves the old commit or the new one,
// never a truncated hash.
func recordPlaced(directory, commit string) error {
	next := filepath.Join(directory, placedFile+".next")
	if err := os.WriteFile(next, []byte(commit+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(next, filepath.Join(directory, placedFile))
}

// forgetPlaced deletes placedFile. A file that does not exist is not an
// error, because a tree that place has not finished has none.
func forgetPlaced(directory string) error {
	err := os.Remove(filepath.Join(directory, placedFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// placedCommit returns an empty string when placedFile does not exist.
func placedCommit(directory string) string {
	content, _ := os.ReadFile(filepath.Join(directory, placedFile))
	return trimLine(string(content))
}

func trimLine(out string) string {
	for len(out) > 0 && (out[len(out)-1] == '\n' || out[len(out)-1] == '\r') {
		out = out[:len(out)-1]
	}
	return out
}

// replaceTree makes published hold exactly the entries of fresh, and
// deletes the entries that fresh does not have. When both have a
// directory of the same name, it recurses into the pair, so the
// directory keeps its inode and a bind mount of it keeps working. It
// moves every other entry with one rename, so a reader sees the old file
// or the new one, never a partial write.
func replaceTree(fresh, published string) error {
	incoming, err := os.ReadDir(fresh)
	if err != nil {
		return err
	}
	existing, err := os.ReadDir(published)
	if err != nil {
		return err
	}

	keep := make(map[string]bool, len(incoming))
	for _, entry := range incoming {
		keep[entry.Name()] = true
	}
	for _, entry := range existing {
		if !keep[entry.Name()] {
			if err := os.RemoveAll(filepath.Join(published, entry.Name())); err != nil {
				return err
			}
		}
	}

	for _, entry := range incoming {
		from := filepath.Join(fresh, entry.Name())
		to := filepath.Join(published, entry.Name())
		there, err := os.Lstat(to)
		switch {
		case err == nil && entry.IsDir() && there.IsDir():
			if err := replaceTree(from, to); err != nil {
				return err
			}
			continue
		case err == nil && entry.IsDir() != there.IsDir():
			if err := os.RemoveAll(to); err != nil {
				return err
			}
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
	}
	return nil
}

// treeSize returns the total size in bytes of the regular files under
// dir.
func treeSize(dir string) (int64, error) {
	var total int64
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}
