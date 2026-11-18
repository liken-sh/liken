package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// committedVolume returns a node, a writeable volume whose claim's class
// has the parameters, and the path of the volume's bare remote. The
// volume has one commit that the remote does not have.
func committedVolume(t *testing.T, logs io.Writer, parameters map[string]string) (*node, *volume, string) {
	t.Helper()
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	n, _ := testNode(t, logs)
	vol := armedVolume(t, n, "config", fileURL(remote), parameters)
	unwatched(t, n, vol)
	writeFiles(t, vol.tree, map[string]string{"one.yaml": "1"})
	n.commit(t.Context(), vol, vol.policyNow())
	return n, vol, remote
}

func TestPushDueFollowsTheQuiesceAndTheMaximumLatency(t *testing.T) {
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
				t.Errorf("pushDue returned %v, want %v", got, c.want)
			}
		})
	}
}

func TestACommitTimeIsWholeSeconds(t *testing.T) {
	if got := commitTime("1757030400"); !got.Equal(time.Unix(1757030400, 0)) {
		t.Errorf("commitTime returned %s, want 1757030400 seconds after the epoch", got)
	}
	if got := commitTime("recently"); !got.IsZero() {
		t.Errorf("commitTime returned %s, want no time", got)
	}
}

func TestTheUnpushedCommitsAreTheOnesAfterPushedRef(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, vol, _ := committedVolume(t, io.Discard, nil)
		count, oldest, err := vol.work.unpushed(t.Context(), "main")
		if err != nil {
			t.Fatalf("unpushed: %v", err)
		}
		if count != 1 {
			t.Errorf("the work tree has %d unpushed commits, want 1", count)
		}
		if oldest.IsZero() {
			t.Error("the oldest unpushed commit has no time, want one")
		}
		n.push(t.Context(), vol)
		count, _, err = vol.work.unpushed(t.Context(), "main")
		if err != nil {
			t.Fatalf("unpushed: %v", err)
		}
		if count != 0 {
			t.Errorf("the work tree has %d unpushed commits after a push, want 0", count)
		}
	})
}

func TestAPushSendsTheBranchAndTheMetadataRef(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, vol, remote := committedVolume(t, io.Discard, nil)
		n.push(t.Context(), vol)

		if got := strings.TrimSpace(git(t, remote, "log", "--format=%s", "-1", "main")); got != "Update 1 paths" {
			t.Errorf("the remote's main is at %q, want the driver's commit", got)
		}
		if got := strings.TrimSpace(git(t, remote, "rev-parse", "--verify", metadataRef)); got == "" {
			t.Error("the remote has no metadata ref, want one")
		}
		pushed := eventsWithReason(t, n, reasonPushed)
		if len(pushed) != 2 {
			t.Fatalf("the push posted %v, want one Event on the pod and one on the claim", pushed)
		}
		if pushed[0].Message != "Pushed 1 commits to main at "+
			short(strings.TrimSpace(gitIn(t, vol.work, "rev-parse", "HEAD"))) {
			t.Errorf("the pushed event message is %q, want the commit count, the branch, and the short commit", pushed[0].Message)
		}
		n.metrics.record(vol)
		if got, found := gaugeOf(t, n.metrics,
			"git_csi_unpushed_commits", "home", "config"); !found || got != 0 {
			t.Errorf("the gauge reports %v unpushed commits, want 0", got)
		}
		if _, found := gaugeOf(t, n.metrics,
			"git_csi_last_push_timestamp_seconds", "home", "config"); !found {
			t.Error("the gauge has no last push time, want one")
		}
	})
}

func TestAVolumeThatHasNeverPushedReportsNoTime(t *testing.T) {
	registry := newMetrics()
	registry.record(reported(claimReference{namespace: "home", name: "config"}, true, 0))
	if _, found := gaugeOf(t, registry,
		"git_csi_last_push_timestamp_seconds", "home", "config"); found {
		t.Error("a volume that has never pushed reports a last push time, want none")
	}
}

