package main

// A wait for a file ends when the kernel reports that the file
// arrived, and not on a fixed short interval while the file is
// missing.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A watch on a fresh runtime directory, for the socket name the
// compositor uses.
func arrivalsIn(t *testing.T, dir string) *arrivals {
	t.Helper()
	watch := newArrivals(filepath.Join(dir, socketName))
	t.Cleanup(watch.close)
	return watch
}

func TestTheSocketsArrivalEndsTheWait(t *testing.T) {
	cases := []struct {
		name   string
		arrive func(t *testing.T, path string)
	}{
		{"a socket", func(t *testing.T, path string) { listenOnSocket(t, path) }},
		{"a created file", func(t *testing.T, path string) {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"a file moved in", func(t *testing.T, path string) {
			staged := filepath.Join(filepath.Dir(path), "staged")
			if err := os.WriteFile(staged, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(staged, path); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			watch := arrivalsIn(t, dir)

			c.arrive(t, filepath.Join(dir, socketName))

			if !watch.wait(t.Context(), time.Minute) {
				t.Error("the wait ended on its fallback timer, and the socket arrived")
			}
		})
	}
}

// Weston creates a lock file beside its socket, and the lock file is
// not the socket.
func TestAnotherFileDoesNotEndTheWait(t *testing.T) {
	dir := t.TempDir()
	watch := arrivalsIn(t, dir)

	if err := os.WriteFile(filepath.Join(dir, socketName+".lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if watch.wait(t.Context(), 50*time.Millisecond) {
		t.Error("the wait ended on the lock file")
	}
}

// An arrival before ready is old news: the dial after ready already
// sees the socket.
func TestReadyForgetsAnEarlierArrival(t *testing.T) {
	dir := t.TempDir()
	watch := arrivalsIn(t, dir)
	listenOnSocket(t, filepath.Join(dir, socketName))
	if !watch.wait(t.Context(), time.Minute) {
		t.Fatal("the wait missed the socket")
	}
	if err := os.Remove(filepath.Join(dir, socketName)); err != nil {
		t.Fatal(err)
	}
	listenOnSocket(t, filepath.Join(dir, socketName))
	time.Sleep(50 * time.Millisecond)

	watch.ready()

	if watch.wait(t.Context(), 50*time.Millisecond) {
		t.Error("the wait ended on an arrival from before ready")
	}
}

func TestTheWaitEndsWithItsContext(t *testing.T) {
	watch := arrivalsIn(t, t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if watch.wait(ctx, time.Minute) {
		t.Error("the wait reported an arrival, and its context ended")
	}
}

// The directory does not exist when the watch starts, so the first
// wait lasts its fallback. Ready starts the watch once the directory
// exists, and the file's arrival ends the next wait.
func TestTheWatchStartsWhenItsDirectoryArrives(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runtime")
	watch := arrivalsIn(t, dir)
	if watch.wait(t.Context(), 50*time.Millisecond) {
		t.Fatal("the wait woke with no directory to watch")
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	watch.ready()
	listenOnSocket(t, filepath.Join(dir, socketName))

	if !watch.wait(t.Context(), time.Minute) {
		t.Error("the wait ended on its fallback timer, and the socket arrived")
	}
}

// The watched directory is removed and made again. The removal wakes
// the waiter, ready watches the new directory, and the socket's
// arrival in it ends the next wait.
func TestTheWatchFollowsADirectoryMadeAgain(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	watch := arrivalsIn(t, dir)

	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if !watch.wait(t.Context(), time.Minute) {
		t.Fatal("the removal of the directory did not wake the wait")
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	watch.ready()
	listenOnSocket(t, filepath.Join(dir, socketName))

	if !watch.wait(t.Context(), time.Minute) {
		t.Error("the wait ended on its fallback timer, and the socket arrived in the new directory")
	}
}

// The wait between dials doubles, up to the limit, so a compositor
// that stays down costs one dial every few seconds.
func TestTheDialDelayDoublesUpToItsLimit(t *testing.T) {
	cases := []struct {
		delay time.Duration
		want  time.Duration
	}{
		{compositorDialInterval, 2 * compositorDialInterval},
		{compositorDialLimit / 2, compositorDialLimit},
		{compositorDialLimit, compositorDialLimit},
	}
	for _, c := range cases {
		t.Run(c.delay.String(), func(t *testing.T) {
			if got := nextDialDelay(c.delay, compositorDialLimit); got != c.want {
				t.Errorf("nextDialDelay(%s) = %s, want %s", c.delay, got, c.want)
			}
		})
	}
}

// The watch starts with no compositor, and its fallback timer is far
// longer than the test. The compositor's socket arrives, and the watch
// connects to it on the arrival.
func TestTheWatchConnectsWhenTheSocketArrives(t *testing.T) {
	path := filepath.Join(t.TempDir(), socketName)
	watch := newOutputWatch(path, func(bool) {})
	watch.retry, watch.retryLimit = time.Hour, time.Hour
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	go watch.run(ctx)
	time.Sleep(50 * time.Millisecond)

	server := westonBenchOn(t, path, map[uint32]string{1: "HDMI-A-1"})

	server.client()
}

// A compositor that is killed leaves its socket file behind, and the
// next one replaces the file. The watch connects to the new
// compositor on the replacement, not on its fallback timer.
func TestTheWatchConnectsWhenAStaleSocketIsReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), socketName)
	first := westonBenchOn(t, path, map[uint32]string{1: "HDMI-A-1"})
	watch := newOutputWatch(path, func(bool) {})
	watch.retry, watch.retryLimit = time.Hour, time.Hour
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	go watch.run(ctx)
	session := first.client()
	first.stop()
	first.end(session)
	time.Sleep(50 * time.Millisecond)

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	second := westonBenchOn(t, path, map[uint32]string{1: "HDMI-A-1"})

	second.client()
}

// The runtime directory does not exist yet when the watch starts. The
// watch keeps dialing on its fallback timer, and connects once the
// directory and the socket appear.
func TestTheWatchConnectsWhenTheDirectoryArrivesLater(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runtime")
	path := filepath.Join(dir, socketName)
	watch := newOutputWatch(path, func(bool) {})
	watch.retry = 10 * time.Millisecond
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	go watch.run(ctx)
	time.Sleep(50 * time.Millisecond)

	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	server := westonBenchOn(t, path, map[uint32]string{1: "HDMI-A-1"})

	server.client()
}
