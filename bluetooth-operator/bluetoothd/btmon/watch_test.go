package main

// These tests run the inotify watch against a real directory, outside a
// synctest bubble, because the watch reads a kernel file descriptor.
// Each wait has a bound, so a broken watch fails in seconds.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// watchTimeout bounds each wait for the kernel to report an event.
const watchTimeout = 5 * time.Second

// openTestWatch opens a watch on a new directory, and closes it when
// the test ends.
func openTestWatch(t *testing.T) (*watch, string) {
	t.Helper()
	settings := t.TempDir()
	w, err := openWatch(settings)
	if err != nil {
		t.Fatalf("openWatch: %v", err)
	}
	t.Cleanup(w.close)
	return w, settings
}

// expectChange fails the test unless the watch reports a change within
// the bound.
func expectChange(t *testing.T, w *watch) {
	t.Helper()
	select {
	case <-w.changes:
	case err := <-w.failed:
		t.Fatalf("the watch failed: %v", err)
	case <-time.After(watchTimeout):
		t.Fatalf("no change within %s", watchTimeout)
	}
}

// Each way that the btmon file can change reports a change: bondfetch
// writes it in place, the operator renames a new copy over it, and a
// person can remove it.
func TestTheWatchReportsEachChangeToTheFile(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, settings string)
	}{
		{name: "a write", change: func(t *testing.T, settings string) {
			if err := os.WriteFile(filepath.Join(settings, "btmon"), []byte("true\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a rename", change: func(t *testing.T, settings string) {
			if err := os.WriteFile(filepath.Join(settings, ".btmon.new"), []byte("true\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(settings, ".btmon.new"), filepath.Join(settings, "btmon")); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, settings := openTestWatch(t)

			c.change(t, settings)

			expectChange(t, w)
		})
	}
}

func TestTheWatchReportsARemovedFile(t *testing.T) {
	settings := t.TempDir()
	if err := os.WriteFile(filepath.Join(settings, "btmon"), []byte("true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := openWatch(settings)
	if err != nil {
		t.Fatalf("openWatch: %v", err)
	}
	t.Cleanup(w.close)

	if err := os.Remove(filepath.Join(settings, "btmon")); err != nil {
		t.Fatal(err)
	}

	expectChange(t, w)
}

// A directory that goes away ends the watch with an error, because the
// watch can report nothing more about it.
func TestTheWatchFailsWhenTheDirectoryGoesAway(t *testing.T) {
	w, settings := openTestWatch(t)

	if err := os.Remove(settings); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-w.failed:
		if err == nil {
			t.Error("the watch failed with a nil error")
		}
	case <-time.After(watchTimeout):
		t.Fatalf("the watch did not fail within %s", watchTimeout)
	}
}

// A directory that does not exist cannot be watched, and the program
// ends before it reads anything.
func TestAWatchOnAMissingDirectoryFails(t *testing.T) {
	if _, err := openWatch(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("openWatch reported success for a missing directory")
	}
}
