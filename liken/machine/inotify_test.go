package machine

// These tests run the inotify helpers against the real kernel. The
// record parser is the exception: its tests use a buffer built by hand,
// so they check the byte layout without the kernel.

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// testCtx returns a context that is cancelled when the test ends, so
// the reader goroutines of the test stop.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

func mustWatchDir(t *testing.T, ctx context.Context, dir string) <-chan struct{} {
	t.Helper()
	ch, err := WatchDir(ctx, dir)
	if err != nil {
		t.Fatalf("watching %s: %v", dir, err)
	}
	return ch
}

// renameInto writes a file into dir the way writeAtomic does: it
// creates a temporary file in dir and renames it to name.
func renameInto(t *testing.T, dir, name string) {
	t.Helper()
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		t.Fatalf("creating the temporary file: %v", err)
	}
	tmp.WriteString("x")
	tmp.Close()
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		t.Fatalf("renaming the file into place: %v", err)
	}
}

// writeClose writes a file into dir directly, the way a person does at
// the console. The write ends with a close and has no rename.
func writeClose(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
}

func burstRenames(t *testing.T, dir string, n int) {
	t.Helper()
	for i := range n {
		renameInto(t, dir, fmt.Sprintf("b%d", i))
	}
}

// awaitWake fails the test if no wake arrives within 2 seconds. The
// limit is long so that a loaded machine does not fail the test.
func awaitWake(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("no wake arrived before the timeout, want one")
	}
}

func requireNoWake(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("the channel got a wake, want silence")
	case <-time.After(500 * time.Millisecond):
	}
}

func drainWake(ch <-chan struct{}) {
	select {
	case <-ch:
	default:
	}
}

// countWakes receives wakes until the channel is quiet for the given
// duration, and returns how many it received.
func countWakes(ch <-chan struct{}, quiet time.Duration) int {
	count := 0
	for {
		select {
		case <-ch:
			count++
		case <-time.After(quiet):
			return count
		}
	}
}

func assertAtLeast(t *testing.T, got, min int) {
	t.Helper()
	if got < min {
		t.Fatalf("the watch sent %d wakes, want at least %d", got, min)
	}
}

func assertAtMost(t *testing.T, got, max int) {
	t.Helper()
	if got > max {
		t.Fatalf("the watch sent %d wakes, want at most %d", got, max)
	}
}

func TestWatchDirWakesOnRename(t *testing.T) {
	dir := t.TempDir()
	ch := mustWatchDir(t, testCtx(t), dir)
	renameInto(t, dir, "fact")
	awaitWake(t, ch)
}

func TestWatchDirWakesOnDirectWrite(t *testing.T) {
	dir := t.TempDir()
	ch := mustWatchDir(t, testCtx(t), dir)
	writeClose(t, dir, "fact")
	awaitWake(t, ch)
}

func TestWatchDirCoalescesBurst(t *testing.T) {
	dir := t.TempDir()
	ch := mustWatchDir(t, testCtx(t), dir)
	const n = 64
	burstRenames(t, dir, n)
	got := countWakes(ch, 300*time.Millisecond)
	assertAtLeast(t, got, 1)
	assertAtMost(t, got, n/4)
}

func TestWatchDirMissingDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	_, err := WatchDir(testCtx(t), missing)
	if err == nil {
		t.Fatal("WatchDir on a missing directory returned no error, want one")
	}
}

func TestWatchDirSendsNoWakeAfterCancel(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	ch := mustWatchDir(t, ctx, dir)

	renameInto(t, dir, "live")
	awaitWake(t, ch)

	cancel()
	// Give the reader time to return before the write.
	time.Sleep(100 * time.Millisecond)
	drainWake(ch)
	renameInto(t, dir, "after")
	requireNoWake(t, ch)
}

// The test replaces the inotify descriptor with a number that the
// process never opened, so poll reports POLLNVAL and read fails with
// EBADF. The reader must treat a failed read as the end of the watch
// and close the channel.
func TestWatchClosesItsChannelWhenTheReaderFails(t *testing.T) {
	w, err := newWatch()
	if err != nil {
		t.Fatalf("creating the watch: %v", err)
	}
	unix.Close(w.fd)
	w.fd = 1 << 20
	w.start(testCtx(t))

	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-w.wake:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("the wake channel did not close after the reader failed")
		}
	}
}

// The test runs the reader itself, so it can wait for the reader to
// return before it checks the channel.
func TestWatchKeepsItsChannelOpenAfterCancel(t *testing.T) {
	w, err := newWatch()
	if err != nil {
		t.Fatalf("creating the watch: %v", err)
	}
	done := make(chan struct{})
	go func() {
		w.run()
		close(done)
	}()

	unix.Close(w.cancelW)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the reader did not return after the cancel")
	}
	select {
	case _, ok := <-w.wake:
		if !ok {
			t.Fatal("the wake channel closed on a cancel")
		}
	default:
	}
}

func TestWatchFactsTreeMissingRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "facts")
	_, err := WatchFactsTree(testCtx(t), missing)
	if err == nil {
		t.Fatal("WatchFactsTree on a missing root returned no error, want one")
	}
}

