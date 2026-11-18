package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testTimeout limits each wait, so a broken wait fails the test instead
// of hanging the test run.
const testTimeout = 10 * time.Second

// annotations writes the annotations file of a downward API volume into
// dir. content is in the kubelet's format (readSignal).
func annotations(t *testing.T, dir, content string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, annotationsFile), []byte(content))
}

func TestTheSignalIsTheWriteAnnotationWithGo(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"the signal", "firmware.liken.sh/machine=\"node-2\"\nfirmware.liken.sh/write=\"go\"\nfirmware.liken.sh/write-at=\"2026-10-22T12:00:00Z\"", true},
		{"no signal", "firmware.liken.sh/machine=\"node-2\"", false},
		{"another value", "firmware.liken.sh/write=\"wait\"", false},
		{"another key that starts the same", "firmware.liken.sh/write-at=\"go\"", false},
		{"a value that does not unquote", "firmware.liken.sh/write=go", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			annotations(t, dir, c.content)

			got, err := readSignal(dir)

			if err != nil || got != c.want {
				t.Errorf("readSignal = %v, %v, want %v", got, err, c.want)
			}
		})
	}
}

// A missing annotations file means no signal. Any other read error is
// returned.
func TestReadingTheSignal(t *testing.T) {
	missing, err := readSignal(t.TempDir())
	if missing || err != nil {
		t.Errorf("readSignal of an empty directory = %v, %v, want no signal", missing, err)
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, annotationsFile), 0o755); err != nil {
		t.Fatalf("creating a directory in place of the annotations file: %v", err)
	}
	if _, err := readSignal(dir); err == nil {
		t.Error("readSignal of a directory in the file's place returned no error")
	}
}

// fakeWatch is a directory watch that a test controls. opened runs when
// the wait opens the watch, and a send on changes is a watch event.
type fakeWatch struct {
	opened  func()
	changes chan struct{}
	closed  bool
}

func (f *fakeWatch) open(string) (watcher, error) {
	f.opened()
	return watcher{changes: f.changes, close: func() error { f.closed = true; return nil }}, nil
}

func await(t *testing.T, dir string, f *fakeWatch) <-chan error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- awaitSignal(ctx, dir, f.open) }()
	return done
}

// The signal is written while the watch opens, and no watch event
// follows. Only the read after the open can find it.
func TestASignalBeforeTheWatchOpensIsSeen(t *testing.T) {
	dir := t.TempDir()
	annotations(t, dir, "firmware.liken.sh/machine=\"node-2\"")
	f := &fakeWatch{changes: make(chan struct{}),
		opened: func() { annotations(t, dir, "firmware.liken.sh/write=\"go\"") }}

	if err := <-await(t, dir, f); err != nil {
		t.Errorf("awaitSignal = %v, want the signal", err)
	}
	if !f.closed {
		t.Error("the watch is still open")
	}
}

func TestAnEventAfterTheSignalEndsTheWait(t *testing.T) {
	dir := t.TempDir()
	annotations(t, dir, "firmware.liken.sh/machine=\"node-2\"")
	f := &fakeWatch{changes: make(chan struct{}), opened: func() {}}
	done := await(t, dir, f)

	f.changes <- struct{}{}
	annotations(t, dir, "firmware.liken.sh/write=\"go\"")
	// The read after the first event can already find the signal and
	// end the wait before the second event is sent. Both orders pass.
	var err error
	select {
	case f.changes <- struct{}{}:
		err = <-done
	case err = <-done:
	}

	if err != nil {
		t.Errorf("awaitSignal = %v, want the signal", err)
	}
}

func TestAWaitThatCannotWatchEnds(t *testing.T) {
	cases := []struct {
		name string
		open func(string) (watcher, error)
		want string
	}{
		{"a watch that does not open", func(string) (watcher, error) { return watcher{}, errors.New("too many open files") },
			"watching DIR: too many open files"},
		{"a watch that fails", func(string) (watcher, error) {
			changes := make(chan struct{})
			close(changes)
			return watcher{changes: changes, close: func() error { return nil }}, nil
		}, "the watch of DIR ended before the signal arrived"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			annotations(t, dir, "firmware.liken.sh/machine=\"node-2\"")

			err := awaitSignal(context.Background(), dir, c.open)

			if want := strings.ReplaceAll(c.want, "DIR", dir); err == nil || err.Error() != want {
				t.Errorf("awaitSignal = %v, want %s", err, want)
			}
		})
	}
}

func TestAStoppedWaitEnds(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fakeWatch{changes: make(chan struct{}), opened: func() {}}

	if err := awaitSignal(ctx, dir, f.open); !errors.Is(err, context.Canceled) {
		t.Errorf("awaitSignal = %v, want the context's end", err)
	}
}
