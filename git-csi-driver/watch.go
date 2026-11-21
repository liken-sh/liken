package main

// watch.go watches a published work tree with inotify, and scans it
// when the pod stops writing and on a timer. A scan commits and pushes
// what the pod wrote when the volume's class allows it.

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

// defaultQuiesce is how long the tree must go without a write before a
// scan commits it, unless the volume's class sets its own. defaultSweep
// is how often the tree is scanned when no event arrives.
const (
	defaultQuiesce = 30 * time.Second
	defaultSweep   = time.Minute
)

const watchMask = unix.IN_CREATE | unix.IN_DELETE | unix.IN_MODIFY |
	unix.IN_MOVED_FROM | unix.IN_MOVED_TO | unix.IN_CLOSE_WRITE

type watcher struct {
	node    *node
	volume  *volume
	quiesce time.Duration
	sweep   time.Duration
	clock   clock
	cancel  context.CancelFunc
	changes chan struct{}
	// republishWake wakes run to push when a republish brings a credential,
	// either one that was missing after a restart or a rotated one.
	republishWake chan struct{}
	// classed wakes run to restart the quiesce timer with the class's
	// value.
	classed chan struct{}
	running sync.WaitGroup

	// InotifyAddWatch needs the raw descriptor, and calling Fd on the
	// os.File would switch the descriptor to blocking mode, so read
	// could no longer be stopped by closing the file. So the watcher
	// keeps the descriptor and the file separately.
	descriptor int
	inotify    *os.File
	watched    map[int32]string

	// written is when read last received events, or when the watch
	// started. The quiesce counts from it.
	mu      sync.Mutex
	written time.Time
}

// watch does nothing for a read-only volume. The caller holds the
// node's lock.
func (n *node) watch(published *volume) {
	if !published.writeable() {
		return
	}
	ctx, cancel := context.WithCancel(n.base)
	w := &watcher{
		node:          n,
		volume:        published,
		quiesce:       n.quiesce,
		sweep:         n.sweep,
		clock:         n.clock,
		cancel:        cancel,
		changes:       make(chan struct{}, 1),
		republishWake: make(chan struct{}, 1),
		classed:       make(chan struct{}, 1),
		watched:       map[int32]string{},
		written:       n.clock.Now(),
	}
	n.watchers[published.id] = w
	w.running.Add(2)
	w.open(ctx)
	go w.read(ctx)
	go w.run(ctx)
}

// unwatch waits for both goroutines to end, so an unpublished volume
// leaves no inotify file open. The caller holds the node's lock.
func (n *node) unwatch(published *volume) {
	w, found := n.watchers[published.id]
	if !found {
		return
	}
	delete(n.watchers, published.id)
	w.cancel()
	w.running.Wait()
}

// When the kernel refuses an inotify instance, for example at the
// per-user limit, only the sweep scans the volume.
func (w *watcher) open(ctx context.Context) {
	fd, err := w.node.inotify(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		w.node.logger.WarnContext(ctx, "The inotify instance was refused, so only the sweep scans the volume",
			"volume", w.volume.id, "error", err)
		return
	}
	w.descriptor = fd
	w.inotify = os.NewFile(uintptr(fd), "inotify")
	w.add(ctx, w.volume.tree)
}

// add watches dir and every directory under it, because an inotify
// watch does not cover subdirectories.
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
		w.node.logger.WarnContext(ctx, "The inotify watches do not cover every directory of the volume",
			"volume", w.volume.id, "directory", dir, "error", err)
	}
}

// read records one write for each batch of inotify events that has an
// event inside the volume's paths, and adds a watch for each directory
// the pod created. It returns when run closes the file.
//
// An event outside the paths does not restart the quiesce, because the
// driver never commits what it changed. A pod that writes a log outside
// its paths all the time would otherwise never be quiet long enough to
// commit. The first write to each file outside the paths posts an
// Event.
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
		written, outside := false, []string{}
		for _, event := range w.events(buffer[:count]) {
			if event.directory != "" {
				w.add(ctx, event.directory)
			}
			switch {
			case !event.known || inside(w.volume.work.paths, event.path):
				written = true
			case event.write:
				outside = append(outside, event.path)
			}
		}
		if written {
			w.noteWrite()
		}
		w.node.skipOutside(w.volume, outside)
	}
}

// inotifyEvent is one decoded event. directory is the path of a
// directory the pod created, or empty for any other event. path is the
// path that the event names, relative to the root of the tree with
// forward slashes. known is false when the event names no watched
// directory, such as the event that says the kernel dropped events,
// and then path is empty. write is true for an event that created or
// changed a file that is not a directory.
type inotifyEvent struct {
	directory string
	path      string
	known     bool
	write     bool
}

// writeMask is the events that create or change a file.
const writeMask = unix.IN_CREATE | unix.IN_MODIFY | unix.IN_CLOSE_WRITE | unix.IN_MOVED_TO

