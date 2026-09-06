package main

// push.go decides when the driver sends its commits to the
// remote, and sends them.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// pushedRef is the mark the driver moves after a push that
// worked, so what is unpushed is what stands after it.
const pushedRef = refPrefix + "pushed"

// unpushed is what the work tree holds that the remote does not,
// and when the oldest of those commits was made.
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

// commitTime reads git's %ct, a whole number of seconds since the
// epoch. A field that is not one names no time at all.
func commitTime(field string) time.Time {
	seconds, err := strconv.ParseInt(field, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(seconds, 0)
}

// markPushed moves the mark to the commit the remote now holds.
func (w *workTree) markPushed(ctx context.Context, commit string) error {
	_, err := w.git(ctx, "update-ref", pushedRef, commit)
	return err
}

// pushTo sends the branch to remote, the branch it takes on the remote,
// which is the ref itself until the volume takes a side branch. The
// metadata ref goes beside it when the work tree holds one.
func (w *workTree) pushTo(
	ctx context.Context, env []string, url, ref, remote string,
) (gitOutput, error) {
	specs := []string{"refs/heads/" + ref + ":refs/heads/" + remote}
	if w.refCommit(ctx, metadataRef) != "" {
		specs = append(specs, metadataRef+":"+metadataRef)
	}
	return w.gitWith(ctx, env, append([]string{"push", "--quiet", url}, specs...)...)
}

// fetchMetadata takes the record the remote holds, onto the ref the
// caller names. A restore fetches it over the volume's own record,
// and a rebase fetches it beside the volume's own, so the caller
// says which.
func (w *workTree) fetchMetadata(ctx context.Context, env []string, url, into string) error {
	_, err := w.gitWith(ctx, env, "fetch", "--quiet", "--no-tags", url,
		"+"+metadataRef+":"+into)
	return err
}

// pushDue is the policy's answer: the tree has rested for the
// quiesce, or the oldest unpushed commit has outlived push.maxLatency.
func pushDue(rules *policy, unpushed int, oldest, now time.Time, quiet time.Duration) bool {
	if unpushed == 0 {
		return false
	}
	if quiet >= rules.quiesce {
		return true
	}
	return rules.maxLatency != 0 && !oldest.IsZero() && now.Sub(oldest) > rules.maxLatency
}

// pushIfDue counts what is unpushed, reports it, and pushes when
// the policy says so.
func (n *node) pushIfDue(ctx context.Context, held *volume, rules *policy, quiet time.Duration) {
	if rules == nil {
		return
	}
	count, oldest, err := n.counted(ctx, held)
	if err != nil {
		return
	}
	if pushDue(rules, count, oldest, time.Now(), quiet) {
		n.pushNow(ctx, held, count)
	}
}

// push is what unpublish and unstage do. It asks the policy nothing,
// because durability is the last push.
func (n *node) push(ctx context.Context, held *volume) {
	count, _, err := n.counted(ctx, held)
	if err != nil || count == 0 {
		return
	}
	n.pushNow(ctx, held, count)
}

// counted reads the unpushed commits and records them, which is
// what the volume's report and the gauges carry.
func (n *node) counted(ctx context.Context, held *volume) (int, time.Time, error) {
	count, oldest, err := held.work.unpushed(ctx, held.attributes.ref)
	if err != nil {
		n.logger.WarnContext(ctx, "the unpushed commits were not counted",
			"volume", held.id, "error", err)
		return 0, time.Time{}, err
	}
	held.reportUnpushed(count, oldest)
	return count, oldest, nil
}

// pushNow sends what the work tree holds and moves the mark. A
// failure is the volume's report until a push works.
// A push the remote rejects as non-fast-forward means another
// writer pushed first. The driver rebases onto what the remote holds
// now and pushes again, and takes its side branch only when that
// fails too.
func (n *node) pushNow(ctx context.Context, held *volume, count int) {
	if held.refIsDeleted() {
		return
	}
	head, err := held.work.head(ctx)
	if err != nil {
		n.logger.WarnContext(ctx, "the tree's commit was not read",
			"volume", held.id, "error", err)
		return
	}
	branch := held.divergedFrom()
	if branch != "" && n.healIfMerged(ctx, held, branch) {
		branch = ""
		head = held.work.refCommit(ctx, "HEAD")
	}
	remote := held.attributes.ref
	if branch != "" {
		remote = branch
	}
	output, pushErr := n.sendTo(ctx, held, remote)
	if pushErr != nil && branch == "" && rejectedPush(output) {
		if rebased, landed := n.rebaseAndRetry(ctx, held, count); landed {
			head, pushErr = rebased, nil
		} else {
			n.diverge(ctx, held)
			remote = held.divergedFrom()
			// A retry that rebased and still lost moved the branch,
			// so the side branch's mark is the commit the tree holds
			// now, not the one the push started from.
			head = held.work.refCommit(ctx, "HEAD")
			_, pushErr = n.sendTo(ctx, held, remote)
		}
	}
	if pushErr != nil {
		n.pushFailed(ctx, held, pushErr.Error())
		return
	}
	if err := held.work.markPushed(ctx, head); err != nil {
		n.logger.WarnContext(ctx, "the pushed mark did not move",
			"volume", held.id, "error", err)
	}
	held.reportPushed(time.Now())
	claim, _, _ := held.reading()
	n.report(ctx, held, claim, corev1.EventTypeNormal, reasonPushed,
		fmt.Sprintf("pushed %d commits to %s at %s", count, remote, short(head)))
	n.logger.InfoContext(ctx, "pushed",
		"volume", held.id, "commits", count, "branch", remote, "commit", short(head))
	n.readings.record(held)
	n.noteHealth(ctx, held)
}

// rebaseAttempts bounds the retry. A loss is a race with another
// writer, and three losses in a row say the writers race faster
// than a rebase settles.
const rebaseAttempts = 3

// rebaseAndRetry puts the volume's commits on top of the ref the
// remote holds now and pushes again. It answers the commit that
// landed, or false when the volume has to take its side branch.
func (n *node) rebaseAndRetry(ctx context.Context, held *volume, count int) (string, bool) {
	for range rebaseAttempts {
		upstream, err := n.fetchUpstream(ctx, held)
		if err != nil {
			n.logger.WarnContext(ctx, "the ref was not fetched",
				"volume", held.id, "error", err)
			return "", false
		}
		before := held.work.refCommit(ctx, "HEAD")
		if err := n.rebaseAside(ctx, held, upstream); err != nil {
			n.logger.WarnContext(ctx, "the rebase was aborted",
				"volume", held.id, "error", err)
			return "", false
		}
		// The record goes on top of the remote's in the same pass,
		// because a push sends the branch and the record together
		// and the remote accepts or rejects each on its own.
		if err := held.work.reparentMetadata(ctx, held.authorEnv()); err != nil {
			n.logger.WarnContext(ctx, "the record was not put on top of the remote's",
				"volume", held.id, "error", err)
			return "", false
		}
		head := held.work.refCommit(ctx, "HEAD")
		if _, err := n.sendTo(ctx, held, held.attributes.ref); err != nil {
			continue
		}
		// The Event goes out after the push it describes, and only
		// when the rebase moved the tree. A push the remote rejected
		// for the record alone rebased nothing.
		if head != before {
			claim, _, _ := held.reading()
			n.report(ctx, held, claim, corev1.EventTypeNormal, reasonRebased,
				fmt.Sprintf("rebased %d commits onto %s and pushed to %s",
					count, short(upstream), held.attributes.ref))
			n.logger.InfoContext(ctx, "rebased", "volume", held.id,
				"commits", count, "onto", short(upstream))
		}
		return head, true
	}
	return "", false
}

// fetchUpstream takes what the remote holds on the ref now, under
// the repository's lock and in a credential window of its own, the
// way a stage does.
func (n *node) fetchUpstream(ctx context.Context, held *volume) (string, error) {
	repo := held.work.repository
	defer repo.lock()()

	env, remove, err := held.credentials.use(held.directory)
	if err != nil {
		return "", err
	}
	defer remove()
	if err := repo.fetch(ctx, env, held.attributes.ref, 0); err != nil {
		return "", err
	}
	// The record comes in the same window as the ref. A remote
	// that holds none leaves the rebase nothing to replay.
	_ = held.work.fetchMetadata(ctx, env, held.attributes.url, remoteMetadataRef)
	return repo.resolve(ctx, held.attributes.ref)
}

// sendTo opens the credential window, pushes, and closes it, so
// no key file outlives the invocation that reads it.
func (n *node) sendTo(ctx context.Context, held *volume, remote string) (gitOutput, error) {
	env, remove, err := held.credentials.use(held.directory)
	if err != nil {
		return gitOutput{}, err
	}
	defer remove()
	return held.work.pushTo(ctx, env, held.attributes.url, held.attributes.ref, remote)
}

// pushFailed reports the failure in every place a person reads
// and posts its Event once, at the first failure after a push that
// worked.
func (n *node) pushFailed(ctx context.Context, held *volume, message string) {
	if held.reportTrouble(message) {
		claim, _, _ := held.reading()
		n.report(ctx, held, claim, corev1.EventTypeWarning, reasonPushFailed, message)
	}
	n.readings.pushFailed(held)
	n.noteHealth(ctx, held)
	n.logger.WarnContext(ctx, "the push failed", "volume", held.id, "error", message)
}
