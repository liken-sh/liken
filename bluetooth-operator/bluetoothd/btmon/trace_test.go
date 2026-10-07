package main

// These tests cover the supervisor: it runs one btmon while the btmon
// file says true, stops it when the file says false or goes away, and
// starts it again after it exits on its own. The child is a fake, so
// no btmon runs, and each test runs in a synctest bubble, so the
// restart delay takes no real time.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// fakeTrace is one btmon the fake starter started. It exits when the
// supervisor stops it, or when the test ends it with exit. mu is the
// fixture's, and it guards stopped.
type fakeTrace struct {
	mu      *sync.Mutex
	exited  chan struct{}
	stopped bool
}

func (f *fakeTrace) done() <-chan struct{} { return f.exited }

func (f *fakeTrace) stop() {
	f.mu.Lock()
	f.stopped = true
	f.mu.Unlock()
	f.exit()
}

// exit ends the fake the way a btmon that fails ends.
func (f *fakeTrace) exit() {
	select {
	case <-f.exited:
	default:
		close(f.exited)
	}
}

// traceFixture is a settings directory, the supervisor that reads it,
// and the channels that stand in for the inotify watch.
//
// The supervisor starts a fake from its own goroutine, and a timer in
// the bubble wakes it, which gives the race detector no order between
// that goroutine and the test. mu gives the order.
type traceFixture struct {
	t        *testing.T
	settings string
	changes  chan struct{}
	failed   chan error
	result   chan error

	mu      sync.Mutex
	started []*fakeTrace
	refuse  error
}

// newTraceFixture answers an empty settings directory and the channels
// of a watch. The caller runs in a synctest bubble.
func newTraceFixture(t *testing.T) *traceFixture {
	t.Helper()
	return &traceFixture{
		t:        t,
		settings: t.TempDir(),
		changes:  make(chan struct{}, 1),
		failed:   make(chan error, 1),
		result:   make(chan error, 1),
	}
}

// run starts the supervisor with ctx. The file the test wrote before
// the call is the first read.
func (f *traceFixture) run(ctx context.Context) {
	s := &supervisor{
		setting: filepath.Join(f.settings, "btmon"),
		start:   f.start,
		delay:   restartDelay,
	}
	go func() { f.result <- s.run(ctx, f.changes, f.failed) }()
	synctest.Wait()
}

func (f *traceFixture) start() (trace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuse != nil {
		return nil, f.refuse
	}
	child := &fakeTrace{mu: &f.mu, exited: make(chan struct{})}
	f.started = append(f.started, child)
	return child, nil
}

// refuseStarts makes each start fail with err, and nil lets them
// succeed.
func (f *traceFixture) refuseStarts(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refuse = err
}

// starts counts the fakes the supervisor started.
func (f *traceFixture) starts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started)
}

// child answers the nth fake the supervisor started.
func (f *traceFixture) child(n int) *fakeTrace {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started[n]
}

// stopped reports whether the supervisor stopped the nth fake.
func (f *traceFixture) stopped(n int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started[n].stopped
}

// put writes value into the btmon file, the way bondfetch leaves it
// before the container starts.
func (f *traceFixture) put(value string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.settings, "btmon"), []byte(value+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// write puts value in the btmon file and tells the supervisor, the way
// the inotify watch does.
func (f *traceFixture) write(value string) {
	f.t.Helper()
	f.put(value)
	f.changed()
}

// remove deletes the btmon file and tells the supervisor.
func (f *traceFixture) remove() {
	f.t.Helper()
	if err := os.Remove(filepath.Join(f.settings, "btmon")); err != nil {
		f.t.Fatal(err)
	}
	f.changed()
}

func (f *traceFixture) changed() {
	f.changes <- struct{}{}
	synctest.Wait()
}

// running counts the fakes that have not exited.
func (f *traceFixture) running() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, child := range f.started {
		select {
		case <-child.exited:
		default:
			count++
		}
	}
	return count
}

