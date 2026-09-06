package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// logbook is a log a test reads while the driver's own loops write it.
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

// waitForPending waits until the volume's pending set holds the count, or
// fails on the deadline.
func waitForPending(t *testing.T, held *volume, want int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, count := held.reading(); count == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, _, count := held.reading()
	t.Fatalf("the volume holds %d pending paths within 30s, want %d", count, want)
}

func TestTheWatchReadsTheTreeAfterTheQuiesce(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	// The sweep is long, so the inotify watch is what reads the tree here.
	answering.sweep = 30 * time.Second
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	published, _ := writeableVolume(t, answering, "config", fileURL(source))

	writeFiles(t, published.tree, map[string]string{
		"one.txt": "1", "two.txt": "22", "three.txt": "333",
	})
	waitForPending(t, published, 3)

	abnormal, message := published.report()
	if !abnormal {
		t.Errorf("an unarmed volume with work pending reported %q", message)
	}
	want := "unarmed: 3 paths pending, no class on claim /"
	if message != want {
		t.Errorf("the condition says %q, want %q", message, want)
	}
}

func TestTheSweepReadsATreeTheWatchMissed(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	// The quiesce is long, so the sweep is what reads the tree here.
	answering.quiesce = 30 * time.Second
	answering.sweep = 20 * time.Millisecond
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	published, _ := writeableVolume(t, answering, "config", fileURL(source))

	writeFiles(t, published.tree, map[string]string{"one.txt": "1"})
	waitForPending(t, published, 1)
}

func TestTheWatchFollowsADirectoryThePodMade(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	answering.sweep = 30 * time.Second
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	published, _ := writeableVolume(t, answering, "config", fileURL(source))

	// The write repeats, because the watch of a new directory is added
	// after the create that made it.
	writeFiles(t, published.tree, map[string]string{"sub/.keep": ""})
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
				writeFiles(t, published.tree, map[string]string{"sub/one.txt": "1"})
			}
		}
	}()
	waitForPending(t, published, 2)
}

func TestTheWatchPostsOneEventWhenTheTreeFirstHoldsWork(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	answering.sweep = 20 * time.Millisecond
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	published, _ := writeableVolume(t, answering, "config", fileURL(source))

	writeFiles(t, published.tree, map[string]string{"one.txt": "1"})
	waitForPending(t, published, 1)
	// The sweep runs again and again, and the Event is posted once.
	time.Sleep(100 * time.Millisecond)

	pending := []corev1.Event{}
	for _, posted := range eventsOf(t, answering) {
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
}

func TestTheWatchRunsWithNoInotify(t *testing.T) {
	logs := &logbook{}
	answering, _ := testNode(t, logs)
	answering.inotify = func(int) (int, error) { return 0, errors.New("no inotify here") }
	answering.sweep = 20 * time.Millisecond
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	published, _ := writeableVolume(t, answering, "config", fileURL(source))

	writeFiles(t, published.tree, map[string]string{"one.txt": "1"})
	waitForPending(t, published, 1)
	if !strings.Contains(logs.String(), "the watch did not start") {
		t.Errorf("the log is %q, want the refused watch in it", logs)
	}
}

func TestTheWatchReportsADirectoryItCannotAdd(t *testing.T) {
	logs := &logbook{}
	answering, _ := testNode(t, logs)
	seeing := &watcher{
		node:       answering,
		volume:     &volume{id: "config", tree: t.TempDir()},
		descriptor: -1,
		watched:    map[int32]string{},
	}
	seeing.add(t.Context(), seeing.volume.tree)
	if !strings.Contains(logs.String(), "the watch missed a directory") {
		t.Errorf("the log is %q, want the refused watch in it", logs)
	}
}

func TestTheWatchReportsATreeItCannotRead(t *testing.T) {
	logs := &logbook{}
	answering, _ := testNode(t, logs)
	holder := newStore(t.TempDir())
	seeing := &watcher{
		node:   answering,
		volume: &volume{id: "config", work: holder.workTree(holder.repository("file:///gone"), "config")},
	}
	seeing.scan(t.Context())
	if !strings.Contains(logs.String(), "the tree was not read") {
		t.Errorf("the log is %q, want the failed read in it", logs)
	}
}

func TestTheEventsCarryTheDirectoryThePodMade(t *testing.T) {
	seeing := &watcher{watched: map[int32]string{7: "/store/tree"}}
	buffer := inotifyRecord(7, unix.IN_CREATE|unix.IN_ISDIR, "sub")
	buffer = append(buffer, inotifyRecord(7, unix.IN_MODIFY, "a.txt")...)
	buffer = append(buffer, inotifyRecord(7, unix.IN_MODIFY, "")...)

	found := seeing.events(buffer)
	if len(found) != 3 {
		t.Fatalf("events answered %v, want three", found)
	}
	if found[0].directory != "/store/tree/sub" {
		t.Errorf("the first event names %q, want the new directory", found[0].directory)
	}
	if found[1].directory != "" || found[2].directory != "" {
		t.Errorf("events answered %v, want no directory for a write", found)
	}
}

// inotifyRecord is one event as the kernel writes it: the watch, the mask,
// the cookie, the name's length, and the name padded to a word with zeros.
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
	answering, _ := testNode(t, io.Discard)
	answering.mu.Lock()
	defer answering.mu.Unlock()
	answering.unwatch(&volume{id: "csi-9"})
	answering.watch(&volume{id: "csi-9"})
	if got := len(answering.watchers); got != 0 {
		t.Errorf("the node holds %d watches of a read-only volume, want 0", got)
	}
}

