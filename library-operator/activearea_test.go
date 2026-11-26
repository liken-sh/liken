package main

// activearea_test.go proves how the probe measures the active area of a
// video stream: what one sample of cropdetect output gives, how the samples
// make one area, where the samples are, and what a failed sample does. The
// recorded output in testdata/cropdetect is what ffmpeg 8.0.1 printed for
// the arguments that activeAreaArguments gives.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// Every test of the package that probes a file reads its samples through
// a fake, so no test runs ffmpeg. A test that does not measure an area
// answers each sample with a black frame.
func init() {
	readActiveAreaSample = func(context.Context, string, int, float64) ([]byte, error) {
		return cropdetectFixture(nil, "black-frame.txt"), nil
	}
}

// cropdetectFixture reads one recorded output of a sample. A nil test is
// the package's own default fake, which has no test to fail.
func cropdetectFixture(t *testing.T, name string) []byte {
	data, err := os.ReadFile(filepath.Join("testdata", "cropdetect", name))
	if err != nil {
		if t == nil {
			panic(err)
		}
		t.Fatal(err)
	}
	return data
}

func TestASampleIsTheUnionOfTheFramesThatFoundPicture(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		want    pictureBox
		found   bool
	}{
		{name: "a frame with encoded bars", fixture: "scope-bars.txt",
			want: pictureBox{left: 0, top: 131, right: 1919, bottom: 947}, found: true},
		{name: "two frames of a dark scene", fixture: "dark-scene.txt",
			want: pictureBox{left: 13, top: 132, right: 1215, bottom: 854}, found: true},
		{name: "a black frame", fixture: "black-frame.txt"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, found := sampleBox(cropdetectFixture(t, test.fixture))
			if got != test.want || found != test.found {
				t.Errorf("sampleBox = %+v, %v, want %+v, %v", got, found, test.want, test.found)
			}
		})
	}
}

func TestASampleWithNoFrameFindsNoPicture(t *testing.T) {
	if _, found := sampleBox(nil); found {
		t.Error("an empty output found picture")
	}
}

// The rows of plan 102's samples of one episode, and of the films it
// names, as boxes the full width of a 1920-column frame.
func rows(pairs ...[2]int) []pictureBox {
	boxes := make([]pictureBox, len(pairs))
	for i, pair := range pairs {
		boxes[i] = pictureBox{left: 0, top: pair[0], right: 1919, bottom: pair[1]}
	}
	return boxes
}

func TestTheAreaIsTheUnionOfSamplesThatAgreeOnEachEdge(t *testing.T) {
	cases := []struct {
		name  string
		boxes []pictureBox
		want  *probedActiveArea
	}{
		{
			name: "an episode with encoded bars and two dark scenes",
			boxes: rows([2]int{131, 947}, [2]int{131, 947}, [2]int{148, 947}, [2]int{131, 947},
				[2]int{131, 947}, [2]int{131, 734}, [2]int{131, 947}, [2]int{131, 881}),
			want: &probedActiveArea{Left: 0, Top: 131, Width: 1920, Height: 817},
		},
		{
			name:  "edges a pixel apart",
			boxes: rows([2]int{131, 947}, [2]int{132, 946}, [2]int{140, 800}),
			want:  &probedActiveArea{Left: 0, Top: 131, Width: 1920, Height: 817},
		},
		{
			name: "a film with IMAX scenes",
			boxes: rows([2]int{241, 1079}, [2]int{137, 941}, [2]int{137, 941}, [2]int{0, 1079},
				[2]int{0, 1079}, [2]int{0, 1079}),
			want: &probedActiveArea{Left: 0, Top: 0, Width: 1920, Height: 1080},
		},
		{
			name: "a lower edge that one sample alone found",
			boxes: rows([2]int{29, 1046}, [2]int{30, 1046}, [2]int{22, 1057}, [2]int{22, 1046},
				[2]int{37, 1046}),
		},
		{name: "one sample", boxes: rows([2]int{131, 947})},
		{name: "no sample that found picture"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := agreedArea(test.boxes); !reflect.DeepEqual(got, test.want) {
				t.Errorf("agreedArea = %+v, want %+v", got, test.want)
			}
		})
	}
}

// The samples a fake answered, and the output it gave for each.
type fakeSamples struct {
	outputs [][]byte
	asked   []float64
	streams []int
	err     error
}

