package main

// store.go lays out the driver's directories on the node: one bare
// repository per URL under repos/, and one directory per volume under
// volumes/.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// checkout writes the files of the commit into dir. The index file is
// written beside dir and deleted afterward, so a pod sees no git
// metadata in dir.
func (r *repository) checkout(ctx context.Context, commit, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	index := dir + ".index"
	defer os.Remove(index)

	_, err := runGit(ctx, r.dir, []string{"GIT_INDEX_FILE=" + index},
		"--work-tree="+dir, "-c", "core.bare=false",
		"checkout", "--force", commit, "--", ".")
	return err
}

// nextTree is the directory where place checks out a commit before it
// moves the files into the published tree.
const nextTree = "next"

// place checks the commit out into nextTree and moves its files into
// tree. When tree does not exist yet, one rename moves the checkout into
// place. When tree exists, a pod can have it bind-mounted, and a bind
// mount follows the directory and not its name. Renaming a new
// directory over tree would leave the pod reading the old files, so
// replaceTree moves the files into the existing directory.
func (r *repository) place(ctx context.Context, commit, directory, tree string) error {
	fresh := filepath.Join(directory, nextTree)
	if err := os.RemoveAll(fresh); err != nil {
		return err
	}
	if err := r.checkout(ctx, commit, fresh); err != nil {
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
// before the next fetch.
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
