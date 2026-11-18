package main

// Several parts of this operator wait for a file to appear: a
// compositor's socket, the weston.ini that the declare container
// writes, and the files of the serving certificate. Each waits on an
// inotify event for the file and checks for it when the event arrives.
// Polling on a short fixed interval would wake the operator several
// times a second for as long as the file is missing.

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

func nextDialDelay(delay, limit time.Duration) time.Duration {
	return min(2*delay, limit)
}

// listenGrace is the wait after a failed dial that the socket's arrival
// triggered. A server creates the socket file in bind and accepts
// connections only after listen, so a dial right after the file
// appears can be refused. listen follows bind almost at once, so the
// retry waits only this long.
const listenGrace = 20 * time.Millisecond

// nextDialWait returns the wait before the next dial of a socket: first
// after a connection that served, listenGrace after a failed dial that
// the socket's arrival triggered, and otherwise double the last wait,
// up to limit.
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

// arrivals is an inotify watch on a directory for files with the given
// names. A socket created with bind and a file created with open both
// raise IN_CREATE, and a file renamed into place raises IN_MOVED_TO.
//
// The watch is on the directory's inode, so when the directory is
// removed the kernel sends IN_IGNORED and the watch ends. The watch
// then wakes its waiter, and the next call to ready starts a new watch
// on whatever directory is at the path by then. The directories this
// operator watches are volume mounts, so this happens only when a
// mount goes away under a running pod.
type arrivals struct {
	dir   string
	names []string
	// arrived has a buffer of one, because a waiter needs to know that
	// the file arrived, not how many events there were.
	arrived chan struct{}

	mu      sync.Mutex
	inotify *os.File
	closed  bool
}

// newArrivals returns a watch for the file at path. The directory does
// not have to exist yet. Until it does, the watch is not running and
// each wait lasts its whole fallback time.
func newArrivals(path string) *arrivals {
	return newArrivalsIn(filepath.Dir(path), filepath.Base(path))
}

func newArrivalsIn(dir string, names ...string) *arrivals {
	a := &arrivals{dir: dir, names: names, arrived: make(chan struct{}, 1)}
	_ = a.arm()
	return a
}

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
	// A non-blocking descriptor is registered with Go's network
	// poller, so a blocked read does not use an OS thread, and closing
	// the file ends the read.
	a.inotify = os.NewFile(uintptr(fd), "inotify")
	go a.read(a.inotify)
	return nil
}

// read wakes the waiter for each event that names a watched file, until
// the file is closed or the directory is removed.
func (a *arrivals) read(file *os.File) {
	buffer := make([]byte, 64*(unix.SizeofInotifyEvent+unix.NAME_MAX+1))
	for {
		n, err := file.Read(buffer)
		if err != nil {
			return
		}
		for offset := 0; offset+unix.SizeofInotifyEvent <= n; {
			// struct inotify_event is in native byte order, with mask
			// at offset 4 and len at offset 12.
			mask := binary.NativeEndian.Uint32(buffer[offset+4 : offset+8])
			length := int(binary.NativeEndian.Uint32(buffer[offset+12 : offset+16]))
			start := offset + unix.SizeofInotifyEvent
			end := min(start+length, n)
			if mask&unix.IN_IGNORED != 0 {
				a.lost(file)
				return
			}
			// After an overflow the kernel has dropped events, and one
			// of them can be the arrival, so wake the waiter to check.
			if mask&unix.IN_Q_OVERFLOW != 0 || slices.Contains(a.names, string(bytes.TrimRight(buffer[start:end], "\x00"))) {
				a.wake()
			}
			offset = start + length
		}
	}
}

// lost ends the watch after the directory is removed and wakes the
// waiter, whose next call to ready starts a new watch.
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

// ready starts the watch if it is not running and discards any pending
// wake. Call it before checking for the file: the check finds anything
// that arrived earlier, and a pending wake would only repeat the check.
func (a *arrivals) ready() {
	_ = a.arm()
	select {
	case <-a.arrived:
	default:
	}
}

// wait blocks until the watch wakes, ctx ends, or fallback passes. It
// returns true when the watch woke, because the file arrived or the
// directory was removed.
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
