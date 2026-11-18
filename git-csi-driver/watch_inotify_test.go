package main

// These tests run the kernel's inotify watch outside a synctest bubble,
// for the reason in clock.go. Each wait ends when the watch sets a
// timer or the node posts an Event, and inotifyTimeout makes a broken
// watch fail the test instead of hanging it.

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	kevents "github.com/liken-sh/liken/kubernetes/events"
	"golang.org/x/sys/unix"
)

const inotifyTimeout = 30 * time.Second

// manualClock moves only when a test calls advance. The watch reads
// inotify and runs git in real time, and measures the quiesce on this
// clock.
type manualClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*manualTimer
	// reset closes when a timer is set, which wakes waitForTimer.
	reset chan struct{}
}

// manualTimer sends one value when the clock reaches its deadline, as a
// `time.Timer` does.
type manualTimer struct {
	clock    *manualClock
	fire     chan time.Time
	deadline time.Time
	running  bool
}

// clockStart is the time at which every manualClock starts, so a test
// states a deadline as an offset from it.
var clockStart = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newManualClock() *manualClock {
	return &manualClock{now: clockStart, reset: make(chan struct{})}
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) NewTimer(wait time.Duration) timer {
	made := &manualTimer{clock: c, fire: make(chan time.Time, 1)}
	c.mu.Lock()
	c.timers = append(c.timers, made)
	c.mu.Unlock()
	made.Reset(wait)
	return made
}

func (c *manualClock) advance(by time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(by)
	for _, each := range c.timers {
		each.fireIfDue()
	}
}

// waitForTimer waits until a running timer has its deadline at the
// offset from clockStart. The watch sets its timers on its own
// goroutine, so a test that advanced the clock before the timer was set
// would never fire it.
func (c *manualClock) waitForTimer(t *testing.T, at time.Duration) {
	t.Helper()
	timeout := time.NewTimer(inotifyTimeout)
	defer timeout.Stop()
	for {
		c.mu.Lock()
		found := c.runningAt(clockStart.Add(at))
		reset := c.reset
		c.mu.Unlock()
		if found {
			return
		}
		select {
		case <-reset:
		case <-timeout.C:
			t.Fatalf("no timer expired at %s within %s", at, inotifyTimeout)
		}
	}
}

// runningAt reports whether a running timer has the deadline. The
// caller holds the clock's lock.
func (c *manualClock) runningAt(when time.Time) bool {
	for _, each := range c.timers {
		if each.running && each.deadline.Equal(when) {
			return true
		}
	}
	return false
}

func (t *manualTimer) Chan() <-chan time.Time { return t.fire }

// Reset discards a value that the timer sent and nobody received, as a
// `time.Timer` does.
func (t *manualTimer) Reset(wait time.Duration) {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	t.drain()
	t.deadline, t.running = t.clock.now.Add(wait), true
	close(t.clock.reset)
	t.clock.reset = make(chan struct{})
	t.fireIfDue()
}

func (t *manualTimer) Stop() {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	t.drain()
	t.running = false
}

// fireIfDue sends the time when the clock has reached the deadline. The
// caller holds the clock's lock.
func (t *manualTimer) fireIfDue() {
	if !t.running || t.clock.now.Before(t.deadline) {
		return
	}
	t.running = false
	t.fire <- t.clock.now
}

func (t *manualTimer) drain() {
	select {
	case <-t.fire:
	default:
	}
}

// awaitPosted waits until the node has posted an Event with the reason,
// and returns it. It checks the stored Events each time the fake API
// server signals a write, so it does not poll.
func awaitPosted(t *testing.T, n *node, reason string) kevents.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), inotifyTimeout)
	defer cancel()
	target := recordingOf(t, n.events)
	for {
		for _, posted := range target.stored.List() {
			if posted.Reason == reason {
				return posted
			}
		}
		select {
		case <-target.republishWake:
		case <-ctx.Done():
			t.Fatalf("the node posted no %s Event within %s", reason, inotifyTimeout)
		}
	}
}

