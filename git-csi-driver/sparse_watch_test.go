package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	kevents "github.com/liken-sh/liken/kubernetes/events"
	"golang.org/x/sys/unix"
)

func TestTheEventsNameThePathAndWhetherItWasAWrite(t *testing.T) {
	w := &watcher{
		volume:  &volume{tree: "/store/tree"},
		watched: map[int32]string{7: "/store/tree", 8: "/store/tree/app"},
	}
	for _, c := range []struct {
		name string
		raw  []byte
		want inotifyEvent
	}{
		{
			name: "a file written at the root",
			raw:  inotifyRecord(7, unix.IN_CLOSE_WRITE, "log.txt"),
			want: inotifyEvent{path: "log.txt", known: true, write: true},
		},
		{
			name: "a file moved into a directory",
			raw:  inotifyRecord(8, unix.IN_MOVED_TO, "a.yaml"),
			want: inotifyEvent{path: "app/a.yaml", known: true, write: true},
		},
		{
			name: "a directory created",
			raw:  inotifyRecord(8, unix.IN_CREATE|unix.IN_ISDIR, "sub"),
			want: inotifyEvent{directory: "/store/tree/app/sub", path: "app/sub", known: true},
		},
		{
			name: "a file deleted",
			raw:  inotifyRecord(8, unix.IN_DELETE, "a.yaml"),
			want: inotifyEvent{path: "app/a.yaml", known: true},
		},
		{
			name: "dropped events",
			raw:  inotifyRecord(-1, unix.IN_Q_OVERFLOW, ""),
			want: inotifyEvent{},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			found := w.events(c.raw)
			if len(found) != 1 || found[0] != c.want {
				t.Errorf("events returned %+v, want %+v", found, c.want)
			}
		})
	}
}

func TestAVolumeRecordsEachPathOutsideItsPathsOnce(t *testing.T) {
	vol := &volume{}
	if got := vol.reportOutside([]string{"log.txt", "log.txt", "README.md"}); len(got) != 2 {
		t.Errorf("the first writes returned %q, want log.txt and README.md", got)
	}
	if got := vol.reportOutside([]string{"log.txt"}); len(got) != 0 {
		t.Errorf("a second write returned %q, want nothing new", got)
	}
	if _, _, skipped := vol.pushing(); skipped != 2 {
		t.Errorf("the volume counts %d skipped files, want 2", skipped)
	}

	vol.forgetOutside(func(name string) bool { return name == "README.md" })

	if _, _, skipped := vol.pushing(); skipped != 1 {
		t.Errorf("after log.txt was deleted the volume counts %d skipped files, want 1", skipped)
	}
	if got := vol.reportOutside([]string{"log.txt"}); len(got) != 1 {
		t.Errorf("a write to the deleted log.txt returned %q, want it new again", got)
	}
}

// moveFileInto writes a file outside the tree and renames it into the
// tree. The rename is one inotify event for a file, so the watch reads
// it in one batch.
func moveFileInto(t *testing.T, tree, name, content string) {
	t.Helper()
	outside := filepath.Join(t.TempDir(), "file")
	writeFiles(t, filepath.Dir(outside), map[string]string{"file": content})
	if err := os.Rename(outside, filepath.Join(tree, filepath.FromSlash(name))); err != nil {
		t.Fatalf("moving %s into the tree: %v", name, err)
	}
}

// awaitMessage waits until the node has posted an Event with the reason
// and the message, and returns it.
func awaitMessage(t *testing.T, n *node, reason, message string) kevents.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), inotifyTimeout)
	defer cancel()
	target := recordingOf(t, n.events)
	for {
		for _, posted := range target.stored.List() {
			if posted.Reason == reason && posted.Message == message {
				return posted
			}
		}
		select {
		case <-target.republishWake:
		case <-ctx.Done():
			t.Fatalf("the node posted no %s Event %q within %s", reason, message, inotifyTimeout)
		}
	}
}

// sinceWrite returns how long ago the watch of the volume recorded a
// write on its clock.
func sinceWrite(n *node, id string) time.Duration {
	n.mu.Lock()
	w := n.watchers[id]
	n.mu.Unlock()
	return w.sinceWrite()
}

func TestAWriteOutsideThePathsPostsAnEventAndLeavesTheQuiesce(t *testing.T) {
	n, clock := inotifyNode(t)
	remote := appRemote(t)
	vol, _ := stagedPaths(t, n, "config", fileURL(remote), "app-a")
	clock.advance(time.Second)

	for _, name := range []string{"log.txt", "README.md"} {
		moveFileInto(t, vol.tree, name, "outside")
		awaitMessage(t, n, reasonSkipped,
			`Skipped "`+name+`" because it is outside the volume's paths "app-a"`)
	}

	// The watch records a write before it posts the Event of the same
	// batch, so a write outside the paths that restarted the quiesce
	// would show here.
	if got := sinceWrite(n, "config"); got != time.Second {
		t.Errorf("the watch last recorded a write %s ago, want 1s, from the start of the watch", got)
	}
	if found, _ := vol.work.pending(t.Context()); len(found) != 0 {
		t.Errorf("the tree has %v pending, want nothing", found)
	}
	if _, _, skipped := vol.pushing(); skipped != 2 {
		t.Errorf("the volume counts %d skipped files, want 2", skipped)
	}

	moveFileInto(t, vol.tree, "app-a/new.yaml", "inside")
	clock.waitForTimer(t, time.Second+n.quiesce)
}
