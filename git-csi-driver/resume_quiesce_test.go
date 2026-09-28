package main

import (
	"io"
	"strings"
	"testing"
	"time"
)

// waitForPushed waits until the volume reports a push, or fails on the
// deadline. The remote's ref moves before git push returns, because the
// remote's receive-pack runs its maintenance after the ref update, and
// the maintenance writes a lock file in the remote's objects directory.
// A test that ends when the ref moves races that write, and the removal
// of the test's temporary directories fails. The volume reports the push
// only after git push returns, so the push writes nothing in the remote
// when this wait ends.
func waitForPushed(t *testing.T, held *volume, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if _, last, _ := held.pushing(); !last.IsZero() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, report := held.report()
	t.Fatalf("the volume reports no push within %s: %s", within, report)
}

// remoteSubject is the subject of the commit the remote's main holds.
func remoteSubject(t *testing.T, remote string) string {
	t.Helper()
	return strings.TrimSpace(git(t, remote, "log", "--format=%s", "-1", "main"))
}

// A write made while the driver was down sends no inotify event, so the
// watch that starts after the restart has to start the quiesce itself.
// The class arrives after the watch starts, from the claim's own watch.
// The driver's own quiesce is what the timer takes until the class
// arrives. A short one can run out first and find the volume unarmed,
// and a long one is still running when the class shortens it. The order
// depends on how fast the claim's watch delivers the class, so the two
// cases name the quiesce and not the order.
func TestAWriteMadeWhileTheDriverWasDownIsCommittedAfterTheClassQuiesce(t *testing.T) {
	for _, c := range []struct {
		name    string
		quiesce time.Duration
	}{
		{name: "a short quiesce of the driver's own", quiesce: 20 * time.Millisecond},
		{name: "a long quiesce of the driver's own", quiesce: time.Hour},
	} {
		t.Run(c.name, func(t *testing.T) {
			answering, _ := testNode(t, io.Discard)
			remote := bareRemote(t, map[string]string{"a.txt": "one"})
			held := armedVolume(t, answering, "config", fileURL(remote),
				map[string]string{quiesceParameter: "5s"})
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

			waitForPushed(t, resumedVolume(again, "config"), 20*time.Second)
			if got := remoteSubject(t, remote); got != "Update 1 paths" {
				t.Errorf("the remote's main is at %q, want %q", got, "Update 1 paths")
			}
		})
	}
}

// A watch that starts on a tree whose class is already known starts the
// quiesce from that class, with no event and no class change to start
// it. A pod that starts on a tree written while no watch ran is the case.
func TestAWatchThatStartsOnAnArmedTreeCommitsAWriteThatSentNoEvent(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	// The sweep is an hour, so only the quiesce can commit within the
	// test.
	answering.sweep = time.Hour
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	held := armedVolume(t, answering, "config", fileURL(remote),
		map[string]string{quiesceParameter: "5s"})
	unwatched(t, answering, held)
	writeFiles(t, held.tree, map[string]string{"one.yaml": "1"})

	answering.mu.Lock()
	answering.watch(held)
	answering.mu.Unlock()

	waitForPushed(t, held, 20*time.Second)
	if got := remoteSubject(t, remote); got != "Update 1 paths" {
		t.Errorf("the remote's main is at %q, want %q", got, "Update 1 paths")
	}
}
