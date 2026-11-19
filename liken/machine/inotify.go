package machine

// init writes the facts tree, and machine-operator reads it. Both
// sides must see a change when it happens, not on the next tick of a
// timer, so the helpers in this file watch directories with inotify and
// send a wake on a channel when something in them changes. The reader
// then reads the current state again. The helpers ignore which file
// each event names, so the wake channel has a buffer of one and a
// burst of events gives one wake. When the kernel's event queue
// overflows, it sends IN_Q_OVERFLOW, which is a wake too, so a dropped
// event loses nothing.
//
// inotify watches an inode, not a path. The operator reads the tree
// through a hostPath mount of the same inodes that init writes, so it
// receives the events of init's writes. A watch on a file is lost when
// writeAtomic renames a new file over it, so the helpers watch the
// directory, and the rename is an event on the directory.
//
// Each constructor adds its watch before it returns, so a caller that
// reads the state after the call misses no change.
//
// When the watched directory leaves its path, because it is removed,
// replaced by a rename, renamed away, or unmounted, no later change at
// the path reaches the watch. The reader then closes the wake channel,
// as it does when a read fails, and the caller must watch the path
// again and read the whole state. Cancelling the context stops the
// reader and leaves the channel open.
//
// Closing an inotify descriptor does not wake a thread that is blocked
// in read on it. So the descriptor is non-blocking, and the reader
// blocks in poll on it and on the read end of a pipe. When the context
// is done, another goroutine closes the pipe's write end, and poll
// returns.

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

// errWatchClosed is what Sync returns after the reader has stopped and
// closed the inotify descriptor.
var errWatchClosed = errors.New("the inotify watch is closed")

// dirMask is the event set of WatchDir. writeAtomic ends with a
// rename, which raises IN_MOVED_TO. A person who writes a fact file at
// the console, for example with echo, closes it with no rename, which
// raises IN_CLOSE_WRITE. IN_ONLYDIR makes the kernel refuse a path that
// is not a directory, so passing a file by mistake is an error.
const dirMask = unix.IN_MOVED_TO | unix.IN_CLOSE_WRITE | unix.IN_ONLYDIR

// selfMask is added to the mask of each single-directory watch, so the
// reader receives an event when the directory leaves its path.
const selfMask = unix.IN_DELETE_SELF | unix.IN_MOVE_SELF

// goneMask is the set of events on the watched directory that stop the
// reader. The kernel sends IN_IGNORED when it drops a watch, and
// IN_UNMOUNT on an unmount, even when the mask does not ask for them.
// IN_DELETE_SELF covers a removal and a rename over the directory.
// After IN_MOVE_SELF the watch stays on the directory, but the
// directory has another path.
const goneMask = unix.IN_IGNORED | unix.IN_UNMOUNT | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF

// treeMask is the event set for each directory of a TreeWatch. It adds
// IN_CREATE to dirMask, because a new subdirectory raises IN_CREATE or
// IN_MOVED_TO, and the next Sync adds a watch on it. IN_DELETE_SELF and
// IN_MOVE_SELF stop the reader when the root raises them, and are a
// wake when a subdirectory raises them.
const treeMask = unix.IN_MOVED_TO | unix.IN_CLOSE_WRITE |
	unix.IN_CREATE | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF | unix.IN_ONLYDIR

type watch struct {
	fd      int
	cancelR int
	cancelW int
	wake    chan struct{}

	// name, when it is set, limits the wakes to events on that name. An
	// overflow still wakes, because a dropped event can be on that name.
	name string

	// dir is the watch descriptor of the directory that the caller
	// passed, or of the root of a TreeWatch. It is set before the reader
	// starts, so the reader reads it without a lock.
	dir int32

	// fdMu serializes the reader's close of fd with TreeWatch.Sync.
	// Without it, Sync could add a watch to a descriptor number that the
	// reader closed and the process then opened for another file.
	fdMu     sync.Mutex
	fdClosed bool
}

