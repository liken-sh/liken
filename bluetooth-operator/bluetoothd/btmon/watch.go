package main

// The inotify watch is on the settings directory and not on the btmon
// file, because the operator writes a new copy of the file and renames
// it over the old one. Each rename replaces the file's inode, and a
// watch on the old inode would see no later change. A watch on the
// directory sees the rename, bondfetch's write in place, and a removal.

import (
	"encoding/binary"
	"fmt"
	"os"
	"syscall"
)

// watchMask selects the events that change a file in the directory and
// the events that end the directory. The kernel sends IN_IGNORED and
// IN_UNMOUNT whatever the mask says.
const watchMask = syscall.IN_CLOSE_WRITE | syscall.IN_MOVED_TO | syscall.IN_MOVED_FROM |
	syscall.IN_DELETE | syscall.IN_DELETE_SELF | syscall.IN_MOVE_SELF

// watchEnded is the set of events that make read fail: the directory
// was deleted, moved, or unmounted, or the kernel removed the watch.
// After a move, the writers' path no longer leads to this directory.
const watchEnded = syscall.IN_IGNORED | syscall.IN_UNMOUNT | syscall.IN_DELETE_SELF | syscall.IN_MOVE_SELF

// watch reports changes in one directory.
type watch struct {
	dir  string
	file *os.File

	// changes has a buffer of one and read never blocks on it, so a
	// burst of events wakes the supervisor once. failed receives at most
	// one error.
	changes chan struct{}
	failed  chan error
}

// openWatch makes the descriptor nonblocking so that os.NewFile
// registers it with Go's poller. A read then waits in the poller
// instead of blocking a thread, and close ends a read that is waiting.
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

// read signals changes for each batch of events, and signals failed
// when a read fails or an event in watchEnded arrives. An overflow of
// the kernel's event queue also signals changes, and the supervisor
// reads the whole file on each signal, so a lost event does no harm.
func (w *watch) read() {
	buf := make([]byte, 4096)
	for {
		n, err := w.file.Read(buf)
		if err != nil {
			w.failed <- fmt.Errorf("reading the watch on %s: %w", w.dir, err)
			return
		}
		for offset := 0; offset+syscall.SizeofInotifyEvent <= n; {
			// struct inotify_event: wd, mask, cookie, and len, each a
			// 32-bit value in native byte order, then len bytes of name.
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
