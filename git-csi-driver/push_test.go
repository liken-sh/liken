package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pushedVolume is an armed volume against a bare remote, with one commit
// of its own that the remote does not hold.
func pushedVolume(t *testing.T, logs io.Writer, parameters map[string]string) (*node, *volume, string) {
	t.Helper()
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	answering, _ := testNode(t, logs)
	held := armedVolume(t, answering, "config", fileURL(remote), parameters)
	unwatched(t, answering, held)
	writeFiles(t, held.tree, map[string]string{"one.yaml": "1"})
	answering.commit(t.Context(), held, held.policyNow())
	return answering, held, remote
}

func TestThePolicySaysWhenToPush(t *testing.T) {
	now := time.Now()
	rested := policy{quiesce: 30 * time.Second, maxLatency: 5 * time.Minute}
	never := policy{quiesce: 30 * time.Second}
	for _, c := range []struct {
		name     string
		rules    policy
		unpushed int
		oldest   time.Time
		quiet    time.Duration
		want     bool
	}{
		{name: "nothing unpushed", rules: rested, unpushed: 0, quiet: time.Hour},
		{
			name:  "the tree has rested for the quiesce",
			rules: rested, unpushed: 1, oldest: now, quiet: 30 * time.Second, want: true,
		},
		{
			name:  "the tree is still being written",
			rules: rested, unpushed: 1, oldest: now, quiet: time.Second,
		},
		{
			name:  "a commit older than the latency",
			rules: rested, unpushed: 1, oldest: now.Add(-6 * time.Minute),
			quiet: time.Second, want: true,
		},
		{
			name:  "a commit older than the latency, with no latency",
			rules: never, unpushed: 1, oldest: now.Add(-time.Hour), quiet: time.Second,
		},
		{
			name:  "a commit whose time the driver does not know",
			rules: rested, unpushed: 1, quiet: time.Second,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := pushDue(&c.rules, c.unpushed, c.oldest, now, c.quiet); got != c.want {
				t.Errorf("pushDue answered %v, want %v", got, c.want)
			}
		})
	}
}

func TestACommitTimeIsWholeSeconds(t *testing.T) {
	if got := commitTime("1757030400"); !got.Equal(time.Unix(1757030400, 0)) {
		t.Errorf("commitTime answered %s", got)
	}
	if got := commitTime("recently"); !got.IsZero() {
		t.Errorf("commitTime answered %s, want no time", got)
	}
}

func TestTheUnpushedCommitsAreTheOnesAfterTheMark(t *testing.T) {
	answering, held, _ := pushedVolume(t, io.Discard, nil)
	count, oldest, err := held.work.unpushed(t.Context(), "main")
	if err != nil {
		t.Fatalf("unpushed: %v", err)
	}
	if count != 1 {
		t.Errorf("the work tree holds %d unpushed commits, want 1", count)
	}
	if oldest.IsZero() {
		t.Error("the oldest unpushed commit names no time")
	}
	answering.push(t.Context(), held)
	count, _, err = held.work.unpushed(t.Context(), "main")
	if err != nil {
		t.Fatalf("unpushed: %v", err)
	}
	if count != 0 {
		t.Errorf("the work tree holds %d unpushed commits after a push, want 0", count)
	}
}

func TestAPushSendsTheBranchAndTheMetadataRef(t *testing.T) {
	answering, held, remote := pushedVolume(t, io.Discard, nil)
	answering.push(t.Context(), held)

	if got := strings.TrimSpace(git(t, remote, "log", "--format=%s", "-1", "main")); got != "Update 1 paths" {
		t.Errorf("the remote's main is at %q, want the driver's commit", got)
	}
	if got := strings.TrimSpace(git(t, remote, "rev-parse", "--verify", metadataRef)); got == "" {
		t.Error("the remote holds no metadata ref")
	}
	pushed := eventsWithReason(t, answering, reasonPushed)
	if len(pushed) != 2 {
		t.Fatalf("the push posted %v, want one Event on the pod and one on the claim", pushed)
	}
	if pushed[0].Message != "pushed 1 commits to main at "+
		short(strings.TrimSpace(gitIn(t, held.work, "rev-parse", "HEAD"))) {
		t.Errorf("the event says %q", pushed[0].Message)
	}
	answering.readings.record(held)
	if got, found := gaugeOf(t, answering.readings,
		"git_csi_unpushed_commits", "home", "config"); !found || got != 0 {
		t.Errorf("the gauge reports %v unpushed commits, want 0", got)
	}
	if _, found := gaugeOf(t, answering.readings,
		"git_csi_last_push_timestamp_seconds", "home", "config"); !found {
		t.Error("the gauge names no time of the last push")
	}
}

