// Package edltest writes media files with known ID3 tags, for the tests
// of the packages that read tags.
package edltest

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"
)

// Frame is one frame of an ID3v2.3 tag: its four-character ID and the
// bytes after the frame header.
type Frame struct {
	Name    string
	Payload []byte
}

// TextFrame returns a text frame with ISO-8859-1 encoding.
func TextFrame(name, value string) Frame {
	return Frame{Name: name, Payload: append([]byte{0}, value...)}
}

// WriteTrack writes an ID3v2.3 tag with the frames to path, followed by
// a few 0xFF bytes in place of audio, and returns path.
func WriteTrack(t *testing.T, path string, frames ...Frame) string {
	t.Helper()
	var body bytes.Buffer
	for _, frame := range frames {
		body.WriteString(frame.Name)
		if err := binary.Write(&body, binary.BigEndian, uint32(len(frame.Payload))); err != nil {
			t.Fatalf("writing the size of frame %s: %v", frame.Name, err)
		}
		body.Write([]byte{0, 0})
		body.Write(frame.Payload)
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

	if err := os.WriteFile(path, file.Bytes(), 0o644); err != nil {
		t.Fatalf("writing the track %s: %v", path, err)
	}
	return path
}