// A new subdirectory wakes the watch on its parent. After a Sync, a
// write inside the new subdirectory wakes the watch too.
func TestWatchFactsTreeWakesForANewSubdirectory(t *testing.T) {
	root := t.TempDir()
	tw, err := WatchFactsTree(testCtx(t), root)
	if err != nil {
		t.Fatalf("watching the facts tree: %v", err)
	}

	sub := filepath.Join(root, "hardware")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
	awaitWake(t, tw.Wake)

	if err := tw.Sync(); err != nil {
		t.Fatalf("syncing the watches: %v", err)
	}
	drainWake(tw.Wake)

	renameInto(t, sub, "cpus")
	awaitWake(t, tw.Wake)
}

func TestTreeWatchSyncDropsVanishedDir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "modules")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
	tw, err := WatchFactsTree(testCtx(t), root)
	if err != nil {
		t.Fatalf("watching the facts tree: %v", err)
	}
	if _, ok := tw.wds[sub]; !ok {
		t.Fatalf("the initial watch set lacks %q, want it", sub)
	}

	if err := os.RemoveAll(sub); err != nil {
		t.Fatalf("removing the directory: %v", err)
	}
	if err := tw.Sync(); err != nil {
		t.Fatalf("syncing the watches: %v", err)
	}
	if _, ok := tw.wds[sub]; ok {
		t.Fatalf("the watch set still holds the removed directory %q, want it dropped", sub)
	}
}

func TestTreeWatchSyncNestedAndFiles(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "hardware", "blockDevices", "sda")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
	writeClose(t, root, "role")
	writeClose(t, nested, "sizeBytes")

	tw, err := WatchFactsTree(testCtx(t), root)
	if err != nil {
		t.Fatalf("watching the facts tree: %v", err)
	}
	if _, ok := tw.wds[nested]; !ok {
		t.Fatalf("the watch set lacks the nested directory %q, want it", nested)
	}
	if _, ok := tw.wds[filepath.Join(root, "role")]; ok {
		t.Fatal("the watch set holds a regular file, want only directories")
	}

	before := len(tw.wds)
	if err := tw.Sync(); err != nil {
		t.Fatalf("syncing the watches: %v", err)
	}
	if len(tw.wds) != before {
		t.Fatalf("a Sync with no changes took the watch set from %d to %d entries, want no change", before, len(tw.wds))
	}
}

// event is one record that parseInotifyEvents passed to its callback.
type event struct {
	wd   int32
	mask uint32
	name string
}

// encodeEvent builds one inotify record: the 16-byte header in the
// host's byte order, then the name padded with null bytes to padTo
// bytes. An empty name gives a zero-length name field, as in an
// IN_Q_OVERFLOW record.
func encodeEvent(wd int32, mask, cookie uint32, name string, padTo int) []byte {
	header := make([]byte, unix.SizeofInotifyEvent)
	binary.NativeEndian.PutUint32(header[0:4], uint32(wd))
	binary.NativeEndian.PutUint32(header[4:8], mask)
	binary.NativeEndian.PutUint32(header[8:12], cookie)
	var nameField []byte
	if name != "" {
		nameField = make([]byte, padTo)
		copy(nameField, name)
	}
	binary.NativeEndian.PutUint32(header[12:16], uint32(len(nameField)))
	return append(header, nameField...)
}

// The buffer holds an IN_Q_OVERFLOW record with a watch descriptor of -1
// and no name, then a record whose name has null padding after it.
func TestParseInotifyEvents(t *testing.T) {
	buf := encodeEvent(-1, unix.IN_Q_OVERFLOW, 0, "", 0)
	buf = append(buf, encodeEvent(3, unix.IN_MOVED_TO, 0, "systemA", 12)...)

	var got []event
	parseInotifyEvents(buf, func(wd int32, mask uint32, name string) {
		got = append(got, event{wd: wd, mask: mask, name: name})
	})

	want := []event{
		{wd: -1, mask: unix.IN_Q_OVERFLOW, name: ""},
		{wd: 3, mask: unix.IN_MOVED_TO, name: "systemA"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parsed events are %+v, want %+v", got, want)
	}
}

func TestParseInotifyEventsTruncated(t *testing.T) {
	buf := encodeEvent(1, unix.IN_MOVED_TO, 0, "whole", 8)
	// Claim a name longer than the bytes that follow.
	binary.NativeEndian.PutUint32(buf[12:16], 64)

	var count int
	parseInotifyEvents(buf, func(int32, uint32, string) { count++ })
	if count != 0 {
		t.Errorf("a truncated record gave %d visits, want 0", count)
	}
}

func mustWatchName(t *testing.T, dir, name string) <-chan struct{} {
	t.Helper()
	ch, err := WatchName(testCtx(t), dir, name)
	if err != nil {
		t.Fatalf("watching %s in %s: %v", name, dir, err)
	}
	return ch
}

// The temporary file that renameInto creates has another name, so only
// the rename onto the watched name sends a wake.
func TestWatchNameWakesOnARenameOntoTheName(t *testing.T) {
	dir := t.TempDir()
	ch := mustWatchName(t, dir, "hosts")
	renameInto(t, dir, "hosts")
	awaitWake(t, ch)
	requireNoWake(t, ch)
}

// init writes resolv.conf in the same directory as hosts, and that must
// not wake a watch on hosts.
func TestWatchNameIgnoresTheOtherNamesInTheDirectory(t *testing.T) {
	dir := t.TempDir()
	ch := mustWatchName(t, dir, "hosts")
	writeClose(t, dir, "resolv.conf")
	renameInto(t, dir, "resolv.conf")
	requireNoWake(t, ch)
}

// When someone deletes /etc/hosts, the wake starts a machine-operator
// pass, and the pass writes the file again.
func TestWatchNameWakesWhenTheNameLeaves(t *testing.T) {
	dir := t.TempDir()
	writeClose(t, dir, "hosts")
	ch := mustWatchName(t, dir, "hosts")
	if err := os.Remove(filepath.Join(dir, "hosts")); err != nil {
		t.Fatalf("removing the hosts file: %v", err)
	}
	awaitWake(t, ch)
}

func TestWatchNameMissingDirectory(t *testing.T) {
	if _, err := WatchName(testCtx(t), filepath.Join(t.TempDir(), "absent"), "hosts"); err == nil {
		t.Error("WatchName on a missing directory succeeded, want an error")
	}
}

// awaitClosed fails the test if the channel does not close within 2
// seconds. It ignores wakes before the close.
func awaitClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("the wake channel did not close")
		}
	}
}