func TestAVolumeThatHasNeverPushedReportsNoTime(t *testing.T) {
	readings := newMetrics()
	readings.record(reported(claimReference{namespace: "home", name: "config"}, true, 0))
	if _, found := gaugeOf(t, readings,
		"git_csi_last_push_timestamp_seconds", "home", "config"); found {
		t.Error("a volume that has never pushed names a time")
	}
}

func TestAPushThatFailsIsTheConditionUntilOneWorks(t *testing.T) {
	logs := &logbook{}
	answering, held, remote := pushedVolume(t, logs, nil)
	if err := os.RemoveAll(remote); err != nil {
		t.Fatalf("removing the remote: %v", err)
	}

	answering.push(t.Context(), held)
	abnormal, message := held.report()
	if !abnormal || !strings.Contains(message, "push --quiet") {
		t.Errorf("the condition is %v, %q, want the failed push", abnormal, message)
	}
	if got := len(eventsWithReason(t, answering, reasonPushFailed)); got != 2 {
		t.Fatalf("the failure posted %d events, want one on the pod and one on the claim", got)
	}
	answering.push(t.Context(), held)
	if got := len(eventsWithReason(t, answering, reasonPushFailed)); got != 2 {
		t.Errorf("a second failure posted %d events, want the two of the first", got)
	}
	counted, found := counterOf(t, answering.readings, "git_csi_push_failures_total", "home", "config")
	if !found || counted != 2 {
		t.Errorf("the counter reports %v failures, want 2", counted)
	}
	if !strings.Contains(logs.String(), "the push failed") {
		t.Errorf("the log is %q, want the failed push in it", logs)
	}
}

// counterOf is what one counter reads for the claim.
func counterOf(t *testing.T, readings *metrics, name, namespace, claim string) (float64, bool) {
	t.Helper()
	families, err := readings.registry.Gather()
	if err != nil {
		t.Fatalf("gathering the metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["namespace"] == namespace && labels["claim"] == claim {
				return metric.GetCounter().GetValue(), true
			}
		}
	}
	return 0, false
}

func TestAPushWithNoCredentialItCanWriteFails(t *testing.T) {
	answering, held, _ := pushedVolume(t, io.Discard, nil)
	held.credentials = &credentials{privateKey: "a key"}
	held.directory = filepath.Join(t.TempDir(), "gone")

	answering.push(t.Context(), held)
	abnormal, message := held.report()
	if !abnormal || message == "" {
		t.Errorf("the condition is %v, %q, want the failure", abnormal, message)
	}
}

func TestAPushReportsWhatItCannotRead(t *testing.T) {
	for _, c := range []struct {
		name  string
		stand func(t *testing.T, held *volume)
		says  string
	}{
		{
			name: "a work tree whose commits it cannot count",
			stand: func(t *testing.T, held *volume) {
				if err := os.RemoveAll(held.work.gitDir); err != nil {
					t.Fatalf("removing the git directory: %v", err)
				}
			},
			says: "the unpushed commits were not counted",
		},
		{
			name: "a mark it cannot move",
			stand: func(t *testing.T, held *volume) {
				if err := os.MkdirAll(filepath.Join(held.work.gitDir,
					filepath.FromSlash(pushedRef)+".lock"), 0o755); err != nil {
					t.Fatalf("making the lock directory: %v", err)
				}
			},
			says: "the pushed mark did not move",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			logs := &logbook{}
			answering, held, _ := pushedVolume(t, logs, nil)
			c.stand(t, held)
			answering.push(t.Context(), held)
			if !strings.Contains(logs.String(), c.says) {
				t.Errorf("the log is %q, want %q in it", logs, c.says)
			}
		})
	}
}

