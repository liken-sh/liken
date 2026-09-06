package main

// reconcile.go brings a work tree the node already holds to the
// ref the remote holds now. Stage is the one moment no pod writes the
// tree, so it is the one moment the tree may change under the driver.

import (
	"context"
	"fmt"
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
	// A reset or a rebase rewrites the tree, and the tree may hold writes
	// no commit carries yet: an unarmed volume's whole work, or an armed
	// volume's last writes before the quiesce fired. The driver loses
	// nothing, so such a tree is left as it is and the condition says
	// upstream moved. The next stage after a commit reconciles it.
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
	if _, err := staging.work.gitWith(ctx, staging.authorEnv(),
		"rebase", "--quiet", "--end-of-options", upstream); err != nil {
		// A rebase that never started leaves nothing to abort,
		// and the volume diverges either way.
		_, _ = staging.work.git(ctx, "rebase", "--abort")
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
