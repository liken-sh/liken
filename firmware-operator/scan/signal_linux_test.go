package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// kubeletVolume writes a downward API volume the way the kubelet does:
// each version in its own directory, a rename that points the symbolic
// link ..data at it, and the file itself a symbolic link through ..data.
type kubeletVolume struct {
	dir      string
	versions int
}

func newKubeletVolume(t *testing.T, content string) *kubeletVolume {
	t.Helper()
	v := &kubeletVolume{dir: t.TempDir()}
	v.write(t, content)
	if err := os.Symlink(filepath.Join("..data", annotationsFile), filepath.Join(v.dir, annotationsFile)); err != nil {
		t.Fatalf("linking %s to ..data: %v", annotationsFile, err)
	}
	return v
}

func (v *kubeletVolume) write(t *testing.T, content string) {
	t.Helper()
	v.versions++
	version := filepath.Join(v.dir, "..version-"+string(rune('0'+v.versions)))
	writeFile(t, filepath.Join(version, annotationsFile), []byte(content))
	staged := filepath.Join(v.dir, "..data_tmp")
	if err := os.Symlink(filepath.Base(version), staged); err != nil {
		t.Fatalf("staging the new ..data link: %v", err)
	}
	if err := os.Rename(staged, filepath.Join(v.dir, "..data")); err != nil {
		t.Fatalf("replacing ..data: %v", err)
	}
}

// The kubelet replaces ..data with a rename inside the watched directory.
func TestTheWatchReportsTheKubeletsSwap(t *testing.T) {
	v := newKubeletVolume(t, "firmware.liken.sh/machine=\"node-2\"")
	w, err := inotifyWatch(v.dir)
	if err != nil {
		t.Fatalf("watching the volume directory: %v", err)
	}
	defer w.close()

	v.write(t, "firmware.liken.sh/machine=\"node-2\"\nfirmware.liken.sh/write=\"go\"")

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	select {
	case <-w.changes:
	case <-ctx.Done():
		t.Fatal("the watch reported no change after the kubelet swapped ..data")
	}
	if signalled, err := readSignal(v.dir); !signalled || err != nil {
		t.Errorf("readSignal = %v, %v, want the signal through ..data", signalled, err)
	}
}

func TestAClosedWatchEnds(t *testing.T) {
	w, err := inotifyWatch(t.TempDir())
	if err != nil {
		t.Fatalf("watching a directory: %v", err)
	}

	if err := w.close(); err != nil {
		t.Fatalf("closing the watch: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	select {
	case _, open := <-w.changes:
		if open {
			t.Error("the watch reported a change after its close, want its end")
		}
	case <-ctx.Done():
		t.Fatal("the closed watch did not end before the timeout")
	}
}

func TestAWatchNeedsTheDirectory(t *testing.T) {
	if _, err := inotifyWatch(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("inotifyWatch of a missing directory returned no error, want one")
	}
}