func TestAPushReportsAHeadItCannotRead(t *testing.T) {
	logs := &logbook{}
	answering, held, _ := pushedVolume(t, logs, nil)
	if err := os.Remove(filepath.Join(held.work.gitDir, "HEAD")); err != nil {
		t.Fatalf("removing HEAD: %v", err)
	}
	answering.pushNow(t.Context(), held, 1)
	if !strings.Contains(logs.String(), "the tree's commit was not read") {
		t.Errorf("the log is %q, want the unreadable commit in it", logs)
	}
}

func TestAnUnarmedVolumePushesNothingOnTheTimer(t *testing.T) {
	answering, held, remote := pushedVolume(t, io.Discard, nil)
	answering.pushIfDue(t.Context(), held, nil, time.Hour)
	if got := strings.TrimSpace(git(t, remote, "log", "--format=%s", "-1", "main")); got != "one" {
		t.Errorf("the remote's main is at %q, want the commit it started with", got)
	}
}

func TestTheTimerPushesWhenTheTreeHasRested(t *testing.T) {
	answering, held, remote := pushedVolume(t, io.Discard, nil)
	answering.pushIfDue(t.Context(), held, held.policyNow(), time.Hour)
	if got := strings.TrimSpace(git(t, remote, "log", "--format=%s", "-1", "main")); got != "Update 1 paths" {
		t.Errorf("the remote's main is at %q, want the driver's commit", got)
	}
}

func TestTheTimerWaitsWhileTheTreeIsWritten(t *testing.T) {
	answering, held, remote := pushedVolume(t, io.Discard, nil)
	answering.pushIfDue(t.Context(), held, held.policyNow(), time.Second)
	if got := strings.TrimSpace(git(t, remote, "log", "--format=%s", "-1", "main")); got != "one" {
		t.Errorf("the remote's main is at %q, want the commit it started with", got)
	}
}

func TestTheTimerReportsAWorkTreeItCannotCount(t *testing.T) {
	logs := &logbook{}
	answering, held, _ := pushedVolume(t, logs, nil)
	if err := os.RemoveAll(held.work.gitDir); err != nil {
		t.Fatalf("removing the git directory: %v", err)
	}
	answering.pushIfDue(t.Context(), held, held.policyNow(), time.Hour)
	if !strings.Contains(logs.String(), "the unpushed commits were not counted") {
		t.Errorf("the log is %q, want the failure in it", logs)
	}
}

func TestAnOverdueCommitIsAbnormal(t *testing.T) {
	held := &volume{
		attributes: &attributes{ref: "main"},
		kind:       writeableVolume,
		armed:      true,
		rules:      &policy{quiesce: 30 * time.Second, maxLatency: 5 * time.Minute},
		unpushed:   3,
		oldest:     time.Now().Add(-time.Hour),
	}
	abnormal, message := held.report()
	if !abnormal || message != "3 unpushed commits, the oldest older than 5m0s" {
		t.Errorf("the condition is %v, %q, want the overdue commits", abnormal, message)
	}
}

