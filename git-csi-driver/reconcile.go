package main

// reconcile.go brings an existing work tree up to the remote's ref, at
// stage and after a rejected push. A rebase runs in a scratch tree,
// because a rebase in the mounted tree would rewrite files that a pod
// may have open.

import (
	"context"
	"fmt"
	"os"
)

// reconcile brings the tree to upstream at stage. For a diverged
// volume, stageDiverged runs in its place. Otherwise a tree equal to upstream or
// ahead of it stays as it is, a tree behind upstream is
// fast-forwarded, and a tree whose history split from upstream is
// rebased onto it. A tree with uncommitted changes is not rewritten.
func (n *node) reconcile(ctx context.Context, staging *volume, head, upstream, side string) error {
	if branch := staging.work.divergedBranch(ctx); branch != "" {
		return n.stageDiverged(ctx, staging, branch, head, upstream, side)
	}
	switch {
	case head == upstream:
		return nil
	case staging.work.ancestor(ctx, upstream, head):
		return nil
	}
	// A reset or a rebase would overwrite uncommitted changes. An unarmed
	// volume never commits, and an armed volume does not commit a write
	// until the quiesce time has passed. So the tree stays as it is, and
	// the volume reports that upstream moved. A later stage reconciles
	// the tree after the changes are committed.
	if pending, _ := staging.work.pending(ctx); len(pending) > 0 {
		staging.reportUpstreamMoved(fmt.Sprintf(
			"Upstream moved: %s is at %s and the tree has %d uncommitted paths",
			staging.attributes.ref, short(upstream), len(pending)))
		return nil
	}
	if staging.work.ancestor(ctx, head, upstream) {
		return n.fastForward(ctx, staging, upstream)
	}
	return n.rebase(ctx, staging, upstream)
}

func (w *workTree) ancestor(ctx context.Context, commit, descendant string) bool {
	_, err := w.git(ctx, "merge-base", "--is-ancestor", "--end-of-options", commit, descendant)
	return err == nil
}

func (w *workTree) reset(ctx context.Context, commit string) error {
	_, err := w.git(ctx, "reset", "--hard", "--quiet", "--end-of-options", commit)
	return err
}

// fastForward resets a tree that has no commits of its own to upstream,
// and moves pushedRef there too, because the remote has every commit
// that the tree now has.
func (n *node) fastForward(ctx context.Context, staging *volume, upstream string) error {
	if err := staging.work.reset(ctx, upstream); err != nil {
		return err
	}
	if err := staging.work.markPushed(ctx, upstream); err != nil {
		return err
	}
	n.restore(ctx, staging)
	n.logger.InfoContext(ctx, "Fast-forwarded the tree of the volume to upstream",
		"volume", staging.id, "commit", short(upstream))
	return nil
}

// rebase replays the tree's own commits onto upstream. When the rebase
// fails, the tree stays as it was and the volume moves to its side
// branch, because the driver does not resolve conflicts. After a
// rebase, pushedRef is upstream, so every commit above it is unpushed.
func (n *node) rebase(ctx context.Context, staging *volume, upstream string) error {
	if err := n.rebaseInScratchTree(ctx, staging, upstream); err != nil {
		n.logger.WarnContext(ctx, "The rebase of the tree onto upstream did not complete",
			"volume", staging.id, "error", err)
		n.diverge(ctx, staging)
		return nil
	}
	if err := staging.work.markPushed(ctx, upstream); err != nil {
		return err
	}
	n.restore(ctx, staging)
	n.logger.InfoContext(ctx, "Rebased the tree of the volume onto upstream",
		"volume", staging.id, "commit", short(upstream))
	return nil
}

// rebaseInScratchTree rebases the volume's commits onto upstream in a scratch
// tree. Then moveCheckout updates only the paths of the mounted tree that the
// rebase changed.
func (n *node) rebaseInScratchTree(ctx context.Context, vol *volume, upstream string) error {
	head := vol.work.refCommit(ctx, "HEAD")
	dir, cleanup, err := vol.work.addScratchTree(ctx)
	if err != nil {
		return err
	}
	rebased, err := replay(ctx, dir, vol.authorEnv(), upstream)
	cleanup()
	if err != nil {
		return err
	}
	if err := vol.work.moveCheckout(ctx, head, rebased); err != nil {
		return err
	}
	n.restoreChangedMetadata(ctx, vol, vol.work.changedPaths(ctx, head, rebased))
	return nil
}

// restoreChangedMetadata applies the remote's recorded modes, owners,
// and empty directories to the changed paths. A failure is logged and
// does not fail the rebase.
func (n *node) restoreChangedMetadata(ctx context.Context, vol *volume, changed []string) {
	if err := vol.work.replayMetadataFor(ctx, n.logger, os.Geteuid() == 0, changed); err != nil {
		n.logger.InfoContext(ctx, "Did not apply the metadata record of the remote to the changed paths",
			"volume", vol.id, "error", err)
	}
}

// replay rebases the scratch tree onto upstream and returns the new
// HEAD. When git cannot resolve HEAD, replay returns an empty string,
// and moveCheckout then fails.
func replay(ctx context.Context, dir string, author []string, upstream string) (string, error) {
	if _, err := runGit(ctx, dir, author,
		"rebase", "--quiet", "--end-of-options", upstream); err != nil {
		// The abort fails when the rebase never started, which is fine.
		_, _ = runGit(ctx, dir, nil, "rebase", "--abort")
		return "", err
	}
	output, _ := runGit(ctx, dir, nil, "rev-parse", "--verify", "--end-of-options", "HEAD")
	return trimLine(output.stdout), nil
}
