package main

// The RIFF WAVE header a tap sends ahead of the raw samples pw-record
// produces.
//
// The Go code writes it rather than an encoder because the samples
// pw-record prints with --raw are already the PCM the data chunk
// carries, so the whole of WAV is these 44 bytes.
//
// The two sizes are 0xFFFFFFFF. A live tap has no length, and a pipe
// has no seek, so neither size can be filled in later. ffmpeg's WAV
// muxer writes the same -1 placeholders and never returns to fix them
// on a pipe (libavformat/wavenc.c and riffenc.c), so every player
// that reads an ffmpeg-piped WAV reads this one.

import "encoding/binary"

// wavSampleBits is the sample width. Every tap is s16le, and 16 bits
// is the width every player reads.
const wavSampleBits = 16

// wavFormatPCM is the one format tag in the WAVE_FORMAT_ list that
// names plain integer PCM.
const wavFormatPCM = 1

// wavHeaderBytes is the header's own length: the RIFF chunk's twelve
// bytes, the fmt chunk's twenty-four, and the data chunk's eight.
const wavHeaderBytes = 44

// wavUnknownSize is what the two size fields carry. It is the
// streaming convention players accept, not a length.
const wavUnknownSize = 0xFFFFFFFF

// wavHeader builds the header for one endpoint's rate and channel
// count. The fmt chunk's own size is the sixteen bytes of the plain
// PCM shape, with no extension.
func wavHeader(rate, channels int) []byte {
	header := make([]byte, wavHeaderBytes)
	blockAlign := channels * sampleBytes

	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], wavUnknownSize)
	copy(header[8:12], "WAVE")

	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], wavFormatPCM)
	binary.LittleEndian.PutUint16(header[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(header[24:28], uint32(rate))
	binary.LittleEndian.PutUint32(header[28:32], uint32(rate*blockAlign))
	binary.LittleEndian.PutUint16(header[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(header[34:36], wavSampleBits)

	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], wavUnknownSize)
	return header
}