func TestARejectedPushRebasesAndLandsOnTheRef(t *testing.T) {
	answering, held, remote := pushedVolume(t, io.Discard, nil)
	upstream := remoteCommit(t, remote, map[string]string{"maps/m.yaml": "m"})

	answering.push(t.Context(), held)

	want := map[string]string{"a.txt": "one", "one.yaml": "1", "maps/m.yaml": "m"}
	if got := readTree(t, held.tree); !sameTree(got, want) {
		t.Errorf("the tree holds %v, want %v", got, want)
	}
	got := git(t, remote, "ls-tree", "-r", "--name-only", "main")
	if !strings.Contains(got, "one.yaml") || !strings.Contains(got, "maps/m.yaml") {
		t.Errorf("main on the remote holds %q, want both writers' files", got)
	}
	if branches := branchesOn(t, remote); strings.Contains(branches, "main.config") {
		t.Errorf("the remote holds %q, want no side branch", branches)
	}
	if branch := held.divergedFrom(); branch != "" {
		t.Errorf("the volume pushes to %q, want the ref", branch)
	}
	rebased := eventsWithReason(t, answering, reasonRebased)
	if len(rebased) != 2 {
		t.Fatalf("the rebase posted %v, want one Event on the pod and one on the claim", rebased)
	}
	message := fmt.Sprintf("rebased 1 commits onto %s and pushed to main", short(upstream))
	if rebased[0].Message != message {
		t.Errorf("the event says %q, want %q", rebased[0].Message, message)
	}
	head := held.work.refCommit(t.Context(), "HEAD")
	if mark := held.work.refCommit(t.Context(), pushedRef); mark != head {
		t.Errorf("the pushed mark is at %s, want the rebased head %s", mark, head)
	}
}

// declineHook is a forge that refuses every push to the ref and
// records the attempt, which is what a writer that loses the race
// every time meets.
const declineHook = `#!/bin/sh
while read -r old new ref; do
	if [ "$ref" = refs/heads/main ]; then
		echo "$old..$new" >> %s
		echo "the forge declines main" >&2
		exit 1
	fi
done
exit 0
`

// declineMain installs the hook on the remote and answers the file
// the refused pushes are counted in.
func declineMain(t *testing.T, remote string) string {
	t.Helper()
	declined := filepath.Join(t.TempDir(), "declined")
	hook := filepath.Join(remote, "hooks", "pre-receive")
	if err := os.WriteFile(hook, fmt.Appendf(nil, declineHook, declined), 0o755); err != nil {
		t.Fatalf("writing the hook: %v", err)
	}
	return declined
}

// acceptMain removes the hook, so the forge accepts a push again.
func acceptMain(t *testing.T, remote string) {
	t.Helper()
	if err := os.Remove(filepath.Join(remote, "hooks", "pre-receive")); err != nil {
		t.Fatalf("removing the hook: %v", err)
	}
}

// refused counts the pushes to the ref the forge declined.
func refused(t *testing.T, declined string) int {
	t.Helper()
	content, err := os.ReadFile(declined)
	if err != nil {
		t.Fatalf("reading the refused pushes: %v", err)
	}
	return len(strings.Fields(strings.TrimSpace(string(content))))
}

func TestThreeRejectedPushesInARowTakeTheSideBranch(t *testing.T) {
	answering, held, remote := pushedVolume(t, io.Discard, nil)
	declined := declineMain(t, remote)

	answering.push(t.Context(), held)

	if branch := held.divergedFrom(); branch != "main.config" {
		t.Errorf("the volume pushes to %q, want main.config", branch)
	}
	if branches := branchesOn(t, remote); !strings.Contains(branches, "main.config") {
		t.Errorf("the remote holds %q, want the side branch on it", branches)
	}
	if tried := refused(t, declined); tried != 1+rebaseAttempts {
		t.Errorf("the forge refused %d pushes, want %d", tried, 1+rebaseAttempts)
	}
	if rebased := eventsWithReason(t, answering, reasonRebased); len(rebased) != 0 {
		t.Errorf("the volume posted %v, want no rebase it never landed", rebased)
	}
}

func TestARebaseThatConflictsAfterARejectedPushTakesTheSideBranch(t *testing.T) {
	answering, held, remote := pushedVolume(t, io.Discard, nil)
	remoteCommit(t, remote, map[string]string{"one.yaml": "the forge wrote this"})

	answering.push(t.Context(), held)

	want := map[string]string{"a.txt": "one", "one.yaml": "1"}
	if got := readTree(t, held.tree); !sameTree(got, want) {
		t.Errorf("the tree holds %v, want %v", got, want)
	}
	if branch := held.divergedFrom(); branch != "main.config" {
		t.Errorf("the volume pushes to %q, want main.config", branch)
	}
	if _, err := os.Stat(filepath.Join(held.directory, scratchTree)); !os.IsNotExist(err) {
		t.Errorf("the volume directory holds a scratch work tree: %v", err)
	}
}

