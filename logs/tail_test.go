package main

// These tests run the tailer against real files in a tempdir, and do
// the things that init's rotation actually does to them: growing,
// being renamed aside, reappearing. The tailer follows the file through
// inotify events, and the tests watch its output arrive. A short
// backstop keeps the timing tests fast. The event tests hold the
// backstop far off, so only an inotify wake can deliver a change in
// time, which proves the tailer is event-driven and not just polling
// under another name.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a threadsafe output destination. The tailer writes
// from its own goroutine while the test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fastBackstop shortens the tailer's backstop timer and makes every
// checkpoint immediate, so a timing test measures behavior instead of
// waiting. It lets the backstop, not an event, drive the tailer, which
// is what a behavior test wants: the outcome must hold whichever wakes
// the tailer. The backstop is a package variable, which is why tests in
// this package must not run in parallel.
func fastBackstop(t *testing.T) {
	t.Helper()
	immediateCheckpoints(t)
	old := backstopInterval
	backstopInterval = 2 * time.Millisecond
	t.Cleanup(func() { backstopInterval = old })
}

// eventOnly holds the backstop timer far out of reach and makes every
// checkpoint immediate. A test that still sees fast delivery under this
// helper proves the inotify path carried the change, because the timer
// could not have.
func eventOnly(t *testing.T) {
	t.Helper()
	immediateCheckpoints(t)
	old := backstopInterval
	backstopInterval = time.Minute
	t.Cleanup(func() { backstopInterval = old })
}

// appendLine grows the file the way a live writer does: open for
// append, write, close. It writes the line verbatim, including the
// terminator, so a test can also leave a line unterminated.
func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// followedFile runs the tailer against path in the background. The
// returned stop function cancels the tailer and waits for a clean
// exit.
func followedFile(t *testing.T, path, curDir string) (*syncBuffer, func()) {
	t.Helper()
	out := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	now := func() time.Time { return observed }
	go func() {
		done <- tailFile(ctx, path, newEnvelopeWriter(out), curDir, now)
	}()
	stop := func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("tailer exited with %v, want context.Canceled", err)
		}
	}
	return out, stop
}

