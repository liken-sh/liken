package main

import (
	"os"
	"syscall"
)

// directoryEvents are the inotify events that a change of the volume
// makes: the kubelet creates a directory for the new copy and a link
// to it, renames the link over ..data, and removes the old copy.
const directoryEvents = syscall.IN_CREATE | syscall.IN_MOVED_TO | syscall.IN_DELETE | syscall.IN_CLOSE_WRITE

// inotifyWatch watches one directory with inotify. The descriptor is
// non-blocking, so os.File reads it through Go's poller, and a close
// ends a read that waits.
func inotifyWatch(dir string) (watcher, error) {
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK)
	if err != nil {
		return watcher{}, os.NewSyscallError("inotify_init1", err)
	}
	if _, err := syscall.InotifyAddWatch(fd, dir, directoryEvents); err != nil {
		syscall.Close(fd)
		return watcher{}, &os.PathError{Op: "inotify_add_watch", Path: dir, Err: err}
	}
	events := os.NewFile(uintptr(fd), "inotify")
	changes := make(chan struct{}, 1)
	go func() {
		defer close(changes)
		// The events themselves are not decoded: any change of the
		// directory sends the waiter to read the file again. A queue
		// that overflowed also ends a read, which is a change too.
		buf := make([]byte, 64*1024)
		for {
			if _, err := events.Read(buf); err != nil {
				return
			}
			select {
			case changes <- struct{}{}:
			default:
			}
		}
	}()
	return watcher{changes: changes, close: events.Close}, nil
}