func TestAPathThePodAndUpstreamBothWroteTakesTheSideBranch(t *testing.T) {
	answering, held, remote := pushedVolume(t, io.Discard, nil)
	remoteCommit(t, remote, map[string]string{"a.txt": "the forge wrote this"})
	writeFiles(t, held.tree, map[string]string{"a.txt": "the pod is writing"})

	answering.push(t.Context(), held)

	want := map[string]string{"a.txt": "the pod is writing", "one.yaml": "1"}
	if got := readTree(t, held.tree); !sameTree(got, want) {
		t.Errorf("the tree holds %v, want %v", got, want)
	}
	if branch := held.divergedFrom(); branch != "main.config" {
		t.Errorf("the volume pushes to %q, want main.config", branch)
	}
	if got := git(t, remote, "ls-tree", "-r", "--name-only", "main.config"); !strings.Contains(got, "one.yaml") {
		t.Errorf("the side branch holds %q, want the pod's work on it", got)
	}
}

func TestAPathUpstreamDidNotWriteSurvivesTheRebase(t *testing.T) {
	answering, held, remote := pushedVolume(t, io.Discard, nil)
	remoteCommit(t, remote, map[string]string{"maps/m.yaml": "m"})
	writeFiles(t, held.tree, map[string]string{"draft.yaml": "unsaved"})

	answering.push(t.Context(), held)

	want := map[string]string{
		"a.txt": "one", "one.yaml": "1", "maps/m.yaml": "m", "draft.yaml": "unsaved",
	}
	if got := readTree(t, held.tree); !sameTree(got, want) {
		t.Errorf("the tree holds %v, want %v", got, want)
	}
	if branch := held.divergedFrom(); branch != "" {
		t.Errorf("the volume pushes to %q, want the ref", branch)
	}
}

func TestARetryThatCannotReachTheRemoteDoesNotRebase(t *testing.T) {
	for _, c := range []struct {
		name  string
		stand func(t *testing.T, answering *node, held *volume, remote string)
		says  string
	}{
		{
			name: "a credential it cannot write",
			stand: func(_ *testing.T, _ *node, held *volume, _ string) {
				held.credentials = &credentials{privateKey: "a key"}
				held.directory = filepath.Join(held.directory, "gone")
			},
			says: "the ref was not fetched",
		},
		{
			name: "a fetch the store cannot hold",
			stand: func(t *testing.T, answering *node, held *volume, _ string) {
				where := filepath.Join(answering.store.repository(held.attributes.url).dir,
					"FETCH_HEAD")
				if err := os.Remove(where); err != nil {
					t.Fatalf("removing the fetch record: %v", err)
				}
				if err := os.MkdirAll(where, 0o755); err != nil {
					t.Fatalf("making the fetch directory: %v", err)
				}
			},
			says: "the ref was not fetched",
		},
		{
			name: "a record it cannot put on top of the remote's",
			stand: func(t *testing.T, _ *node, held *volume, remote string) {
				remoteRecord(t, remote, recordLine(0o600, "maps/secret.yaml"))
				if err := os.MkdirAll(filepath.Join(held.work.gitDir,
					filepath.FromSlash(metadataRef)+".lock"), 0o755); err != nil {
					t.Fatalf("making the lock directory: %v", err)
				}
			},
			says: "the record was not put on top of the remote's",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			logs := &logbook{}
			answering, held, remote := pushedVolume(t, logs, nil)
			remoteCommit(t, remote, map[string]string{"maps/m.yaml": "m"})
			c.stand(t, answering, held, remote)

			if _, landed := answering.rebaseAndRetry(t.Context(), held, 1); landed {
				t.Error("the retry answered a push that landed")
			}
			if !strings.Contains(logs.String(), c.says) {
				t.Errorf("the log is %q, want %q in it", logs, c.says)
			}
		})
	}
}

// recordLine is one line of a record, in the format the driver
// writes and reads.
func recordLine(mode int, path string) string {
	return fmt.Sprintf("%04o %d %d %s", mode, os.Getuid(), os.Getgid(), path)
}