// events decodes the struct inotify_event records in buffer: watch
// descriptor, mask, cookie, name length, then the name padded with zero
// bytes.
func (w *watcher) events(buffer []byte) []inotifyEvent {
	found := []inotifyEvent{}
	for offset := 0; offset+unix.SizeofInotifyEvent <= len(buffer); {
		descriptor := int32(binary.NativeEndian.Uint32(buffer[offset:]))
		mask := binary.NativeEndian.Uint32(buffer[offset+4:])
		length := int(binary.NativeEndian.Uint32(buffer[offset+12:]))
		name := ""
		if length > 0 {
			name = string(trimZeros(buffer[offset+unix.SizeofInotifyEvent : offset+unix.SizeofInotifyEvent+length]))
		}
		dir, known := w.watched[descriptor]
		one := inotifyEvent{
			known: known,
			write: mask&writeMask != 0 && mask&unix.IN_ISDIR == 0,
		}
		if known {
			one.path = w.relative(filepath.Join(dir, name))
		}
		if mask&unix.IN_CREATE != 0 && mask&unix.IN_ISDIR != 0 {
			one.directory = filepath.Join(dir, name)
		}
		found = append(found, one)
		offset += unix.SizeofInotifyEvent + length
	}
	return found
}

// relative returns the path of a file in the tree relative to the root
// of the tree, with forward slashes. The root itself is ".".
func (w *watcher) relative(path string) string {
	name, _ := filepath.Rel(w.volume.tree, path)
	return filepath.ToSlash(name)
}

func trimZeros(name []byte) []byte {
	for len(name) > 0 && name[len(name)-1] == 0 {
		name = name[:len(name)-1]
	}
	return name
}

// noteWrite records the time of a write and wakes run to restart the
// quiesce timer. The send never blocks, so a burst of writes leaves at
// most one wake-up in the channel.
func (w *watcher) noteWrite() {
	w.mu.Lock()
	w.written = w.clock.Now()
	w.mu.Unlock()
	select {
	case w.changes <- struct{}{}:
	default:
	}
}

func (w *watcher) sinceWrite() time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.clock.Now().Sub(w.written)
}

// quiesceNow returns the class's quiesce when the volume is armed, and
// the driver's default otherwise.
func (w *watcher) quiesceNow() time.Duration {
	if rules := w.volume.policyNow(); rules != nil {
		return rules.quiesce
	}
	return w.quiesce
}

// classChanged tells the watcher that the volume was armed or its
// quiesce changed. The watcher usually starts before the class is read,
// so its timer may already have fired while the volume was unarmed, or
// may be running with the driver's default quiesce. Without this, the
// last write before the class arrived is not committed until the next
// sweep.
func (n *node) classChanged(staged *volume) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if w, found := n.watchers[staged.id]; found && w.volume == staged {
		select {
		case w.classed <- struct{}{}:
		default:
		}
	}
}

// run scans the tree once at start, once the quiesce has passed with
// no write, and on every sweep. The sweep covers writes that inotify did
// not report, such as in a directory whose watch the kernel refused.
//
// The quiesce timer starts with the watch, because a write made while
// the driver was down sends no event. The driver cannot tell when such a
// write ended, so the quiesce counts from the start of the watch.
func (w *watcher) run(ctx context.Context) {
	defer w.running.Done()
	quiesce := w.clock.NewTimer(w.quiesceNow())
	defer quiesce.Stop()
	sweep := w.clock.NewTimer(w.sweep)
	defer sweep.Stop()

	w.scan(ctx)
	for {
		select {
		case <-ctx.Done():
			w.close()
			return
		case <-w.changes:
			quiesce.Reset(w.quiesceNow())
		case <-w.classed:
			quiesce.Reset(max(0, w.quiesceNow()-w.sinceWrite()))
		case <-quiesce.Chan():
			w.scan(ctx)
		case <-sweep.Chan():
			sweep.Reset(w.sweep)
			w.scan(ctx)
		case <-w.republishWake:
			w.pushArrived(ctx)
		}
	}
}

// Closing the inotify file makes the blocked Read in read return an
// error, which ends that goroutine.
func (w *watcher) close() {
	if w.inotify != nil {
		_ = w.inotify.Close()
	}
}

// scan commits and pushes what the class allows, and reports the paths
// that git still shows as changed. An unarmed volume commits nothing, so
// the report is how a person sees its uncommitted work.
func (w *watcher) scan(ctx context.Context) {
	rules, quiet := w.volume.policyNow(), w.sinceWrite()
	// A sweep can fire while the pod is still writing. Checking the
	// quiesce here keeps a half-written file out of a commit.
	if rules != nil && quiet >= rules.quiesce {
		w.node.commit(ctx, w.volume, rules)
	}
	found, err := w.volume.work.pending(ctx)
	if err != nil {
		w.node.logger.WarnContext(ctx, "The pending paths of the volume tree were not read",
			"volume", w.volume.id, "error", err)
		return
	}
	if w.volume.reportPending(found) {
		claim, _, count := w.volume.gaugeValues()
		w.node.logger.InfoContext(ctx, "The volume tree has pending paths",
			"volume", w.volume.id, "paths", count)
		w.node.report(w.volume, claim, corev1.EventTypeNormal, reasonPending,
			pendingMessage(count))
	}
	w.node.pushIfDue(ctx, w.volume, rules, quiet)
	w.volume.forgetOutside(w.volume.work.present)
	w.node.metrics.record(w.volume)
	w.node.recordHealth(ctx, w.volume)
}

func pendingMessage(count int) string {
	return fmt.Sprintf("%d paths pending", count)
}
