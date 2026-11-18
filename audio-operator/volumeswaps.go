package main

// The kubelet does not rewrite the files of a Secret or ConfigMap
// volume. It writes the new files to a new directory, points a symlink
// ..data_tmp at it, and renames ..data_tmp onto ..data. Every visible
// file is a symlink through ..data, so a single rename replaces all of
// them, and a watch on the volume's directory receives it as
// IN_MOVED_TO with the name ..data.
//
// When the volume was empty, the kubelet renames ..data into place
// before it creates the visible symlinks, so a read right after the
// rename can find no tls.crt yet. The watch also fires on IN_CREATE
// for each visible name, and the read after the last symlink finds
// every file.
//
// A watch on tls.crt itself would miss updates, because the kubelet
// never writes through that symlink and deletes the file it pointed to
// after the rename.

import (
	"bytes"
	"encoding/binary"
	"os"
	"slices"
	"sync"

	"golang.org/x/sys/unix"
)

const volumeData = "..data"

// volumeSwaps sends on swapped when a file named ..data or one of
// names is created in the directory or renamed into it.
//
// inotify watches the directory's inode, not its path. When the
// directory is removed or its filesystem is unmounted, the kernel
// sends IN_IGNORED and drops the watch. volumeSwaps then signals, so
// the reader reads the volume again and its next arm watches the
// directory that is at the path by then.
type volumeSwaps struct {
	directory string
	names     []string
	// swapped holds one pending signal, because the reader needs only
	// to learn that the volume changed, not how many times.
	swapped chan struct{}

	mu      sync.Mutex
	inotify *os.File
	closed  bool
}

// newVolumeSwaps watches nothing until the first arm.
func newVolumeSwaps(directory string, names ...string) *volumeSwaps {
	return &volumeSwaps{
		directory: directory,
		names:     append([]string{volumeData}, names...),
		swapped:   make(chan struct{}, 1),
	}
}

// arm starts the inotify watch if it is not running, and discards a
// pending signal. A caller arms before it reads the volume, so the read
// includes every swap before the arm, and a swap after the arm sends a
// new signal.
func (v *volumeSwaps) arm() error {
	select {
	case <-v.swapped:
	default:
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || v.inotify != nil {
		return nil
	}
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return os.NewSyscallError("inotify_init1", err)
	}
	if _, err := unix.InotifyAddWatch(fd, v.directory, unix.IN_CREATE|unix.IN_MOVED_TO); err != nil {
		_ = unix.Close(fd)
		return os.NewSyscallError("inotify_add_watch "+v.directory, err)
	}
	// os.NewFile registers a non-blocking descriptor with Go's network
	// poller, so a blocked Read parks only its goroutine, and Close makes
	// it return.
	v.inotify = os.NewFile(uintptr(fd), "inotify")
	go v.read(v.inotify)
	return nil
}

// read signals for each event on one of v.names, until the file closes
// or the kernel sends IN_IGNORED.
func (v *volumeSwaps) read(file *os.File) {
	buffer := make([]byte, 64*(unix.SizeofInotifyEvent+unix.NAME_MAX+1))
	for {
		n, err := file.Read(buffer)
		if err != nil {
			return
		}
		for offset := 0; offset+unix.SizeofInotifyEvent <= n; {
			// The kernel writes inotify_event in the machine's byte order.
			mask := binary.NativeEndian.Uint32(buffer[offset+4 : offset+8])
			length := int(binary.NativeEndian.Uint32(buffer[offset+12 : offset+16]))
			start := offset + unix.SizeofInotifyEvent
			end := min(start+length, n)
			if mask&unix.IN_IGNORED != 0 {
				v.lost(file)
				return
			}
			if slices.Contains(v.names, string(bytes.TrimRight(buffer[start:end], "\x00"))) {
				v.wake()
			}
			offset = start + length
		}
	}
}

// lost closes a watch that received IN_IGNORED and signals, so the
// reader reads the volume and calls arm to start a new watch.
func (v *volumeSwaps) lost(file *os.File) {
	v.mu.Lock()
	if v.inotify == file {
		v.inotify = nil
	}
	v.mu.Unlock()
	_ = file.Close()
	v.wake()
}

func (v *volumeSwaps) wake() {
	select {
	case v.swapped <- struct{}{}:
	default:
	}
}

// close stops the watch for good, and a later arm does nothing.
func (v *volumeSwaps) close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.closed = true
	if v.inotify != nil {
		_ = v.inotify.Close()
		v.inotify = nil
	}
}
