package solver

// The watch of the index directory. A Job can download index files
// into the claim while the solver runs, and a person can copy more in
// later, so the solver follows the directory with inotify instead of
// reading it once. It opens the watch first, then reads the whole
// directory, so a file that lands during the read is not lost. When the
// watch ends, it opens a new one and reads the directory again.
//
// A file joins the set only when its writer closes it or renames it
// into the directory, and it leaves the set at its first write, its
// delete, or its rename away. So the engine never opens a file that a
// download has half written. A file already in the directory when the
// watch opens may still be in the middle of its download, so each read
// checks that the file holds all of its data (fits.Complete).
//
// inotify watches an inode, and reports the writes that this machine's
// kernel makes. Every pod on a node that mounts a local claim writes
// the same inodes, so a Job on the solver's node reaches the watch. A
// network file system, such as NFS, reports no write that another
// machine makes.

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

// The events of the index directory. IN_MODIFY is the first write of a
// file in place, so the file leaves the set until IN_CLOSE_WRITE. The
// events of reopenMask end the watch: the directory left its path, or
// the kernel's queue overflowed and dropped events. The kernel sends
// IN_IGNORED, IN_UNMOUNT, and IN_Q_OVERFLOW with no request.
const (
	joinMask   = unix.IN_CLOSE_WRITE | unix.IN_MOVED_TO
	leaveMask  = unix.IN_MODIFY | unix.IN_DELETE | unix.IN_MOVED_FROM
	reopenMask = unix.IN_DELETE_SELF | unix.IN_MOVE_SELF | unix.IN_IGNORED | unix.IN_UNMOUNT | unix.IN_Q_OVERFLOW
	indexMask  = joinMask | leaveMask | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF | unix.IN_ONLYDIR
	// aboveMask watches the nearest directory above a directory that
	// does not exist yet, for the directory that a Job makes.
	aboveMask = unix.IN_CREATE | unix.IN_MOVED_TO | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF | unix.IN_ONLYDIR
)

// inotify is one inotify instance that watches one directory.
type inotify struct {
	file *os.File
	buf  []byte
}

// watchDir opens an inotify instance on one directory. The descriptor
// is non-blocking, so os.File reads it through Go's poller, and a Close
// ends a read that waits.
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

// event is one inotify event: its mask, and the name in the directory
// it is about, empty for an event on the directory itself.
type event struct {
	mask uint32
	name string
}

// read waits for events and answers them. The kernel writes each
// record in the host's byte order: the watch descriptor, the mask, a
// cookie, the length of the name, and the name padded with zero bytes.
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

// indexWatch keeps an index set current with one directory.
type indexWatch struct {
	dir  string
	set  *indexSet
	logs io.Writer
}

// open opens a watch and reads the state it covers. When the directory
// exists, it watches it and reads every file in it. When it does not,
// it watches the nearest directory above it that exists, and empties
// the set, so the next event there tries the directory again. inside
// answers which of the two the watch covers. A watch that the kernel
// refuses for any other reason is an error.
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
		// The directory can appear between the two watches, and no event
		// above would then announce it, so look once more.
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
// root always exists, so the walk ends.
func watchAbove(dir string) (*inotify, error) {
	for above := filepath.Dir(dir); ; above = filepath.Dir(above) {
		watch, err := watchDir(above, aboveMask)
		if !errors.Is(err, fs.ErrNotExist) {
			return watch, err
		}
	}
}

// run follows the watch until ctx ends, and opens a new watch each time
// one ends. It answers the error of a watch that the kernel refuses, or
// of a configuration that does not write.
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

// follow applies each event to the set until the watch ends: an event
// that ends the watch, any event above a directory that does not exist,
// an overflow of the kernel's queue, or a read that fails, such as the
// read that the close at the end of ctx ends. Each of them needs a new
// read of the whole directory.
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

// apply adds a file that its writer finished, and drops one that is
// written, deleted, or renamed away, or that does not read.
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
