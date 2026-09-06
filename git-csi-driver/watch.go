package main

// watch.go holds the inotify watch on a published work tree and the
// sweep that backs it up. Together they decide when the driver reads
// what the pod wrote.

import (
	"context"
	"encoding/binary"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	corev1 "k8s.io/api/core/v1"
)

// defaultQuiesce is how long a tree rests before the driver reads what
// is pending. defaultSweep is how often it reads that anyway. The class
// sets the quiesce in plan 05.
const (
	defaultQuiesce = 30 * time.Second
	defaultSweep   = time.Minute
)

// watchMask is the events that mean the pod changed the tree: a write,
// a create, a delete, and both halves of a rename.
const watchMask = unix.IN_CREATE | unix.IN_DELETE | unix.IN_MODIFY |
	unix.IN_MOVED_FROM | unix.IN_MOVED_TO | unix.IN_CLOSE_WRITE

// watcher is the inotify watch and the sweep of one published work
// tree.
type watcher struct {
	node    *node
	volume  *volume
	quiesce time.Duration
	sweep   time.Duration
	cancel  context.CancelFunc
	changes chan struct{}
	running sync.WaitGroup

	// The raw descriptor adds watches, and the file reads events. Calling
	// Fd on the file would take it out of the runtime's poller and make
	// the read blocking, so both are kept.
	descriptor int
	inotify    *os.File
	watched    map[int32]string

	// When the pod last changed the tree, which is what the
	// quiesce is measured from.
	mu      sync.Mutex
	written time.Time
}

// watch starts the loops that read one published work tree. The caller
// holds the node's lock.
func (n *node) watch(published *volume) {
	if !published.writeable() {
		return
	}
	ctx, cancel := context.WithCancel(n.base)
	seeing := &watcher{
		node:    n,
		volume:  published,
		quiesce: n.quiesce,
		sweep:   n.sweep,
		cancel:  cancel,
		changes: make(chan struct{}, 1),
		watched: map[int32]string{},
		written: time.Now(),
	}
	n.watchers[published.id] = seeing
	seeing.running.Add(2)
	seeing.open(ctx)
	go seeing.read(ctx)
	go seeing.run(ctx)
}

// unwatch ends both loops and waits for them, so a volume the kubelet
// unpublished holds no file open. The caller holds the node's lock.
func (n *node) unwatch(published *volume) {
	seeing, found := n.watchers[published.id]
	if !found {
		return
	}
	delete(n.watchers, published.id)
	seeing.cancel()
	seeing.running.Wait()
}

// open takes an inotify file and adds a watch for the tree and every
// directory under it. A watch the kernel refuses, for example at the
// inotify limit, leaves the sweep as the whole watch, which is why the
// sweep exists.
func (w *watcher) open(ctx context.Context) {
	fd, err := w.node.inotify(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		w.node.logger.WarnContext(ctx, "the watch did not start",
			"volume", w.volume.id, "error", err)
		return
	}
	w.descriptor = fd
	w.inotify = os.NewFile(uintptr(fd), "inotify")
	w.add(ctx, w.volume.tree)
}

// add watches the directory and everything under it, because inotify
// watches one directory at a time.
func (w *watcher) add(ctx context.Context, dir string) {
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return nil
		}
		watched, err := unix.InotifyAddWatch(w.descriptor, path, watchMask)
		if err != nil {
			return err
		}
		w.watched[int32(watched)] = path
		return nil
	})
	if err != nil {
		w.node.logger.WarnContext(ctx, "the watch missed a directory",
			"volume", w.volume.id, "directory", dir, "error", err)
	}
}

// read turns every batch of inotify events into one nudge and adds a
// watch for each directory the pod created. It ends when run closes the
// file.
func (w *watcher) read(ctx context.Context) {
	defer w.running.Done()
	if w.inotify == nil {
		return
	}
	buffer := make([]byte, 16*1024)
	for {
		count, err := w.inotify.Read(buffer)
		if err != nil {
			return
		}
		for _, event := range w.events(buffer[:count]) {
			if event.directory != "" {
				w.add(ctx, event.directory)
			}
		}
		w.nudge()
	}
}

// arrival is one inotify event: the directory the pod created, or
// nothing.
type arrival struct {
	directory string
}

