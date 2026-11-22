package main

// imagesubtitles.go is the probe's measurement of the image subtitle
// streams of one file, PGS and VobSub. After the header read, a file with
// an image stream gets a read of one window of its subtitle packets, and
// sometimes a second. The probe decodes the packets of each image stream,
// counts the cues, and records the stream's numbers in its ledger record,
// so no reader after it opens the file for them. Plan 97 has the design and
// what a read costs.

import (
	"context"
	"math"
	"strconv"
)

// The codec names that ffprobe gives the two image subtitle formats.
const (
	codecPGS    = "hdmv_pgs_subtitle"
	codecVobSub = "dvd_subtitle"
)

// The probe stops ffprobe when every image stream has this many lower cues.
// On the 24 tracks that plan 97 sampled, the first 20 lower cues took 33 to
// 249 seconds of the title, so this ends most reads before the window does.
const enoughLowerCues = 20

// Whether a stream is a subtitle stream of pictures, which the probe
// measures.
func (s probedStream) isImageSubtitle() bool {
	return s.Kind == fileTypeSubtitle && (s.Codec == codecPGS || s.Codec == codecVobSub)
}

// One image stream as the probe reads it.
type imageStream struct {
	decoder interface {
		decode(packet []byte) []imageBitmap
		finish() []imageBitmap
	}
	cues imageCues
}

// measureImageSubtitles reads the windows of one file and records the
// numbers of each image stream in the record. The first window starts at a
// quarter of the duration, after the opening titles. A stream with fewer
// than fewestLowerCues lower cues after it gets a second window at half the
// duration, and the cues of both windows count. A file with no duration has
// the one window at its start.
//
// The record is measured when every window was read, whether or not each
// stream gave enough cues, so the probe does not read a stream with too few
// cues again. A window read that fails leaves the record unmeasured, and the
// error goes to the caller.
func measureImageSubtitles(ctx context.Context, read subtitleWindowRead, absolute string, record *probedFile) error {
	streams := map[int]*imageStream{}
	for index, stream := range record.Streams {
		switch {
		case !stream.isImageSubtitle():
		case stream.Codec == codecPGS:
			streams[index] = &imageStream{decoder: &pgsDecoder{}}
		case stream.Height > 0:
			// A VobSub stream with no height has no size line, and its canvas
			// is unknown.
			streams[index] = &imageStream{decoder: &vobsubDecoder{canvasHeight: stream.Height}}
		}
	}
	if len(streams) > 0 {
		last, err := readImageWindow(ctx, read, absolute, record.Duration/4, streams, math.Inf(-1))
		if err != nil {
			return err
		}
		few := map[int]*imageStream{}
		for index, stream := range streams {
			if stream.cues.lower() < fewestLowerCues {
				few[index] = stream
			}
		}
		if len(few) > 0 && record.Duration > 0 {
			if _, err := readImageWindow(ctx, read, absolute, record.Duration/2, few, last); err != nil {
				return err
			}
		}
	}
	for index, stream := range streams {
		record.Streams[index].Image = stream.cues.measurement()
	}
	record.Measured = true
	return nil
}

// readImageWindow reads one window into the streams, and gives the time of
// the last packet it read. A packet at or before after was read in the
// window before, which a short file's windows can overlap, and does not
// count again. A packet whose bytes do not read is dropped. The read stops
// when every stream has enoughLowerCues lower cues, and the cue on screen
// when the window ends counts.
func readImageWindow(ctx context.Context, read subtitleWindowRead, absolute string, start float64,
	streams map[int]*imageStream, after float64) (float64, error) {
	last := after
	err := read(ctx, absolute, start, func(packet subtitlePacket) bool {
		stream := streams[packet.Stream]
		at, err := strconv.ParseFloat(packet.Time, 64)
		if stream == nil || err != nil || at <= after {
			return true
		}
		last = max(last, at)
		data, err := packetBytes(packet.Data)
		if err != nil {
			return true
		}
		for _, bitmap := range stream.decoder.decode(data) {
			stream.cues.add(bitmap)
		}
		for _, each := range streams {
			if each.cues.lower() < enoughLowerCues {
				return true
			}
		}
		return false
	})
	for _, stream := range streams {
		for _, bitmap := range stream.decoder.finish() {
			stream.cues.add(bitmap)
		}
	}
	return last, err
}
