package main

// A wait for a file ends when the kernel reports that the file
// arrived. These tests check that it does not depend on a short
// polling interval.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// arrivalsIn returns a watch for the compositor's socket name in dir.
func arrivalsIn(t *testing.T, dir string) *arrivals {
	t.Helper()
	watch := newArrivals(filepath.Join(dir, socketName))
	t.Cleanup(watch.close)
	return watch
}

func TestTheSocketArrivingEndsTheWait(t *testing.T) {
	cases := []struct {
		name   string
		arrive func(t *testing.T, path string)
	}{
		{"a socket", func(t *testing.T, path string) { listenOnSocket(t, path) }},
		{"a created file", func(t *testing.T, path string) {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatalf("creating the socket file: %v", err)
			}
		}},
		{"a file moved in", func(t *testing.T, path string) {
			staged := filepath.Join(filepath.Dir(path), "staged")
			if err := os.WriteFile(staged, nil, 0o600); err != nil {
				t.Fatalf("writing the staged file: %v", err)
			}
			if err := os.Rename(staged, path); err != nil {
				t.Fatalf("moving the staged file into place: %v", err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			watch := arrivalsIn(t, dir)

			c.arrive(t, filepath.Join(dir, socketName))

			if !watch.wait(t.Context(), time.Minute) {
				t.Error("the wait ended on its fallback timer, want it to end when the socket arrived")
			}
		})
	}
}

// Weston creates a lock file next to its socket. The watch ignores it
// because its name differs from the socket's.
func TestAnotherFileDoesNotEndTheWait(t *testing.T) {
	dir := t.TempDir()
	watch := arrivalsIn(t, dir)

	if err := os.WriteFile(filepath.Join(dir, socketName+".lock"), nil, 0o600); err != nil {
		t.Fatalf("writing the lock file: %v", err)
	}

	if watch.wait(t.Context(), 50*time.Millisecond) {
		t.Error("the wait ended when the lock file arrived, want it to ignore the lock file")
	}
}

// An arrival before ready is discarded, because the dial after ready
// already finds the socket.
func TestReadyForgetsAnEarlierArrival(t *testing.T) {
	dir := t.TempDir()
	watch := arrivalsIn(t, dir)
	listenOnSocket(t, filepath.Join(dir, socketName))
	if !watch.wait(t.Context(), time.Minute) {
		t.Fatal("the wait ended on its timer, want it to end when the socket arrived")
	}
	if err := os.Remove(filepath.Join(dir, socketName)); err != nil {
		t.Fatalf("removing the socket: %v", err)
	}
	listenOnSocket(t, filepath.Join(dir, socketName))
	time.Sleep(50 * time.Millisecond)

	watch.ready()

	if watch.wait(t.Context(), 50*time.Millisecond) {
		t.Error("the wait ended on an arrival from before ready, want it to ignore that arrival")
	}
}

func TestTheWaitEndsWithItsContext(t *testing.T) {
	watch := arrivalsIn(t, t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if watch.wait(ctx, time.Minute) {
		t.Error("the wait reported an arrival, want false because its context ended")
	}
}

// The directory does not exist when the watch is created, so the
// first wait lasts its whole fallback. After the directory exists,
// ready starts the watch, and the file's arrival ends the next wait.
func TestTheWatchStartsWhenItsDirectoryArrives(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runtime")
	watch := arrivalsIn(t, dir)
	if watch.wait(t.Context(), 50*time.Millisecond) {
		t.Fatal("the wait ended before the directory existed, want it to last its whole timeout")
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("creating the runtime directory: %v", err)
	}

	watch.ready()
	listenOnSocket(t, filepath.Join(dir, socketName))

	if !watch.wait(t.Context(), time.Minute) {
		t.Error("the wait ended on its fallback timer, want it to end when the socket arrived")
	}
}

// The watched directory is removed and created again. The removal
// wakes the waiter, ready starts a watch on the new directory, and the
// socket's arrival in it ends the next wait.
func TestTheWatchFollowsARecreatedDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("creating the runtime directory: %v", err)
	}
	watch := arrivalsIn(t, dir)

	if err := os.Remove(dir); err != nil {
		t.Fatalf("removing the socket: %v", err)
	}
	if !watch.wait(t.Context(), time.Minute) {
		t.Fatal("the removal of the directory did not wake the wait")
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("creating the runtime directory: %v", err)
	}
	watch.ready()
	listenOnSocket(t, filepath.Join(dir, socketName))

	if !watch.wait(t.Context(), time.Minute) {
		t.Error("the wait ended on its fallback timer, want it to end when the socket arrived in the new directory")
	}
}

// The wait between dials doubles up to the limit, so a compositor
// that stays down is dialed once every compositorDialLimit.
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

// After a connection that served, the wait starts over. After a failed
// dial that the socket's arrival triggered, the wait is listenGrace.
func TestTheWaitBeforeTheNextDial(t *testing.T) {
	cases := []struct {
		name    string
		served  bool
		arrived bool
		want    time.Duration
	}{
		{"after a connection that served", true, false, compositorDialInterval},
		{"after a refusal the arrival started", false, true, listenGrace},
		{"after a refusal the fallback started", false, false, 2 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := nextDialWait(c.served, c.arrived, time.Second, compositorDialInterval, compositorDialLimit)
			if got != c.want {
				t.Errorf("nextDialWait = %s, want %s", got, c.want)
			}
		})
	}
}

// The watch starts with no compositor, and its fallback timer is much
// longer than the test. When the compositor's socket arrives, the
// watch connects to it.
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
// next compositor replaces the file. The watch connects to the new
// compositor when the file is replaced, not when its fallback timer
// ends.
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
		t.Fatalf("removing the socket: %v", err)
	}
	second := westonBenchOn(t, path, map[uint32]string{1: "HDMI-A-1"})

	second.client()
}

// The runtime directory does not exist when the watch starts. The
// watch keeps dialing on its fallback timer and connects after the
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
		t.Fatalf("creating the runtime directory: %v", err)
	}
	server := westonBenchOn(t, path, map[uint32]string{1: "HDMI-A-1"})

	server.client()
}
