package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	kevents "github.com/liken-sh/liken/kubernetes/events"
	"golang.org/x/sys/unix"
)

// logbook is a log that a test reads while the driver's goroutines
// write it.
type logbook struct {
	mu      sync.Mutex
	written bytes.Buffer
}

func (l *logbook) Write(line []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.written.Write(line)
}

func (l *logbook) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.written.String()
}

// waitForPending waits until every goroutine in the bubble is blocked,
// then fails unless the volume reports want uncommitted paths.
func waitForPending(t *testing.T, vol *volume, want int) {
	t.Helper()
	synctest.Wait()
	if _, _, count := vol.gaugeValues(); count != want {
		t.Fatalf("the volume has %d pending paths, want %d", count, want)
	}
}

func TestTheSweepReadsATreeTheWatchMissed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		// The quiesce is longer than the test, so only the sweep can scan.
		n.quiesce = 30 * time.Second
		n.sweep = 20 * time.Millisecond
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		published, _ := stagedWriteable(t, n, "config", fileURL(source))

		writeFiles(t, published.tree, map[string]string{"one.txt": "1"})
		time.Sleep(n.sweep)
		waitForPending(t, published, 1)
	})
}

func TestTheWatchPostsOneEventWhenTheTreeFirstHasUncommittedWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		n.sweep = 20 * time.Millisecond
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		published, _ := stagedWriteable(t, n, "config", fileURL(source))

		writeFiles(t, published.tree, map[string]string{"one.txt": "1"})
		time.Sleep(n.sweep)
		waitForPending(t, published, 1)
		// Five more sweeps run, and the Event is still posted once.
		time.Sleep(5 * n.sweep)
		synctest.Wait()

		pending := []kevents.Event{}
		for _, posted := range eventsOf(t, n) {
			if posted.Reason == reasonPending {
				pending = append(pending, posted)
			}
		}
		if len(pending) != 1 {
			t.Fatalf("the watch posted %v, want one pending event", pending)
		}
		if pending[0].Message != "1 paths pending" || pending[0].InvolvedObject.Name != "writer" {
			t.Errorf("the event is %q on %q", pending[0].Message, pending[0].InvolvedObject.Name)
		}
	})
}

func TestTheWatchRunsWithNoInotify(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := &logbook{}
		n, _ := testNode(t, logs)
		n.inotify = func(int) (int, error) { return 0, errors.New("no inotify here") }
		n.sweep = 20 * time.Millisecond
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		published, _ := stagedWriteable(t, n, "config", fileURL(source))

		writeFiles(t, published.tree, map[string]string{"one.txt": "1"})
		time.Sleep(n.sweep)
		waitForPending(t, published, 1)
		if !strings.Contains(logs.String(), "The inotify instance was refused") {
			t.Errorf("the log is %q, want the refused watch in it", logs)
		}
	})
}

func TestTheWatchReportsADirectoryItCannotAdd(t *testing.T) {
	logs := &logbook{}
	n, _ := testNode(t, logs)
	w := &watcher{
		node:       n,
		volume:     &volume{id: "config", tree: t.TempDir()},
		descriptor: -1,
		watched:    map[int32]string{},
	}
	w.add(t.Context(), w.volume.tree)
	if !strings.Contains(logs.String(), "The inotify watches do not cover every directory") {
		t.Errorf("the log is %q, want the refused watch in it", logs)
	}
}

func TestTheWatchReportsATreeItCannotRead(t *testing.T) {
	logs := &logbook{}
	n, _ := testNode(t, logs)
	holder := newStore(t.TempDir())
	w := &watcher{
		node:   n,
		clock:  n.clock,
		volume: &volume{id: "config", work: holder.workTree(holder.repository("file:///gone"), "config", nil)},
	}
	w.scan(t.Context())
	if !strings.Contains(logs.String(), "The pending paths of the volume tree were not read") {
		t.Errorf("the log is %q, want the failed read in it", logs)
	}
}

