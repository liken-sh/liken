package main

import (
	"io"
	"strings"
	"testing"
	"time"
)

// waitForRemoteSubject waits until the remote's main carries a commit
// with the subject, or fails on the deadline.
func waitForRemoteSubject(t *testing.T, remote, subject string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if strings.TrimSpace(git(t, remote, "log", "--format=%s", "-1", "main")) == subject {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the remote's main is at %q within %s, want %q", strings.TrimSpace(
		git(t, remote, "log", "--format=%s", "-1", "main")), within, subject)
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

			waitForRemoteSubject(t, remote, "Update 1 paths", 20*time.Second)
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

	waitForRemoteSubject(t, remote, "Update 1 paths", 20*time.Second)
}