func TestAFailedPushStaysInTheHealthReportUntilAPushSucceeds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := &logbook{}
		n, vol, remote := committedVolume(t, logs, nil)
		if err := os.RemoveAll(remote); err != nil {
			t.Fatalf("removing the remote: %v", err)
		}

		n.push(t.Context(), vol)
		abnormal, message := vol.health()
		if !abnormal || !strings.Contains(message, "push --quiet") {
			t.Errorf("the health report is %v, %q, want the failed push", abnormal, message)
		}
		if got := len(eventsWithReason(t, n, reasonPushFailed)); got != 2 {
			t.Fatalf("the failure posted %d events, want one on the pod and one on the claim", got)
		}
		n.push(t.Context(), vol)
		if got := len(eventsWithReason(t, n, reasonPushFailed)); got != 2 {
			t.Errorf("a second failure posted %d events, want the two of the first", got)
		}
		count, found := counterOf(t, n.metrics, "git_csi_push_failures_total", "home", "config")
		if !found || count != 2 {
			t.Errorf("the counter reports %v failures, want 2", count)
		}
		if !strings.Contains(logs.String(), "The push to the remote did not complete") {
			t.Errorf("the log is %q, want the failed push in it", logs)
		}
	})
}

// counterOf returns the value of one counter for the claim, and whether
// the counter has a series for it.
func counterOf(t *testing.T, registry *metrics, name, namespace, claim string) (float64, bool) {
	t.Helper()
	families, err := registry.registry.Gather()
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
	synctest.Test(t, func(t *testing.T) {
		n, vol, _ := committedVolume(t, io.Discard, nil)
		vol.credentials = &credentials{privateKey: "a key"}
		vol.directory = filepath.Join(t.TempDir(), "gone")

		n.push(t.Context(), vol)
		abnormal, message := vol.health()
		if !abnormal || message == "" {
			t.Errorf("the health report is %v, %q, want the failure", abnormal, message)
		}
	})
}

func TestAPushReportsWhatItCannotRead(t *testing.T) {
	for _, c := range []struct {
		name        string
		setup       func(t *testing.T, vol *volume)
		wantMessage string
	}{
		{
			name: "a work tree whose commits it cannot count",
			setup: func(t *testing.T, vol *volume) {
				if err := os.RemoveAll(vol.work.gitDir); err != nil {
					t.Fatalf("removing the git directory: %v", err)
				}
			},
			wantMessage: "The work tree's unpushed commits were not counted",
		},
		{
			name: "a pushedRef it cannot update",
			setup: func(t *testing.T, vol *volume) {
				if err := os.MkdirAll(filepath.Join(vol.work.gitDir,
					filepath.FromSlash(pushedRef)+".lock"), 0o755); err != nil {
					t.Fatalf("making the lock directory: %v", err)
				}
			},
			wantMessage: "The work tree's pushedRef was not updated",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				logs := &logbook{}
				n, vol, _ := committedVolume(t, logs, nil)
				c.setup(t, vol)
				n.push(t.Context(), vol)
				if !strings.Contains(logs.String(), c.wantMessage) {
					t.Errorf("the log is %q, want %q in it", logs, c.wantMessage)
				}
			})
		})
	}
}

func TestAPushReportsAHeadItCannotRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := &logbook{}
		n, vol, _ := committedVolume(t, logs, nil)
		if err := os.Remove(filepath.Join(vol.work.gitDir, "HEAD")); err != nil {
			t.Fatalf("removing HEAD: %v", err)
		}
		n.pushNow(t.Context(), vol, 1)
		if !strings.Contains(logs.String(), "The work tree's HEAD commit was not read") {
			t.Errorf("the log is %q, want the unreadable commit in it", logs)
		}
	})
}

func TestAnUnarmedVolumePushesNothingOnTheTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, vol, remote := committedVolume(t, io.Discard, nil)
		n.pushIfDue(t.Context(), vol, nil, time.Hour)
		if got := strings.TrimSpace(git(t, remote, "log", "--format=%s", "-1", "main")); got != "one" {
			t.Errorf("the remote's main is at %q, want the commit it started with", got)
		}
	})
}

func TestTheTimerPushesAfterTheQuiesce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, vol, remote := committedVolume(t, io.Discard, nil)
		n.pushIfDue(t.Context(), vol, vol.policyNow(), time.Hour)
		if got := strings.TrimSpace(git(t, remote, "log", "--format=%s", "-1", "main")); got != "Update 1 paths" {
			t.Errorf("the remote's main is at %q, want the driver's commit", got)
		}
	})
}

