package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// kubeletVolume plays the kubelet's writer of a downward API volume. It
// writes each version of the files into a directory of its own, and
// points the symbolic link ..data at it with a rename, so a reader
// never sees half a file. The file in the volume is a symbolic link
// through ..data.
type kubeletVolume struct {
	dir      string
	versions int
}

func newKubeletVolume(t *testing.T, content string) *kubeletVolume {
	t.Helper()
	v := &kubeletVolume{dir: t.TempDir()}
	v.write(t, content)
	if err := os.Symlink(filepath.Join("..data", annotationsFile), filepath.Join(v.dir, annotationsFile)); err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}
	if err := os.Rename(staged, filepath.Join(v.dir, "..data")); err != nil {
		t.Fatal(err)
	}
}

// The kubelet's swap of ..data is a rename in the volume's directory,
// so the watch of the directory reports it.
func TestTheWatchReportsTheKubeletsSwap(t *testing.T) {
	v := newKubeletVolume(t, "firmware.liken.sh/machine=\"node-2\"")
	w, err := inotifyWatch(v.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.close()

	v.write(t, "firmware.liken.sh/machine=\"node-2\"\nfirmware.liken.sh/write=\"go\"")

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	select {
	case <-w.changes:
	case <-ctx.Done():
		t.Fatal("the watch reported no change")
	}
	if signalled, err := readSignal(v.dir); !signalled || err != nil {
		t.Errorf("readSignal = %v, %v, want the signal through ..data", signalled, err)
	}
}

func TestAClosedWatchEnds(t *testing.T) {
	w, err := inotifyWatch(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if err := w.close(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	select {
	case _, open := <-w.changes:
		if open {
			t.Error("the watch reported a change after its close, want its end")
		}
	case <-ctx.Done():
		t.Fatal("the closed watch did not end")
	}
}

func TestAWatchNeedsTheDirectory(t *testing.T) {
	if _, err := inotifyWatch(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("inotifyWatch of a missing directory answered no error")
	}
}
