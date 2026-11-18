package main

// sweep.go deletes the bare repositories and the refs that no volume
// uses. Volumes of the same URL share one bare repository, so removing a
// volume leaves its repository in place. The sweep deletes a repository
// when no volume and no work tree refers to it, and in each repository
// it keeps, deletes the refs that no volume follows.

import (
	"context"
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
	for _, entry := range entries {
		n.sweepRepository(ctx, &repository{
			store: n.store,
			name:  entry.Name(),
			dir:   filepath.Join(root, entry.Name()),
		})
	}
}

// sweepRepository holds the repository's lock throughout, so it cannot
// delete a repository while a stage or a fetch loop writes it.
func (n *node) sweepRepository(ctx context.Context, repo *repository) {
	defer repo.lock()()
	if n.usesRepository(repo.dir) {
		n.sweepRefs(ctx, repo)
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
// no volume for it.
func (n *node) usesRepository(dir string) bool {
	for _, vol := range n.allVolumes() {
		if n.store.repository(vol.attributes.url).dir == dir {
			return true
		}
	}
	entries, err := os.ReadDir(filepath.Join(n.store.root, "volumes"))
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if n.store.tree(entry.Name()).alternate() == dir {
			return true
		}
	}
	return false
}

// sweepRefs deletes every ref under refPrefix that no volume follows.
// Without it, a workload whose volume names a new tag for each release
// would leave every old tag in the repository, and git gc would keep the
// history of each one.
func (n *node) sweepRefs(ctx context.Context, repo *repository) {
	followed := n.followedRefs(ctx, repo.dir)
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
// does not hold.
func (n *node) followedRefs(ctx context.Context, dir string) map[string]bool {
	followed := map[string]bool{}
	for _, vol := range n.allVolumes() {
		if n.store.repository(vol.attributes.url).dir == dir {
			followed[refPrefix+vol.attributes.ref] = true
		}
	}
	// A missing volumes directory means there are no volumes.
	entries, _ := os.ReadDir(filepath.Join(n.store.root, "volumes"))
	for _, entry := range entries {
		n.followedInStore(ctx, followed, dir, entry.Name())
	}
	return followed
}

// followedInStore adds the ref that the volume directory id follows,
// from its record and from the HEAD of a work tree whose alternates file
// points to dir. Both are read because a volume that was staged and
// never published has a work tree and no record.
func (n *node) followedInStore(ctx context.Context, followed map[string]bool, dir, id string) {
	if rec, err := readRecord(filepath.Join(n.store.volumeDir(id), recordFile)); err == nil {
		if parsed, err := parseVolumeContext(rec.Attributes); err == nil &&
			n.store.repository(parsed.url).dir == dir {
			followed[refPrefix+parsed.ref] = true
		}
	}
	work := n.store.tree(id)
	if work.alternate() != dir {
		return
	}
	if ref := work.followedRef(ctx); ref != "" {
		followed[refPrefix+ref] = true
	}
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
