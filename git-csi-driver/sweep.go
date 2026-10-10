package main

// sweep.go walks the store's bare repositories. A bare repository is
// shared by every volume of its URL on the node, so it outlives any one
// volume: it goes when no volume and no work tree names it any more, and
// the refs in it go when no volume follows them. A read-only volume
// leaves the store at every unstage, and nothing reports that to the
// repository it read from, so the walk finds it. reclaim.go removes the
// work trees, and runs this walk after each removal.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// defaultSweepEvery is how often the driver walks the repositories. The
// hour is a clock and not a search for a change: a repository that
// stays an hour after its last volume costs disk and nothing else, and
// the walk runs git gc on each repository that stays.
const defaultSweepEvery = time.Hour

// sweeping walks the store on the interval until the driver
// stops.
func (n *node) sweeping(ctx context.Context) {
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

// sweepStore is one pass: the bare repositories no volume names, then a
// measure of what is left.
func (n *node) sweepStore(ctx context.Context) {
	n.sweepRepositories(ctx)
	n.measureStore(ctx)
}

// measureStore records git_csi_store_bytes on the sweep's own timer. A
// walk of the whole store costs too much to repeat on every scrape, and
// the sweep already walks it once an interval to collect what nothing
// uses.
func (n *node) measureStore(ctx context.Context) {
	size, err := treeSize(n.store.root)
	if err != nil {
		n.logger.WarnContext(ctx, "the store was not measured", "error", err)
		return
	}
	n.readings.setStoreBytes(size)
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

// sweepRepository removes one bare repository under the same lock
// a stage takes, so a repository a stage is fetching into is never
// removed under it. A repository that stays loses the refs no volume
// follows and is collected, under that same lock.
func (n *node) sweepRepository(ctx context.Context, repo *repository) {
	defer repo.lock()()
	if n.usesRepository(repo.dir) {
		n.sweepRefs(ctx, repo)
		n.collect(ctx, repo)
		return
	}
	if err := os.RemoveAll(repo.dir); err != nil {
		n.logger.WarnContext(ctx, "the repository stayed",
			"repository", repo.name, "error", err)
		return
	}
	n.logger.InfoContext(ctx, "swept the repository", "repository", repo.name)
}

// usesRepository asks the two things that name a bare repository:
// a published volume's URL, and the alternates file of a work tree the
// store still holds.
func (n *node) usesRepository(dir string) bool {
	for _, held := range n.held() {
		if n.store.repository(held.attributes.url).dir == dir {
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

// sweepRefs deletes every ref in the driver's own namespace that no
// volume follows. A repository followed at a tag per release would
// otherwise keep every release's history alive for as long as the
// repository stays.
func (n *node) sweepRefs(ctx context.Context, repo *repository) {
	followed := n.followedRefs(ctx, repo.dir)
	for _, ref := range repo.refs(ctx) {
		if followed[ref] {
			continue
		}
		if err := repo.deleteRef(ctx, ref); err != nil {
			// The log says which ref stayed and why, and the pass goes on
			// to the next ref.
			n.logger.WarnContext(ctx, "the ref stayed",
				"repository", repo.name, "ref", ref, "error", err)
			continue
		}
		// The log says which ref went from which repository.
		n.logger.InfoContext(ctx, "swept the ref", "repository", repo.name, "ref", ref)
	}
}

// followedRefs is every ref a volume of this repository follows: the
// volumes the node holds, and the records and work trees the store
// holds for the volumes it does not.
func (n *node) followedRefs(ctx context.Context, dir string) map[string]bool {
	followed := map[string]bool{}
	for _, held := range n.held() {
		if n.store.repository(held.attributes.url).dir == dir {
			followed[refPrefix+held.attributes.ref] = true
		}
	}
	// A store with no volumes directory holds no volume, so nothing
	// follows its refs.
	entries, _ := os.ReadDir(filepath.Join(n.store.root, "volumes"))
	for _, entry := range entries {
		n.followedInStore(ctx, followed, dir, entry.Name())
	}
	return followed
}

// followedInStore adds the ref one volume directory follows. The
// directory can name it twice: in the record the publish wrote, and in
// the HEAD of the work tree that reads this repository's objects. A
// stage that was never published leaves the work tree alone.
func (n *node) followedInStore(ctx context.Context, followed map[string]bool, dir, id string) {
	if held, err := readRecord(filepath.Join(n.store.volumeDir(id), recordFile)); err == nil {
		if parsed, err := parseVolumeContext(held.Attributes); err == nil &&
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

// refs is every ref the repository holds in the driver's own namespace,
// and none where git cannot read the repository.
func (r *repository) refs(ctx context.Context) []string {
	output, err := runGit(ctx, r.dir, nil, "for-each-ref", "--format=%(refname)", refPrefix)
	if err != nil {
		return nil
	}
	// A ref name holds no space, so the fields of the output are the refs.
	return strings.Fields(output.stdout)
}

// deleteRef removes one ref from the repository.
func (r *repository) deleteRef(ctx context.Context, ref string) error {
	_, err := runGit(ctx, r.dir, nil, "update-ref", "-d", "--end-of-options", ref)
	return err
}

// collect packs the repository and prunes the objects no ref names,
// at git's own age for that, gc.pruneExpire, two weeks by default. Each
// fetch into the repository runs git maintenance run --auto, which runs
// the same git gc --auto, so one age applies to both. --auto costs nothing on a pass where nothing changed.
// gc.autoDetach=false keeps the work in the foreground, under the
// repository's lock, and brings a failure back to this log instead of a
// gc.log file in the repository.
func (n *node) collect(ctx context.Context, repo *repository) {
	_, err := runGit(ctx, repo.dir, nil, "-c", "gc.autoDetach=false",
		"gc", "--quiet", "--auto")
	if err != nil {
		// A repository that was not collected is logged, and the pass
		// finishes.
		n.logger.WarnContext(ctx, "the repository was not collected",
			"repository", repo.name, "error", err)
	}
}

// held is every volume this node has published or staged.
func (n *node) held() []*volume {
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
