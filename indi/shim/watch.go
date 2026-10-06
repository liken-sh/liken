package main

import (
	"context"
	"os"
	"syscall"
)

// watchDir sends on the channel after each change to an entry of a
// directory, and closes the channel when the watch fails or ctx ends.
// Several changes that arrive before the reader takes one are one
// send, because the reader reads the whole file each time.
//
// The kubelet writes a downward API volume in three moves: it writes
// the files in a new directory, points the link ..data_tmp at it, and
// renames ..data_tmp over ..data. The rename is IN_MOVED_TO, and a file
// that a test writes in place is IN_CLOSE_WRITE.
func watchDir(ctx context.Context, dir string) (<-chan struct{}, error) {
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK)
	if err != nil {
		return nil, os.NewSyscallError("inotify_init1", err)
	}
	const mask = syscall.IN_CREATE | syscall.IN_MOVED_TO | syscall.IN_CLOSE_WRITE | syscall.IN_DELETE | syscall.IN_MOVED_FROM
	if _, err := syscall.InotifyAddWatch(fd, dir, mask); err != nil {
		syscall.Close(fd)
		return nil, os.NewSyscallError("inotify_add_watch "+dir, err)
	}
	// A non-blocking descriptor in an os.File reads through Go's
	// poller, so Close ends a Read that waits.
	events := os.NewFile(uintptr(fd), "inotify")
	changes := make(chan struct{}, 1)
	go func() {
		<-ctx.Done()
		events.Close()
	}()
	go func() {
		defer close(changes)
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
	return changes, nil
}
