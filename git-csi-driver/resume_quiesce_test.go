package main

import (
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// waitForPushed waits until the bubble is blocked, and fails unless the
// volume reports a push. The test waits for synctest.Wait and not for the
// remote's ref, because receive-pack updates the ref and then runs its
// maintenance, which can hold a lock file while the test removes its
// temporary directories. synctest.Wait does not return while a git
// process runs, so it returns after that maintenance ends.
func waitForPushed(t *testing.T, vol *volume) {
	t.Helper()
	synctest.Wait()
	if _, last, _ := vol.pushing(); last.IsZero() {
		_, report := vol.health()
		t.Fatalf("the volume report is %q, want a push", report)
	}
}

// waitForNoPush waits until the bubble is blocked, and fails if the
// volume reports a push.
func waitForNoPush(t *testing.T, vol *volume) {
	t.Helper()
	synctest.Wait()
	if _, last, _ := vol.pushing(); !last.IsZero() {
		t.Fatalf("the volume pushed at %s, want no push before the quiesce ran out", last)
	}
}

// remoteSubject returns the subject of the commit at the remote's main.
func remoteSubject(t *testing.T, remote string) string {
	t.Helper()
	return strings.TrimSpace(git(t, remote, "log", "--format=%s", "-1", "main"))
}

// A write made while the driver was down sends no inotify event, so the
// watch that starts after the restart must start the quiesce itself.
// The class arrives 1s after the watch starts, and until then the timer
// runs with the driver's default quiesce. A default shorter than 1s ends
// first and finds the volume unarmed. A longer default is shortened when
// the class arrives. In both cases the quiesce counts from the start of
// the watch, so the push happens 5s after the start, not 5s after the
// class arrives.
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
				n, _ := testNode(t, io.Discard)
				remote := bareRemote(t, map[string]string{"a.txt": "one"})
				boundVolume(t, n, "config", "")
				createClass(t, n, "config-eager",
					map[string]string{quiesceParameter: "5s"})
				vol, _ := stagedWriteable(t, n, "config", fileURL(remote))
				unwatched(t, n, vol)
				writeFiles(t, vol.tree, map[string]string{"one.yaml": "1"})

				again, _ := testNode(t, io.Discard)
				again.store = n.store
				again.events = n.events
				again.arming.client = n.arming.client
				again.mounted = func(string) bool { return true }
				again.quiesce = c.quiesce
				// The sweep is an hour, so only the quiesce can commit
				// within the test.
				again.sweep = time.Hour
				again.resume(t.Context())
				resumed := resumedVolume(again, "config")

				time.Sleep(time.Second)
				nameClass(t, n, "config-eager")

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

// A pod can start on a tree that was written while no watch ran. The
// watch then starts the quiesce with the volume's class, with no inotify
// event and no change of class to restart it.
func TestAWatchThatStartsOnAnArmedTreeCommitsAWriteThatSentNoEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		// The sweep is an hour, so only the quiesce can commit within
		// the test.
		n.sweep = time.Hour
		remote := bareRemote(t, map[string]string{"a.txt": "one"})
		vol := armedVolume(t, n, "config", fileURL(remote),
			map[string]string{quiesceParameter: "5s"})
		unwatched(t, n, vol)
		writeFiles(t, vol.tree, map[string]string{"one.yaml": "1"})

		n.mu.Lock()
		n.watch(vol)
		n.mu.Unlock()

		time.Sleep(5*time.Second - time.Nanosecond)
		waitForNoPush(t, vol)
		time.Sleep(time.Nanosecond)
		waitForPushed(t, vol)
		if got := remoteSubject(t, remote); got != "Update 1 paths" {
			t.Errorf("the remote's main is at %q, want %q", got, "Update 1 paths")
		}
	})
}
