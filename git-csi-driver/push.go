package main

// push.go decides when a writeable volume's commits are pushed to the
// remote, and pushes them.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// pushedRef is a ref in the work tree that points at the last commit
// pushed to the remote. The commits after it are unpushed.
const pushedRef = refPrefix + "pushed"

// unpushed returns how many commits the work tree has that the remote
// does not, and the commit time of the oldest of them.
func (w *workTree) unpushed(ctx context.Context, ref string) (int, time.Time, error) {
	output, err := w.git(ctx, "log", "--format=%ct", "--reverse", "--end-of-options",
		pushedRef+"..refs/heads/"+ref)
	if err != nil {
		return 0, time.Time{}, err
	}
	times := strings.Fields(output.stdout)
	if len(times) == 0 {
		return 0, time.Time{}, nil
	}
	return len(times), commitTime(times[0]), nil
}

// commitTime parses git's %ct, a count of seconds since the Unix epoch.
// It returns the zero time when the field is not a number.
func commitTime(field string) time.Time {
	seconds, err := strconv.ParseInt(field, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(seconds, 0)
}

func (w *workTree) markPushed(ctx context.Context, commit string) error {
	_, err := w.git(ctx, "update-ref", pushedRef, commit)
	return err
}

// pushTo pushes the local branch ref to the branch remote at url. remote
// is the volume's ref, or its side branch after the volume diverged.
// When the work tree has a metadataRef, the same push sends it.
func (w *workTree) pushTo(
	ctx context.Context, env []string, url, ref, remote string,
) (gitOutput, error) {
	specs := []string{"refs/heads/" + ref + ":refs/heads/" + remote}
	if w.refCommit(ctx, metadataRef) != "" {
		specs = append(specs, metadataRef+":"+metadataRef)
	}
	return w.gitWith(ctx, env, append([]string{"push", "--quiet", url}, specs...)...)
}

// fetchMetadata fetches the remote's metadataRef into the local ref
// into. Staging fetches it into metadataRef itself. fetchUpstream
// fetches it into remoteMetadataRef, which the rebase reads.
func (w *workTree) fetchMetadata(ctx context.Context, env []string, url, into string) error {
	_, err := w.gitWith(ctx, env, "fetch", "--quiet", "--no-tags", url,
		"+"+metadataRef+":"+into)
	return err
}

// pushDue reports whether to push now. It is true when something is
// unpushed and either the tree has been quiet for at least the policy's
// quiesce, or the oldest unpushed commit is older than maxLatency. A
// maxLatency of zero means there is no age limit.
func pushDue(rules *policy, unpushed int, oldest, now time.Time, quiet time.Duration) bool {
	if unpushed == 0 {
		return false
	}
	if quiet >= rules.quiesce {
		return true
	}
	return rules.maxLatency != 0 && !oldest.IsZero() && now.Sub(oldest) > rules.maxLatency
}

// pushIfDue counts the unpushed commits, records the count, and pushes
// when pushDue says to.
func (n *node) pushIfDue(ctx context.Context, vol *volume, rules *policy, quiet time.Duration) {
	if rules == nil {
		return
	}
	count, oldest, err := n.countUnpushed(ctx, vol)
	if err != nil {
		return
	}
	if pushDue(rules, count, oldest, time.Now(), quiet) {
		n.pushNow(ctx, vol, count)
	}
}

// push is the final push at unpublish and unstage. It ignores the push
// policy, because the pod is gone and the commits must reach the remote.
//
// A volume that a restarted driver resumed has no credential, and no
// republish brings one after the pod is gone. push reports that as a
// failed push, because the commits then exist only on this node.
func (n *node) push(ctx context.Context, vol *volume) {
	count, _, err := n.countUnpushed(ctx, vol)
	if err != nil || count == 0 {
		return
	}
	if vol.waitsForCredential() {
		n.pushFailed(ctx, vol, fmt.Sprintf("%d commits stay on this node: %s", count, lostCredential))
		return
	}
	n.pushNow(ctx, vol, count)
}

// countUnpushed calls unpushed, and records the count and the oldest commit's
// time in the volume's status.
func (n *node) countUnpushed(ctx context.Context, vol *volume) (int, time.Time, error) {
	count, oldest, err := vol.work.unpushed(ctx, vol.attributes.ref)
	if err != nil {
		n.logger.WarnContext(ctx, "The work tree's unpushed commits were not counted",
			"volume", vol.id, "error", err)
		return 0, time.Time{}, err
	}
	vol.reportUnpushed(count, oldest)
	return count, oldest, nil
}

// pushNow pushes the work tree and moves pushedRef. A failure stays in
// the volume's status until a push succeeds.
//
// A non-fast-forward rejection means another writer pushed first. The
// driver rebases onto the remote's branch and pushes again. Only when
// that fails does it diverge onto a side branch.
func (n *node) pushNow(ctx context.Context, vol *volume, count int) {
	// Push nothing while the remote has no ref, or while a resumed volume
	// has no credential and a push can only fail. The commits stay in the
	// work tree, and the republish that brings the credential pushes them.
	if vol.refIsDeleted() || vol.waitsForCredential() {
		return
	}
	head, err := vol.work.head(ctx)
	if err != nil {
		n.logger.WarnContext(ctx, "The work tree's HEAD commit was not read",
			"volume", vol.id, "error", err)
		return
	}
	branch := vol.sideBranch()
	if branch != "" && n.returnToRefIfMerged(ctx, vol, branch) {
		branch = ""
		head = vol.work.refCommit(ctx, "HEAD")
	}
	remote := vol.attributes.ref
	if branch != "" {
		remote = branch
	}
	output, pushErr := n.sendTo(ctx, vol, remote)
	if pushErr != nil && branch == "" && rejectedPush(output) {
		if rebased, pushed := n.rebaseAndRetry(ctx, vol, count); pushed {
			head, pushErr = rebased, nil
		} else {
			n.diverge(ctx, vol)
			remote = vol.sideBranch()
			// The retries may have rebased the branch, so mark the current
			// HEAD as pushed, not the commit this call started with.
			head = vol.work.refCommit(ctx, "HEAD")
			_, pushErr = n.sendTo(ctx, vol, remote)
		}
	}
	if pushErr != nil {
		n.pushFailed(ctx, vol, pushErr.Error())
		return
	}
	if err := vol.work.markPushed(ctx, head); err != nil {
		n.logger.WarnContext(ctx, "The work tree's pushedRef was not updated",
			"volume", vol.id, "error", err)
	}
	vol.reportPushed(time.Now())
	claim, _, _ := vol.gaugeValues()
	n.report(vol, claim, corev1.EventTypeNormal, reasonPushed,
		fmt.Sprintf("Pushed %d commits to %s at %s", count, remote, short(head)))
	n.logger.InfoContext(ctx, "Pushed the volume's commits",
		"volume", vol.id, "commits", count, "branch", remote, "commit", short(head))
	n.metrics.record(vol)
	n.recordHealth(ctx, vol)
}

// rebaseAttempts is how many times rebaseAndRetry fetches, rebases, and
// pushes before the volume diverges onto a side branch.
const rebaseAttempts = 3

// rebaseAndRetry rebases the volume's commits onto the remote's current
// branch and pushes again. It returns the pushed commit and true, or
// false when the volume must diverge onto a side branch.
func (n *node) rebaseAndRetry(ctx context.Context, vol *volume, count int) (string, bool) {
	for range rebaseAttempts {
		upstream, err := n.fetchUpstream(ctx, vol)
		if err != nil {
			n.logger.WarnContext(ctx, "The ref was not fetched from the remote",
				"volume", vol.id, "error", err)
			return "", false
		}
		before := vol.work.refCommit(ctx, "HEAD")
		if err := n.rebaseInScratchTree(ctx, vol, upstream); err != nil {
			n.logger.WarnContext(ctx, "The rebase of the tree onto upstream did not complete",
				"volume", vol.id, "error", err)
			return "", false
		}
		// Rebase the metadata record onto the remote's as well. One push
		// sends the branch and the record, and the remote accepts or
		// rejects each ref separately.
		if err := vol.work.reparentMetadata(ctx, vol.authorEnv()); err != nil {
			n.logger.WarnContext(ctx, "The metadata record was not rebased onto the remote's record",
				"volume", vol.id, "error", err)
			return "", false
		}
		head := vol.work.refCommit(ctx, "HEAD")
		if _, err := n.sendTo(ctx, vol, vol.attributes.ref); err != nil {
			continue
		}
		// Post the Event only if the rebase changed HEAD. A push that the
		// remote rejected only for the metadata ref rebased no commits.
		if head != before {
			claim, _, _ := vol.gaugeValues()
			n.report(vol, claim, corev1.EventTypeNormal, reasonRebased,
				fmt.Sprintf("Rebased %d commits onto %s and pushed them to %s",
					count, short(upstream), vol.attributes.ref))
			n.logger.InfoContext(ctx, "Rebased the volume's commits", "volume", vol.id,
				"commits", count, "onto", short(upstream))
		}
		return head, true
	}
	return "", false
}

// fetchUpstream fetches the volume's ref from the remote and returns its
// commit. It holds the repository's lock and uses the credential for the
// duration of the fetch only, as stage does.
func (n *node) fetchUpstream(ctx context.Context, vol *volume) (string, error) {
	repo := vol.work.repository
	defer repo.lock()()

	env, remove, err := vol.credential().use(vol.directory)
	if err != nil {
		return "", err
	}
	defer remove()
	if err := n.metrics.timeFetch(repo.name, func() error {
		return repo.fetch(ctx, env, vol.attributes.ref, 0)
	}); err != nil {
		return "", err
	}
	// A remote without a metadataRef fails this fetch, and the rebase
	// then has no remote record to replay, so the error is ignored.
	_ = vol.work.fetchMetadata(ctx, env, vol.attributes.url, remoteMetadataRef)
	return repo.resolve(ctx, vol.attributes.ref)
}

// sendTo pushes to the branch named remote. The credential's files exist
// only while the push runs.
func (n *node) sendTo(ctx context.Context, vol *volume, remote string) (gitOutput, error) {
	env, remove, err := vol.credential().use(vol.directory)
	if err != nil {
		return gitOutput{}, err
	}
	defer remove()
	return vol.work.pushTo(ctx, env, vol.attributes.url, vol.attributes.ref, remote)
}

// pushFailed records the failure in the volume's status, the metrics,
// and the log. It posts an Event only for the first failure after a
// successful push.
func (n *node) pushFailed(ctx context.Context, vol *volume, message string) {
	if vol.reportPushFailed(message) {
		claim, _, _ := vol.gaugeValues()
		n.report(vol, claim, corev1.EventTypeWarning, reasonPushFailed,
			fmt.Sprintf("The push to %s failed: %s", vol.attributes.ref, message))
	}
	n.metrics.pushFailed(vol)
	n.recordHealth(ctx, vol)
	n.logger.WarnContext(ctx, "The push to the remote did not complete", "volume", vol.id, "error", message)
}
