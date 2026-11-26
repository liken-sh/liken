package main

// activearea.go is the probe's measurement of the active area of each video
// stream: the rectangle of the frame that contains picture. A film stored
// with its letterbox bars in the frame has black rows above and below its
// picture, and mpv draws those rows as picture. media-operator's display
// reads the area to put a subtitle cue of two or more lines in those black
// rows, as it puts one in a bar that mpv draws. Plan 102 has the design and
// the measurements that chose each number here.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// The version of the definitions in this file, which the probe writes in
// each record that it measures. A record of a lower version that holds a
// video stream opens the probe gap again, so a change to the definitions
// raises this number and every video is read once more.
const activeAreaMeasurement = 1

// The probe reads this many samples of each video stream, at 1/9 to 8/9 of
// the duration, so no sample is in the opening titles or the end credits.
const activeAreaSamples = 8

// Each edge of the union of the samples must be within activeAreaTolerance
// pixels of the same edge of at least activeAreaAgreement samples. One
// sample alone does not decide an edge, and dark samples that end the
// picture on different rows give no area.
const (
	activeAreaAgreement = 2
	activeAreaTolerance = 2
)

// The luma above which cropdetect counts a row or a column as picture, as a
// share of the pixel's range: 24/255, FFmpeg's default. A share is the same
// for 8-bit and 10-bit video. An absolute 24 reads the black of a 10-bit
// frame, 64 of 1023, as picture.
const activeAreaLimit = "0.094"

// The active area of one video stream, in pixels of its frame: its first
// column and row, and its size.
type probedActiveArea struct {
	Left   int `yaml:"left"`
	Top    int `yaml:"top"`
	Width  int `yaml:"width"`
	Height int `yaml:"height"`
}

// One run of ffmpeg over one sample of one video stream, which a test
// replaces with recorded output. It returns what the metadata filter
// printed.
type activeAreaSampleRead func(ctx context.Context, path string, stream int, at float64) ([]byte, error)

// The sample read the probe makes of every video stream. It is a variable
// so a test answers in ffmpeg's place.
var readActiveAreaSample activeAreaSampleRead = ffmpegActiveAreaSample

// measureActiveAreas records the active area of each video stream of the
// record. The samples of one file share the limit of the header read. A
// sample that fails leaves the record without the measurement's version,
// and the error goes to the caller, so the probe reads the file again after
// the error's period. A stream whose samples do not agree gets no area in a
// measured record, and the probe does not read it again.
func measureActiveAreas(ctx context.Context, read activeAreaSampleRead, absolute string, record *probedFile) error {
	timed, cancel := context.WithTimeout(ctx, ffprobeTimeout)
	defer cancel()
	for index, stream := range record.Streams {
		if stream.Kind != fileTypeVideo {
			continue
		}
		area, err := measureActiveArea(timed, read, absolute, index, record.Duration)
		if err != nil {
			return err
		}
		record.Streams[index].ActiveArea = area
	}
	record.ActiveAreaMeasurement = activeAreaMeasurement
	return nil
}

// measureActiveArea reads the samples of one stream and returns their
// agreed union, or nil. A file with no duration has no place for the
// samples, so it gets no area.
func measureActiveArea(ctx context.Context, read activeAreaSampleRead, absolute string, stream int,
	duration float64) (*probedActiveArea, error) {
	if duration <= 0 {
		return nil, nil
	}
	var boxes []pictureBox
	for sample := 1; sample <= activeAreaSamples; sample++ {
		at := duration * float64(sample) / float64(activeAreaSamples+1)
		output, err := read(ctx, absolute, stream, at)
		if err != nil {
			return nil, err
		}
		if box, found := sampleBox(output); found {
			boxes = append(boxes, box)
		}
	}
	return agreedArea(boxes), nil
}

// The rectangle of one frame that has picture, by its first and last
// column and row, as cropdetect's x1, x2, y1, and y2 give them.
type pictureBox struct {
	left, top, right, bottom int
}

// The smallest box that contains both boxes.
func (b pictureBox) union(other pictureBox) pictureBox {
	return pictureBox{
		left: min(b.left, other.left), top: min(b.top, other.top),
		right: max(b.right, other.right), bottom: max(b.bottom, other.bottom),
	}
}

