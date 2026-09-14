package main

// These tests cover what the shim does before mpv sees an argument: an album
// directory becomes one written timeline, and a run of nothing but music draws
// no video.

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/media-operator/edl"
)

// useAlbumDir moves the directory the shim writes timelines into for the
// length of one test, the way useSocket moves the IPC socket.
func useAlbumDir(t *testing.T, path string) {
	t.Helper()
	was := albumTimelineDir
	t.Cleanup(func() { albumTimelineDir = was })
	albumTimelineDir = path
}

// id3Frame is one frame of the fixture tag: the four-character name and the
// bytes that follow the frame header.
type id3Frame struct {
	name    string
	payload []byte
}

// textFrame builds one ISO-8859-1 text frame, the encoding byte and the text.
func textFrame(name, value string) id3Frame {
	return id3Frame{name: name, payload: append([]byte{0}, value...)}
}

// writeMedia writes one media file with an ID3v2.3 tag: the ten-byte header,
// the frames, and a few 0xFF bytes where the audio would start. The tag is the
// whole of what a reader takes, so the fixture needs no audio.
func writeMedia(t *testing.T, path string, frames ...id3Frame) string {
	t.Helper()
	var body bytes.Buffer
	for _, frame := range frames {
		body.WriteString(frame.name)
		mustSucceed(t, binary.Write(&body, binary.BigEndian, uint32(len(frame.payload))))
		body.Write([]byte{0, 0})
		body.Write(frame.payload)
	}

	var file bytes.Buffer
	file.WriteString("ID3")
	file.Write([]byte{3, 0, 0})
	size := 10 + body.Len()
	file.Write([]byte{
		byte(size>>21) & 0x7f, byte(size>>14) & 0x7f,
		byte(size>>7) & 0x7f, byte(size) & 0x7f,
	})
	file.Write(body.Bytes())
	file.Write(bytes.Repeat([]byte{0xff}, 8))

	mustSucceed(t, os.WriteFile(path, file.Bytes(), 0o644))
	return path
}

// albumFixture writes one album folder: two tracks, one tagged and one not,
// and a cover file that is no track.
func albumFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeMedia(t, filepath.Join(dir, "02 - Masterswarm.mp3"))
	writeMedia(t, filepath.Join(dir, "01 - first.mp3"), textFrame("TIT2", "Oh No"))
	mustSucceed(t, os.WriteFile(filepath.Join(dir, "cover.jpg"), []byte("not a track"), 0o644))
	return dir
}

// An album item becomes one playlist entry: the timeline the shim wrote, with
// one segment per track in name order and the title mpv turns into a chapter.
func TestExpandItemsWritesTheAlbumsTimeline(t *testing.T) {
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

// A Play mixes albums and tracks freely: only the item its block marks as an
// album expands, and every other item is the path the resolver settled.
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

// An album with no audio files in it fails the run, the way any item the pod
// cannot play fails it.
func TestExpandItemsRefusesAnEmptyAlbum(t *testing.T) {
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

// The music item that is one file passes through, because a track is already
// one playlist entry.
func TestExpandItemsPassesAStandaloneTrackThrough(t *testing.T) {
	useAlbumDir(t, t.TempDir())

	entries, err := expandItems(
		[]string{"/media/1/track.flac"},
		[]json.RawMessage{json.RawMessage(`{"type":"music"}`)})
	mustSucceed(t, err)
	mustMatchAll(t, entries, []string{"/media/1/track.flac"})
}

// A run of nothing but music draws no video, so the display owns the frame.
// One item that is not music keeps video on for the whole run.
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