// The first read decides whether a trace runs: true starts one, and
// false, a missing file, and a value that is not true start none.
func TestTheFirstReadStartsATraceOnlyForTrue(t *testing.T) {
	cases := []struct {
		name  string
		write func(f *traceFixture)
		want  int
	}{
		{name: "true", write: func(f *traceFixture) { f.put("true") }, want: 1},
		{name: "false", write: func(f *traceFixture) { f.put("false") }, want: 0},
		{name: "no file", write: func(f *traceFixture) {}, want: 0},
		{name: "a typing error", write: func(f *traceFixture) { f.put("yes") }, want: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newTraceFixture(t)
				c.write(f)

				f.run(t.Context())

				if f.starts() != c.want || f.running() != c.want {
					t.Errorf("started %d and running %d, want %d", f.starts(), f.running(), c.want)
				}
			})
		})
	}
}

// A change to false stops the trace, and a change back to true starts
// a new one.
func TestAChangeStopsAndStartsTheTrace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newTraceFixture(t)
		f.run(t.Context())

		f.write("true")
		f.write("false")
		f.write("true")

		if f.starts() != 2 || !f.stopped(0) || f.running() != 1 {
			t.Errorf("started %d, first stopped %v, running %d; want 2 started, the first stopped, 1 running",
				f.starts(), f.stopped(0), f.running())
		}
	})
}

// A file that goes away is false.
func TestARemovedFileStopsTheTrace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newTraceFixture(t)
		f.run(t.Context())

		f.write("true")
		f.remove()

		if f.running() != 0 {
			t.Errorf("running %d after the file went away, want 0", f.running())
		}
	})
}

// A second true while a trace runs starts no second trace.
func TestARepeatedTrueKeepsOneTrace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newTraceFixture(t)
		f.run(t.Context())

		f.write("true")
		f.write("true")

		if f.starts() != 1 {
			t.Errorf("started %d, want 1", f.starts())
		}
	})
}

// A trace that exits on its own while the file says true starts again
// after the delay, and not before it.
func TestATraceThatExitsStartsAgainAfterTheDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newTraceFixture(t)
		f.run(t.Context())
		f.write("true")

		f.child(0).exit()
		time.Sleep(restartDelay - time.Millisecond)
		synctest.Wait()
		before := f.starts()
		time.Sleep(time.Millisecond)
		synctest.Wait()

		if before != 1 || f.starts() != 2 || f.running() != 1 {
			t.Errorf("started %d before the delay and %d after it, running %d; want 1, 2, and 1",
				before, f.starts(), f.running())
		}
	})
}

// A change to false during the delay cancels the restart.
func TestFalseDuringTheDelayCancelsTheRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newTraceFixture(t)
		f.run(t.Context())
		f.write("true")

		f.child(0).exit()
		f.write("false")
		time.Sleep(2 * restartDelay)
		synctest.Wait()

		if f.starts() != 1 {
			t.Errorf("started %d, want 1", f.starts())
		}
	})
}

// A start that fails is tried again after the delay, the same as a
// trace that exits at once.
func TestAFailedStartIsTriedAgainAfterTheDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newTraceFixture(t)
		f.refuseStarts(errors.New("exec: no such file"))
		f.run(t.Context())
		f.write("true")

		f.refuseStarts(nil)
		time.Sleep(restartDelay)
		synctest.Wait()

		if f.starts() != 1 || f.running() != 1 {
			t.Errorf("started %d and running %d after the delay, want 1 and 1", f.starts(), f.running())
		}
	})
}

// A watch that fails ends the supervisor with the watch's error, and
// stops the trace, so the kubelet starts the container again and the
// new container reads the file again.
func TestAFailedWatchStopsTheTraceAndEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newTraceFixture(t)
		f.run(t.Context())
		f.write("true")

		f.failed <- errors.New("the settings directory went away")
		synctest.Wait()

		if err := <-f.result; err == nil {
			t.Error("the supervisor ended with no error after the watch failed")
		}
		if f.running() != 0 {
			t.Errorf("running %d after the watch failed, want 0", f.running())
		}
	})
}

// The kubelet's TERM ends the supervisor with no error, and stops the
// trace first.
func TestTheEndOfTheContextStopsTheTrace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newTraceFixture(t)
		ctx, cancel := context.WithCancel(t.Context())
		f.run(ctx)
		f.write("true")

		cancel()
		synctest.Wait()

		if err := <-f.result; err != nil {
			t.Errorf("the supervisor ended with %v, want no error", err)
		}
		if f.running() != 0 {
			t.Errorf("running %d after the end, want 0", f.running())
		}
	})
}