func TestTheTimerWaitsWhileTheTreeIsWritten(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, vol, remote := committedVolume(t, io.Discard, nil)
		n.pushIfDue(t.Context(), vol, vol.policyNow(), time.Second)
		if got := strings.TrimSpace(git(t, remote, "log", "--format=%s", "-1", "main")); got != "one" {
			t.Errorf("the remote's main is at %q, want the commit it started with", got)
		}
	})
}

func TestTheTimerReportsAWorkTreeItCannotCount(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := &logbook{}
		n, vol, _ := committedVolume(t, logs, nil)
		if err := os.RemoveAll(vol.work.gitDir); err != nil {
			t.Fatalf("removing the git directory: %v", err)
		}
		n.pushIfDue(t.Context(), vol, vol.policyNow(), time.Hour)
		if !strings.Contains(logs.String(), "The work tree's unpushed commits were not counted") {
			t.Errorf("the log is %q, want the failure in it", logs)
		}
	})
}

func TestAnOverdueCommitIsAbnormal(t *testing.T) {
	vol := &volume{
		attributes: &attributes{ref: "main"},
		kind:       writeableVolume,
		armed:      true,
		rules:      &policy{quiesce: 30 * time.Second, maxLatency: 5 * time.Minute},
		unpushed:   3,
		oldest:     time.Now().Add(-time.Hour),
	}
	abnormal, message := vol.health()
	if !abnormal || message != "3 unpushed commits, the oldest older than 5m0s" {
		t.Errorf("the health report is %v, %q, want the overdue commits", abnormal, message)
	}
}

func TestARejectedPushIsRebasedAndPushedToTheRef(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, vol, remote := committedVolume(t, io.Discard, nil)
		upstream := remoteCommit(t, remote, map[string]string{"maps/m.yaml": "m"})

		n.push(t.Context(), vol)

		want := map[string]string{"a.txt": "one", "one.yaml": "1", "maps/m.yaml": "m"}
		if got := readTree(t, vol.tree); !sameTree(got, want) {
			t.Errorf("the tree contains %v, want %v", got, want)
		}
		got := git(t, remote, "ls-tree", "-r", "--name-only", "main")
		if !strings.Contains(got, "one.yaml") || !strings.Contains(got, "maps/m.yaml") {
			t.Errorf("main on the remote has %q, want both writers' files", got)
		}
		if branches := branchesOn(t, remote); strings.Contains(branches, "main.config") {
			t.Errorf("the remote has %q, want no side branch", branches)
		}
		if branch := vol.sideBranch(); branch != "" {
			t.Errorf("the volume pushes to %q, want the ref", branch)
		}
		rebased := eventsWithReason(t, n, reasonRebased)
		if len(rebased) != 2 {
			t.Fatalf("the rebase posted %v, want one Event on the pod and one on the claim", rebased)
		}
		message := fmt.Sprintf("Rebased 1 commits onto %s and pushed them to main", short(upstream))
		if rebased[0].Message != message {
			t.Errorf("the Event message is %q, want %q", rebased[0].Message, message)
		}
		head := vol.work.refCommit(t.Context(), "HEAD")
		if mark := vol.work.refCommit(t.Context(), pushedRef); mark != head {
			t.Errorf("pushedRef is at %s, want the rebased head %s", mark, head)
		}
	})
}

// declineHook is a pre-receive hook that rejects every push to main and
// appends each attempt to a file. It simulates a remote where a writer
// loses the race on every push.
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

// declineMain installs declineHook on the remote and returns the path of
// the file that records the rejected pushes.
func declineMain(t *testing.T, remote string) string {
	t.Helper()
	declined := filepath.Join(t.TempDir(), "declined")
	hook := filepath.Join(remote, "hooks", "pre-receive")
	if err := os.WriteFile(hook, fmt.Appendf(nil, declineHook, declined), 0o755); err != nil {
		t.Fatalf("writing the hook: %v", err)
	}
	return declined
}

// acceptMain removes the hook, so the remote accepts pushes again.
func acceptMain(t *testing.T, remote string) {
	t.Helper()
	if err := os.Remove(filepath.Join(remote, "hooks", "pre-receive")); err != nil {
		t.Fatalf("removing the hook: %v", err)
	}
}

