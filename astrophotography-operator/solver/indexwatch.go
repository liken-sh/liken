package solver

// indexWatch keeps the set of index files current with the index
// directory. A Job can download index files into the claim while the
// solver runs, and a person can copy more in later, so the solver
// follows the directory with inotify instead of reading it once. It
// opens the watch first and then reads the whole directory, so a file
// that arrives during the read is not lost. When the watch ends, it
// opens a new one and reads the directory again.
//
// A file joins the set only when its writer closes it or renames it
// into the directory, and leaves the set at a write, a delete, or a
// rename away. So the solver never opens a file that a download has
// half written. A file that is already in the directory when the watch
// opens can still be partway through a download, so each read also
// checks with fits.Complete that the file has all of its data.
//
// inotify reports only the changes that this machine's kernel makes. A
// Job on the solver's node writes to a local claim through the same
// kernel, so the watch receives events for its files. On a network file system such as
// NFS, the watch receives no event for a file that another machine writes.

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// A file that a writer changes in place leaves the set on IN_MODIFY and
// joins again on IN_CLOSE_WRITE. An event in reopenMask means that the
// directory left its path or that the kernel's queue overflowed and
// dropped events, so the watch must open again and read the whole
// directory. The kernel sends IN_IGNORED, IN_UNMOUNT, and IN_Q_OVERFLOW
// without a request, so indexMask leaves them out.
const (
	joinMask   = unix.IN_CLOSE_WRITE | unix.IN_MOVED_TO
	leaveMask  = unix.IN_MODIFY | unix.IN_DELETE | unix.IN_MOVED_FROM
	reopenMask = unix.IN_DELETE_SELF | unix.IN_MOVE_SELF | unix.IN_IGNORED | unix.IN_UNMOUNT | unix.IN_Q_OVERFLOW
	indexMask  = joinMask | leaveMask | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF | unix.IN_ONLYDIR
	// aboveMask watches the nearest existing directory above an index
	// directory that does not exist yet, so the watch receives an event when a
	// Job creates it.
	aboveMask = unix.IN_CREATE | unix.IN_MOVED_TO | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF | unix.IN_ONLYDIR
)

type inotify struct {
	file *os.File
	buf  []byte
}

// watchDir opens an inotify instance on one directory. The descriptor
// is non-blocking, so os.File reads it through Go's poller, and closing
// the file ends a read that is waiting.
func watchDir(path string, mask uint32) (*inotify, error) {
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		return nil, os.NewSyscallError("inotify_init1", err)
	}
	if _, err := unix.InotifyAddWatch(fd, path, mask); err != nil {
		_ = unix.Close(fd)
		return nil, &fs.PathError{Op: "inotify_add_watch", Path: path, Err: err}
	}
	return &inotify{file: os.NewFile(uintptr(fd), "inotify"), buf: make([]byte, 64*1024)}, nil
}

func (w *inotify) close() { _ = w.file.Close() }

// event is one inotify event. name is empty for an event on the watched
// directory itself.
type event struct {
	mask uint32
	name string
}

// read waits for events and returns them. The kernel writes each record
// in the host's byte order: the watch descriptor, the mask, a cookie,
// the length of the name, and the name padded with zero bytes.
func (w *inotify) read() ([]event, error) {
	n, err := w.file.Read(w.buf)
	if err != nil {
		return nil, err
	}
	var events []event
	for buf := w.buf[:n]; len(buf) >= unix.SizeofInotifyEvent; {
		mask := binary.NativeEndian.Uint32(buf[4:8])
		end := unix.SizeofInotifyEvent + int(binary.NativeEndian.Uint32(buf[12:16]))
		name, _, _ := bytes.Cut(buf[unix.SizeofInotifyEvent:end], []byte{0})
		events = append(events, event{mask: mask, name: string(name)})
		buf = buf[end:]
	}
	return events, nil
}

type indexWatch struct {
	dir  string
	set  *indexSet
	logs io.Writer
}

// open watches the index directory and then loads every index file in
// it. When the directory does not exist, open empties the set and
// watches the nearest existing directory above it instead, and inside
// is false. The next event there ends that watch, and run calls open
// again. open returns an error when the kernel refuses a watch for any
// reason other than a missing directory.
func (w *indexWatch) open() (watch *inotify, inside bool, err error) {
	for {
		watch, err := watchDir(w.dir, indexMask)
		if err == nil {
			files, skipped, err := ScanIndexes(w.dir)
			if err != nil {
				watch.close()
				return nil, false, err
			}
			for _, skip := range skipped {
				fmt.Fprintf(w.logs, "left out %v\n", skip)
			}
			if err := w.set.replace(files); err != nil {
				watch.close()
				return nil, false, err
			}
			return watch, true, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, false, err
		}
		watch, err = watchAbove(w.dir)
		if err != nil {
			return nil, false, err
		}
		// The directory can appear after the first watch failed and
		// before the watch above opened. No event reports it then, so
		// check for it once more.
		if _, err := os.Stat(w.dir); err == nil {
			watch.close()
			continue
		}
		fmt.Fprintf(w.logs, "%s does not exist, so the solver waits for it\n", w.dir)
		if err := w.set.replace(nil); err != nil {
			watch.close()
			return nil, false, err
		}
		return watch, false, nil
	}
}

// watchAbove watches the nearest directory above dir that exists. The
// loop ends at the root, which always exists.
func watchAbove(dir string) (*inotify, error) {
	for above := filepath.Dir(dir); ; above = filepath.Dir(above) {
		watch, err := watchDir(above, aboveMask)
		if !errors.Is(err, fs.ErrNotExist) {
			return watch, err
		}
	}
}

// run follows the watch until ctx ends, and opens a new watch each time
// one ends. It returns an error when the kernel refuses a watch or the
// set fails to write the engine's configuration.
func (w *indexWatch) run(ctx context.Context, watch *inotify, inside bool) error {
	for {
		stop := context.AfterFunc(ctx, watch.close)
		err := w.follow(watch, inside)
		stop()
		watch.close()
		if ctx.Err() != nil || err != nil {
			return err
		}
		watch, inside, err = w.open()
		if err != nil {
			return err
		}
	}
}

// follow applies each event to the set. It returns nil, so that run
// opens a new watch and reads the whole directory again, on an event in
// reopenMask, on any event of a watch above a missing directory, and on
// a failed read, such as the read that run interrupts when ctx ends.
func (w *indexWatch) follow(watch *inotify, inside bool) error {
	for {
		events, err := watch.read()
		if err != nil {
			return nil
		}
		for _, e := range events {
			if !inside || e.mask&reopenMask != 0 {
				return nil
			}
			if !isIndexName(e.name) {
				continue
			}
			if err := w.apply(e); err != nil {
				return err
			}
		}
	}
}

// apply adds a file on an event in joinMask, and drops it on any other
// event or when it cannot be read.
func (w *indexWatch) apply(e event) error {
	path := filepath.Join(w.dir, e.name)
	if e.mask&joinMask == 0 {
		return w.set.drop(path)
	}
	file, err := readIndex(path)
	if err != nil {
		fmt.Fprintf(w.logs, "left out %v\n", err)
		return w.set.drop(path)
	}
	return w.set.put(file)
}
