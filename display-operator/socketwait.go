package main

// The output watch holds a connection to the compositor for the life
// of the pod, and the compositor restarts on every mode change and
// every heal. Between two compositors the socket is missing, or it is
// a stale file that refuses the connect. The watch waits for the
// kernel to report that the socket file arrived, and dials then. A
// dial on a short fixed interval would cost four wakes a second for as
// long as the compositor stays down.

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// The first wait between two dials, and the longest that wait grows
// to. The arrival of the socket ends a wait early, so these bound only
// the dials that no arrival starts: the dial into a socket that is
// bound and not yet listening, and every dial while the runtime
// directory has no watch.
const (
	compositorDialInterval = 250 * time.Millisecond
	compositorDialLimit    = 4 * time.Second
)

// The wait after a dial that failed, from the wait before it.
func nextDialDelay(delay, limit time.Duration) time.Duration {
	return min(2*delay, limit)
}

// An inotify watch on one directory for one file name. A compositor
// creates its socket with bind, which the kernel reports as a create
// in the directory, and a file renamed into place reports as a move.
type arrivals struct {
	inotify *os.File
	// Arrived holds one wake at most. A wait needs to know that the
	// file arrived, not how many times.
	arrived chan struct{}
}

// Start the watch on the directory that holds path. The file need not
// exist, and the directory must.
func watchArrivals(path string) (*arrivals, error) {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, os.NewSyscallError("inotify_init1", err)
	}
	if _, err := unix.InotifyAddWatch(fd, filepath.Dir(path), unix.IN_CREATE|unix.IN_MOVED_TO); err != nil {
		_ = unix.Close(fd)
		return nil, os.NewSyscallError("inotify_add_watch", err)
	}
	// A non-blocking descriptor joins Go's poller, so the read below
	// parks the goroutine instead of a thread, and close ends the
	// read.
	watch := &arrivals{
		inotify: os.NewFile(uintptr(fd), "inotify"),
		arrived: make(chan struct{}, 1),
	}
	go watch.read(filepath.Base(path))
	return watch, nil
}

// Read events until the watch closes, and wake on each one that names
// the file.
func (a *arrivals) read(name string) {
	buffer := make([]byte, 64*(unix.SizeofInotifyEvent+unix.NAME_MAX+1))
	for {
		n, err := a.inotify.Read(buffer)
		if err != nil {
			return
		}
		for offset := 0; offset+unix.SizeofInotifyEvent <= n; {
			// The name's length is the event's last field, in the
			// machine's own byte order.
			length := int(binary.NativeEndian.Uint32(buffer[offset+12 : offset+16]))
			start := offset + unix.SizeofInotifyEvent
			end := min(start+length, n)
			if string(bytes.TrimRight(buffer[start:end], "\x00")) == name {
				select {
				case a.arrived <- struct{}{}:
				default:
				}
			}
			offset = start + length
		}
	}
}

// Forget every arrival so far. The dial that follows sees the file
// that arrived, so an arrival before it owes no second dial.
func (a *arrivals) drain() {
	if a == nil {
		return
	}
	select {
	case <-a.arrived:
	default:
	}
}

// Wait for the file to arrive, for at most fallback. The answer is
// true when the file arrived. A nil watch waits the whole fallback,
// which is the watch's state while the directory does not exist.
func (a *arrivals) wait(ctx context.Context, fallback time.Duration) bool {
	var arrived chan struct{}
	if a != nil {
		arrived = a.arrived
	}
	timer := time.NewTimer(fallback)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-arrived:
		return true
	case <-timer.C:
		return false
	}
}

func (a *arrivals) close() {
	if a != nil {
		_ = a.inotify.Close()
	}
}
