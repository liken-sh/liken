package main

// diverged.go holds the side branch: the state a volume takes
// when upstream and the tree have both moved, where its pushes go while
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
// the one push failure the driver answers with a branch of its own.
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

// diverge moves the volume to its side branch and reports it in
// every place a person reads, and a state it cannot write is still the
// state in force for this run.
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
		if err := n.deleteSide(ctx, staging, branch); err != nil {
			n.logger.WarnContext(ctx, "the side branch stayed on the remote",
				"volume", staging.id, "branch", branch, "error", err)
		}
	}
	if err := staging.work.clearDiverged(ctx); err != nil {
		return err
	}
	staging.reportHealed()
	claim := n.claimFor(ctx, staging)
	n.report(ctx, staging, claim, corev1.EventTypeNormal, reasonHealed,
		fmt.Sprintf("healed: the tree is back on %s and %s is gone",
			staging.attributes.ref, branch))
	n.logger.InfoContext(ctx, "healed", "volume", staging.id, "branch", branch)
	n.readings.record(staging)
	n.noteHealth(ctx, staging)
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
