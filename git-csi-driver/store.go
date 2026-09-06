package main

// store.go holds the driver's directories on the node: one bare
// repository per URL under repos/, and one published tree per volume
// under volumes/.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// refPrefix is where a store repository keeps the refs it follows. The
// driver's own namespace keeps them apart from anything the remote
// names, and lets one bare repository serve volumes on different refs.
const refPrefix = "refs/git-csi/"

// store is the root directory and one lock per repository, made on
// first use.
type store struct {
	root string

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newStore(root string) *store {
	return &store{root: root, locks: map[string]*sync.Mutex{}}
}

// repositoryURLFile is the file a bare repository carries beside its
// objects, so a person and the sweep both read the remote without hashing anything.
const repositoryURLFile = "url"

// repository is one bare repository, shared by every volume of the same
// URL on this node.
type repository struct {
	store *store
	url   string
	name  string
	dir   string
}

// repository names the directory by the sha256 of the URL, because a URL
// is not a file name. create writes the URL beside it for a reader.
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

// volumeDir is where a volume keeps its published tree and, around each
// git invocation, its credential files.
func (s *store) volumeDir(id string) string {
	return filepath.Join(s.root, "volumes", id)
}

// lock takes this repository's lock and returns the release. A fetch
// and a publish of the same URL never run at once.
func (r *repository) lock() func() {
	r.store.mu.Lock()
	held, found := r.store.locks[r.name]
	if !found {
		held = &sync.Mutex{}
		r.store.locks[r.name] = held
	}
	r.store.mu.Unlock()

	held.Lock()
	return held.Unlock
}

// exists reports whether the store already holds this repository.
func (r *repository) exists() bool {
	_, err := os.Stat(filepath.Join(r.dir, "HEAD"))
	return err == nil
}

// create makes the bare repository and writes the URL beside it, so a
// person can read the store without hashing anything.
func (r *repository) create(ctx context.Context) error {
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return err
	}
	if _, err := runGit(ctx, r.dir, nil, "init", "--quiet", "--bare"); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.dir, repositoryURLFile), []byte(r.url+"\n"), 0o644)
}

// fetch moves the driver's own ref to what the remote holds now. depth
// applies only when the caller asks, which is the first fetch of a new
// repository.
func (r *repository) fetch(ctx context.Context, env []string, ref string, depth int) error {
	args := []string{"fetch", "--quiet", "--no-tags"}
	if depth > 0 {
		args = append(args, "--depth="+strconv.Itoa(depth))
	}
	args = append(args, r.url, "+"+ref+":"+refPrefix+ref)
	_, err := runGit(ctx, r.dir, env, args...)
	return err
}

// resolve is the commit the driver's own ref names, or an error when
// the store holds no copy of the ref.
func (r *repository) resolve(ctx context.Context, ref string) (string, error) {
	output, err := runGit(ctx, r.dir, nil, "rev-parse", "--verify", "--end-of-options",
		refPrefix+ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return trimLine(output.stdout), nil
}

// checkout writes the commit into dir. The index file lives outside the
// tree and is removed after, so the tree holds only what the commit
// holds and no pod ever sees a git file.
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

// nextTree is the checkout a placement makes beside the published tree.
const nextTree = "next"

// place puts the commit in the published tree. A tree that is not there
// yet arrives whole, with one rename. A tree a pod already reads is
// replaced entry by entry, because a bind mount follows the directory
// the driver bound and not its name: a renamed directory leaves every
// reader on the old tree. So the new tree arrives inside the directory
// the pod holds, and each file appears in one rename.
func (r *repository) place(ctx context.Context, commit, directory, tree string) error {
	fresh := filepath.Join(directory, nextTree)
	if err := os.RemoveAll(fresh); err != nil {
		return err
	}
	if err := r.checkout(ctx, commit, fresh); err != nil {
		return err
	}
	if _, err := os.Stat(tree); err != nil {
		return os.Rename(fresh, tree)
	}
	if err := replaceTree(fresh, tree); err != nil {
		return err
	}
	// The pod already reads the new tree, and the next placement removes
	// this checkout again, so a removal that fails is not a failure of
	// the placement.
	_ = os.RemoveAll(fresh)
	return nil
}

// trimLine removes the newline from one line of git output.
func trimLine(out string) string {
	for len(out) > 0 && (out[len(out)-1] == '\n' || out[len(out)-1] == '\r') {
		out = out[:len(out)-1]
	}
	return out
}

// replaceTree makes published hold exactly what fresh holds. Entries
// upstream removed go first. A directory present on both sides is
// recursed into, so its inode and the pod's view of it stay. Every other
// entry moves with one rename, so a reader sees the old file or the
// new one and never a partial write.
func replaceTree(fresh, published string) error {
	arriving, err := os.ReadDir(fresh)
	if err != nil {
		return err
	}
	standing, err := os.ReadDir(published)
	if err != nil {
		return err
	}

	keep := make(map[string]bool, len(arriving))
	for _, entry := range arriving {
		keep[entry.Name()] = true
	}
	for _, entry := range standing {
		if !keep[entry.Name()] {
			if err := os.RemoveAll(filepath.Join(published, entry.Name())); err != nil {
				return err
			}
		}
	}

	for _, entry := range arriving {
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

// treeSize is the bytes the tree's regular files hold, which
// NodeGetVolumeStats reports as used.
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
