package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTheCheckComparesTheDropInWithTheSocket(t *testing.T) {
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		written time.Time
		want    bool
	}{
		{"a drop-in the declare container wrote before PipeWire started", start.Add(-2 * time.Second), false},
		{"a drop-in the operator wrote after PipeWire started", start.Add(time.Minute), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			dropIn, socket := filepath.Join(dir, dropInName), filepath.Join(dir, "pipewire-0")
			for path, at := range map[string]time.Time{dropIn: c.written, socket: start} {
				if err := os.WriteFile(path, nil, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, at, at); err != nil {
					t.Fatal(err)
				}
			}
			stale, err := declarationNewer(dropIn, socket)
			if err != nil {
				t.Fatal(err)
			}
			if stale != c.want {
				t.Errorf("stale = %v, want %v", stale, c.want)
			}
		})
	}
}

// A file that is not there is an error, which the probe passes: a
// restart repairs neither a missing drop-in nor a missing socket.
func TestTheCheckCannotCompareWhatIsNotThere(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "present")
	if err := os.WriteFile(present, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing")
	for _, paths := range [][2]string{{missing, present}, {present, missing}} {
		if _, err := declarationNewer(paths[0], paths[1]); err == nil {
			t.Errorf("compared %v", paths)
		}
	}
}