func TestTheClassSetsTheQuiesceWithNoRemount(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	armedVolume(t, answering, "config",
		fileURL(bareRemote(t, map[string]string{"a.txt": "one"})),
		map[string]string{quiesceParameter: "5s"})
	answering.mu.Lock()
	seeing := answering.watchers["config"]
	answering.mu.Unlock()
	if got := seeing.rest(); got != 5*time.Second {
		t.Errorf("the watch rests for %s, want 5s", got)
	}

	classes := cluster(t, answering).StorageV1().VolumeAttributesClasses()
	class, err := classes.Get(t.Context(), "config-eager", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the class: %v", err)
	}
	class.Parameters = map[string]string{quiesceParameter: "10s"}
	if _, err := classes.Update(t.Context(), class, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("changing the class: %v", err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if seeing.rest() == 10*time.Second {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the watch rests for %s within 30s, want the new quiesce of 10s", seeing.rest())
}

func TestAnUnarmedVolumeRestsForTheDriversOwnQuiesce(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	writeableVolume(t, answering, "config", fileURL(source))
	answering.mu.Lock()
	seeing := answering.watchers["config"]
	answering.mu.Unlock()
	if got := seeing.rest(); got != answering.quiesce {
		t.Errorf("the watch rests for %s, want the driver's own %s", got, answering.quiesce)
	}
}

func TestTheTreeIsCommittedAndPushedOnTheTimer(t *testing.T) {
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	answering, _ := testNode(t, io.Discard)
	held := armedVolume(t, answering, "config", fileURL(remote),
		map[string]string{quiesceParameter: "5s"})
	writeFiles(t, held.tree, map[string]string{"one.yaml": "1"})

	// The sweep reads the tree while the quiesce is still long, so the
	// commit lands before the timer does.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if strings.TrimSpace(git(t, remote, "log", "--format=%s", "-1", "main")) == "Update 1 paths" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the remote's main is at %q within 30s, want the driver's commit",
		strings.TrimSpace(git(t, remote, "log", "--format=%s", "-1", "main")))
}

func TestTheSweepCommitsNothingWhileTheTreeIsWritten(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	held := armedVolume(t, answering, "config",
		fileURL(bareRemote(t, map[string]string{"a.txt": "one"})),
		map[string]string{quiesceParameter: "1h", maxLatencyParameter: neverLatency})
	before := strings.TrimSpace(gitIn(t, held.work, "rev-parse", "HEAD"))

	writeFiles(t, held.tree, map[string]string{"one.yaml": "1"})
	waitForPending(t, held, 1)

	if after := strings.TrimSpace(gitIn(t, held.work, "rev-parse", "HEAD")); after != before {
		t.Errorf("the tree moved to %s before it rested, want %s", after, before)
	}
}
