package main

// diverged.go handles a volume whose commits cannot be rebased onto the
// remote ref. The driver pushes them to a side branch instead, and goes
// back to the ref once upstream contains that work, normally because a
// person merged the side branch.

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// divergedKey is the git config key in the volume's git directory that
// records the side branch name. It is on disk so that a restarted driver
// and a later stage still push to the side branch.
const divergedKey = "git-csi.diverged"

// rejectionWords are the strings in git's stderr that show the remote
// rejected a push. Only a rejected push makes the driver rebase, and it
// diverges when the push after the rebase fails too.
var rejectionWords = []string{"rejected", "non-fast-forward", "fetch first"}

// The side branch name includes the volume id so that two volumes of
// the same ref on one remote do not share a branch.
func sideBranch(ref, id string) string {
	return ref + "." + id
}

// rejectedPush searches all of stderr, because git prints hints after
// the rejection line.
func rejectedPush(output gitOutput) bool {
	for _, word := range rejectionWords {
		if strings.Contains(output.stderr, word) {
			return true
		}
	}
	return false
}

// divergedBranch returns the empty string when the volume is not
// diverged.
func (w *workTree) divergedBranch(ctx context.Context) string {
	output, err := w.git(ctx, "config", "--get", "--", divergedKey)
	if err != nil {
		return ""
	}
	return trimLine(output.stdout)
}

func (w *workTree) markDiverged(ctx context.Context, branch string) error {
	_, err := w.git(ctx, "config", "--local", "--", divergedKey, branch)
	return err
}

func (w *workTree) clearDiverged(ctx context.Context) error {
	_, err := w.git(ctx, "config", "--unset", "--", divergedKey)
	return err
}

func (w *workTree) deleteBranch(ctx context.Context, env []string, url, branch string) error {
	_, err := w.gitWith(ctx, env, "push", "--quiet", url, ":refs/heads/"+branch)
	return err
}

// diverge switches the volume to its side branch, and reports it in the
// volume's status, an Event, the log, and the metrics. When the git
// config cannot be written, the volume still pushes to the side branch
// until the driver restarts.
func (n *node) diverge(ctx context.Context, vol *volume) {
	branch := sideBranch(vol.attributes.ref, vol.id)
	if err := vol.work.markDiverged(ctx, branch); err != nil {
		n.logger.WarnContext(ctx, "The diverged record was not written to the volume git config",
			"volume", vol.id, "error", err)
	}
	vol.reportDiverged(branch)
	claim := n.claimFor(ctx, vol)
	n.report(vol, claim, corev1.EventTypeWarning, reasonDiverged,
		fmt.Sprintf("Every push goes to the side branch %s, not to %s", branch, vol.attributes.ref))
	n.logger.WarnContext(ctx, "The volume diverged", "volume", vol.id, "branch", branch)
	n.metrics.record(vol)
	n.recordHealth(ctx, vol)
}

// stageDiverged runs when a stage finds a diverged volume. When
// upstream contains the tree's HEAD, it returns the volume to its ref.
// Otherwise it reports the volume as still diverged.
func (n *node) stageDiverged(
	ctx context.Context, staging *volume, branch, head, upstream, side string,
) error {
	if !staging.work.ancestor(ctx, head, upstream) {
		staging.reportDiverged(branch)
		n.metrics.record(staging)
		n.recordHealth(ctx, staging)
		return nil
	}
	return n.resetToUpstream(ctx, staging, branch, upstream, side)
}

// resetToUpstream resets the tree to upstream, moves pushedRef there,
// and replays the file metadata that the reset dropped. Then it deletes the
// side branch when the remote still has one, and clears the diverged
// record.
func (n *node) resetToUpstream(ctx context.Context, staging *volume, branch, upstream, side string) error {
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
	return n.recordReturnToRef(ctx, staging, branch)
}

// returnToRefIfMerged runs before a diverged volume pushes. It fetches
// the ref, and returns the volume to its ref when upstream contains the
// side branch's work. It returns whether the volume is on its ref again.
func (n *node) returnToRefIfMerged(ctx context.Context, vol *volume, branch string) bool {
	upstream, err := n.fetchUpstream(ctx, vol)
	if err != nil {
		n.logger.WarnContext(ctx, "The upstream ref was not fetched",
			"volume", vol.id, "error", err)
		return false
	}
	// pushedRef is the last commit pushed to the side branch.
	if !vol.work.ancestor(ctx, pushedRef, upstream) {
		return false
	}
	if err := n.returnToRefByRebase(ctx, vol, branch, upstream); err != nil {
		n.logger.WarnContext(ctx, "The volume was not returned to its ref",
			"volume", vol.id, "error", err)
		return false
	}
	return true
}

// returnToRefByRebase returns a volume to its ref while a pod has the
// tree mounted. It rebases in a scratch tree instead of resetting,
// because a reset rewrites every file under the running pod. pushedRef
// moves with the push that follows.
func (n *node) returnToRefByRebase(ctx context.Context, vol *volume, branch, upstream string) error {
	if err := n.rebaseInScratchTree(ctx, vol, upstream); err != nil {
		return err
	}
	n.removeSide(ctx, vol, branch)
	return n.recordReturnToRef(ctx, vol, branch)
}

// A failure to delete the side branch is only logged, because the tree
// is back on its ref either way.
func (n *node) removeSide(ctx context.Context, vol *volume, branch string) {
	if err := n.deleteSide(ctx, vol, branch); err != nil {
		n.logger.WarnContext(ctx, "The side branch was not deleted from the remote",
			"volume", vol.id, "branch", branch, "error", err)
	}
}

// recordReturnToRef clears the diverged record, and reports the return
// in the same places where diverge reports a divergence.
func (n *node) recordReturnToRef(ctx context.Context, vol *volume, branch string) error {
	if err := vol.work.clearDiverged(ctx); err != nil {
		return err
	}
	vol.reportReturnedToRef()
	claim := n.claimFor(ctx, vol)
	n.report(vol, claim, corev1.EventTypeNormal, reasonHealed,
		fmt.Sprintf("Pushes go to %s again, and the side branch %s was deleted",
			vol.attributes.ref, branch))
	n.logger.InfoContext(ctx, "The volume returned to its ref", "volume", vol.id, "branch", branch)
	n.metrics.record(vol)
	n.recordHealth(ctx, vol)
	return nil
}

// claimFor returns the volume's claim for an Event. A stage can diverge
// or return to the ref before the arming loop has found the claim, so claimFor then
// looks up the claim from the volume's handle.
func (n *node) claimFor(ctx context.Context, vol *volume) claimReference {
	claim, _, _ := vol.gaugeValues()
	if claim.name != "" || n.arming.client == nil {
		return claim
	}
	found, err := n.arming.claimOf(ctx, vol.id)
	if err != nil {
		n.logger.InfoContext(ctx, "The claim of the volume was not found for its Event",
			"volume", vol.id, "error", err)
		return claim
	}
	return found
}

// deleteSide writes the credential for this one call and removes it
// after, as every call to the remote does.
func (n *node) deleteSide(ctx context.Context, vol *volume, branch string) error {
	env, remove, err := vol.credential().use(vol.directory)
	if err != nil {
		return err
	}
	defer remove()
	return vol.work.deleteBranch(ctx, env, vol.attributes.url, branch)
}
