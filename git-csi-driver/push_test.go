package main

import (
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
		writeable:  true,
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
