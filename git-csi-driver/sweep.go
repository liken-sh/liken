package main

// sweep.go deletes the bare repositories and the refs that no volume
// uses. Volumes of the same URL share one bare repository, so removing a
// volume leaves its repository in place. The sweep deletes a repository
// when no volume and no work tree refers to it, and in each repository
// it keeps, deletes the refs that no volume follows.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// defaultSweepEvery is the interval between sweeps. reclaim sweeps as
// soon as it removes a work tree, but the removal of a read-only volume
// runs no sweep, so its unused repository and refs take disk space until
// the next sweep. Each sweep also runs `git gc --auto` and measures the
// store.
const defaultSweepEvery = time.Hour

func (n *node) sweepPeriodically(ctx context.Context) {
	ticker := time.NewTicker(n.sweepEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.sweepStore(ctx)
		}
	}
}

func (n *node) sweepStore(ctx context.Context) {
	n.sweepRepositories(ctx)
	n.measureStore(ctx)
}

// measureStore sets the git_csi_store_bytes metric. A walk of the whole
// store is too slow for each Prometheus scrape, so the sweep does it.
func (n *node) measureStore(ctx context.Context) {
	size, err := treeSize(n.store.root)
	if err != nil {
		n.logger.WarnContext(ctx, "The size of the store was not measured", "error", err)
		return
	}
	n.metrics.setStoreBytes(size)
}

func (n *node) sweepRepositories(ctx context.Context) {
	root := filepath.Join(n.store.root, "repos")
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	keepRefs := n.unreadableRecords(ctx)
	for _, entry := range entries {
		n.sweepRepository(ctx, &repository{
			store: n.store,
			name:  entry.Name(),
			dir:   filepath.Join(root, entry.Name()),
		}, keepRefs)
	}
}

// unreadableRecords reports whether a record in the store cannot be read
// or parsed, and logs each such record once for the sweep. A record
// names its repository by URL, so the sweep cannot tell which repository
// an unreadable record follows a ref of, and keeps the refs of every
// repository until the record can be read.
func (n *node) unreadableRecords(ctx context.Context) bool {
	ids, err := n.store.volumeIDs()
	if err != nil {
		// usesRepository reports the volumes directory it cannot read.
		return false
	}
	found := false
	for _, id := range ids {
		_, err := readRecord(filepath.Join(n.store.volumeDir(id), recordFile))
		if err == nil || absent(err) {
			continue
		}
		n.logger.WarnContext(ctx, "The refs of every repository are kept until the record of the volume can be read",
			"volume", id, "error", err)
		found = true
	}
	return found
}

// sweepRepository holds the repository's lock throughout, so it cannot
// delete a repository while a stage or a fetch loop writes it.
// keepRefs skips the deletion of refs, because a record that the sweep
// cannot read can follow any ref.
func (n *node) sweepRepository(ctx context.Context, repo *repository, keepRefs bool) {
	defer repo.lock()()
	used, err := n.usesRepository(repo.dir)
	if err != nil {
		// A sweep that cannot read the store cannot tell whether a work
		// tree reads its objects from the repository, and a work tree
		// whose repository is deleted loses its history. The next sweep
		// tries again.
		n.logger.WarnContext(ctx, "The repository was not swept, because the store was not read",
			"repository", repo.name, "error", err)
		return
	}
	if used {
		if !keepRefs {
			n.sweepRefs(ctx, repo)
		}
		n.collect(ctx, repo)
		return
	}
	if err := os.RemoveAll(repo.dir); err != nil {
		n.logger.WarnContext(ctx, "The repository directory was not removed",
			"repository", repo.name, "error", err)
		return
	}
	n.logger.InfoContext(ctx, "Removed the repository that no volume uses", "repository", repo.name)
}

// usesRepository reports whether a volume in memory has the URL of the
// repository at dir, or the alternates file of a work tree in the store
// points to it. A work tree on disk counts even when the driver holds
// no volume for it. It returns an error when it cannot read the store,
// because then it cannot tell.
func (n *node) usesRepository(dir string) (bool, error) {
	for _, vol := range n.allVolumes() {
		if n.store.repository(vol.attributes.url).dir == dir {
			return true, nil
		}
	}
	ids, err := n.store.volumeIDs()
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		alternate, err := n.store.tree(id).alternate()
		if err != nil {
			return false, err
		}
		if alternate == dir {
			return true, nil
		}
	}
	return false, nil
}

// volumeIDs returns the names of the volume directories in the store.
// A missing volumes directory means there are no volumes.
func (s *store) volumeIDs() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, "volumes"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.Name())
	}
	return ids, nil
}