// events reads the kernel's own record: a watch descriptor, a mask, a
// cookie, the name's length, and the name.
func (w *watcher) events(buffer []byte) []arrival {
	found := []arrival{}
	for offset := 0; offset+unix.SizeofInotifyEvent <= len(buffer); {
		descriptor := int32(binary.NativeEndian.Uint32(buffer[offset:]))
		mask := binary.NativeEndian.Uint32(buffer[offset+4:])
		length := int(binary.NativeEndian.Uint32(buffer[offset+12:]))
		name := ""
		if length > 0 {
			name = string(trimZeros(buffer[offset+unix.SizeofInotifyEvent : offset+unix.SizeofInotifyEvent+length]))
		}
		one := arrival{}
		if mask&unix.IN_CREATE != 0 && mask&unix.IN_ISDIR != 0 {
			one.directory = filepath.Join(w.watched[descriptor], name)
		}
		found = append(found, one)
		offset += unix.SizeofInotifyEvent + length
	}
	return found
}

// trimZeros removes the zero bytes inotify pads a name with.
func trimZeros(name []byte) []byte {
	for len(name) > 0 && name[len(name)-1] == 0 {
		name = name[:len(name)-1]
	}
	return name
}

// nudge restarts the quiesce timer. The channel has one slot and the
// send never blocks, so a burst of writes costs one nudge.
func (w *watcher) nudge() {
	w.mu.Lock()
	w.written = time.Now()
	w.mu.Unlock()
	select {
	case w.changes <- struct{}{}:
	default:
	}
}

// quietFor is how long the tree has rested, which is what the
// push policy measures its quiesce against.
func (w *watcher) quietFor() time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return time.Since(w.written)
}

// rest is the quiesce in force. A class that changes it reaches
// the timer at the next nudge, so no remount is needed.
func (w *watcher) rest() time.Duration {
	if rules := w.volume.policyNow(); rules != nil {
		return rules.quiesce
	}
	return w.quiesce
}

// run waits until the tree has been quiet for the quiesce, then reads
// what is pending. The sweep reads it anyway on a timer, because an
// inotify watch the kernel refused reports nothing.
func (w *watcher) run(ctx context.Context) {
	defer w.running.Done()
	quiesce := time.NewTimer(w.quiesce)
	quiesce.Stop()
	defer quiesce.Stop()
	sweep := time.NewTicker(w.sweep)
	defer sweep.Stop()

	w.scan(ctx)
	for {
		select {
		case <-ctx.Done():
			w.close()
			return
		case <-w.changes:
			quiesce.Reset(w.rest())
		case <-quiesce.C:
			w.scan(ctx)
		case <-sweep.C:
			w.scan(ctx)
		}
	}
}

// close ends the read loop, because a read of a closed file answers an
// error.
func (w *watcher) close() {
	if w.inotify != nil {
		_ = w.inotify.Close()
	}
}

// scan commits and pushes what the class allows, then records what git
// still finds in the tree. An unarmed volume commits none of it, and
// its report is what a person reads before a class arms the volume.
func (w *watcher) scan(ctx context.Context) {
	rules, quiet := w.volume.policyNow(), w.quietFor()
	// The sweep reads the tree on its own timer, so the rest is
	// asked for again here: a commit before the tree has rested is a
	// commit of a write the application has not finished.
	if rules != nil && quiet >= rules.quiesce {
		w.node.commit(ctx, w.volume, rules)
	}
	found, err := w.volume.work.pending(ctx)
	if err != nil {
		w.node.logger.WarnContext(ctx, "the tree was not read",
			"volume", w.volume.id, "error", err)
		return
	}
	if w.volume.reportPending(found) {
		claim, _, count := w.volume.reading()
		w.node.logger.InfoContext(ctx, "the tree holds work",
			"volume", w.volume.id, "paths", count)
		w.node.report(ctx, w.volume, claim, corev1.EventTypeNormal, reasonPending,
			pendingMessage(count))
	}
	w.node.pushIfDue(ctx, w.volume, rules, quiet)
	w.node.readings.record(w.volume)
	w.node.noteHealth(ctx, w.volume)
}

// pendingMessage is what the Event says about a tree the driver has not
// committed.
func pendingMessage(count int) string {
	return fmt.Sprintf("%d paths pending", count)
}