func (w *watch) closeFd() {
	w.fdMu.Lock()
	defer w.fdMu.Unlock()
	if !w.fdClosed {
		w.fdClosed = true
		unix.Close(w.fd)
	}
}

// newWatch creates the inotify descriptor and the cancel pipe. The
// caller adds its watches, then calls start.
func newWatch() (*watch, error) {
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		return nil, fmt.Errorf("creating the inotify descriptor: %w", err)
	}
	var pipe [2]int
	if err := unix.Pipe2(pipe[:], unix.O_CLOEXEC); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("creating the pipe that cancels the watch: %w", err)
	}
	return &watch{
		fd:      fd,
		cancelR: pipe[0],
		cancelW: pipe[1],
		wake:    make(chan struct{}, 1),
	}, nil
}

// closeFds closes every descriptor of a watch that failed before
// start. After start, the goroutines of start close them.
func (w *watch) closeFds() {
	w.closeFd()
	unix.Close(w.cancelR)
	unix.Close(w.cancelW)
}

// start runs the reader and the cancel goroutine. The cancel goroutine
// also returns when the reader stops by itself, so a failed watch
// leaves no goroutine running.
func (w *watch) start(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
		}
		unix.Close(w.cancelW)
	}()
	go func() {
		w.run()
		close(done)
	}()
}

// run is the reader. It closes the wake channel only when it stops by
// itself, so a caller can tell a failed watch from a cancelled one.
// Only run sends on the channel, so the close cannot race a send. It
// closes the inotify descriptor before the channel, so a caller that
// receives the close and then calls Sync gets errWatchClosed.
func (w *watch) run() {
	defer w.closeFd()
	defer unix.Close(w.cancelR)
	fds := []unix.PollFd{
		{Fd: int32(w.fd), Events: unix.POLLIN},
		{Fd: int32(w.cancelR), Events: unix.POLLIN},
	}
	buf := make([]byte, 64*1024)
	for {
		_, err := unix.Poll(fds, -1)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			w.closeFd()
			close(w.wake)
			return
		}
		if fds[1].Revents != 0 {
			return
		}
		if !w.drain(buf) {
			w.closeFd()
			close(w.wake)
			return
		}
	}
}

// drain reads events until the descriptor returns EAGAIN. It returns
// false when a read fails or the watched directory raised a goneMask
// event. It checks the directory before the name, because IN_IGNORED
// and IN_UNMOUNT have no name.
func (w *watch) drain(buf []byte) bool {
	for {
		n, err := unix.Read(w.fd, buf)
		switch {
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.EAGAIN):
			return true
		case err != nil:
			return false
		}
		gone := false
		parseInotifyEvents(buf[:n], func(wd int32, mask uint32, name string) {
			switch {
			case gone:
			case wd == w.dir && mask&goneMask != 0:
				gone = true
			case w.name == "" || name == w.name || mask&unix.IN_Q_OVERFLOW != 0:
				w.signal()
			}
		})
		if gone {
			return false
		}
	}
}

func (w *watch) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// parseInotifyEvents calls visit for each record in the buffer of one
// inotify read. A record is a struct inotify_event in the host's byte
// order: the watch descriptor, the mask, a cookie, and the length of
// the name field, then the name padded with null bytes. A record whose
// name runs past the end of the buffer stops the walk.
func parseInotifyEvents(buf []byte, visit func(wd int32, mask uint32, name string)) {
	for len(buf) >= unix.SizeofInotifyEvent {
		wd := int32(binary.NativeEndian.Uint32(buf[0:4]))
		mask := binary.NativeEndian.Uint32(buf[4:8])
		nameLen := int(binary.NativeEndian.Uint32(buf[12:16]))
		end := unix.SizeofInotifyEvent + nameLen
		if end > len(buf) {
			return
		}
		name := ""
		if nameLen > 0 {
			raw := buf[unix.SizeofInotifyEvent:end]
			if i := bytes.IndexByte(raw, 0); i >= 0 {
				raw = raw[:i]
			}
			name = string(raw)
		}
		visit(wd, mask, name)
		buf = buf[end:]
	}
}

