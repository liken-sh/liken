package main

import (
	"io"
	"os"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// waitForFault waits until every goroutine in the bubble is blocked,
// then fails the test unless the volume reports a fault.
func waitForFault(t *testing.T, vol *volume) {
	t.Helper()
	synctest.Wait()
	if _, fault := vol.commitAndFault(); fault == "" {
		t.Fatal("the volume reports no fault, want one")
	}
}

func TestARotatedCredentialFetchesAtOnceAfterAFailedFetch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		// With --demand-min-interval at an hour, a new demand cannot
		// pull during the test, and the retry of the failed fetch waits
		// maxDemandRetry. The bubble's clock does not reach either, so
		// only the republish with the new credential can fetch b.txt.
		n.demandMin = time.Hour
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		staged := readOnlyStage(t, "franchises", fileURL(source), map[string]string{"pull": "on-demand"})
		staged.Secrets = secretA
		if _, err := n.NodeStageVolume(t.Context(), staged); err != nil {
			t.Fatalf("staging the volume: %v", err)
		}
		request := readOnlyPublish(t, staged, "reader-a")
		request.Secrets = secretA
		if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("publishing the volume: %v", err)
		}
		vol := resumedVolume(n, "franchises")

		// Moving the remote away makes the demanded fetch fail, the way
		// it fails when the remote has revoked the old key.
		away := source + ".away"
		if err := os.Rename(source, away); err != nil {
			t.Fatalf("moving the remote away: %v", err)
		}
		n.demand(vol)
		waitForFault(t, vol)
		passEnded(n, fileURL(source))
		if err := os.Rename(away, source); err != nil {
			t.Fatalf("moving the remote back: %v", err)
		}
		commitFiles(t, source, map[string]string{"b.txt": "two"})

		request.Secrets = secretB
		if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("publishing the volume again with the new credential: %v", err)
		}
		waitForFile(t, vol.tree, "b.txt")
		waitForCondition(t, vol, "main at")
	})
}

func TestARotatedCredentialPushesAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		// With the sweep and push.quiesce at an hour and push.maxLatency
		// at never, only the republish with the new credential can start
		// a push.
		n.sweep = time.Hour
		remote := bareRemote(t, map[string]string{"a.txt": "one"})
		boundVolume(t, n, "config", "config-calm")
		createClass(t, n, "config-calm",
			map[string]string{quiesceParameter: "1h", maxLatencyParameter: neverLatency})
		staged := stageRequest(t, "config", fileURL(remote), nil)
		staged.Secrets = secretA
		if _, err := n.NodeStageVolume(t.Context(), staged); err != nil {
			t.Fatalf("staging the volume: %v", err)
		}
		request := persistentPublish(t, staged)
		request.Secrets = secretA
		if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("publishing the volume: %v", err)
		}
		vol := resumedVolume(n, "config")
		waitForArmed(t, vol, true)
		// n.commit does not push, so the remote lacks this commit, as it
		// would after a push with the old key failed.
		writeFiles(t, vol.tree, map[string]string{"one.yaml": "1"})
		n.commit(t.Context(), vol, vol.policyNow())
		committed := vol.work.refCommit(t.Context(), "HEAD")

		request.Secrets = secretB
		if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("publishing the volume again with the new credential: %v", err)
		}
		waitForPushed(t, vol)
		if got := strings.TrimSpace(git(t, remote, "rev-parse", "main")); got != committed {
			t.Errorf("the remote main is at %s after the credential rotation, want %s", got, committed)
		}
	})
}
