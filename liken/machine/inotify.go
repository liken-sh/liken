package machine

// The facts tree carries state between init and the operator, and both
// sides must see a change the moment it lands, not on the next
// tick of a timer. inotify is how the kernel tells a program that a
// directory changed. These helpers wrap it for one use only: a wake.
//
// An event is a trigger, never a message. The kernel reports what
// changed and where, but this package throws that content away. A woken
// reader re-reads the current state of the tree, so it always acts on
// what the filesystem holds now, not on what an event claimed a moment
// ago. This is the reason a burst of events collapses into one wake:
// the reader would read the same final state whether it woke once or a
// hundred times. The wake channel has room for one pending wake, so
// every extra event during a burst is dropped, and the reader does one
// read for the whole burst.
//
// inotify watches an inode, not a path. This matters twice. First, the
// operator reads the tree through a read-only hostPath, which is a bind
// mount of the same inodes that init writes on the host. An event fires
// on the inode, so it crosses the bind mount, and the operator sees a
// change that init made on the other side. Second, a watch on a file
// goes stale the moment writeAtomic renames a new file into place,
// because the rename installs a new inode and the old watched inode is
// gone. So these helpers watch directories, never files. A directory's
// inode is stable across the renames of the files inside it, and a
// rename into the directory is itself an event on the directory.
//
// The watch must exist before the first read, or a change between the
// read and the watch is lost forever. So every constructor here
// establishes the watch, and only then returns to the caller for its
// first scan. A caller that reads before the watch exists has opened a
// window; a caller that watches first has closed it.
//
// The event mask asks for the writes that liken makes and the writes a
// person makes. writeAtomic only ever renames, so IN_MOVED_TO is the
// event that every normal fact write produces. IN_CLOSE_WRITE is here
// for the other writer: a person who, during an incident, echoes a
// value straight into a fact file at the console. That write ends with
// a close, not a rename, and the operator should still wake and read
// it. A watch that ignored the console would make the tree feel dead to
// the one person most likely to be poking at it.
//
// The kernel's event queue can overflow if a reader falls behind. The
// kernel then delivers one IN_Q_OVERFLOW and drops the events it could
// not hold. This is safe here, because an overflow is just another
// wake: the reader re-reads the whole state and misses nothing. The
// content it would have parsed from the lost events is content it never
// trusted.
//
// A watch ends when its directory leaves the path that the caller
// named: a remove, a rename of another directory over it, a rename of
// it away, or an unmount. The kernel then drops the watch, or keeps it
// on an inode that the path no longer names, and no later change at
// the path reaches the reader. So the reader stops and closes its
// channel, the same way it does when a read fails, and the caller
// watches the path again.
//
// Cancellation cannot rely on closing the inotify descriptor, because a
// close does not wake a thread already blocked in a read on that
// descriptor. So the reader never blocks in the read itself. The
// descriptor is non-blocking, and the reader blocks in poll over two
// descriptors: the inotify descriptor and the read end of a cancel
// pipe. When the context is done, a second goroutine closes the write
// end of the pipe, the poll wakes on the hangup, and the reader
// returns. No goroutine outlives its context.

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io/fs"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

// errWatchClosed is what Sync answers once the reader has stopped and
// closed the inotify descriptor.
var errWatchClosed = errors.New("the inotify watch is closed")

// dirMask is the event set for a single watched directory. IN_MOVED_TO
// catches every write that goes through writeAtomic, because that write
// ends in a rename into the directory. IN_CLOSE_WRITE catches a
// person's direct write at the console. IN_ONLYDIR makes the kernel
// refuse the watch if the path is not a directory, which turns a caller
// that passes a file by mistake into an error instead of a stale watch.
const dirMask = unix.IN_MOVED_TO | unix.IN_CLOSE_WRITE | unix.IN_ONLYDIR

// selfMask asks for the events on the watched directory itself, so the
// reader learns that the directory left its path. Every single
// directory watch adds it to the caller's mask. The kernel sends
// IN_IGNORED and IN_UNMOUNT without a request.
const selfMask = unix.IN_DELETE_SELF | unix.IN_MOVE_SELF

// goneMask is the set of events on the watched directory that end the
// watch. IN_IGNORED arrives every time the kernel drops a watch, after
// IN_DELETE_SELF for a remove or a rename over the directory, and
// after IN_UNMOUNT for an unmount. The reader stops on the first of
// them, so it does not send a wake for a directory that is already
// gone. IN_MOVE_SELF leaves the watch in place, but on a directory
// that now has another path, so it ends the watch too.
const goneMask = unix.IN_IGNORED | unix.IN_UNMOUNT | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF

