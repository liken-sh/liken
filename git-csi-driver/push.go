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

// pushTo sends the branch and the metadata ref beside it, and
// names the metadata ref only when the work tree holds one.
func (w *workTree) pushTo(ctx context.Context, env []string, url, ref string) error {
	branch := "refs/heads/" + ref
	specs := []string{branch + ":" + branch}
	if w.refCommit(ctx, metadataRef) != "" {
		specs = append(specs, metadataRef+":"+metadataRef)
	}
	_, err := w.gitWith(ctx, env, append([]string{"push", "--quiet", url}, specs...)...)
	return err
}

// fetchMetadata takes the record the remote holds, which is what
// a restore replays.
func (w *workTree) fetchMetadata(ctx context.Context, env []string, url string) error {
	_, err := w.gitWith(ctx, env, "fetch", "--quiet", "--no-tags", url,
		"+"+metadataRef+":"+metadataRef)
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
// what the condition and the gauge carry.
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

// pushNow sends what the work tree holds and moves the mark. A failure
// is the condition's message until a push works.
func (n *node) pushNow(ctx context.Context, held *volume, count int) {
	head, err := held.work.head(ctx)
	if err != nil {
		n.logger.WarnContext(ctx, "the tree's commit was not read",
			"volume", held.id, "error", err)
		return
	}
	env, remove, err := held.credentials.use(held.directory)
	if err != nil {
		n.pushFailed(ctx, held, err.Error())
		return
	}
	pushErr := held.work.pushTo(ctx, env, held.attributes.url, held.attributes.ref)
	remove()
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
		fmt.Sprintf("pushed %d commits to %s at %s", count, held.attributes.ref, short(head)))
	n.logger.InfoContext(ctx, "pushed",
		"volume", held.id, "commits", count, "commit", short(head))
	n.readings.record(held)
}

// pushFailed reports the failure in all three places and posts
// its Event once, at the first failure after a push that worked.
func (n *node) pushFailed(ctx context.Context, held *volume, message string) {
	if held.reportTrouble(message) {
		claim, _, _ := held.reading()
		n.report(ctx, held, claim, corev1.EventTypeWarning, reasonPushFailed, message)
	}
	n.readings.pushFailed(held)
	n.logger.WarnContext(ctx, "the push failed", "volume", held.id, "error", message)
}