// WatchDirMask watches the directory dir for the events in mask. It is
// for a writer that WatchDir does not cover, such as the log tailer,
// whose file grows by appends and raises IN_MODIFY.
func WatchDirMask(ctx context.Context, dir string, mask uint32) (<-chan struct{}, error) {
	w, err := newWatch()
	if err != nil {
		return nil, err
	}
	wd, err := unix.InotifyAddWatch(w.fd, dir, mask|selfMask|unix.IN_ONLYDIR)
	if err != nil {
		w.closeFds()
		return nil, fmt.Errorf("watching %s: %w", dir, err)
	}
	w.dir = int32(wd)
	w.start(ctx)
	return w.wake, nil
}

// nameMask is the event set of WatchName: the name is created, renamed
// onto, written and closed, deleted, or renamed away.
const nameMask = unix.IN_CREATE | unix.IN_MOVED_TO | unix.IN_CLOSE_WRITE |
	unix.IN_DELETE | unix.IN_MOVED_FROM

// WatchName watches one file in dir and ignores the other names there.
// machine-operator watches hosts in the host's /etc with it, so init's
// writes of resolv.conf and the operator's own temporary file do not
// start a pass.
func WatchName(ctx context.Context, dir, name string) (<-chan struct{}, error) {
	w, err := newWatch()
	if err != nil {
		return nil, err
	}
	w.name = name
	wd, err := unix.InotifyAddWatch(w.fd, dir, nameMask|selfMask|unix.IN_ONLYDIR)
	if err != nil {
		w.closeFds()
		return nil, fmt.Errorf("watching %s: %w", dir, err)
	}
	w.dir = int32(wd)
	w.start(ctx)
	return w.wake, nil
}

func WatchDir(ctx context.Context, dir string) (<-chan struct{}, error) {
	return WatchDirMask(ctx, dir, dirMask)
}

// TreeWatch watches every directory of the facts tree, because inotify
// does not recurse. The caller calls Sync before each read, so a new
// subdirectory is watched before the caller reads it. Wake closes only
// when the root leaves its path or a read fails. A subdirectory that
// leaves the tree only sends a wake.
type TreeWatch struct {
	Wake <-chan struct{}

	w    *watch
	root string
	wds  map[string]int
}

// WatchFactsTree watches each directory under root. A root that does
// not exist is an error.
func WatchFactsTree(ctx context.Context, root string) (*TreeWatch, error) {
	w, err := newWatch()
	if err != nil {
		return nil, err
	}
	t := &TreeWatch{
		Wake: w.wake,
		w:    w,
		root: root,
		wds:  map[string]int{},
	}
	if err := t.Sync(); err != nil {
		w.closeFds()
		return nil, err
	}
	w.dir = int32(t.wds[root])
	w.start(ctx)
	return t, nil
}

// Sync makes the set of watches match the directories under root.
// Adding a watch on a directory that has one returns the same watch
// descriptor. A missing root is an error. A subdirectory that is
// removed during the walk is not, because init can be writing the tree.
func (t *TreeWatch) Sync() error {
	t.w.fdMu.Lock()
	defer t.w.fdMu.Unlock()
	if t.w.fdClosed {
		return errWatchClosed
	}
	seen := map[string]bool{}
	err := filepath.WalkDir(t.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == t.root {
				return err
			}
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.IsDir() {
			return nil
		}
		wd, err := unix.InotifyAddWatch(t.w.fd, path, treeMask)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("watching %s: %w", path, err)
		}
		t.wds[path] = wd
		seen[path] = true
		return nil
	})
	if err != nil {
		return err
	}
	for path, wd := range t.wds {
		if seen[path] {
			continue
		}
		unix.InotifyRmWatch(t.w.fd, uint32(wd))
		delete(t.wds, path)
	}
	return nil
}
