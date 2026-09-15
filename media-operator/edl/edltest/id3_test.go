package edltest

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteTrackWritesAnID3Tag(t *testing.T) {
	path := WriteTrack(t, filepath.Join(t.TempDir(), "track.mp3"),
		TextFrame("TIT2", "Song"), TextFrame("TPE1", "Band"))

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("wanted no error, got %v", err)
	}
	if !bytes.HasPrefix(written, []byte("ID3")) {
		t.Errorf("the file starts with %q, want ID3", written[:3])
	}
	for _, want := range []string{"TIT2", "Song", "TPE1", "Band"} {
		if !bytes.Contains(written, []byte(want)) {
			t.Errorf("the tag carries no %q", want)
		}
	}
}