func (f *fakeSamples) read(_ context.Context, _ string, stream int, at float64) ([]byte, error) {
	f.asked = append(f.asked, at)
	f.streams = append(f.streams, stream)
	if f.err != nil {
		return nil, f.err
	}
	return f.outputs[(len(f.asked)-1)%len(f.outputs)], nil
}

// A film of 9000 seconds with a cover, one video stream, and one audio
// stream, in ffprobe's order.
func filmWithACover() probedFile {
	return probedFile{Duration: 9000, Streams: []probedStream{
		{Kind: fileTypeImage, Codec: "mjpeg"},
		{Kind: fileTypeVideo, Codec: "hevc", Width: 1920, Height: 1080},
		{Kind: fileTypeAudio, Codec: "eac3"},
	}}
}

// The samples are at 1/9 to 8/9 of the duration, of the video stream alone
// and not of the cover, by the stream's index in the container.
func TestTheProbeSamplesEachVideoStreamAcrossItsDuration(t *testing.T) {
	samples := &fakeSamples{outputs: [][]byte{cropdetectFixture(t, "scope-bars.txt")}}
	record := filmWithACover()

	if err := measureActiveAreas(t.Context(), samples.read, "film.mkv", &record); err != nil {
		t.Fatal(err)
	}

	if want := []float64{1000, 2000, 3000, 4000, 5000, 6000, 7000, 8000}; !slices.Equal(samples.asked, want) {
		t.Errorf("samples at %v, want %v", samples.asked, want)
	}
	if slices.ContainsFunc(samples.streams, func(stream int) bool { return stream != 1 }) {
		t.Errorf("samples of streams %v, want stream 1 alone", samples.streams)
	}
	want := &probedActiveArea{Left: 0, Top: 131, Width: 1920, Height: 817}
	if got := record.Streams[1].ActiveArea; !reflect.DeepEqual(got, want) {
		t.Errorf("area = %+v, want %+v", got, want)
	}
	if record.Streams[0].ActiveArea != nil || record.ActiveAreaMeasurement != activeAreaMeasurement {
		t.Errorf("record = %+v, want an area on the video stream alone and version %d", record, activeAreaMeasurement)
	}
}

// Black samples and a file with no duration give a measured record with no
// area, so the probe does not read the file again.
func TestAStreamWithNoAgreedAreaIsMeasuredWithoutOne(t *testing.T) {
	cases := []struct {
		name     string
		duration float64
		asked    int
	}{
		{name: "black samples", duration: 9000, asked: activeAreaSamples},
		{name: "no duration", duration: 0, asked: 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			samples := &fakeSamples{outputs: [][]byte{cropdetectFixture(t, "black-frame.txt")}}
			record := filmWithACover()
			record.Duration = test.duration

			if err := measureActiveAreas(t.Context(), samples.read, "film.mkv", &record); err != nil {
				t.Fatal(err)
			}

			if record.Streams[1].ActiveArea != nil || record.ActiveAreaMeasurement != activeAreaMeasurement ||
				len(samples.asked) != test.asked {
				t.Errorf("record = %+v after %d samples, want version %d, no area, and %d samples",
					record, len(samples.asked), activeAreaMeasurement, test.asked)
			}
		})
	}
}

func TestAFailedSampleLeavesTheRecordUnmeasured(t *testing.T) {
	failed := errors.New("ffmpeg: exit status 1: Invalid data found when processing input")
	samples := &fakeSamples{err: failed}
	record := filmWithACover()

	err := measureActiveAreas(t.Context(), samples.read, "film.mkv", &record)

	if !errors.Is(err, failed) || record.ActiveAreaMeasurement != 0 {
		t.Errorf("error = %v and version %d, want the sample's error and no version", err, record.ActiveAreaMeasurement)
	}
}

// The arguments are those of plan 102, with the stream by its index.
func TestASampleSeeksToAKeyframeAndDecodesOneFrameOnOneThread(t *testing.T) {
	got := activeAreaArguments("/media/film.mkv", 1, 1234.5)
	want := []string{"-nostdin", "-v", "error", "-threads", "1", "-filter_threads", "1",
		"-noaccurate_seek", "-ss", "1234.500", "-i", "/media/film.mkv", "-map", "0:1", "-frames:v", "1",
		"-vf", "cropdetect=limit=0.094:skip=0,metadata=mode=print:file=-", "-f", "null", "-"}
	if !slices.Equal(got, want) {
		t.Errorf("arguments = %q, want %q", got, want)
	}
}