// sweepRefs deletes every ref under refPrefix that no volume follows.
// Without it, a workload whose volume names a new tag for each release
// would leave every old tag in the repository, and git gc would keep the
// history of each one.
func (n *node) sweepRefs(ctx context.Context, repo *repository) {
	followed, err := n.followedRefs(ctx, repo.dir)
	if err != nil {
		n.logger.WarnContext(ctx, "The refs of the repository were not swept, because the store was not read",
			"repository", repo.name, "error", err)
		return
	}
	for _, ref := range repo.refs(ctx) {
		if followed[ref] {
			continue
		}
		if err := repo.deleteRef(ctx, ref); err != nil {
			n.logger.WarnContext(ctx, "The ref was not deleted from the repository",
				"repository", repo.name, "ref", ref, "error", err)
			continue
		}
		n.logger.InfoContext(ctx, "Deleted the ref that no volume follows", "repository", repo.name, "ref", ref)
	}
}

// followedRefs returns the refs under refPrefix that a volume of the
// repository at dir follows. It reads the volumes in memory, and the
// records and work trees on disk, which also cover volumes the driver
// does not hold. It returns an error when it cannot read the store.
func (n *node) followedRefs(ctx context.Context, dir string) (map[string]bool, error) {
	followed := map[string]bool{}
	for _, vol := range n.allVolumes() {
		if n.store.repository(vol.attributes.url).dir == dir {
			followed[refPrefix+vol.attributes.ref] = true
		}
	}
	ids, err := n.store.volumeIDs()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if err := n.followedInStore(ctx, followed, dir, id); err != nil {
			return nil, err
		}
	}
	return followed, nil
}

// followedInStore adds the ref that the volume directory id follows,
// from its record and from the HEAD of a work tree whose alternates file
// points to dir. Both are read because a volume that was staged and
// never published has a work tree and no record.
func (n *node) followedInStore(ctx context.Context, followed map[string]bool, dir, id string) error {
	rec, err := readRecord(filepath.Join(n.store.volumeDir(id), recordFile))
	switch {
	case err == nil:
		if parsed, err := parseVolumeContext(rec.Attributes); err == nil &&
			n.store.repository(parsed.url).dir == dir {
			followed[refPrefix+parsed.ref] = true
		}
	case !absent(err):
		// A record that cannot be read or parsed can name any ref.
		return err
	}
	work := n.store.tree(id)
	alternate, err := work.alternate()
	if err != nil {
		return err
	}
	if alternate != dir {
		return nil
	}
	ref, err := work.followedRef(ctx)
	if err != nil {
		return err
	}
	if ref != "" {
		followed[refPrefix+ref] = true
	}
	return nil
}

// refs returns nil when git cannot read the repository.
func (r *repository) refs(ctx context.Context) []string {
	output, err := runGit(ctx, r.dir, nil, "for-each-ref", "--format=%(refname)", refPrefix)
	if err != nil {
		return nil
	}
	// git does not allow whitespace in a ref name.
	return strings.Fields(output.stdout)
}

func (r *repository) deleteRef(ctx context.Context, ref string) error {
	_, err := runGit(ctx, r.dir, nil, "update-ref", "-d", "--end-of-options", ref)
	return err
}

// collect runs `git gc --auto`, which packs the repository and prunes
// the objects that no ref reaches and that are older than gc.pruneExpire,
// two weeks by default. git does nothing when the repository needs no
// work. A fetch already runs `git maintenance run --auto`, which runs the
// same gc with the same prune age. gc.autoDetach=false keeps gc in the
// foreground under the repository's lock, and returns a failure here in
// place of a gc.log file in the repository.
func (n *node) collect(ctx context.Context, repo *repository) {
	_, err := runGit(ctx, repo.dir, nil, "-c", "gc.autoDetach=false",
		"gc", "--quiet", "--auto")
	if err != nil {
		// The next sweep tries again.
		n.logger.WarnContext(ctx, "git gc did not finish on the repository",
			"repository", repo.name, "error", err)
	}
}

// allVolumes returns every volume in the node's published and staged sets. A
// volume in both sets is returned twice.
func (n *node) allVolumes() []*volume {
	n.mu.Lock()
	defer n.mu.Unlock()
	found := make([]*volume, 0, len(n.volumes)+len(n.staged))
	for _, one := range n.volumes {
		found = append(found, one)
	}
	for _, one := range n.staged {
		found = append(found, one)
	}
	return found
}
