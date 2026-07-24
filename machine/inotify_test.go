package machine

// These tests drive the inotify helpers against a real kernel, because
// the whole point of the helpers is the kernel's own notifications. The
// one exception is the record parser, which runs on a crafted buffer so
// a test can pin the byte layout without a kernel producing it.

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// testCtx returns a context that a test's cleanup cancels, so every
// watch goroutine a test starts stops before the next test runs. A
// leaked reader would keep a descriptor open and watch a directory that
// the next test reuses.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

// mustWatchDir starts a directory watch or fails the test.
func mustWatchDir(t *testing.T, ctx context.Context, dir string) <-chan struct{} {
	t.Helper()
	ch, err := WatchDir(ctx, dir)
	if err != nil {
		t.Fatalf("WatchDir(%q): %v", dir, err)
	}
	return ch
}

// renameInto writes a file into dir the way writeAtomic does: a temp
// file in the same directory, then a rename. The rename is the event
// that a normal fact write produces.
func renameInto(t *testing.T, dir, name string) {
	t.Helper()
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		t.Fatal(err)
	}
	tmp.WriteString("x")
	tmp.Close()
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
}

// writeClose writes a file into dir with an open, a write, and a close,
// the way a person does at the console. This ends in a close, not a
// rename.
func writeClose(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// burstRenames writes n files into dir in a tight loop, to prove that a
// burst of events collapses into a small number of wakes.
func burstRenames(t *testing.T, dir string, n int) {
	t.Helper()
	for i := range n {
		renameInto(t, dir, fmt.Sprintf("b%d", i))
	}
}

// awaitWake fails the test if no wake arrives within a generous limit.
// The limit is long for a test, because the machine that runs it may be
// under load, and a false failure is worse than a slow pass.
func awaitWake(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a wake")
	}
}

// refuteWake fails the test if any wake arrives within a short window.
// It proves silence, so it does not wait long.
func refuteWake(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("got a wake, wanted silence")
	case <-time.After(500 * time.Millisecond):
	}
}

// drainWake clears a pending wake so a later assertion starts from a
// known-empty channel. The channel holds one wake at most.
func drainWake(ch <-chan struct{}) {
	select {
	case <-ch:
	default:
	}
}

// countWakes receives wakes until the channel stays quiet for the given
// window, then returns how many it received. It is how a coalescing
// test counts the wakes a whole burst produced.
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
		t.Fatalf("got %d, want at least %d", got, min)
	}
}

func assertAtMost(t *testing.T, got, max int) {
	t.Helper()
	if got > max {
		t.Fatalf("got %d, want at most %d", got, max)
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

// TestWatchDirCoalescesBurst proves the wake channel collapses a burst.
// Sixty-four writes cannot produce sixty-four wakes, because the reader
// re-reads the whole state and one read serves the whole burst.
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
		t.Fatal("WatchDir on a missing directory should fail")
	}
}

// TestWatchDirStopsAfterCancel proves the reader goroutine exits when
// its context is done. After the cancel settles, a write produces no
// wake, because no reader is draining the descriptor anymore.
func TestWatchDirStopsAfterCancel(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	ch := mustWatchDir(t, ctx, dir)

	renameInto(t, dir, "live")
	awaitWake(t, ch)

	cancel()
	// The reader exits on the next scheduler turn. Allow a bounded grace
	// for it, then demand silence under a write that a live reader would
	// have reported.
	time.Sleep(100 * time.Millisecond)
	drainWake(ch)
	renameInto(t, dir, "after")
	refuteWake(t, ch)
}

func TestWatchFactsTreeMissingRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "facts")
	_, err := WatchFactsTree(testCtx(t), missing)
	if err == nil {
		t.Fatal("WatchFactsTree on a missing root should fail")
	}
}

// TestWatchFactsTreeSeesNewSubdir proves the recursive watch grows. A
// new subdirectory wakes the watch on its parent, and after a Sync the
// watch also covers the new subdirectory, so a write inside it wakes
// too.
func TestWatchFactsTreeSeesNewSubdir(t *testing.T) {
	root := t.TempDir()
	tw, err := WatchFactsTree(testCtx(t), root)
	if err != nil {
		t.Fatal(err)
	}

	sub := filepath.Join(root, "hardware")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	awaitWake(t, tw.Wake)

	if err := tw.Sync(); err != nil {
		t.Fatal(err)
	}
	drainWake(tw.Wake)

	renameInto(t, sub, "cpus")
	awaitWake(t, tw.Wake)
}

// TestTreeWatchSyncDropsVanishedDir proves Sync forgets a directory
// that is gone. The kernel already removed the watch when the directory
// vanished, so Sync only has to drop the bookkeeping.
func TestTreeWatchSyncDropsVanishedDir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "modules")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	tw, err := WatchFactsTree(testCtx(t), root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tw.wds[sub]; !ok {
		t.Fatalf("the initial watch set should hold %q", sub)
	}

	if err := os.RemoveAll(sub); err != nil {
		t.Fatal(err)
	}
	if err := tw.Sync(); err != nil {
		t.Fatal(err)
	}
	if _, ok := tw.wds[sub]; ok {
		t.Fatalf("Sync should have dropped the vanished %q", sub)
	}
}

// TestTreeWatchSyncNestedAndFiles proves Sync walks a nested tree, adds
// a watch for every directory, skips the regular files, and stays
// idempotent when nothing changed. A second Sync over the same tree
// returns the same watch set.
func TestTreeWatchSyncNestedAndFiles(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "hardware", "blockDevices", "sda")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	writeClose(t, root, "role")
	writeClose(t, nested, "sizeBytes")

	tw, err := WatchFactsTree(testCtx(t), root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tw.wds[nested]; !ok {
		t.Fatalf("Sync should watch the nested directory %q", nested)
	}
	if _, ok := tw.wds[filepath.Join(root, "role")]; ok {
		t.Fatal("Sync should not watch a regular file")
	}

	before := len(tw.wds)
	if err := tw.Sync(); err != nil {
		t.Fatal(err)
	}
	if len(tw.wds) != before {
		t.Fatalf("a no-op Sync changed the watch set: %d then %d", before, len(tw.wds))
	}
}

// event is one decoded record, so a parser test can compare what the
// parser walked against what the crafted buffer held.
type event struct {
	wd   int32
	mask uint32
	name string
}

// encodeEvent builds one inotify record: the sixteen-byte header in the
// host's byte order, then the name padded with null bytes to padTo
// bytes. A record with no name has a zero-length name field, the shape
// of an IN_Q_OVERFLOW record.
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

// TestParseInotifyEvents pins the record walk. The buffer holds two
// records back to back: an IN_Q_OVERFLOW with a descriptor of -1 and no
// name, then a normal event whose name is padded past its own length.
// The parser must walk both and trim the padding.
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
		t.Errorf("parsed events:\nwant %+v\ngot  %+v", want, got)
	}
}

// TestParseInotifyEventsTruncated proves a record whose name runs past
// the buffer ends the walk instead of reading out of bounds. A short
// kernel read can leave a partial trailing record.
func TestParseInotifyEventsTruncated(t *testing.T) {
	buf := encodeEvent(1, unix.IN_MOVED_TO, 0, "whole", 8)
	// Claim a name longer than the bytes that follow.
	binary.NativeEndian.PutUint32(buf[12:16], 64)

	var count int
	parseInotifyEvents(buf, func(int32, uint32, string) { count++ })
	if count != 0 {
		t.Errorf("a truncated record should walk to nothing, got %d", count)
	}
}
