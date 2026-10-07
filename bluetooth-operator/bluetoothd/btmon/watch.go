package main

// The inotify watch on the settings directory.
//
// The watch is on the directory, not on the file. The operator writes
// a new copy of the file and renames it over the old one, so the old
// file's inode, and any watch on it, goes away at each write. A watch
// on the directory sees the rename, a write in place, and a removal.

import (
	"encoding/binary"
	"fmt"
	"os"
	"syscall"
)

// watchMask names the events that change what the btmon file holds,
// and the events that end the directory itself. The kernel sends
// IN_IGNORED and IN_UNMOUNT whatever the mask says.
const watchMask = syscall.IN_CLOSE_WRITE | syscall.IN_MOVED_TO | syscall.IN_MOVED_FROM |
	syscall.IN_DELETE | syscall.IN_DELETE_SELF | syscall.IN_MOVE_SELF

// watchEnded are the events after which the watch reports nothing more
// about the directory.
const watchEnded = syscall.IN_IGNORED | syscall.IN_UNMOUNT | syscall.IN_DELETE_SELF | syscall.IN_MOVE_SELF

// watch reports each change in one directory.
type watch struct {
	dir  string
	file *os.File

	// changes has one slot, so a burst of events makes one wake, and
	// the supervisor reads the file once for the burst. failed has one
	// slot and receives at most one error.
	changes chan struct{}
	failed  chan error
}

// openWatch opens an inotify watch on dir.
//
// The descriptor is nonblocking, so os.NewFile hands it to Go's
// poller. A read then waits in the poller, and close ends the read.
func openWatch(dir string) (*watch, error) {
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK)
	if err != nil {
		return nil, fmt.Errorf("opening an inotify instance: %w", err)
	}
	if _, err := syscall.InotifyAddWatch(fd, dir, watchMask); err != nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("watching %s: %w", dir, err)
	}
	w := &watch{
		dir:     dir,
		file:    os.NewFile(uintptr(fd), "inotify"),
		changes: make(chan struct{}, 1),
		failed:  make(chan error, 1),
	}
	go w.read()
	return w, nil
}

func (w *watch) close() { _ = w.file.Close() }

// read wakes the supervisor for each batch of events, and reports a
// failure when a read fails or the directory goes away.
//
// An overflow of the kernel's queue also wakes the supervisor. The
// supervisor reads the whole file on each wake, so a lost event loses
// nothing.
func (w *watch) read() {
	buf := make([]byte, 4096)
	for {
		n, err := w.file.Read(buf)
		if err != nil {
			w.failed <- fmt.Errorf("reading the watch on %s: %w", w.dir, err)
			return
		}
		for offset := 0; offset+syscall.SizeofInotifyEvent <= n; {
			// struct inotify_event: wd, mask, cookie, and len, each 32
			// bits in the machine's own order, then len bytes of name.
			mask := binary.NativeEndian.Uint32(buf[offset+4:])
			length := binary.NativeEndian.Uint32(buf[offset+12:])
			if mask&watchEnded != 0 {
				w.failed <- fmt.Errorf("the watch on %s ended: the directory went away or was unmounted", w.dir)
				return
			}
			offset += syscall.SizeofInotifyEvent + int(length)
		}
		select {
		case w.changes <- struct{}{}:
		default:
		}
	}
}
