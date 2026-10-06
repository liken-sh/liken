package main

// These tests run the kernel's inotify watch. A goroutine that reads
// the inotify file is not durably blocked, so a synctest bubble's clock
// never moves while the watch runs, and these tests run outside a
// bubble. The quiesce runs on a manualClock that the test moves, and
// every wait is on an event: a timer the watch sets, or an Event the
// node posts. inotifyTimeout bounds each wait, so a broken watch fails
// the test instead of hanging it.

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

// inotifyTimeout bounds each wait on the watch. It is not a wait: every
// wait returns at the event it waits for.
const inotifyTimeout = 30 * time.Second

// manualClock is a clock whose time moves only when a test advances
// it. The watch reads inotify and runs git on real time, and measures
// the quiesce on this clock, so the test decides when a quiesce runs
// out.
type manualClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*manualTimer
	// reset closes when a timer is set, which wakes waitForTimer.
	reset chan struct{}
}

// manualTimer sends once when the clock reaches its deadline, as a
// `time.Timer` does.
type manualTimer struct {
	clock    *manualClock
	fire     chan time.Time
	deadline time.Time
	armed    bool
}

// clockStart is where every manualClock starts, so a test names each
// deadline as an offset from it.
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

// advance moves the clock and fires every armed timer whose deadline
// it reached.
func (c *manualClock) advance(by time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(by)
	for _, each := range c.timers {
		each.fireIfDue()
	}
}

// waitForTimer waits until an armed timer runs out at the offset from
// clockStart. The watch sets its timers on its own goroutine, so a test
// waits for the timer before it advances past it.
func (c *manualClock) waitForTimer(t *testing.T, at time.Duration) {
	t.Helper()
	bound := time.NewTimer(inotifyTimeout)
	defer bound.Stop()
	for {
		c.mu.Lock()
		found := c.armedAt(clockStart.Add(at))
		reset := c.reset
		c.mu.Unlock()
		if found {
			return
		}
		select {
		case <-reset:
		case <-bound.C:
			t.Fatalf("no timer runs out at %s within %s", at, inotifyTimeout)
		}
	}
}

// armedAt reports an armed timer with the deadline. The caller holds
// the clock's lock.
func (c *manualClock) armedAt(when time.Time) bool {
	for _, each := range c.timers {
		if each.armed && each.deadline.Equal(when) {
			return true
		}
	}
	return false
}

func (t *manualTimer) Chan() <-chan time.Time { return t.fire }

// Reset discards a value the timer sent and nobody received, as a
// `time.Timer` does.
func (t *manualTimer) Reset(wait time.Duration) {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	t.drain()
	t.deadline, t.armed = t.clock.now.Add(wait), true
	close(t.clock.reset)
	t.clock.reset = make(chan struct{})
	t.fireIfDue()
}

func (t *manualTimer) Stop() {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	t.drain()
	t.armed = false
}

// fireIfDue sends when the clock has reached the deadline. The caller
// holds the clock's lock.
func (t *manualTimer) fireIfDue() {
	if !t.armed || t.clock.now.Before(t.deadline) {
		return
	}
	t.armed = false
	t.fire <- t.clock.now
}

func (t *manualTimer) drain() {
	select {
	case <-t.fire:
	default:
	}
}

// awaitPosted waits until the node has posted an Event for the reason,
// and returns it. The fake API server signals each write it answers,
// and the test reads what it holds after each signal, so it never
// polls. The kernel's watch cannot run in a synctest bubble, so the
// wait has a real limit.
func awaitPosted(t *testing.T, answering *node, reason string) kevents.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), inotifyTimeout)
	defer cancel()
	target := recordingOf(t, answering.events)
	for {
		for _, posted := range target.held.List() {
			if posted.Reason == reason {
				return posted
			}
		}
		select {
		case <-target.arrived:
		case <-ctx.Done():
			t.Fatalf("the node posted no %s Event within %s", reason, inotifyTimeout)
		}
	}
}