// rejectedPushes counts the pushes that declineHook rejected.
func rejectedPushes(t *testing.T, declined string) int {
	t.Helper()
	content, err := os.ReadFile(declined)
	if err != nil {
		t.Fatalf("reading the refused pushes: %v", err)
	}
	return len(strings.Fields(strings.TrimSpace(string(content))))
}

func TestThreeRejectedPushesInARowDivergeToTheSideBranch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, vol, remote := committedVolume(t, io.Discard, nil)
		declined := declineMain(t, remote)

		n.push(t.Context(), vol)

		if branch := vol.sideBranch(); branch != "main.config" {
			t.Errorf("the volume pushes to %q, want main.config", branch)
		}
		if branches := branchesOn(t, remote); !strings.Contains(branches, "main.config") {
			t.Errorf("the remote has %q, want the side branch on it", branches)
		}
		if tried := rejectedPushes(t, declined); tried != 1+rebaseAttempts {
			t.Errorf("the forge refused %d pushes, want %d", tried, 1+rebaseAttempts)
		}
		if rebased := eventsWithReason(t, n, reasonRebased); len(rebased) != 0 {
			t.Errorf("the volume posted %v, want no rebase Event for a push that did not complete", rebased)
		}
	})
}

func TestARebaseThatConflictsAfterARejectedPushDivergesToTheSideBranch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, vol, remote := committedVolume(t, io.Discard, nil)
		remoteCommit(t, remote, map[string]string{"one.yaml": "the forge wrote this"})

		n.push(t.Context(), vol)

		want := map[string]string{"a.txt": "one", "one.yaml": "1"}
		if got := readTree(t, vol.tree); !sameTree(got, want) {
			t.Errorf("the tree contains %v, want %v", got, want)
		}
		if branch := vol.sideBranch(); branch != "main.config" {
			t.Errorf("the volume pushes to %q, want main.config", branch)
		}
		if _, err := os.Stat(filepath.Join(vol.directory, scratchTree)); !os.IsNotExist(err) {
			t.Errorf("the volume directory has a scratch work tree: %v", err)
		}
	})
}

func TestAPathThePodAndUpstreamBothWroteDivergesToTheSideBranch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, vol, remote := committedVolume(t, io.Discard, nil)
		remoteCommit(t, remote, map[string]string{"a.txt": "the forge wrote this"})
		writeFiles(t, vol.tree, map[string]string{"a.txt": "the pod is writing"})

		n.push(t.Context(), vol)

		want := map[string]string{"a.txt": "the pod is writing", "one.yaml": "1"}
		if got := readTree(t, vol.tree); !sameTree(got, want) {
			t.Errorf("the tree contains %v, want %v", got, want)
		}
		if branch := vol.sideBranch(); branch != "main.config" {
			t.Errorf("the volume pushes to %q, want main.config", branch)
		}
		if got := git(t, remote, "ls-tree", "-r", "--name-only", "main.config"); !strings.Contains(got, "one.yaml") {
			t.Errorf("the side branch has %q, want the pod's work on it", got)
		}
	})
}

func TestAPathUpstreamDidNotWriteSurvivesTheRebase(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, vol, remote := committedVolume(t, io.Discard, nil)
		remoteCommit(t, remote, map[string]string{"maps/m.yaml": "m"})
		writeFiles(t, vol.tree, map[string]string{"draft.yaml": "unsaved"})

		n.push(t.Context(), vol)

		want := map[string]string{
			"a.txt": "one", "one.yaml": "1", "maps/m.yaml": "m", "draft.yaml": "unsaved",
		}
		if got := readTree(t, vol.tree); !sameTree(got, want) {
			t.Errorf("the tree contains %v, want %v", got, want)
		}
		if branch := vol.sideBranch(); branch != "" {
			t.Errorf("the volume pushes to %q, want the ref", branch)
		}
	})
}