// sampleBox reads the output of one sample: one block of keys for each
// frame that passed the filter, each block after a line that starts with
// frame:. ffmpeg can pass a second frame before -frames:v stops it, so the
// sample is the union of the frames that found picture. A black frame
// gives a first column after its last, or a first row after its last, and
// found is false when every frame was black or no frame passed.
func sampleBox(output []byte) (pictureBox, bool) {
	var sample pictureBox
	found := false
	keep := func(frame map[string]int) {
		box := pictureBox{left: frame["x1"], top: frame["y1"], right: frame["x2"], bottom: frame["y2"]}
		if len(frame) < 4 || box.left > box.right || box.top > box.bottom {
			return
		}
		if !found {
			sample, found = box, true
			return
		}
		sample = sample.union(box)
	}
	var frame map[string]int
	lines := bufio.NewScanner(bytes.NewReader(output))
	for lines.Scan() {
		line := strings.TrimSpace(lines.Text())
		if strings.HasPrefix(line, "frame:") {
			if frame != nil {
				keep(frame)
			}
			frame = map[string]int{}
			continue
		}
		key, value, held := strings.Cut(strings.TrimPrefix(line, "lavfi.cropdetect."), "=")
		if frame == nil || !held || !strings.HasPrefix(line, "lavfi.cropdetect.") {
			continue
		}
		switch key {
		case "x1", "x2", "y1", "y2":
			if number, err := strconv.Atoi(value); err == nil {
				frame[key] = number
			}
		}
	}
	if frame != nil {
		keep(frame)
	}
	return sample, found
}

// agreedArea is the union of the samples' boxes when each of its four edges
// has activeAreaAgreement samples within activeAreaTolerance pixels of it,
// and nil otherwise. The union keeps the picture of the brighter samples
// where a dark scene found less, and the largest shape of a film whose
// shape changes.
func agreedArea(boxes []pictureBox) *probedActiveArea {
	if len(boxes) == 0 {
		return nil
	}
	union := boxes[0]
	for _, box := range boxes[1:] {
		union = union.union(box)
	}
	edges := []func(pictureBox) int{
		func(b pictureBox) int { return b.left }, func(b pictureBox) int { return b.top },
		func(b pictureBox) int { return b.right }, func(b pictureBox) int { return b.bottom },
	}
	for _, edge := range edges {
		agreeing := 0
		for _, box := range boxes {
			if pixelsApart(edge(box), edge(union)) <= activeAreaTolerance {
				agreeing++
			}
		}
		if agreeing < activeAreaAgreement {
			return nil
		}
	}
	return &probedActiveArea{
		Left: union.left, Top: union.top,
		Width: union.right - union.left + 1, Height: union.bottom - union.top + 1,
	}
}

// How many pixels apart two edges are.
func pixelsApart(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

// ffmpegActiveAreaSample runs ffmpeg over one frame of one stream at one
// time. The context carries the limit that the samples of the file share.
func ffmpegActiveAreaSample(ctx context.Context, path string, stream int, at float64) ([]byte, error) {
	output, err := exec.CommandContext(ctx, "ffmpeg", activeAreaArguments(path, stream, at)...).Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: %w%s", err, commandStderr(err))
	}
	return output, nil
}

// The arguments of one sample. -ss before -i seeks through the container's
// index, and -noaccurate_seek starts at the keyframe at or before the time,
// so ffmpeg decodes no frame between the keyframe and the time. One decoder
// thread and one filter thread bound the memory, because FFmpeg's automatic
// threads take a frame buffer for each core of the node, and plan 102 has
// what a 3840x2160 frame took each way. skip=0 makes cropdetect measure the
// first frame, and the metadata filter prints its keys to standard output.
func activeAreaArguments(path string, stream int, at float64) []string {
	return []string{"-nostdin", "-v", "error", "-threads", "1", "-filter_threads", "1",
		"-noaccurate_seek", "-ss", strconv.FormatFloat(at, 'f', 3, 64), "-i", path,
		"-map", "0:" + strconv.Itoa(stream), "-frames:v", "1",
		"-vf", "cropdetect=limit=" + activeAreaLimit + ":skip=0,metadata=mode=print:file=-",
		"-f", "null", "-"}
}