// inotifyNode is a test node that runs the kernel's watch on a
// manualClock. The sweep is an hour, so only the watch reads the tree.
func inotifyNode(t *testing.T) (*node, *manualClock) {
	t.Helper()
	answering, _ := testNode(t, io.Discard)
	answering.inotify = unix.InotifyInit1
	clock := newManualClock()
	answering.clock = clock
	answering.sweep = time.Hour
	return answering, clock
}

// moveInto writes the files in a directory outside the tree and renames
// the directory into the tree. The rename is one inotify event, so the
// watch reads the whole write in one batch and restarts the quiesce
// once. A write in place sends a create, a modify, and a close for each
// file, and a batch that the watch reads after the test advanced the
// clock would restart the quiesce at the new time.
func moveInto(t *testing.T, tree, name string, files map[string]string) {
	t.Helper()
	outside := filepath.Join(t.TempDir(), name)
	writeFiles(t, outside, files)
	if err := os.Rename(outside, filepath.Join(tree, name)); err != nil {
		t.Fatalf("moving %s into the tree: %v", name, err)
	}
}

func TestTheWatchReadsTheTreeAfterTheQuiesce(t *testing.T) {
	answering, clock := inotifyNode(t)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	published, _ := stagedWriteable(t, answering, "config", fileURL(source))
	// The clock moves past the quiesce the watch set when it started,
	// so the timer below is the one the write set.
	clock.advance(time.Second)

	moveInto(t, published.tree, "conf", map[string]string{
		"one.txt": "1", "two.txt": "22", "three.txt": "333",
	})
	clock.waitForTimer(t, time.Second+answering.quiesce)
	clock.advance(answering.quiesce)

	awaitPosted(t, answering, reasonPending)
	abnormal, message := published.report()
	if !abnormal {
		t.Errorf("an unarmed volume with work pending reported %q", message)
	}
	want := "unarmed: 3 paths pending, no class on claim /"
	if message != want {
		t.Errorf("the condition says %q, want %q", message, want)
	}
}

// The watch adds a watch for each directory the pod makes, so a write
// inside the new directory restarts the quiesce. The root's watch does
// not see that write.
func TestTheWatchFollowsADirectoryThePodMade(t *testing.T) {
	answering, clock := inotifyNode(t)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	published, _ := stagedWriteable(t, answering, "config", fileURL(source))
	clock.advance(time.Second)

	// The read loop adds the directory's watch before it wakes the
	// quiesce, so the watch on sub exists once this timer is set.
	if err := os.Mkdir(filepath.Join(published.tree, "sub"), 0o755); err != nil {
		t.Fatalf("making the directory: %v", err)
	}
	clock.waitForTimer(t, time.Second+answering.quiesce)
	clock.advance(time.Second)

	moveInto(t, filepath.Join(published.tree, "sub"), "conf", map[string]string{"one.txt": "1"})
	clock.waitForTimer(t, 2*time.Second+answering.quiesce)
	clock.advance(answering.quiesce)

	posted := awaitPosted(t, answering, reasonPending)
	if posted.Message != "1 paths pending" {
		t.Errorf("the event says %q, want %q", posted.Message, "1 paths pending")
	}
}

// A write restarts the quiesce, so the driver commits and pushes the
// tree when the class's quiesce has passed since the write, not since
// the watch started.
func TestTheTreeIsCommittedAndPushedOnTheTimer(t *testing.T) {
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	answering, clock := inotifyNode(t)
	boundVolume(t, answering, "config", "config-eager")
	armingClass(t, answering, "config-eager", map[string]string{quiesceParameter: "5s"})
	held, _ := stagedWriteable(t, answering, "config", fileURL(remote))
	// Only the class sets a quiesce of 5s, so this timer is also the
	// proof that the class armed the volume.
	clock.waitForTimer(t, 5*time.Second)

	clock.advance(time.Second)
	moveInto(t, held.tree, "conf", map[string]string{"one.yaml": "1"})
	clock.waitForTimer(t, 6*time.Second)
	clock.advance(5 * time.Second)

	awaitPosted(t, answering, reasonPushed)
	if got := remoteSubject(t, remote); got != "Update 1 paths" {
		t.Errorf("the remote's main is at %q, want the driver's commit", got)
	}
}
