package main

import (
	"os"
	"syscall"
)

// directoryEvents are the inotify events of a downward API update. The
// kubelet writes the new version into a new directory, renames a
// symbolic link to it over ..data, and removes the old directory.
const directoryEvents = syscall.IN_CREATE | syscall.IN_MOVED_TO | syscall.IN_DELETE | syscall.IN_CLOSE_WRITE

// inotifyWatch sends on the watcher's changes channel whenever the
// directory changes, and closes the channel when the watcher is closed.
// The descriptor is non-blocking so that os.File reads it through Go's
// poller, where a close ends a read that is waiting.
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
		// The events are not decoded, because the waiter reads the file
		// again after any event, a queue overflow included. The channel
		// has a buffer of one, so a burst of events sends one signal.
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