// remoteRecord puts another writer's record on the remote's
// metadata ref, which is what that writer's push leaves there.
func remoteRecord(t *testing.T, remote string, lines ...string) string {
	t.Helper()
	clone := t.TempDir()
	git(t, clone, "clone", "--quiet", remote, clone)
	git(t, clone, "checkout", "--quiet", "--orphan", "record")
	git(t, clone, "rm", "-r", "--quiet", "--cached", ".")
	writeFiles(t, clone, map[string]string{metadataFile: strings.Join(lines, "\n") + "\n"})
	git(t, clone, "add", "--", metadataFile)
	git(t, clone, "-c", "user.name=lab", "-c", "user.email=lab@liken.sh",
		"commit", "--quiet", "-m", metadataMessage)
	git(t, clone, "push", "--quiet", "origin", "record:"+metadataRef)
	return strings.TrimSpace(git(t, remote, "rev-parse", metadataRef))
}

// modeOf is the mode one path in the tree carries now.
func modeOf(t *testing.T, tree, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(filepath.Join(tree, filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

func TestARebaseTakesUpstreamsModesAndLeavesThePodsOwn(t *testing.T) {
	answering, held, remote := pushedVolume(t, io.Discard, nil)
	if err := os.Chmod(filepath.Join(held.tree, "one.yaml"), 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	remoteCommit(t, remote, map[string]string{"maps/secret.yaml": "s"})
	remoteRecord(t, remote,
		recordLine(0o600, "maps/secret.yaml"),
		recordLine(0o700, "maps/"),
		recordLine(0o700, "maps/storage/"),
		recordLine(0o700, "one.yaml"))

	answering.push(t.Context(), held)

	if got := modeOf(t, held.tree, "maps/secret.yaml"); got != 0o600 {
		t.Errorf("maps/secret.yaml is %v, want the mode upstream recorded", got)
	}
	if got := modeOf(t, held.tree, "maps"); got != 0o700 {
		t.Errorf("maps is %v, want the mode upstream recorded", got)
	}
	if got := modeOf(t, held.tree, "maps/storage"); got != 0o700 {
		t.Errorf("maps/storage is %v, want the empty directory upstream recorded", got)
	}
	if got := modeOf(t, held.tree, "one.yaml"); got != 0o600 {
		t.Errorf("one.yaml is %v, want the mode the pod set", got)
	}
}

func TestAMetadataRefTheRemoteRejectsGoesOnTopOfItAndLands(t *testing.T) {
	answering, held, remote := pushedVolume(t, io.Discard, nil)
	if err := os.Chmod(filepath.Join(held.tree, "one.yaml"), 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	answering.commit(t.Context(), held, held.policyNow())
	theirs := remoteRecord(t, remote, recordLine(0o600, "maps/secret.yaml"))

	answering.push(t.Context(), held)

	if got := strings.TrimSpace(git(t, remote, "log", "--format=%s", "-1", "main")); got != "Update 1 paths" {
		t.Errorf("the remote's main is at %q, want the driver's commit", got)
	}
	if got := git(t, remote, "rev-list", metadataRef); !strings.Contains(got, theirs) {
		t.Errorf("the remote's record history is %q, want the other writer's record in it", got)
	}
	record := git(t, remote, "show", "--no-textconv", metadataRef+":"+metadataFile)
	if !strings.Contains(record, "one.yaml") {
		t.Errorf("the remote's record is %q, want this volume's own record", record)
	}
	if got := len(eventsWithReason(t, answering, reasonPushFailed)); got != 0 {
		t.Errorf("the push posted %d failures, want none", got)
	}
	if got := len(eventsWithReason(t, answering, reasonPushed)); got != 2 {
		t.Errorf("the push posted %d events, want one on the pod and one on the claim", got)
	}
	if got := len(eventsWithReason(t, answering, reasonRebased)); got != 0 {
		t.Errorf("the push posted %d rebases, want none for a push that rebased nothing", got)
	}
}