// inotifyNode returns a test node that runs the kernel's watch with a
// manualClock. The sweep interval is an hour, so no sweep reads the tree
// during a test.
func inotifyNode(t *testing.T) (*node, *manualClock) {
	t.Helper()
	n, _ := testNode(t, io.Discard)
	n.inotify = unix.InotifyInit1
	clock := newManualClock()
	n.clock = clock
	n.sweep = time.Hour
	return n, clock
}

// moveInto writes the files into a directory outside the tree and then
// renames the directory into the tree. The rename is one inotify event,
// so the watch restarts the quiesce once, at a known time. Files written
// in place send several events each, and the watch could read some of
// them after the test advanced the clock.
func moveInto(t *testing.T, tree, name string, files map[string]string) {
	t.Helper()
	outside := filepath.Join(t.TempDir(), name)
	writeFiles(t, outside, files)
	if err := os.Rename(outside, filepath.Join(tree, name)); err != nil {
		t.Fatalf("moving %s into the tree: %v", name, err)
	}
}

func TestTheWatchReadsTheTreeAfterTheQuiesce(t *testing.T) {
	n, clock := inotifyNode(t)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	published, _ := stagedWriteable(t, n, "config", fileURL(source))
	// The watch sets a quiesce timer when it starts. Advancing the
	// clock by 1s gives the write's timer a different deadline, so
	// waitForTimer waits for that timer.
	clock.advance(time.Second)

	moveInto(t, published.tree, "conf", map[string]string{
		"one.txt": "1", "two.txt": "22", "three.txt": "333",
	})
	clock.waitForTimer(t, time.Second+n.quiesce)
	clock.advance(n.quiesce)

	awaitPosted(t, n, reasonPending)
	abnormal, message := published.health()
	if !abnormal {
		t.Errorf("an unarmed volume with pending work reported %q, want an abnormal report", message)
	}
	want := "Unarmed: 3 paths pending, and the claim / sets no class"
	if message != want {
		t.Errorf("the health report message is %q, want %q", message, want)
	}
}

// An inotify watch on a directory does not report a write inside a
// subdirectory, so the watch must add a watch for each directory that
// the pod creates.
func TestTheWatchFollowsADirectoryThePodMade(t *testing.T) {
	n, clock := inotifyNode(t)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	published, _ := stagedWriteable(t, n, "config", fileURL(source))
	clock.advance(time.Second)

	// The watch adds the inotify watch on sub before it restarts the
	// quiesce, so that watch exists once this timer is set.
	if err := os.Mkdir(filepath.Join(published.tree, "sub"), 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
	clock.waitForTimer(t, time.Second+n.quiesce)
	clock.advance(time.Second)

	moveInto(t, filepath.Join(published.tree, "sub"), "conf", map[string]string{"one.txt": "1"})
	clock.waitForTimer(t, 2*time.Second+n.quiesce)
	clock.advance(n.quiesce)

	posted := awaitPosted(t, n, reasonPending)
	if posted.Message != "1 paths pending" {
		t.Errorf("the Event message is %q, want %q", posted.Message, "1 paths pending")
	}
}

// A write restarts the quiesce, so the driver commits and pushes when
// the class's quiesce has passed since the write, not since the watch
// started.
func TestTheTreeIsCommittedAndPushedWhenTheQuiesceAfterAWriteEnds(t *testing.T) {
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	n, clock := inotifyNode(t)
	boundVolume(t, n, "config", "config-eager")
	createClass(t, n, "config-eager", map[string]string{quiesceParameter: "5s"})
	vol, _ := stagedWriteable(t, n, "config", fileURL(remote))
	// The default quiesce is 30s, so a timer at 5s shows that the class
	// armed the volume.
	clock.waitForTimer(t, 5*time.Second)

	clock.advance(time.Second)
	moveInto(t, vol.tree, "conf", map[string]string{"one.yaml": "1"})
	clock.waitForTimer(t, 6*time.Second)
	clock.advance(5 * time.Second)

	awaitPosted(t, n, reasonPushed)
	if got := remoteSubject(t, remote); got != "Update 1 paths" {
		t.Errorf("the remote's main is at %q, want the driver's commit", got)
	}
}
