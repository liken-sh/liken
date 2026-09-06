package main

// diverged.go holds the side branch: the state a volume takes when
// a rebase cannot put its work on the ref, where its pushes go while
// it holds that state, and what ends it.

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// divergedKey is the key the volume's own git directory carries, so a
// driver that restarted and a stage on the same node both read the state the
// last push or the last stage wrote.
const divergedKey = "git-csi.diverged"

// rejectionWords are how git states a rejected push. A rejection is
// the one push failure the driver answers with a rebase, and with a
// branch of its own when the rebase cannot land.
var rejectionWords = []string{"rejected", "non-fast-forward", "fetch first"}

// sideBranch names the ref and the volume, so two volumes of one
// ref on one remote never take the same branch.
func sideBranch(ref, id string) string {
	return ref + "." + id
}

// rejectedPush reads git's whole stderr, because the last line of
// a rejected push is a hint and the rejection is above it.
func rejectedPush(output gitOutput) bool {
	for _, word := range rejectionWords {
		if strings.Contains(output.stderr, word) {
			return true
		}
	}
	return false
}

// divergedBranch is the side branch the git directory records,
// and the empty string where it records none.
func (w *workTree) divergedBranch(ctx context.Context) string {
	output, err := w.git(ctx, "config", "--get", "--", divergedKey)
	if err != nil {
		return ""
	}
	return trimLine(output.stdout)
}

// markDiverged writes the side branch where the next stage reads
// it.
func (w *workTree) markDiverged(ctx context.Context, branch string) error {
	_, err := w.git(ctx, "config", "--local", "--", divergedKey, branch)
	return err
}

// clearDiverged is what a heal writes, so the next push goes to
// the ref again.
func (w *workTree) clearDiverged(ctx context.Context) error {
	_, err := w.git(ctx, "config", "--unset", "--", divergedKey)
	return err
}

// deleteBranch removes the branch from the remote, which is the
// push git makes from an empty source.
func (w *workTree) deleteBranch(ctx context.Context, env []string, url, branch string) error {
	_, err := w.gitWith(ctx, env, "push", "--quiet", url, ":refs/heads/"+branch)
	return err
}

// diverge moves the volume to its side branch, after a rebase could
// not put its work on the ref, and reports it in every place a person
// reads. A state it cannot write is still the state in force for this
// run.
func (n *node) diverge(ctx context.Context, held *volume) {
	branch := sideBranch(held.attributes.ref, held.id)
	if err := held.work.markDiverged(ctx, branch); err != nil {
		n.logger.WarnContext(ctx, "the diverged state was not written",
			"volume", held.id, "error", err)
	}
	held.reportDiverged(branch)
	claim := n.claimFor(ctx, held)
	n.report(ctx, held, claim, corev1.EventTypeWarning, reasonDiverged,
		fmt.Sprintf("diverged: every push goes to %s, not %s", branch, held.attributes.ref))
	n.logger.WarnContext(ctx, "diverged", "volume", held.id, "branch", branch)
	n.readings.record(held)
	n.noteHealth(ctx, held)
}

// healOrHold answers the one question a stage asks a diverged
// volume, which is whether upstream now holds the work.
func (n *node) healOrHold(
	ctx context.Context, staging *volume, branch, head, upstream, side string,
) error {
	if !staging.work.ancestor(ctx, head, upstream) {
		staging.reportDiverged(branch)
		n.readings.record(staging)
		n.noteHealth(ctx, staging)
		return nil
	}
	return n.heal(ctx, staging, branch, upstream, side)
}

// heal takes upstream, deletes the side branch the remote still
// holds, and clears the state, which is the whole end of a divergence.
func (n *node) heal(ctx context.Context, staging *volume, branch, upstream, side string) error {
	if err := staging.work.reset(ctx, upstream); err != nil {
		return err
	}
	if err := staging.work.markPushed(ctx, upstream); err != nil {
		return err
	}
	n.restore(ctx, staging)
	if side != "" {
		n.removeSide(ctx, staging, branch)
	}
	return n.healed(ctx, staging, branch)
}

// healIfMerged fetches the ref before a diverged volume pushes, and
// ends the divergence when upstream holds the side branch's work,
// which is a person's merge. The pod keeps running through it, so
// a merge no longer waits for the next restart.
func (n *node) healIfMerged(ctx context.Context, held *volume, branch string) bool {
	upstream, err := n.fetchUpstream(ctx, held)
	if err != nil {
		n.logger.WarnContext(ctx, "the ref was not fetched",
			"volume", held.id, "error", err)
		return false
	}
	// The pushed mark is the last commit the side branch took.
	// Upstream holds it after the person's merge and at no other time.
	if !held.work.ancestor(ctx, pushedRef, upstream) {
		return false
	}
	if err := n.healInTree(ctx, held, branch, upstream); err != nil {
		n.logger.WarnContext(ctx, "the volume was not healed",
			"volume", held.id, "error", err)
		return false
	}
	return true
}

// healInTree ends a divergence while a pod holds the tree. The
// scratch rebase moves the tree, and not a reset, because a reset
// rewrites every file under the pod. The pushed mark moves with the
// push that follows.
func (n *node) healInTree(ctx context.Context, held *volume, branch, upstream string) error {
	if err := n.rebaseAside(ctx, held, upstream); err != nil {
		return err
	}
	n.removeSide(ctx, held, branch)
	return n.healed(ctx, held, branch)
}

// removeSide deletes the side branch on the remote. A branch that
// stays there is logged and does not stop the heal, because the tree
// is back on its ref either way.
func (n *node) removeSide(ctx context.Context, held *volume, branch string) {
	if err := n.deleteSide(ctx, held, branch); err != nil {
		n.logger.WarnContext(ctx, "the side branch stayed on the remote",
			"volume", held.id, "branch", branch, "error", err)
	}
}

// healed clears the diverged state and reports the end of the
// divergence in every place a person reads.
func (n *node) healed(ctx context.Context, held *volume, branch string) error {
	if err := held.work.clearDiverged(ctx); err != nil {
		return err
	}
	held.reportHealed()
	claim := n.claimFor(ctx, held)
	n.report(ctx, held, claim, corev1.EventTypeNormal, reasonHealed,
		fmt.Sprintf("healed: the tree is back on %s and %s is gone",
			held.attributes.ref, branch))
	n.logger.InfoContext(ctx, "healed", "volume", held.id, "branch", branch)
	n.readings.record(held)
	n.noteHealth(ctx, held)
	return nil
}

// claimFor is the claim the volume already names, and otherwise
// the one the cluster names for its handle, because a stage diverges
// before the loop that reads the claim has run.
func (n *node) claimFor(ctx context.Context, held *volume) claimReference {
	claim, _, _ := held.reading()
	if claim.name != "" || n.arms.client == nil {
		return claim
	}
	found, err := n.arms.claimOf(ctx, held.id)
	if err != nil {
		n.logger.InfoContext(ctx, "the volume names no claim",
			"volume", held.id, "reason", err)
		return claim
	}
	return found
}

// deleteSide sends the deletion in a credential window of its
// own, the way every other call to the remote does.
func (n *node) deleteSide(ctx context.Context, held *volume, branch string) error {
	env, remove, err := held.credentials.use(held.directory)
	if err != nil {
		return err
	}
	defer remove()
	return held.work.deleteBranch(ctx, env, held.attributes.url, branch)
}
