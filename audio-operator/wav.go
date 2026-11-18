package main

// A WAV tap needs no encoder: it writes the header from wavHeader and
// then the output of pw-record --raw, which is already the PCM of a WAV
// data chunk.
//
// The RIFF size and the data size are both 0xFFFFFFFF, because a live
// tap has no length and a header already sent cannot be rewritten.
// ffmpeg's WAV muxer writes the same -1 placeholders when its output is
// a pipe (libavformat/wavenc.c and riffenc.c), so players already
// accept them.

import "encoding/binary"

// wavSampleBits matches sampleBytes, because every tap is s16le.
const wavSampleBits = 16

// wavFormatPCM is the WAVE_FORMAT_PCM tag of integer PCM.
const wavFormatPCM = 1

// wavHeaderBytes is 12 bytes of RIFF header, 24 of fmt chunk, and 8 of
// data chunk header.
const wavHeaderBytes = 44

const wavUnknownSize = 0xFFFFFFFF

// wavHeader writes the fmt chunk in its 16-byte PCM form, with no
// extension, for every channel count.
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
