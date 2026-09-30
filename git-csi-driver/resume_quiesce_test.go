package main

import (
	"io"
	"strings"
	"sync"
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

// manualClock is a clock whose time moves only when a test advances
// it. A class's quiesce is at least 5s, and a watch on this clock
// waits for it with no real wait. The watch still reads inotify and
// runs git on real time, so the test waits for those in real time.
type manualClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*manualTimer
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
	return &manualClock{now: clockStart}
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

// waitForTimer waits in real time until an armed timer runs out at
// the offset from clockStart. The watch sets its timers on its own
// goroutine, so a test waits for the timer before it advances past it.
func (c *manualClock) waitForTimer(t *testing.T, at time.Duration) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if c.armedAt(clockStart.Add(at)) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no timer runs out at %s within 20s", at)
}

func (c *manualClock) armedAt(when time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
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

// A write made while the driver was down sends no inotify event, so the
// watch that starts after the restart has to start the quiesce itself.
// The class arrives after the watch starts, from the claim's own watch.
// The driver's own quiesce is what the timer takes until the class
// arrives. A short one can run out first and find the volume unarmed,
// and a long one is still running when the class shortens it. The rest
// counts from the start of the watch in both cases: the clock moves 1s
// before the test waits for the class's timer, and that timer still
// runs out 5s after the start.
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

			clock := newManualClock()
			again, _ := testNode(t, io.Discard)
			again.store = answering.store
			again.events = answering.events
			again.arms.client = answering.arms.client
			again.mounted = func(string) bool { return true }
			again.quiesce = c.quiesce
			again.clock = clock
			// The sweep is an hour, so only the quiesce can commit
			// within the test.
			again.sweep = time.Hour
			again.resume(t.Context())

			clock.advance(time.Second)
			clock.waitForTimer(t, 5*time.Second)
			clock.advance(4 * time.Second)

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
	clock := newManualClock()
	answering.clock = clock
	// The sweep is an hour, so only the quiesce can commit within the
	// test.
	answering.sweep = time.Hour
	remote := bareRemote(t, map[string]string{"a.txt": "one"})
	held := armedVolume(t, answering, "config", fileURL(remote),
		map[string]string{quiesceParameter: "5s"})
	unwatched(t, answering, held)
	writeFiles(t, held.tree, map[string]string{"one.yaml": "1"})

	// The second watch starts a minute after the first, so the timer the
	// test waits for is the second watch's own.
	clock.advance(time.Minute)
	answering.mu.Lock()
	answering.watch(held)
	answering.mu.Unlock()

	clock.waitForTimer(t, time.Minute+5*time.Second)
	clock.advance(5 * time.Second)

	waitForPushed(t, held, 20*time.Second)
	if got := remoteSubject(t, remote); got != "Update 1 paths" {
		t.Errorf("the remote's main is at %q, want %q", got, "Update 1 paths")
	}
}
