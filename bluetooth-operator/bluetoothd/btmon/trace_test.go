package main

// The child btmon is a fake, and each test runs in a synctest bubble,
// so the restart delay takes no real time.

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

// fakeTrace is a btmon that the fixture started. It exits when the
// supervisor stops it or when the test calls exit. mu is the fixture's
// mutex and guards stopped.
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

// exit makes the fake exit, as a btmon that crashes does.
func (f *fakeTrace) exit() {
	select {
	case <-f.exited:
	default:
		close(f.exited)
	}
}

// traceFixture is a settings directory, the supervisor that reads it,
// and the channels that replace the inotify watch.
//
// The supervisor starts fakes from its own goroutine, and the race
// detector sees no ordering between that goroutine and the test. mu
// provides the ordering.
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

// newTraceFixture returns a fixture with an empty settings directory.
// The caller must run in a synctest bubble.
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

// run starts the supervisor with ctx. The supervisor's first read sees
// any file that the test wrote before this call.
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

// refuseStarts makes later starts fail with err. A nil err makes them
// succeed.
func (f *traceFixture) refuseStarts(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refuse = err
}

func (f *traceFixture) starts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started)
}

// child returns the nth fake that the supervisor started.
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

// put writes value to the btmon file without notifying the supervisor,
// as bondfetch does before the container starts.
func (f *traceFixture) put(value string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.settings, "btmon"), []byte(value+"\n"), 0o644); err != nil {
		f.t.Fatalf("writing the btmon file: %v", err)
	}
}

// write writes value to the btmon file and sends a change to the
// supervisor, as the inotify watch does.
func (f *traceFixture) write(value string) {
	f.t.Helper()
	f.put(value)
	f.changed()
}

// remove deletes the btmon file and sends a change to the supervisor.
func (f *traceFixture) remove() {
	f.t.Helper()
	if err := os.Remove(filepath.Join(f.settings, "btmon")); err != nil {
		f.t.Fatalf("removing the btmon file: %v", err)
	}
	f.changed()
}

func (f *traceFixture) changed() {
	f.changes <- struct{}{}
	synctest.Wait()
}

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

func TestTheFirstReadStartsATraceOnlyForTrue(t *testing.T) {
	cases := []struct {
		name  string
		write func(f *traceFixture)
		want  int
	}{
		{name: "true", write: func(f *traceFixture) { f.put("true") }, want: 1},
		{name: "false", write: func(f *traceFixture) { f.put("false") }, want: 0},
		{name: "no file", write: func(f *traceFixture) {}, want: 0},
		{name: "a value that is not true or false", write: func(f *traceFixture) { f.put("yes") }, want: 0},
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

// When the watch fails, the supervisor stops the trace and returns the
// error, so the container exits and the kubelet restarts it with a
// fresh read of the file.
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

// When the context ends, as it does on the kubelet's SIGTERM, the
// supervisor stops the trace and returns no error.
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
