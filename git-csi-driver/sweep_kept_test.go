package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// keptLines counts the lines where the sweep says it kept a work tree
// for its unpushed commits.
func keptLines(logs *logbook) int {
	return strings.Count(logs.String(), "the work tree holds unpushed commits")
}

// keptTree leaves a work tree the sweep must keep: unstaged past the
// sweep age, with a commit the remote does not hold. withRepository
// false removes the alternates file, so the tree names no repository
// and nothing records it for the next stage.
func keptTree(t *testing.T, answering *node, remote string, withRepository bool) *volume {
	t.Helper()
	held := unstagedVolume(t, answering, "config", fileURL(remote))
	unstagedAgo(t, answering, "config", 2*time.Hour)
	driverCommit(t, answering, held, map[string]string{"c.txt": "three"})
	if !withRepository {
		if err := os.Remove(filepath.Join(held.work.gitDir, alternatesFile)); err != nil {
			t.Fatalf("removing the alternates file: %v", err)
		}
	}
	return held
}

func TestTheSweepLogsAKeptWorkTreeOnce(t *testing.T) {
	for _, tc := range []struct {
		name           string
		withRepository bool
	}{
		{"with its repository", true},
		{"with no repository", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := &logbook{}
			answering, remote := sweepingNode(t, logs)
			keptTree(t, answering, remote, tc.withRepository)

			answering.sweepStore(t.Context())
			answering.sweepStore(t.Context())
			answering.sweepStore(t.Context())

			if got := keptLines(logs); got != 1 {
				t.Errorf("the sweep logged the kept tree %d times, want 1", got)
			}
		})
	}
}

func TestTheSweepLogsAKeptWorkTreeAgainAfterALaterUnstage(t *testing.T) {
	logs := &logbook{}
	answering, remote := sweepingNode(t, logs)
	keptTree(t, answering, remote, true)
	answering.sweepStore(t.Context())

	unstagedAgo(t, answering, "config", 3*time.Hour)
	answering.sweepStore(t.Context())

	if got := keptLines(logs); got != 2 {
		t.Errorf("the sweep logged the kept tree %d times, want 2", got)
	}
}
