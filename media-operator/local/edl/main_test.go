package main

// The tests build an album folder under t.TempDir(), with the tags each case
// names, and check the block the tool prints for it.

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/media-operator/edl"
)

func mustSucceed(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("wanted no error, got %v", err)
	}
}

func mustMatch[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
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

// writeTrack writes one file with an ID3v2.3 tag: the ten-byte header, the
// frames, and a few 0xFF bytes where the audio would start. The tag is the
// whole of what the tool reads, so the fixture needs no audio.
func writeTrack(t *testing.T, path string, frames ...id3Frame) string {
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

// blockJSON is the JSON the tool prints for one album folder.
func blockJSON(t *testing.T, files []string) string {
	t.Helper()
	encoded, err := json.Marshal(albumBlock(files))
	mustSucceed(t, err)
	return string(encoded)
}

// The block carries the album's words from the first tagged file, and omits a
// field no file states. The type and the hint are always there, because the
// block is meant to be pasted into a Play item and the hint is what makes the
// pod expand the folder.
func TestTheBlockFields(t *testing.T) {
	cases := []struct {
		name   string
		frames []id3Frame
		want   string
	}{
		{
			name: "every field",
			frames: []id3Frame{
				textFrame("TPE1", "Aesop Rock"),
				textFrame("TALB", "None Shall Pass"),
				textFrame("TYER", "2007"),
			},
			want: `{"type":"music","hint":"album","artist":"Aesop Rock","album":"None Shall Pass","year":2007}`,
		},
		{
			name:   "no year",
			frames: []id3Frame{textFrame("TPE1", "Aesop Rock"), textFrame("TALB", "None Shall Pass")},
			want:   `{"type":"music","hint":"album","artist":"Aesop Rock","album":"None Shall Pass"}`,
		},
		{
			name:   "no tags at all",
			frames: []id3Frame{textFrame("TCON", "Rock")},
			want:   `{"type":"music","hint":"album"}`,
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTrack(t, filepath.Join(dir, "01.mp3"), each.frames...)
			files, err := edl.AlbumFiles(dir)
			mustSucceed(t, err)
			mustMatch(t, blockJSON(t, files), each.want)
		})
	}
}