// treeMask is the event set for a directory inside a recursive tree
// watch. It adds the events that announce a subdirectory. IN_CREATE and
// IN_MOVED_TO fire when a new subdirectory appears, so the next Sync
// finds it and adds a watch. IN_DELETE_SELF fires when a watched
// directory is removed; the kernel then also delivers IN_IGNORED and
// drops the watch on its own, so the next Sync only has to forget the
// bookkeeping. IN_MOVE_SELF is for the root, which ends the watch when
// it leaves its path (goneMask). On a subdirectory it is one more
// wake.
const treeMask = unix.IN_MOVED_TO | unix.IN_CLOSE_WRITE |
	unix.IN_CREATE | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF | unix.IN_ONLYDIR

// watch holds the descriptors and the wake channel for one inotify
// instance. The inotify descriptor is non-blocking, so the reader can
// wait for it in poll alongside the cancel pipe instead of blocking in
// a read that a close cannot interrupt.
type watch struct {
	fd      int
	cancelR int
	cancelW int
	wake    chan struct{}

	// name, when it is set, limits the wakes to events on that one
	// name in the watched directory. An overflow still wakes, because
	// the events it dropped may have named it.
	name string

	// dir is the watch descriptor of the directory the caller named:
	// the one directory of a single watch, or the root of a tree. The
	// reader ends the watch on a goneMask event for it. It is set
	// before the reader starts and never changes, so the reader reads
	// it without a lock. Zero matches no record, because the kernel
	// numbers watch descriptors from one.
	dir int32

	// fdMu makes the reader's close of fd and a TreeWatch's Sync
	// exclusive. The reader closes the wake channel before it closes
	// fd, so a caller can see an open channel, call Sync, and add a
	// watch to a descriptor number that the process has since given to
	// another file. fdClosed tells Sync that the reader has stopped.
	fdMu     sync.Mutex
	fdClosed bool
}

// closeFd closes the inotify descriptor once, under fdMu.
func (w *watch) closeFd() {
	w.fdMu.Lock()
	defer w.fdMu.Unlock()
	if !w.fdClosed {
		w.fdClosed = true
		unix.Close(w.fd)
	}
}

// newWatch creates a non-blocking inotify instance and the cancel pipe
// that stops its reader. It adds no watches; the caller adds them
// before it starts the reader, so the watch exists before the first
// scan.
func newWatch() (*watch, error) {
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		return nil, err
	}
	var pipe [2]int
	if err := unix.Pipe2(pipe[:], unix.O_CLOEXEC); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return &watch{
		fd:      fd,
		cancelR: pipe[0],
		cancelW: pipe[1],
		wake:    make(chan struct{}, 1),
	}, nil
}

// closeFds releases every descriptor. A caller uses it only on the
// setup path, before the reader starts. Once the reader runs, the
// reader owns the inotify descriptor and the cancel pipe's read end,
// and the cancel goroutine owns the write end.
func (w *watch) closeFds() {
	w.closeFd()
	unix.Close(w.cancelR)
	unix.Close(w.cancelW)
}

// start launches the reader and the goroutine that cancels it. The
// cancel goroutine waits for the context and then closes the pipe's
// write end, which wakes the reader's poll with a hangup. This split of
// ownership means no descriptor is closed twice: the cancel goroutine
// closes the write end, and the reader closes the rest as it returns.
//
// The cancel goroutine also ends when the reader stops on its own, so a
// watch that failed leaves no goroutine and no descriptor behind for
// the life of the context.
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

// run is the reader loop. It blocks in poll over the inotify descriptor
// and the cancel pipe. A ready inotify descriptor means events to
// drain; a ready cancel pipe means the context is done and the loop
// returns. A poll error or a drain error also ends the loop, for good:
// the reader does not retry. It closes the wake channel on that way
// out, and only on that way out, so a caller can tell a watch that
// died from one whose context ended. A watch that died misses every
// change after it, so the caller watches again and reads the whole
// state again. Only this goroutine sends on the channel, so the close
// cannot race a send. It closes the descriptors it owns as it leaves.
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
			close(w.wake)
			return
		}
		if fds[1].Revents != 0 {
			return
		}
		if !w.drain(buf) {
			close(w.wake)
			return
		}
	}
}

// drain reads every queued event and wakes the channel for each one. It
// reads until the kernel reports EAGAIN, which means the queue is
// empty, because the descriptor is non-blocking. It returns whether the
// reader should keep running: true after a normal drain, false after an
// error or a goneMask event on the watched directory, which end the
// watch. The check for the directory comes before the check for the
// name, because IN_IGNORED and IN_UNMOUNT carry no name.
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

// signal does one non-blocking send on the wake channel. The channel
// holds one wake, so the first event during a burst fills it and every
// event after that is dropped until the reader drains it. This is the
// coalescing: a burst becomes one wake, and the reader does one read.
func (w *watch) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// parseInotifyEvents walks the records in one inotify read and calls
// visit for each. It reads the fixed header with the host's byte order,
// because the kernel writes these records in native form. The header is
// SizeofInotifyEvent bytes: the watch descriptor, the mask, a cookie,
// and the length of the name that follows. The name is padded with null
// bytes to align the next record, so the reader trims at the first null.
// A record whose name runs past the buffer is a truncated read, so the
// walk stops. An IN_Q_OVERFLOW record carries a descriptor of -1 and no
// name, and it walks like any other, because a reader treats it as one
// more wake.
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

