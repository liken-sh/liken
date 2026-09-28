// Package edltest builds ID3 fixtures for tests. Three packages read
// tags out of a file, and each needs a file with known tags to read, so
// the builder lives once here and each of them imports it.
package edltest

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"
)

// Frame is one frame of the fixture tag: the four-character name and the
// bytes that follow the frame header.
type Frame struct {
	Name    string
	Payload []byte
}

// TextFrame builds one ISO-8859-1 text frame, the encoding byte and the text.
func TextFrame(name, value string) Frame {
	return Frame{Name: name, Payload: append([]byte{0}, value...)}
}

// WriteTrack writes one media file with an ID3v2.3 tag: the ten-byte header,
// the frames, and a few 0xFF bytes where the audio would start. The tag is the
// whole of what a reader takes, so the fixture needs no audio.
func WriteTrack(t *testing.T, path string, frames ...Frame) string {
	t.Helper()
	var body bytes.Buffer
	for _, frame := range frames {
		body.WriteString(frame.Name)
		if err := binary.Write(&body, binary.BigEndian, uint32(len(frame.Payload))); err != nil {
			t.Fatalf("wanted no error, got %v", err)
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
		t.Fatalf("wanted no error, got %v", err)
	}
	return path
}
