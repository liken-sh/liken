package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestTheWAVHeaderIsTheFortyFourBytesAPlayerReads(t *testing.T) {
	header := wavHeader(48000, 2)
	if len(header) != 44 {
		t.Fatalf("the header is %d bytes, want 44", len(header))
	}
	if got := string(header[0:4]); got != "RIFF" {
		t.Errorf("the first chunk is %q, want RIFF", got)
	}
	if got := string(header[8:12]); got != "WAVE" {
		t.Errorf("the form is %q, want WAVE", got)
	}
	if got := string(header[12:16]); got != "fmt " {
		t.Errorf("the second chunk is %q, want \"fmt \"", got)
	}
	if got := string(header[36:40]); got != "data" {
		t.Errorf("the third chunk is %q, want data", got)
	}

	// Both sizes are the streaming placeholder. ffmpeg's WAV muxer
	// writes the same -1 and never returns to fix it on a pipe, so
	// every player that reads an ffmpeg-piped WAV reads this one.
	if got := binary.LittleEndian.Uint32(header[4:8]); got != 0xFFFFFFFF {
		t.Errorf("the RIFF size is %#x, want 0xFFFFFFFF", got)
	}
	if got := binary.LittleEndian.Uint32(header[40:44]); got != 0xFFFFFFFF {
		t.Errorf("the data size is %#x, want 0xFFFFFFFF", got)
	}

	if got := binary.LittleEndian.Uint32(header[16:20]); got != 16 {
		t.Errorf("the fmt chunk is %d bytes, want 16", got)
	}
	if got := binary.LittleEndian.Uint16(header[20:22]); got != 1 {
		t.Errorf("the format tag is %d, want 1 for PCM", got)
	}
	if got := binary.LittleEndian.Uint16(header[22:24]); got != 2 {
		t.Errorf("the channel count is %d, want 2", got)
	}
	if got := binary.LittleEndian.Uint32(header[24:28]); got != 48000 {
		t.Errorf("the sample rate is %d, want 48000", got)
	}
	// 48000 frames a second, two channels, two bytes a sample.
	if got := binary.LittleEndian.Uint32(header[28:32]); got != 192000 {
		t.Errorf("the byte rate is %d, want 192000", got)
	}
	if got := binary.LittleEndian.Uint16(header[32:34]); got != 4 {
		t.Errorf("the block align is %d, want 4", got)
	}
	if got := binary.LittleEndian.Uint16(header[34:36]); got != 16 {
		t.Errorf("the sample width is %d bits, want 16", got)
	}
}

func TestTheWAVHeaderFollowsTheEndpointsOwnFormat(t *testing.T) {
	cases := []struct {
		rate      int
		channels  int
		byteRate  uint32
		blockSize uint16
	}{
		{44100, 2, 176400, 4},
		{48000, 6, 576000, 12},
		{96000, 1, 192000, 2},
	}
	for _, row := range cases {
		header := wavHeader(row.rate, row.channels)
		if got := binary.LittleEndian.Uint32(header[24:28]); got != uint32(row.rate) {
			t.Errorf("%d Hz: the header says %d", row.rate, got)
		}
		if got := binary.LittleEndian.Uint16(header[22:24]); got != uint16(row.channels) {
			t.Errorf("%d channels: the header says %d", row.channels, got)
		}
		if got := binary.LittleEndian.Uint32(header[28:32]); got != row.byteRate {
			t.Errorf("%d Hz %d channels: the byte rate is %d, want %d",
				row.rate, row.channels, got, row.byteRate)
		}
		if got := binary.LittleEndian.Uint16(header[32:34]); got != row.blockSize {
			t.Errorf("%d channels: the block align is %d, want %d",
				row.channels, got, row.blockSize)
		}
	}
}

func TestTheWAVHeaderIsTheSameBytesEveryTime(t *testing.T) {
	if !bytes.Equal(wavHeader(48000, 2), wavHeader(48000, 2)) {
		t.Error("two headers for one format differ")
	}
}
