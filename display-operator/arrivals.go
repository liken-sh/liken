package main

// Three waits in this operator wait for a file to appear: the output
// watch and the layout link wait for a socket between two
// compositors, and the compositor role waits for the weston.ini the
// declare container writes. Each one waits for the kernel to report
// that the file arrived, and looks then. A look on a short fixed
// interval would cost several wakes a second for as long as the file
// stays missing.

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// The wait after a try that failed, from the wait before it. The
// arrival of the file ends a wait early, so the doubling bounds only
// the tries that no arrival starts.
func nextDialDelay(delay, limit time.Duration) time.Duration {
	return min(2*delay, limit)
}

// A process creates its socket file with bind and accepts connections
// only after listen, so a dial right after the file arrives can meet a
// refusal. The wait after that dial is this short, because listen
// follows bind at once.
const listenGrace = 20 * time.Millisecond

// The wait before the next dial of a socket. A connection that served
// starts the wait over at first. A dial that the socket's arrival
// started and that failed waits listenGrace. Any other failed dial
// doubles the wait, up to limit.
func nextDialWait(served, arrived bool, wait, first, limit time.Duration) time.Duration {
	switch {
	case served:
		return first
	case arrived:
		return listenGrace
	default:
		return nextDialDelay(wait, limit)
	}
}

// An inotify watch on one directory for one or more file names. A process
// creates a socket with bind and a file with open, which the kernel
// reports as a create in the directory, and a file renamed into place
// reports as a move.
//
// The watch is on the directory's inode, so a directory that is
// removed ends it: the kernel sends IN_IGNORED. The watch then wakes
// its waiter and starts again on the next ready, on whatever directory
// holds the path by then. The directories this operator watches are
// volume mounts, which no process can remove while they are mounted,
// so this is the path of a mount that went away under a live pod.
type arrivals struct {
	dir   string
	names []string
	// Arrived holds one wake at most. A waiter needs to know that the
	// file arrived, not how many times.
	arrived chan struct{}

	mu      sync.Mutex
	inotify *os.File
	closed  bool
}

// A watch for path. The directory need not exist yet: until it does,
// the watch is down, and a wait lasts its whole fallback.
func newArrivals(path string) *arrivals {
	return newArrivalsIn(filepath.Dir(path), filepath.Base(path))
}

// A watch for any of several names in one directory.
func newArrivalsIn(dir string, names ...string) *arrivals {
	a := &arrivals{dir: dir, names: names, arrived: make(chan struct{}, 1)}
	_ = a.arm()
	return a
}

// Start the watch when it is down.
func (a *arrivals) arm() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.inotify != nil {
		return nil
	}
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return os.NewSyscallError("inotify_init1", err)
	}
	if _, err := unix.InotifyAddWatch(fd, a.dir, unix.IN_CREATE|unix.IN_MOVED_TO); err != nil {
		_ = unix.Close(fd)
		return os.NewSyscallError("inotify_add_watch", err)
	}
	// A non-blocking descriptor joins Go's poller, so the read parks
	// the goroutine instead of a thread, and a close ends the read.
	a.inotify = os.NewFile(uintptr(fd), "inotify")
	go a.read(a.inotify)
	return nil
}

// Read events until the watch closes or its directory goes, and wake
// on each event that names the file.
func (a *arrivals) read(file *os.File) {
	buffer := make([]byte, 64*(unix.SizeofInotifyEvent+unix.NAME_MAX+1))
	for {
		n, err := file.Read(buffer)
		if err != nil {
			return
		}
		for offset := 0; offset+unix.SizeofInotifyEvent <= n; {
			// The mask and the name's length are fields of the event
			// header, in the machine's own byte order.
			mask := binary.NativeEndian.Uint32(buffer[offset+4 : offset+8])
			length := int(binary.NativeEndian.Uint32(buffer[offset+12 : offset+16]))
			start := offset + unix.SizeofInotifyEvent
			end := min(start+length, n)
			if mask&unix.IN_IGNORED != 0 {
				a.lost(file)
				return
			}
			// An overflow means the kernel dropped events, and one of
			// them can be the arrival, so the waiter looks again.
			if mask&unix.IN_Q_OVERFLOW != 0 || slices.Contains(a.names, string(bytes.TrimRight(buffer[start:end], "\x00"))) {
				a.wake()
			}
			offset = start + length
		}
	}
}

// The directory went, and the watch with it. The waiter wakes so it
// looks again at once and ready starts a new watch.
func (a *arrivals) lost(file *os.File) {
	a.mu.Lock()
	if a.inotify == file {
		a.inotify = nil
	}
	a.mu.Unlock()
	_ = file.Close()
	a.wake()
}

func (a *arrivals) wake() {
	select {
	case a.arrived <- struct{}{}:
	default:
	}
}

// Start the watch if it is down, and forget every arrival so far. A
// caller runs this before it looks for the file: the look sees
// whatever arrived before it, so that arrival owes no second look.
func (a *arrivals) ready() {
	_ = a.arm()
	select {
	case <-a.arrived:
	default:
	}
}

// Wait for the file to arrive, for at most fallback. The answer is
// true when the watch woke, which is an arrival or the loss of the
// directory; either one means look again now.
func (a *arrivals) wait(ctx context.Context, fallback time.Duration) bool {
	timer := time.NewTimer(fallback)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-a.arrived:
		return true
	case <-timer.C:
		return false
	}
}

func (a *arrivals) close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	if a.inotify != nil {
		_ = a.inotify.Close()
		a.inotify = nil
	}
}
