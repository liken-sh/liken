package main

// sweep.go removes what nothing stages any more. The node plugin
// never learns that a PersistentVolume was deleted, so age is the only
// evidence the store has that a work tree is finished with.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// defaultSweepAfter is how long a work tree nothing stages is kept.
// defaultSweepEvery is how often the driver looks.
const (
	defaultSweepAfter = 720 * time.Hour
	defaultSweepEvery = time.Hour
)

// unstagedFile is the file the unstage writes in the volume's directory, which is
// the whole record of when a node last held the volume.
const unstagedFile = "unstaged"

// abandonedTree is a work tree the sweep kept because it holds
// commits the remote does not, named in the report of the next volume
// that stages the same repository.
type abandonedTree struct {
	id       string
	unstaged time.Time
}

// markUnstaged records the moment the kubelet took the volume off
// this node.
func (n *node) markUnstaged(ctx context.Context, held *volume) {
	content := time.Now().UTC().Format(time.RFC3339) + "\n"
	path := filepath.Join(held.directory, unstagedFile)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		n.logger.WarnContext(ctx, "the unstage time was not written",
			"volume", held.id, "error", err)
	}
}

// unstagedAt reads that record, and answers false for a volume
// that no unstage has ever left one for.
func unstagedAt(directory string) (time.Time, bool) {
	content, err := os.ReadFile(filepath.Join(directory, unstagedFile))
	if err != nil {
		return time.Time{}, false
	}
	when, err := time.Parse(time.RFC3339, trimLine(string(content)))
	if err != nil {
		return time.Time{}, false
	}
	return when, true
}

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

// sweepStore is one pass: the work trees first, then the bare
// repositories the trees that stayed no longer name.
func (n *node) sweepStore(ctx context.Context) {
	n.sweepVolumes(ctx)
	n.sweepRepositories(ctx)
}

func (n *node) sweepVolumes(ctx context.Context) {
	entries, err := os.ReadDir(filepath.Join(n.store.root, "volumes"))
	if err != nil {
		return
	}
	for _, entry := range entries {
		n.sweepVolume(ctx, entry.Name())
	}
}

// sweepVolume removes one work tree the node does not hold, whose
// last unstage is older than the age, and whose every commit the remote
// holds. A tree with commits nothing pushed is kept and named instead.
func (n *node) sweepVolume(ctx context.Context, id string) {
	if n.holds(id) {
		return
	}
	work := n.store.tree(id)
	if !work.exists() {
		return
	}
	when, found := unstagedAt(work.directory)
	if !found || time.Since(when) <= n.sweepAfter {
		return
	}
	head := work.refCommit(ctx, "HEAD")
	if head == "" {
		return
	}
	if work.refCommit(ctx, pushedRef) != head || work.divergedBranch(ctx) != "" {
		n.abandon(ctx, work, id, when)
		return
	}
	if err := os.RemoveAll(work.directory); err != nil {
		n.logger.WarnContext(ctx, "the work tree stayed", "volume", id, "error", err)
		return
	}
	n.logger.InfoContext(ctx, "swept the work tree", "volume", id, "unstaged", when)
	n.swept(ctx, id, when)
}

// holds reports the volumes this node has staged or published,
// which the sweep never touches.
func (n *node) holds(id string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	_, staged := n.staged[id]
	_, published := n.volumes[id]
	return staged || published
}

// abandon records the tree by the repository it follows, because
// the volume that reports it is the next one to stage that repository.
func (n *node) abandon(ctx context.Context, work *workTree, id string, when time.Time) {
	url := work.originURL()
	n.logger.InfoContext(ctx, "the work tree holds unpushed commits",
		"volume", id, "url", url, "unstaged", when)
	if url == "" {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.abandoned[url] = abandonedTree{id: id, unstaged: when}
}

// noteAbandoned puts the age of another volume's work tree in
// this volume's report, which is where a person learns that work stays
// on this node with no claim to reach it.
func (n *node) noteAbandoned(staging *volume) {
	n.mu.Lock()
	one, found := n.abandoned[staging.attributes.url]
	n.mu.Unlock()
	if !found || one.id == staging.id {
		return
	}
	staging.reportAbandoned(fmt.Sprintf(
		"the work tree of %s holds unpushed commits and was unstaged %s ago",
		one.id, age(one.unstaged)))
}

// swept posts the Event where a claim is known, which is where
// the PersistentVolume outlived the work tree instead of the other way
// around.
func (n *node) swept(ctx context.Context, id string, when time.Time) {
	if n.arms.client == nil {
		return
	}
	claim, err := n.arms.claimOf(ctx, id)
	if err != nil {
		n.logger.InfoContext(ctx, "the swept volume names no claim",
			"volume", id, "reason", err)
		return
	}
	n.events.postClaim(ctx, claim, corev1.EventTypeNormal, reasonSwept,
		fmt.Sprintf("swept: the work tree was unstaged %s ago and held nothing unpushed",
			age(when)))
}

// age is the whole hours since the moment, which is the number a
// volume's report and an Event carry.
func age(when time.Time) string {
	return fmt.Sprintf("%dh", int(time.Since(when).Hours()))
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

// collect packs the repository and prunes the objects no ref has named
// since the sweep age. --auto costs nothing on a pass where nothing
// changed. gc.autoDetach=false keeps the work in the foreground, under
// the repository's lock, and brings a failure back to this log instead
// of a gc.log file in the repository.
func (n *node) collect(ctx context.Context, repo *repository) {
	_, err := runGit(ctx, repo.dir, nil, "-c", "gc.autoDetach=false",
		"gc", "--quiet", "--auto", "--prune="+pruneDate(n.sweepAfter))
	if err != nil {
		// A repository that was not collected is logged, and the pass
		// finishes.
		n.logger.WarnContext(ctx, "the repository was not collected",
			"repository", repo.name, "error", err)
	}
}

// pruneDate is the sweep age before now, written as a timestamp. Git's
// own age forms are unsafe here: it reads "720h" as this moment, and a
// count of seconds above 99999999 as a second of the epoch. A date says
// the age the driver means for every value of --sweep-after.
func pruneDate(after time.Duration) string {
	return time.Now().Add(-after).UTC().Format(time.RFC3339)
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