// WatchDirMask watches one directory for the events in mask and
// coalesces every event into a wake channel of capacity one. It is the
// seam under WatchDir: a caller whose writer produces a different set of
// events than the fact writers do passes its own mask. The tailer, for
// example, follows a file that a held-open descriptor keeps appending
// to, so it asks for IN_MODIFY rather than the rename and close that a
// fact write ends with. IN_ONLYDIR is always added to the mask, so the
// kernel refuses a watch on a path that is not a directory and a caller
// cannot end up with a stale watch on a file. selfMask is always added
// too, so the watch ends when the directory leaves its path. The watch exists before
// the function returns, so a caller that scans right after the call
// cannot miss a change that lands between the watch and the scan. The
// context ends the watch, and a watch that fails closes the channel
// (run). A directory that does not exist is an error.
func WatchDirMask(ctx context.Context, dir string, mask uint32) (<-chan struct{}, error) {
	w, err := newWatch()
	if err != nil {
		return nil, err
	}
	wd, err := unix.InotifyAddWatch(w.fd, dir, mask|selfMask|unix.IN_ONLYDIR)
	if err != nil {
		w.closeFds()
		return nil, err
	}
	w.dir = int32(wd)
	w.start(ctx)
	return w.wake, nil
}

// nameMask is the event set for one name in a directory: the name
// appears, by a create or a rename onto it, it is written and closed,
// or it leaves, by a delete or a rename away.
const nameMask = unix.IN_CREATE | unix.IN_MOVED_TO | unix.IN_CLOSE_WRITE |
	unix.IN_DELETE | unix.IN_MOVED_FROM

// WatchName watches one name in a directory and coalesces the events
// on that name into a wake channel of capacity one. The watch is on
// the directory, because a writer that replaces the file by a rename
// installs a new inode, and a watch on the old inode would go quiet at
// the first rename. The events on every other name in the directory
// wake nothing. The machine operator watches the host's /etc for the
// name hosts this way, and init's writes of resolv.conf beside it, and
// the operator's own temporary file, do not wake a pass. The watch
// exists before the function returns, the context ends it, and a watch
// that fails or whose directory leaves its path closes the channel
// (run). A directory that does not exist is an error.
func WatchName(ctx context.Context, dir, name string) (<-chan struct{}, error) {
	w, err := newWatch()
	if err != nil {
		return nil, err
	}
	w.name = name
	wd, err := unix.InotifyAddWatch(w.fd, dir, nameMask|selfMask|unix.IN_ONLYDIR)
	if err != nil {
		w.closeFds()
		return nil, err
	}
	w.dir = int32(wd)
	w.start(ctx)
	return w.wake, nil
}

// WatchDir watches one directory for the fact writers' events and
// coalesces every event into a wake channel of capacity one. The watch
// exists before the function returns, so a caller that scans right after
// the call cannot miss a change that lands between the watch and the
// scan. The context ends the watch: when it is done, the reader returns
// and the channel goes quiet. A watch that fails closes the channel
// instead (run). A directory that does not exist is an error.
func WatchDir(ctx context.Context, dir string) (<-chan struct{}, error) {
	return WatchDirMask(ctx, dir, dirMask)
}

// TreeWatch is the recursive form of WatchDir, for the facts tree.
// inotify does not recurse, so one watch covers one directory only. A
// TreeWatch holds a watch for every directory in the tree and adds new
// ones as the tree grows. A caller calls Sync before every read, which
// reconciles the watch set with the tree on disk and closes the window
// between a new subdirectory and the watch on it. Wake fires for a
// change anywhere in the tree, and closes when the watch fails or the
// root leaves its path (run). A subdirectory that leaves is only a
// wake, because the next Sync forgets it.
type TreeWatch struct {
	Wake <-chan struct{}

	w    *watch
	root string
	wds  map[string]int
}

// WatchFactsTree establishes a recursive watch over the tree at root.
// It watches every directory that exists now, then returns, so the
// caller's first read sees a tree that is already watched. A root that
// does not exist is an error, and machine-operator ends the process on
// it, so the kubelet starts it again and the new process watches again.
// The watch ends when the root it found
// leaves its path, and the caller watches again, so a new root at the
// path gets a new watch.
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

// Sync reconciles the watch set with the directories under root. It
// walks the tree and adds a watch on every directory it finds. A watch
// on a directory that already has one is idempotent, because the kernel
// returns the same descriptor for the same inode. It then drops the
// bookkeeping for a directory that is gone; the kernel already removed
// that watch when the directory vanished, so the drop only forgets the
// descriptor. A missing root is an error the caller may retry. A
// directory that vanishes mid-walk is not an error, because the walk is
// a snapshot of a tree that another process is writing.
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
			return err
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