func TestTheEventsIncludeTheDirectoryThePodCreated(t *testing.T) {
	w := &watcher{volume: &volume{tree: "/store/tree"}, watched: map[int32]string{7: "/store/tree"}}
	buffer := inotifyRecord(7, unix.IN_CREATE|unix.IN_ISDIR, "sub")
	buffer = append(buffer, inotifyRecord(7, unix.IN_MODIFY, "a.txt")...)
	buffer = append(buffer, inotifyRecord(7, unix.IN_MODIFY, "")...)

	found := w.events(buffer)
	if len(found) != 3 {
		t.Fatalf("events returned %v, want three", found)
	}
	if found[0].directory != "/store/tree/sub" {
		t.Errorf("the first event names %q, want the new directory", found[0].directory)
	}
	if found[1].directory != "" || found[2].directory != "" {
		t.Errorf("events returned %v, want no directory for a write", found)
	}
}

// inotifyRecord encodes one event as the kernel writes it, with the
// name zero-padded to a multiple of 8 bytes.
func inotifyRecord(descriptor int32, mask uint32, name string) []byte {
	padded := []byte{}
	if name != "" {
		padded = append([]byte(name), 0)
		for len(padded)%8 != 0 {
			padded = append(padded, 0)
		}
	}
	record := make([]byte, unix.SizeofInotifyEvent)
	binary.NativeEndian.PutUint32(record[0:], uint32(descriptor))
	binary.NativeEndian.PutUint32(record[4:], mask)
	binary.NativeEndian.PutUint32(record[8:], 0)
	binary.NativeEndian.PutUint32(record[12:], uint32(len(padded)))
	return append(record, padded...)
}

func TestUnwatchPassesOverAVolumeItNeverWatched(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	n.mu.Lock()
	defer n.mu.Unlock()
	n.unwatch(&volume{id: "csi-9"})
	n.watch(&volume{id: "csi-9"})
	if got := len(n.watchers); got != 0 {
		t.Errorf("the node has %d watches of a read-only volume, want 0", got)
	}
}

func TestTheClassSetsTheQuiesceWithNoRemount(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		armedVolume(t, n, "config",
			fileURL(bareRemote(t, map[string]string{"a.txt": "one"})),
			map[string]string{quiesceParameter: "5s"})
		n.mu.Lock()
		w := n.watchers["config"]
		n.mu.Unlock()
		if got := w.quiesceNow(); got != 5*time.Second {
			t.Errorf("the watch rests for %s, want 5s", got)
		}

		// A class's parameters are immutable, so a claim changes its
		// quiesce by switching to another class.
		createClass(t, n, "config-calm", map[string]string{quiesceParameter: "10s"})
		nameClass(t, n, "config-calm")

		synctest.Wait()
		if got := w.quiesceNow(); got != 10*time.Second {
			t.Fatalf("the watch rests for %s, want the new quiesce of 10s", got)
		}
	})
}

func TestAnUnarmedVolumeUsesTheDefaultQuiesce(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	stagedWriteable(t, n, "config", fileURL(source))
	n.mu.Lock()
	w := n.watchers["config"]
	n.mu.Unlock()
	if got := w.quiesceNow(); got != n.quiesce {
		t.Errorf("the watch rests for %s, want the driver's own %s", got, n.quiesce)
	}
}

func TestTheSweepCommitsNothingWhileTheTreeIsWritten(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		vol := armedVolume(t, n, "config",
			fileURL(bareRemote(t, map[string]string{"a.txt": "one"})),
			map[string]string{quiesceParameter: "1h", maxLatencyParameter: neverLatency})
		before := strings.TrimSpace(gitIn(t, vol.work, "rev-parse", "HEAD"))

		writeFiles(t, vol.tree, map[string]string{"one.yaml": "1"})
		time.Sleep(n.sweep)
		waitForPending(t, vol, 1)

		if after := strings.TrimSpace(gitIn(t, vol.work, "rev-parse", "HEAD")); after != before {
			t.Errorf("the tree moved to %s before it rested, want %s", after, before)
		}
	})
}