// requireOpen fails the test if the channel closes within 500
// milliseconds. It ignores wakes.
func requireOpen(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	window := time.After(500 * time.Millisecond)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				t.Fatal("the wake channel closed, want it open")
			}
		case <-window:
			return
		}
	}
}

// watchedDir creates an empty directory inside a temporary directory,
// so that a test can remove or replace it.
func watchedDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "etc")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
	return dir
}

// directoryRemovals are the ways a watched directory leaves its path. After any
// of them the watch receives no event for the path, so it must end and
// close its channel.
var directoryRemovals = []struct {
	name string
	end  func(t *testing.T, dir string)
}{
	{"removed", func(t *testing.T, dir string) {
		if err := os.Remove(dir); err != nil {
			t.Fatalf("removing the directory: %v", err)
		}
	}},
	{"renamed over", func(t *testing.T, dir string) {
		other := dir + ".new"
		if err := os.Mkdir(other, 0o755); err != nil {
			t.Fatalf("creating the directory: %v", err)
		}
		// os.Rename refuses a directory as the target, so the test
		// calls rename(2) directly.
		if err := unix.Rename(other, dir); err != nil {
			t.Fatalf("renaming the directory: %v", err)
		}
	}},
	{"renamed away", func(t *testing.T, dir string) {
		if err := os.Rename(dir, dir+".old"); err != nil {
			t.Fatalf("renaming the directory away: %v", err)
		}
	}},
}

func TestWatchNameClosesWhenTheDirectoryLeaves(t *testing.T) {
	for _, tc := range directoryRemovals {
		t.Run(tc.name, func(t *testing.T) {
			dir := watchedDir(t)
			ch := mustWatchName(t, dir, "hosts")
			tc.end(t, dir)
			awaitClosed(t, ch)
		})
	}
}

func TestWatchFactsTreeClosesWhenTheRootLeaves(t *testing.T) {
	for _, tc := range directoryRemovals {
		t.Run(tc.name, func(t *testing.T) {
			root := watchedDir(t)
			tw, err := WatchFactsTree(testCtx(t), root)
			if err != nil {
				t.Fatalf("watching the facts tree: %v", err)
			}
			tc.end(t, root)
			awaitClosed(t, tw.Wake)
		})
	}
}

// The root is still in place, so the watch stays open and keeps
// watching the rest of the tree.
func TestWatchFactsTreeStaysOpenWhenASubdirectoryLeaves(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "modules")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
	tw, err := WatchFactsTree(testCtx(t), root)
	if err != nil {
		t.Fatalf("watching the facts tree: %v", err)
	}
	if err := os.Remove(sub); err != nil {
		t.Fatalf("removing the directory: %v", err)
	}
	awaitWake(t, tw.Wake)
	requireOpen(t, tw.Wake)
	renameInto(t, root, "role")
	awaitWake(t, tw.Wake)
}

// Sync must not add a watch after the reader stopped, because the
// descriptor number may belong to another file by then.
func TestSyncAfterTheReaderStoppedAddsNothing(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	w, err := WatchFactsTree(ctx, root)
	if err != nil {
		t.Fatalf("watching the facts tree: %v", err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatalf("removing the directory: %v", err)
	}
	for range w.Wake {
	}
	cancel()

	if err := w.Sync(); !errors.Is(err, errWatchClosed) {
		t.Errorf("Sync after the reader stopped returned %v, want errWatchClosed", err)
	}
}
