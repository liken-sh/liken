package main

import (
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// waitForPushed waits until the bubble is blocked, and fails unless the
// volume reports a push. The remote's ref moves before git push
// returns, because the remote's receive-pack runs its maintenance after
// the ref update. A git process is not durably blocked, so
// synctest.Wait returns only after git push and that maintenance end,
// and the removal of the test's temporary directories finds no lock
// file.
func waitForPushed(t *testing.T, held *volume) {
	t.Helper()
	synctest.Wait()
	if _, last, _ := held.pushing(); last.IsZero() {
		_, report := held.report()
		t.Fatalf("the volume reports no push: %s", report)
	}
}

// waitForNoPush waits until the bubble is blocked, and fails if the
// volume reports a push.
func waitForNoPush(t *testing.T, held *volume) {
	t.Helper()
	synctest.Wait()
	if _, last, _ := held.pushing(); !last.IsZero() {
		t.Fatalf("the volume pushed at %s, before the quiesce ran out", last)
	}
}

// remoteSubject is the subject of the commit the remote's main holds.
func remoteSubject(t *testing.T, remote string) string {
	t.Helper()
	return strings.TrimSpace(git(t, remote, "log", "--format=%s", "-1", "main"))
}

// A write made while the driver was down sends no inotify event, so the
// watch that starts after the restart has to start the quiesce itself.
// The class arrives from the claim's own watch, 1s after the watch
// starts. The driver's own quiesce is what the timer takes until the
// class arrives. A short one runs out first and finds the volume
// unarmed, and a long one is still running when the class shortens it.
// The rest counts from the start of the watch in both cases, so the
// class's timer runs out 5s after the start and not 5s after the class
// arrived.
func TestAWriteMadeWhileTheDriverWasDownIsCommittedAfterTheClassQuiesce(t *testing.T) {
	for _, c := range []struct {
		name    string
		quiesce time.Duration
	}{
		{name: "a short quiesce of the driver's own", quiesce: 20 * time.Millisecond},
		{name: "a long quiesce of the driver's own", quiesce: time.Hour},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				answering, _ := testNode(t, io.Discard)
				remote := bareRemote(t, map[string]string{"a.txt": "one"})
				boundVolume(t, answering, "config", "")
				armingClass(t, answering, "config-eager",
					map[string]string{quiesceParameter: "5s"})
				held, _ := stagedWriteable(t, answering, "config", fileURL(remote))
				unwatched(t, answering, held)
				writeFiles(t, held.tree, map[string]string{"one.yaml": "1"})

				again, _ := testNode(t, io.Discard)
				again.store = answering.store
				again.events = answering.events
				again.arms.client = answering.arms.client
				again.mounted = func(string) bool { return true }
				again.quiesce = c.quiesce
				// The sweep is an hour, so only the quiesce can commit
				// within the test.
				again.sweep = time.Hour
				again.resume(t.Context())
				resumed := resumedVolume(again, "config")

				time.Sleep(time.Second)
				nameClass(t, answering, "config-eager")

				time.Sleep(4*time.Second - time.Nanosecond)
				waitForNoPush(t, resumed)
				time.Sleep(time.Nanosecond)
				waitForPushed(t, resumed)
				if got := remoteSubject(t, remote); got != "Update 1 paths" {
					t.Errorf("the remote's main is at %q, want %q", got, "Update 1 paths")
				}
			})
		})
	}
}

// A watch that starts on a tree whose class is already known starts the
// quiesce from that class, with no event and no class change to start
// it. A pod that starts on a tree written while no watch ran is the case.
func TestAWatchThatStartsOnAnArmedTreeCommitsAWriteThatSentNoEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		answering, _ := testNode(t, io.Discard)
		// The sweep is an hour, so only the quiesce can commit within
		// the test.
		answering.sweep = time.Hour
		remote := bareRemote(t, map[string]string{"a.txt": "one"})
		held := armedVolume(t, answering, "config", fileURL(remote),
			map[string]string{quiesceParameter: "5s"})
		unwatched(t, answering, held)
		writeFiles(t, held.tree, map[string]string{"one.yaml": "1"})

		answering.mu.Lock()
		answering.watch(held)
		answering.mu.Unlock()

		time.Sleep(5*time.Second - time.Nanosecond)
		waitForNoPush(t, held)
		time.Sleep(time.Nanosecond)
		waitForPushed(t, held)
		if got := remoteSubject(t, remote); got != "Update 1 paths" {
			t.Errorf("the remote's main is at %q, want %q", got, "Update 1 paths")
		}
	})
}