func TestARetryThatCannotReachTheRemoteDoesNotRebase(t *testing.T) {
	for _, c := range []struct {
		name        string
		setup       func(t *testing.T, n *node, vol *volume, remote string)
		wantMessage string
	}{
		{
			name: "a credential it cannot write",
			setup: func(_ *testing.T, _ *node, vol *volume, _ string) {
				vol.credentials = &credentials{privateKey: "a key"}
				vol.directory = filepath.Join(vol.directory, "gone")
			},
			wantMessage: "The ref was not fetched from the remote",
		},
		{
			name: "a fetch the store cannot hold",
			setup: func(t *testing.T, n *node, vol *volume, _ string) {
				where := filepath.Join(n.store.repository(vol.attributes.url).dir,
					"FETCH_HEAD")
				if err := os.Remove(where); err != nil {
					t.Fatalf("removing the fetch record: %v", err)
				}
				if err := os.MkdirAll(where, 0o755); err != nil {
					t.Fatalf("making the fetch directory: %v", err)
				}
			},
			wantMessage: "The ref was not fetched from the remote",
		},
		{
			name: "a record it cannot put on top of the remote's",
			setup: func(t *testing.T, _ *node, vol *volume, remote string) {
				remoteRecord(t, remote, recordLine(0o600, "maps/secret.yaml"))
				if err := os.MkdirAll(filepath.Join(vol.work.gitDir,
					filepath.FromSlash(metadataRef)+".lock"), 0o755); err != nil {
					t.Fatalf("making the lock directory: %v", err)
				}
			},
			wantMessage: "The metadata record was not rebased onto the remote's record",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				logs := &logbook{}
				n, vol, remote := committedVolume(t, logs, nil)
				remoteCommit(t, remote, map[string]string{"maps/m.yaml": "m"})
				c.setup(t, n, vol, remote)

				if _, pushed := n.rebaseAndRetry(t.Context(), vol, 1); pushed {
					t.Error("the retry reported a push that completed")
				}
				if !strings.Contains(logs.String(), c.wantMessage) {
					t.Errorf("the log is %q, want %q in it", logs, c.wantMessage)
				}
			})
		})
	}
}

// recordLine formats one line of the metadata record, as the driver
// writes and reads it.
func recordLine(mode int, path string) string {
	return fmt.Sprintf("%04o %d %d %s", mode, os.Getuid(), os.Getgid(), path)
}

// remoteRecord creates a metadata commit on the remote, as another
// writer's push would, and returns its hash.
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

// modeOf returns the current file mode of path in the tree.
func modeOf(t *testing.T, tree, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(filepath.Join(tree, filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

func TestARebaseAppliesUpstreamModesAndKeepsThePodsModes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, vol, remote := committedVolume(t, io.Discard, nil)
		if err := os.Chmod(filepath.Join(vol.tree, "one.yaml"), 0o600); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		remoteCommit(t, remote, map[string]string{"maps/secret.yaml": "s"})
		remoteRecord(t, remote,
			recordLine(0o600, "maps/secret.yaml"),
			recordLine(0o700, "maps/"),
			recordLine(0o700, "maps/storage/"),
			recordLine(0o700, "one.yaml"))

		n.push(t.Context(), vol)

		if got := modeOf(t, vol.tree, "maps/secret.yaml"); got != 0o600 {
			t.Errorf("maps/secret.yaml is %v, want the mode upstream recorded", got)
		}
		if got := modeOf(t, vol.tree, "maps"); got != 0o700 {
			t.Errorf("maps is %v, want the mode upstream recorded", got)
		}
		if got := modeOf(t, vol.tree, "maps/storage"); got != 0o700 {
			t.Errorf("maps/storage is %v, want the empty directory upstream recorded", got)
		}
		if got := modeOf(t, vol.tree, "one.yaml"); got != 0o600 {
			t.Errorf("one.yaml is %v, want the mode the pod set", got)
		}
	})
}

func TestAMetadataRefTheRemoteRejectsIsRebasedAndPushed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, vol, remote := committedVolume(t, io.Discard, nil)
		if err := os.Chmod(filepath.Join(vol.tree, "one.yaml"), 0o600); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		n.commit(t.Context(), vol, vol.policyNow())
		theirs := remoteRecord(t, remote, recordLine(0o600, "maps/secret.yaml"))

		n.push(t.Context(), vol)

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
		if got := len(eventsWithReason(t, n, reasonPushFailed)); got != 0 {
			t.Errorf("the push posted %d failures, want none", got)
		}
		if got := len(eventsWithReason(t, n, reasonPushed)); got != 2 {
			t.Errorf("the push posted %d events, want one on the pod and one on the claim", got)
		}
		if got := len(eventsWithReason(t, n, reasonRebased)); got != 0 {
			t.Errorf("the push posted %d rebases, want none for a push that rebased nothing", got)
		}
	})
}
