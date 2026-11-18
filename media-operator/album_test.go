package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/media-operator/edl"
	"github.com/liken-sh/liken/media-operator/edl/edltest"
)

// useAlbumDir points albumTimelineDir at path for the length of one test.
func useAlbumDir(t *testing.T, path string) {
	t.Helper()
	was := albumTimelineDir
	t.Cleanup(func() { albumTimelineDir = was })
	albumTimelineDir = path
}

// albumFixture writes an album directory with two tracks, one with a
// title tag and one without, and a cover image that is not a track.
func albumFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	edltest.WriteTrack(t, filepath.Join(dir, "02 - Masterswarm.mp3"))
	edltest.WriteTrack(t, filepath.Join(dir, "01 - first.mp3"), edltest.TextFrame("TIT2", "Oh No"))
	mustSucceed(t, os.WriteFile(filepath.Join(dir, "cover.jpg"), []byte("not a track"), 0o644))
	return dir
}

// A timeline has one segment per track in file name order, and each
// segment's title becomes an mpv chapter. An untagged track gets a title
// from its file name, without the leading track number.
func TestExpandItemsWritesATimelineForAnAlbum(t *testing.T) {
	album := albumFixture(t)
	written := t.TempDir()
	useAlbumDir(t, written)

	entries, err := expandItems(
		[]string{album},
		[]json.RawMessage{json.RawMessage(`{"type":"music","hint":"album"}`)})
	mustSucceed(t, err)

	timeline := filepath.Join(written, "album-1.edl")
	mustMatchAll(t, entries, []string{timeline})

	text, err := os.ReadFile(timeline)
	mustSucceed(t, err)
	lines := strings.Split(strings.TrimSuffix(string(text), "\n"), "\n")
	mustMatch(t, len(lines), 3)
	mustMatch(t, lines[0], edl.Header)
	mustMatch(t, lines[1], edl.Quote(filepath.Join(album, "01 - first.mp3"))+",title=%5%Oh No")
	mustMatch(t, lines[2], edl.Quote(filepath.Join(album, "02 - Masterswarm.mp3"))+",title=%11%Masterswarm")
}

func TestExpandItemsLeavesEveryOtherItemAlone(t *testing.T) {
	album := albumFixture(t)
	written := t.TempDir()
	useAlbumDir(t, written)

	entries, err := expandItems(
		[]string{"/media/1/film.mkv", album, "/media/1/track.flac"},
		[]json.RawMessage{
			json.RawMessage(`{"type":"video","hint":"movie"}`),
			json.RawMessage(`{"type":"music","hint":"album"}`),
			json.RawMessage(`{"type":"music"}`),
		})
	mustSucceed(t, err)

	mustMatchAll(t, entries, []string{
		"/media/1/film.mkv",
		filepath.Join(written, "album-2.edl"),
		"/media/1/track.flac",
	})
}

func TestExpandItemsFailsForAnEmptyOrMissingAlbum(t *testing.T) {
	useAlbumDir(t, t.TempDir())

	cases := []struct {
		name  string
		album func(t *testing.T) string
	}{
		{name: "a folder with no tracks", album: func(t *testing.T) string { return t.TempDir() }},
		{
			name: "a folder that is not there",
			album: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "no-such-album")
			},
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			_, err := expandItems(
				[]string{each.album(t)},
				[]json.RawMessage{json.RawMessage(`{"type":"music","hint":"album"}`)})
			mustFail(t, err)
		})
	}
}

func TestExpandItemsPassesAStandaloneTrackThrough(t *testing.T) {
	useAlbumDir(t, t.TempDir())

	entries, err := expandItems(
		[]string{"/media/1/track.flac"},
		[]json.RawMessage{json.RawMessage(`{"type":"music"}`)})
	mustSucceed(t, err)
	mustMatchAll(t, entries, []string{"/media/1/track.flac"})
}

func TestAllMusic(t *testing.T) {
	cases := []struct {
		name   string
		items  int
		blocks []json.RawMessage
		want   bool
	}{
		{
			name:   "every item is music",
			items:  2,
			blocks: []json.RawMessage{json.RawMessage(`{"type":"music","hint":"album"}`), json.RawMessage(`{"type":"music"}`)},
			want:   true,
		},
		{
			name:   "one film in the list",
			items:  2,
			blocks: []json.RawMessage{json.RawMessage(`{"type":"music"}`), json.RawMessage(`{"type":"video"}`)},
			want:   false,
		},
		{
			name:   "an item with no block at all",
			items:  2,
			blocks: []json.RawMessage{json.RawMessage(`{"type":"music"}`)},
			want:   false,
		},
		{
			name:   "no blocks at all",
			items:  1,
			blocks: nil,
			want:   false,
		},
		{
			name:   "no items at all",
			items:  0,
			blocks: nil,
			want:   false,
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatch(t, allMusic(each.blocks, each.items), each.want)
		})
	}
}
