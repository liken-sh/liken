package main

import (
	"io"
	"os"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// waitForTrouble waits until the bubble is blocked, and fails unless
// the volume reports a failed fetch or push.
func waitForTrouble(t *testing.T, held *volume) {
	t.Helper()
	synctest.Wait()
	if _, trouble := held.condition(); trouble == "" {
		t.Fatal("the volume reports no failure")
	}
}

func TestARotatedCredentialFetchesAtOnceAfterAFailedFetch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		answering, _ := testNode(t, io.Discard)
		// A failed demand fetches again after a backoff that starts at
		// --demand-min-interval. An hour leaves the rotation as the only
		// thing that can fetch again within the test.
		answering.demandMin = time.Hour
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		staged := readOnlyStage(t, "franchises", fileURL(source), map[string]string{"pull": "on-demand"})
		staged.Secrets = secretA
		if _, err := answering.NodeStageVolume(t.Context(), staged); err != nil {
			t.Fatalf("NodeStageVolume: %v", err)
		}
		request := readOnlyPublish(t, staged, "reader-a")
		request.Secrets = secretA
		if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("NodePublishVolume: %v", err)
		}
		held := resumedVolume(answering, "franchises")

		// The remote that is gone stands for a remote that revoked the old
		// key: the demanded fetch fails, and the loop waits out its backoff.
		away := source + ".away"
		if err := os.Rename(source, away); err != nil {
			t.Fatalf("moving the remote away: %v", err)
		}
		answering.demand(held)
		waitForTrouble(t, held)
		passEnded(answering, fileURL(source))
		if err := os.Rename(away, source); err != nil {
			t.Fatalf("moving the remote back: %v", err)
		}
		commitFiles(t, source, map[string]string{"b.txt": "two"})

		request.Secrets = secretB
		if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("the republish: %v", err)
		}
		waitForFile(t, held.tree, "b.txt")
		waitForCondition(t, held, "main at")
	})
}

func TestARotatedCredentialPushesAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		answering, _ := testNode(t, io.Discard)
		// The sweep and the class's quiesce are an hour, so only the
		// rotation can push within the test.
		answering.sweep = time.Hour
		remote := bareRemote(t, map[string]string{"a.txt": "one"})
		boundVolume(t, answering, "config", "config-calm")
		armingClass(t, answering, "config-calm",
			map[string]string{quiesceParameter: "1h", maxLatencyParameter: neverLatency})
		staged := stageRequest(t, "config", fileURL(remote), nil)
		staged.Secrets = secretA
		if _, err := answering.NodeStageVolume(t.Context(), staged); err != nil {
			t.Fatalf("NodeStageVolume: %v", err)
		}
		request := persistentPublish(t, staged)
		request.Secrets = secretA
		if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("NodePublishVolume: %v", err)
		}
		held := resumedVolume(answering, "config")
		waitForArmed(t, held, true)
		// The commit stands for one whose push failed with the old key.
		writeFiles(t, held.tree, map[string]string{"one.yaml": "1"})
		answering.commit(t.Context(), held, held.policyNow())
		committed := held.work.refCommit(t.Context(), "HEAD")

		request.Secrets = secretB
		if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("the republish: %v", err)
		}
		waitForPushed(t, held)
		if got := strings.TrimSpace(git(t, remote, "rev-parse", "main")); got != committed {
			t.Errorf("the remote holds %s after the rotation, want %s", got, committed)
		}
	})
}
