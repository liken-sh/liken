package main

// reconcile.go brings a work tree the node already holds to the
// ref the remote holds now, at stage and after a rejected push. The
// rebase runs in a scratch tree in both cases, because a pod may hold
// the tree, and a rebase in the pod's tree would rewrite its files.

import (
	"context"
	"fmt"
	"os"
)

// reconcile is the reconcile table: a tree equal to upstream or ahead
// of it is left alone, a tree behind it takes upstream, and a tree that
// moved beside it is rebased onto it. A tree with uncommitted writes is
// never rewritten.
func (n *node) reconcile(ctx context.Context, staging *volume, head, upstream, side string) error {
	if branch := staging.work.divergedBranch(ctx); branch != "" {
		return n.healOrHold(ctx, staging, branch, head, upstream, side)
	}
	switch {
	case head == upstream:
		return nil
	case staging.work.ancestor(ctx, upstream, head):
		return nil
	}
	// A reset or a rebase rewrites the tree, and the tree may hold
	// writes no commit carries yet: an unarmed volume's whole work, or an
	// armed volume's last writes before the quiesce fired. The driver
	// loses nothing, so such a tree is left as it is and the volume
	// reports that upstream moved. The next stage after a commit
	// reconciles it.
	if pending, _ := staging.work.pending(ctx); len(pending) > 0 {
		staging.reportTrouble(fmt.Sprintf(
			"upstream moved: %s is at %s and the tree holds %d uncommitted paths",
			staging.attributes.ref, short(upstream), len(pending)))
		return nil
	}
	if staging.work.ancestor(ctx, head, upstream) {
		return n.fastForward(ctx, staging, upstream)
	}
	return n.rebase(ctx, staging, upstream)
}

// ancestor reports whether the first commit is in the second
// one's history, which is how git itself answers behind and ahead.
func (w *workTree) ancestor(ctx context.Context, first, second string) bool {
	_, err := w.git(ctx, "merge-base", "--is-ancestor", "--end-of-options", first, second)
	return err == nil
}

// reset moves the branch, the index, and the tree to the commit
// in one call.
func (w *workTree) reset(ctx context.Context, commit string) error {
	_, err := w.git(ctx, "reset", "--hard", "--quiet", "--end-of-options", commit)
	return err
}

// fastForward is what a tree with no commits of its own does with
// an upstream that moved, and the mark moves with it because the remote
// holds everything the tree now holds.
func (n *node) fastForward(ctx context.Context, staging *volume, upstream string) error {
	if err := staging.work.reset(ctx, upstream); err != nil {
		return err
	}
	if err := staging.work.markPushed(ctx, upstream); err != nil {
		return err
	}
	n.restore(ctx, staging)
	n.logger.InfoContext(ctx, "the tree took upstream",
		"volume", staging.id, "commit", short(upstream))
	return nil
}

// rebase puts the tree's own commits on top of upstream, and a
// rebase that conflicts is aborted with the tree as it was, because the
// driver merges nothing. The mark is upstream after a rebase: every
// commit above it is new, and the remote holds none of them.
func (n *node) rebase(ctx context.Context, staging *volume, upstream string) error {
	if err := n.rebaseAside(ctx, staging, upstream); err != nil {
		n.logger.WarnContext(ctx, "the rebase was aborted",
			"volume", staging.id, "error", err)
		n.diverge(ctx, staging)
		return nil
	}
	if err := staging.work.markPushed(ctx, upstream); err != nil {
		return err
	}
	n.restore(ctx, staging)
	n.logger.InfoContext(ctx, "the tree was rebased",
		"volume", staging.id, "commit", short(upstream))
	return nil
}

// rebaseAside replays the volume's commits onto upstream in a
// scratch tree and moves the mounted tree to the result in one step,
// so a pod never sees its own files rewritten.
func (n *node) rebaseAside(ctx context.Context, held *volume, upstream string) error {
	head := held.work.refCommit(ctx, "HEAD")
	dir, cleanup, err := held.work.scratch(ctx)
	if err != nil {
		return err
	}
	rebased, err := replay(ctx, dir, held.authorEnv(), upstream)
	cleanup()
	if err != nil {
		return err
	}
	if err := held.work.take(ctx, head, rebased); err != nil {
		return err
	}
	n.replayTaken(ctx, held, held.work.changedPaths(ctx, head, rebased))
	return nil
}

// replayTaken gives the paths the update rewrote the modes, the
// owners, and the empty directories the remote's record names for
// them, and touches no other path, so the modes the pod set on its
// own files stand.
func (n *node) replayTaken(ctx context.Context, held *volume, changed []string) {
	if err := held.work.replayMetadataFor(ctx, n.logger, os.Geteuid() == 0, changed); err != nil {
		n.logger.InfoContext(ctx, "the remote holds no metadata",
			"volume", held.id, "reason", err)
	}
}

// replay runs the rebase in the scratch tree and answers the commit
// it landed on. A commit git cannot read is the empty string, which
// take refuses like any other commit it cannot resolve.
func replay(ctx context.Context, dir string, author []string, upstream string) (string, error) {
	if _, err := runGit(ctx, dir, author,
		"rebase", "--quiet", "--end-of-options", upstream); err != nil {
		// A rebase that never started leaves nothing to abort, and
		// the caller falls back either way.
		_, _ = runGit(ctx, dir, nil, "rebase", "--abort")
		return "", err
	}
	output, _ := runGit(ctx, dir, nil, "rev-parse", "--verify", "--end-of-options", "HEAD")
	return trimLine(output.stdout), nil
}
