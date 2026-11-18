package main

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/media-operator/edl"
	"github.com/liken-sh/liken/media-operator/edl/edltest"
)

func mustSucceed(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("the step returned an error, want none: %v", err)
	}
}

func mustMatch[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("the value is %+v, want %+v", got, want)
	}
}

func blockJSON(t *testing.T, files []string) string {
	t.Helper()
	encoded, err := json.Marshal(albumBlock(files))
	mustSucceed(t, err)
	return string(encoded)
}

// A field that no file's tags set is left out of the block.
func TestTheBlockFields(t *testing.T) {
	cases := []struct {
		name   string
		frames []edltest.Frame
		want   string
	}{
		{
			name: "every field",
			frames: []edltest.Frame{
				edltest.TextFrame("TPE1", "Aesop Rock"),
				edltest.TextFrame("TALB", "None Shall Pass"),
				edltest.TextFrame("TYER", "2007"),
			},
			want: `{"type":"music","hint":"album","artist":"Aesop Rock","album":"None Shall Pass","year":2007}`,
		},
		{
			name:   "no year",
			frames: []edltest.Frame{edltest.TextFrame("TPE1", "Aesop Rock"), edltest.TextFrame("TALB", "None Shall Pass")},
			want:   `{"type":"music","hint":"album","artist":"Aesop Rock","album":"None Shall Pass"}`,
		},
		{
			name:   "no tags at all",
			frames: []edltest.Frame{edltest.TextFrame("TCON", "Rock")},
			want:   `{"type":"music","hint":"album"}`,
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			dir := t.TempDir()
			edltest.WriteTrack(t, filepath.Join(dir, "01.mp3"), each.frames...)
			files, err := edl.AlbumFiles(dir)
			mustSucceed(t, err)
			mustMatch(t, blockJSON(t, files), each.want)
		})
	}
}