// awaitEnvelopes polls the output until n envelopes have arrived.
func awaitEnvelopes(t *testing.T, out *syncBuffer, n int) []envelope {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		es := parseEnvelopes(t, out.String())
		if len(es) >= n {
			return es
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d envelopes; have:\n%s", n, out.String())
	return nil
}

func TestTailerFollowsAGrowingFile(t *testing.T) {
	fastBackstop(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "k3s.log")
	if err := os.WriteFile(path, []byte("first line\nsecond line\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, stop := followedFile(t, path, t.TempDir())
	defer stop()
	es := awaitEnvelopes(t, out, 2)
	if es[0].Message != "first line" || es[0].Seq != 0 {
		t.Errorf("first: %+v", es[0])
	}
	if es[1].Message != "second line" || es[1].Seq != uint64(len("first line\n")) {
		t.Errorf("second line's seq should be its byte offset: %+v", es[1])
	}

	appendLine(t, path, "third line\n")
	es = awaitEnvelopes(t, out, 3)
	if es[2].Message != "third line" {
		t.Errorf("third: %+v", es[2])
	}
}

func TestTailerLiftsHeadersItRecognizes(t *testing.T) {
	fastBackstop(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "k3s.log")
	line := `time="2026-07-07T13:51:16Z" level=error msg="tunnel disconnected"`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, stop := followedFile(t, path, t.TempDir())
	defer stop()
	es := awaitEnvelopes(t, out, 1)
	if es[0].Severity != "err" || es[0].Time != "2026-07-07T13:51:16Z" {
		t.Errorf("lifted facts: %+v", es[0])
	}
	if es[0].Message != line {
		t.Errorf("message must stay verbatim: %q", es[0].Message)
	}
}

func TestTailerWaitsForTheFileToExist(t *testing.T) {
	fastBackstop(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "containerd.log")

	out, stop := followedFile(t, path, t.TempDir())
	defer stop()
	// Let the tailer's watch settle against nothing, then create it.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte("containerd successfully booted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	es := awaitEnvelopes(t, out, 1)
	if es[0].Message != "containerd successfully booted" {
		t.Errorf("got %+v", es[0])
	}
}

// awaitCheckpoint polls the cursor directory until the tailer has
// checkpointed the given offset. A tailer only checkpoints once it
// reaches EOF. Because of this, watching for the offset confirms
// that the tailer has both sent and recorded everything written so
// far.
func awaitCheckpoint(t *testing.T, curDir string, offset int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var cur tailCursor
		if loadCursor(curDir, &cur) && cur.Offset == offset {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for a checkpoint at offset %d", offset)
}

func TestTailerResumesFromItsCursor(t *testing.T) {
	fastBackstop(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "k3s.log")
	curDir := t.TempDir()
	if err := os.WriteFile(path, []byte("old one\nold two\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, stop := followedFile(t, path, curDir)
	awaitEnvelopes(t, out, 2)
	awaitCheckpoint(t, curDir, int64(len("old one\nold two\n")))
	stop()

	appendLine(t, path, "new after restart\n")

	out2, stop2 := followedFile(t, path, curDir)
	defer stop2()
	es := awaitEnvelopes(t, out2, 2)
	if es[0].Message != "liken-logs: resuming "+path+" at offset 16" {
		t.Errorf("resume notice: %+v", es[0])
	}
	if es[1].Message != "new after restart" || es[1].Seq != 16 {
		t.Errorf("only the new line should ship: %+v", es[1])
	}
}

func TestTailerFollowsARotation(t *testing.T) {
	fastBackstop(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "k3s.log")
	// The old generation ends with an unterminated line, as if a
	// writer stopped mid-write. It ships as it is, once the tailer
	// notices the rotation.
	if err := os.WriteFile(path, []byte("finished line\ncut off mid-wr"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, stop := followedFile(t, path, t.TempDir())
	defer stop()
	awaitEnvelopes(t, out, 1)

	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	es := awaitEnvelopes(t, out, 2)
	if es[1].Message != "cut off mid-wr" {
		t.Errorf("the trailing fragment should ship on rotation: %+v", es[1])
	}

	if err := os.WriteFile(path, []byte("fresh generation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	es = awaitEnvelopes(t, out, 3)
	if es[2].Message != "fresh generation" || es[2].Seq != 0 {
		t.Errorf("the new file starts the offsets over: %+v", es[2])
	}
}

func TestTailerReplaysWhenTheFileShrinks(t *testing.T) {
	fastBackstop(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "k3s.log")
	curDir := t.TempDir()
	if err := os.WriteFile(path, []byte("a long line that moves the cursor well along\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, stop := followedFile(t, path, curDir)
	awaitEnvelopes(t, out, 1)
	awaitCheckpoint(t, curDir, int64(len("a long line that moves the cursor well along\n")))
	stop()

	// Truncate in place: same inode, smaller than the cursor.
	if err := os.WriteFile(path, []byte("tiny\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out2, stop2 := followedFile(t, path, curDir)
	defer stop2()
	es := awaitEnvelopes(t, out2, 2)
	if es[0].Severity != "warning" {
		t.Errorf("shrinking should be called out: %+v", es[0])
	}
	if es[1].Message != "tiny" || es[1].Seq != 0 {
		t.Errorf("and the file replayed from the head: %+v", es[1])
	}
}

// Everything written between the tailer's last catch-up and the
// rename is reachable only through the held file. Because of this,
// the tailer must drain the renamed generation to its final EOF
// before moving on.
func TestTailerDrainsARotatedGenerationToItsEnd(t *testing.T) {
	fastBackstop(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "k3s.log")
	if err := os.WriteFile(path, []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, stop := followedFile(t, path, t.TempDir())
	defer stop()
	awaitEnvelopes(t, out, 1)

	appendLine(t, path, "last full line\ncut off mid-wr")
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	es := awaitEnvelopes(t, out, 3)
	if es[1].Message != "last full line" {
		t.Errorf("the drained line should ship: %+v", es[1])
	}
	if es[2].Message != "cut off mid-wr" {
		t.Errorf("the trailing fragment should ship on rotation: %+v", es[2])
	}

	if err := os.WriteFile(path, []byte("fresh generation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	es = awaitEnvelopes(t, out, 4)
	if es[3].Message != "fresh generation" || es[3].Seq != 0 {
		t.Errorf("the new file starts the offsets over: %+v", es[3])
	}
}

func TestTailerStopsCleanlyWhileAwaitingAFile(t *testing.T) {
	fastBackstop(t)
	path := filepath.Join(t.TempDir(), "never-created.log")
	_, stop := followedFile(t, path, t.TempDir())
	// stop verifies that the tailer exits with context.Canceled, even
	// though the file it was waiting for never appeared.
	stop()
}

// A tailer that cannot send lines must exit with the write error, so
// the kubelet restarts it, instead of reading on and dropping lines.
func TestTailerStopsWhenItsOutputFails(t *testing.T) {
	fastBackstop(t)
	path := filepath.Join(t.TempDir(), "k3s.log")
	if err := os.WriteFile(path, []byte("doomed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := tailFile(context.Background(), path, newEnvelopeWriter(brokenWriter{}), t.TempDir(), time.Now)
	if !errors.Is(err, errStdoutGone) {
		t.Errorf("tailFile should surface the write error, got %v", err)
	}
}

// A cursor directory that refuses writes must stop the tailer, the
// same way a failed send does. The checkpoint is what makes the
// tailer durable.
func TestTailerStopsWhenItCannotCheckpoint(t *testing.T) {
	fastBackstop(t)
	path := filepath.Join(t.TempDir(), "k3s.log")
	if err := os.WriteFile(path, []byte("a line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	curDir := t.TempDir()
	if err := os.Chmod(curDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(curDir, 0o700) })
	out := newEnvelopeWriter(&syncBuffer{})
	err := tailFile(context.Background(), path, out, curDir, time.Now)
	if !errors.Is(err, os.ErrPermission) {
		t.Errorf("tailFile should surface the checkpoint error, got %v", err)
	}
}

// TestTailerShipsGrowthOnAnEvent proves the tailer follows a growing
// file through inotify, not a timer. The backstop is a minute away, so
// the appended line can only arrive on an IN_MODIFY event. It arrives
// fast, which is the proof.
func TestTailerShipsGrowthOnAnEvent(t *testing.T) {
	eventOnly(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "k3s.log")
	if err := os.WriteFile(path, []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, stop := followedFile(t, path, t.TempDir())
	defer stop()
	// The first envelope proves the tailer has reached EOF and parked in
	// the wait, with its watch already in place.
	awaitEnvelopes(t, out, 1)

	appendLine(t, path, "second\n")
	es := awaitEnvelopes(t, out, 2)
	if es[1].Message != "second" {
		t.Errorf("growth should ship on the event: %+v", es[1])
	}
}

// TestTailerFollowsARotationOnAnEvent proves the tailer notices a
// rotation through inotify. The rename raises IN_MOVED_FROM, which wakes
// the tailer to drain the renamed generation. With the backstop a minute
// away, only that event can deliver the trailing fragment in time.
func TestTailerFollowsARotationOnAnEvent(t *testing.T) {
	eventOnly(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "k3s.log")
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, stop := followedFile(t, path, t.TempDir())
	defer stop()
	awaitEnvelopes(t, out, 1)

	appendLine(t, path, "trailing fragment")
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	es := awaitEnvelopes(t, out, 2)
	if es[1].Message != "trailing fragment" {
		t.Errorf("rotation should ship the drained fragment on the event: %+v", es[1])
	}
}

// TestTailerAwaitsCreationOnAnEvent proves awaitFile wakes on the
// creation event, not the backstop. containerd.log does not exist when
// the tailer starts, so the tailer parks in awaitFile. With the backstop
// a minute away, only the IN_CREATE event can wake it to open the new
// file.
func TestTailerAwaitsCreationOnAnEvent(t *testing.T) {
	eventOnly(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "containerd.log")

	out, stop := followedFile(t, path, t.TempDir())
	defer stop()
	// Let the watch establish before the file appears, so the creation
	// event is the thing that wakes the tailer.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte("containerd up\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	es := awaitEnvelopes(t, out, 1)
	if es[0].Message != "containerd up" {
		t.Errorf("creation should wake awaitFile on the event: %+v", es[0])
	}
}

// A watch that cannot start is a real fault. The tailer returns the
// error, so the relay exits nonzero and the kubelet restarts it, rather
// than falling back to a poll that would hide the fault. A parent
// directory that does not exist makes the watch fail.
func TestTailerStopsWhenTheWatchCannotStart(t *testing.T) {
	fastBackstop(t)
	path := filepath.Join(t.TempDir(), "gone", "k3s.log")
	err := tailFile(context.Background(), path, newEnvelopeWriter(&syncBuffer{}), t.TempDir(), time.Now)
	if err == nil || errors.Is(err, context.Canceled) {
		t.Errorf("tailFile should surface the watch error, got %v", err)
	}
}
