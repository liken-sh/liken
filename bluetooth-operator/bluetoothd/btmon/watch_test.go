package main

// These tests run the inotify watch on a real directory outside a
// synctest bubble, because the watch reads a kernel file descriptor.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// watchTimeout bounds each wait for the kernel to report an event.
const watchTimeout = 5 * time.Second

// openTestWatch watches a new temporary directory and closes the watch
// when the test ends.
func openTestWatch(t *testing.T) (*watch, string) {
	t.Helper()
	settings := t.TempDir()
	w, err := openWatch(settings)
	if err != nil {
		t.Fatalf("opening the watch: %v", err)
	}
	t.Cleanup(w.close)
	return w, settings
}

// expectChange fails the test unless the watch signals a change within
// watchTimeout.
func expectChange(t *testing.T, w *watch) {
	t.Helper()
	select {
	case <-w.changes:
	case err := <-w.failed:
		t.Fatalf("the watch ended with %v, want a change", err)
	case <-time.After(watchTimeout):
		t.Fatalf("the watch reported no change within %s", watchTimeout)
	}
}

// bondfetch writes the btmon file in place, and the operator renames a
// new copy over it. Each signals a change.
func TestTheWatchReportsEachChangeToTheFile(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, settings string)
	}{
		{name: "a write", change: func(t *testing.T, settings string) {
			if err := os.WriteFile(filepath.Join(settings, "btmon"), []byte("true\n"), 0o644); err != nil {
				t.Fatalf("writing the btmon file: %v", err)
			}
		}},
		{name: "a rename", change: func(t *testing.T, settings string) {
			if err := os.WriteFile(filepath.Join(settings, ".btmon.new"), []byte("true\n"), 0o644); err != nil {
				t.Fatalf("writing the new copy of the btmon file: %v", err)
			}
			if err := os.Rename(filepath.Join(settings, ".btmon.new"), filepath.Join(settings, "btmon")); err != nil {
				t.Fatalf("renaming the new copy over the btmon file: %v", err)
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

// A person can remove the btmon file.
func TestTheWatchReportsARemovedFile(t *testing.T) {
	settings := t.TempDir()
	if err := os.WriteFile(filepath.Join(settings, "btmon"), []byte("true\n"), 0o644); err != nil {
		t.Fatalf("writing the btmon file: %v", err)
	}
	w, err := openWatch(settings)
	if err != nil {
		t.Fatalf("opening the watch: %v", err)
	}
	t.Cleanup(w.close)

	if err := os.Remove(filepath.Join(settings, "btmon")); err != nil {
		t.Fatalf("removing the btmon file: %v", err)
	}

	expectChange(t, w)
}

// A removed directory ends the watch with an error.
func TestTheWatchFailsWhenTheDirectoryGoesAway(t *testing.T) {
	w, settings := openTestWatch(t)

	if err := os.Remove(settings); err != nil {
		t.Fatalf("removing the watched directory: %v", err)
	}

	select {
	case err := <-w.failed:
		if err == nil {
			t.Error("the watch ended with a nil error, want an error")
		}
	case <-time.After(watchTimeout):
		t.Fatalf("the watch reported no end within %s", watchTimeout)
	}
}

// openWatch returns an error for a directory that does not exist, and
// main exits on that error.
func TestAWatchOnAMissingDirectoryFails(t *testing.T) {
	if _, err := openWatch(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("openWatch returned no error for a missing directory, want one")
	}
}
