package main

// An inotify watch on a Secret volume for the kubelet's update.
//
// The kubelet does not write a Secret volume's files in place. It
// writes the new files into a new directory beside the old one, points
// a symlink named ..data_tmp at it, and renames that symlink onto
// ..data. Every visible file is a symlink through ..data, so the
// rename changes all of them at once. The kernel reports the rename to
// a watch on the volume's directory as IN_MOVED_TO for the name ..data.
//
// The first update of a volume that was empty is different. The
// kubelet renames ..data into place and then creates the visible
// symlinks, so a read on the rename can find no tls.crt yet. The watch
// also wakes on IN_CREATE for each visible name, and the read after
// the last symlink finds every file.
//
// A watch on tls.crt itself would miss the update: the kubelet never
// writes to that symlink, and the file it names is in a directory the
// kubelet deletes after the swap.

import (
	"bytes"
	"encoding/binary"
	"os"
	"slices"
	"sync"

	"golang.org/x/sys/unix"
)

// volumeData is the name the kubelet renames into place on every
// update of a Secret volume.
const volumeData = "..data"

// volumeSwaps wakes its reader once for each swap of ..data, and for
// each creation of a visible name it follows.
//
// The watch is on the directory's inode, so a directory that is
// removed ends it: the kernel sends IN_IGNORED. The watch then wakes
// its reader and starts again on the next arm, on whatever directory
// holds the path by then. A volume mount cannot be removed while it is
// mounted, so this is the path of a mount that went away under a live
// pod.
type volumeSwaps struct {
	directory string
	names     []string
	// swapped holds one wake at most. A reader needs to know that the
	// volume changed, not how many times.
	swapped chan struct{}

	mu      sync.Mutex
	inotify *os.File
	closed  bool
}

// newVolumeSwaps watches directory for the swap of ..data and for the
// creation of each of the visible names.
func newVolumeSwaps(directory string, names ...string) *volumeSwaps {
	return &volumeSwaps{
		directory: directory,
		names:     append([]string{volumeData}, names...),
		swapped:   make(chan struct{}, 1),
	}
}

// arm starts the watch when it is down, and forgets every swap so far.
// A caller arms before it reads the volume: the read sees whatever
// swapped before it, so that swap owes no second read.
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
	// A non-blocking descriptor joins Go's poller, so the read parks
	// the goroutine instead of a thread, and a close ends the read.
	v.inotify = os.NewFile(uintptr(fd), "inotify")
	go v.read(v.inotify)
	return nil
}

// read reads events until the watch closes or its directory goes, and
// wakes on each event that names ..data or a visible name.
func (v *volumeSwaps) read(file *os.File) {
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

// lost ends a watch whose directory went. The reader wakes so it reads
// the volume at once, and its next arm starts a new watch.
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

// close ends the watch for good. The close of the descriptor ends the
// read goroutine.
func (v *volumeSwaps) close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.closed = true
	if v.inotify != nil {
		_ = v.inotify.Close()
		v.inotify = nil
	}
}
