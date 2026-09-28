package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

// One capture, read the way the container reads it.
func ffprobeAnswerOf(t *testing.T, capture string) ffprobeAnswer {
	t.Helper()
	var answer ffprobeAnswer
	if err := json.Unmarshal([]byte(capture), &answer); err != nil {
		t.Fatal(err)
	}
	return answer
}

func TestTheRecordHoldsEveryStreamOfAFileFFprobeOpened(t *testing.T) {
	cases := []struct {
		name    string
		capture string
		want    probedFile
	}{
		{
			name:    "a 1080p H.264 film with one audio track and one subtitle",
			capture: ffprobeOfOneFile,
			want: probedFile{
				Duration: 8673.123,
				Bitrate:  6200321,
				Streams: []probedStream{
					{
						Kind: fileTypeVideo, Codec: "h264", Profile: "High",
						Width: 1920, Height: 1080, Depth: 8, FrameRate: "24000/1001",
						Color:   probedColor{Primaries: "bt709", Transfer: "bt709", Space: "bt709"},
						Bitrate: 5750424, Default: true,
					},
					{
						Kind: fileTypeAudio, Codec: "ac3", Channels: 6, Layout: "5.1(side)",
						SampleRate: 48000, Bitrate: 448000, Language: "eng",
						Title: "Surround AC3 5.1", Default: true,
					},
					{Kind: fileTypeSubtitle, Codec: "subrip", Bitrate: 31, Language: "eng", Title: "English"},
				},
			},
		},
		{
			name:    "a 4K HEVC Main 10 film in HDR10",
			capture: ffprobeOfAnHDRFile,
			want: probedFile{
				Duration: 8462.752,
				Bitrate:  19535025,
				Streams: []probedStream{
					{
						Kind: fileTypeVideo, Codec: "hevc", Profile: "Main 10",
						Width: 3840, Height: 2160, Depth: 10, FrameRate: "24/1",
						Color:   probedColor{Primaries: "bt2020", Transfer: "smpte2084", Space: "bt2020nc"},
						Bitrate: 18992713, Default: true,
					},
					{
						Kind: fileTypeAudio, Codec: "eac3", Profile: "Dolby Digital Plus + Dolby Atmos",
						Channels: 6, Layout: "5.1(side)", SampleRate: 48000, Bitrate: 768000,
						Language: "eng", Default: true,
					},
					{Kind: fileTypeSubtitle, Codec: "subrip", Bitrate: 86, Language: "eng", Title: "English [SDH]"},
					{Kind: fileTypeSubtitle, Codec: "subrip", Bitrate: 115, Language: "ara", Title: "Arabic"},
				},
			},
		},
		{
			name:    "an MP3 with a cover",
			capture: ffprobeOfAMusicFile,
			want: probedFile{
				Duration: 178.7882,
				Bitrate:  323145,
				Streams: []probedStream{
					{
						Kind: fileTypeAudio, Codec: "mp3", Channels: 2, Layout: "stereo",
						SampleRate: 44100, Bitrate: 320000,
					},
					{
						Kind: fileTypeImage, Codec: "mjpeg", Profile: "Baseline",
						Width: 600, Height: 600, Depth: 8, Title: "cover.jpg",
					},
				},
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := ffprobeAnswerOf(t, test.capture).probedFile()

			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("record = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestADolbyVisionStreamIsMarkedAsOne(t *testing.T) {
	cases := []struct {
		name    string
		capture string
		want    bool
	}{
		{name: "a file with a configuration record", capture: ffprobeOfADolbyVisionFile, want: true},
		{name: "the same file without one", capture: ffprobeOfAnHDRFile, want: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			record := ffprobeAnswerOf(t, test.capture).probedFile()

			if got := record.Streams[0].DolbyVision; got != test.want {
				t.Errorf("dolby vision = %v, want %v", got, test.want)
			}
		})
	}
}

func TestAStreamOfNoKindTheBrowserPlaysIsData(t *testing.T) {
	cases := []struct {
		name   string
		stream ffprobeStream
		want   string
	}{
		{name: "an attachment", stream: ffprobeStream{CodecType: "attachment"}, want: probedKindData},
		{name: "a data stream", stream: ffprobeStream{CodecType: "data"}, want: probedKindData},
		{name: "a kind ffprobe has not named here", stream: ffprobeStream{CodecType: "nut"}, want: probedKindData},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := test.stream.probed().Kind; got != test.want {
				t.Errorf("kind = %q, want %q", got, test.want)
			}
		})
	}
}

func TestADepthComesFromThePixelFormatWhereFFprobeStatesNone(t *testing.T) {
	cases := []struct {
		name   string
		stream ffprobeStream
		want   int
	}{
		{
			name:   "the raw sample count wins",
			stream: ffprobeStream{CodecType: fileTypeVideo, BitsPerRawSample: "8", PixelFormat: "yuv420p10le"},
			want:   8,
		},
		{
			name:   "ten bits in the pixel format",
			stream: ffprobeStream{CodecType: fileTypeVideo, PixelFormat: "yuv420p10le"},
			want:   10,
		},
		{
			name:   "twelve bits in the pixel format",
			stream: ffprobeStream{CodecType: fileTypeVideo, PixelFormat: "yuv420p12le"},
			want:   12,
		},
		{
			name:   "a pixel format that names no depth",
			stream: ffprobeStream{CodecType: fileTypeVideo, PixelFormat: "yuv420p"},
			want:   8,
		},
		{
			name:   "an audio stream that states none",
			stream: ffprobeStream{CodecType: fileTypeAudio},
			want:   0,
		},
		{
			name:   "an audio stream that states one",
			stream: ffprobeStream{CodecType: fileTypeAudio, BitsPerRawSample: "24"},
			want:   24,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := test.stream.probed().Depth; got != test.want {
				t.Errorf("depth = %d, want %d", got, test.want)
			}
		})
	}
}

func TestAFrameRateTakesTheRealRateWhereTheAverageIsNone(t *testing.T) {
	cases := []struct {
		name   string
		stream ffprobeStream
		want   string
	}{
		{
			name:   "an average rate",
			stream: ffprobeStream{CodecType: fileTypeVideo, AverageFrameRate: "24000/1001", RealFrameRate: "24/1"},
			want:   "24000/1001",
		},
		{
			name:   "no average rate",
			stream: ffprobeStream{CodecType: fileTypeVideo, AverageFrameRate: "0/0", RealFrameRate: "24/1"},
			want:   "24/1",
		},
		{
			name:   "neither rate",
			stream: ffprobeStream{CodecType: fileTypeVideo, AverageFrameRate: "0/0", RealFrameRate: "0/0"},
			want:   "",
		},
		{
			name:   "an audio stream carries none",
			stream: ffprobeStream{CodecType: fileTypeAudio, AverageFrameRate: "24/1"},
			want:   "",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := test.stream.probed().FrameRate; got != test.want {
				t.Errorf("frame rate = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAStreamBitrateTakesTheTagWhereTheContainerStatesNone(t *testing.T) {
	cases := []struct {
		name   string
		stream ffprobeStream
		want   int64
	}{
		{
			name:   "the stream's own rate",
			stream: ffprobeStream{CodecType: fileTypeAudio, BitRate: "448000", Tags: ffprobeTags{BPS: "1"}},
			want:   448000,
		},
		{
			name:   "the BPS tag",
			stream: ffprobeStream{CodecType: fileTypeVideo, Tags: ffprobeTags{BPS: "5750424"}},
			want:   5750424,
		},
		{
			name:   "the BPS-eng tag",
			stream: ffprobeStream{CodecType: fileTypeVideo, Tags: ffprobeTags{BPSEnglish: "5750424"}},
			want:   5750424,
		},
		{
			name:   "no rate at all",
			stream: ffprobeStream{CodecType: fileTypeSubtitle},
			want:   0,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := test.stream.probed().Bitrate; got != test.want {
				t.Errorf("bitrate = %d, want %d", got, test.want)
			}
		})
	}
}

func TestADurationTakesTheVideoStreamsOwnWhereTheContainerStatesNone(t *testing.T) {
	cases := []struct {
		name    string
		streams []ffprobeStream
		want    float64
	}{
		{
			name:    "a video stream that states one",
			streams: []ffprobeStream{{CodecType: fileTypeVideo, CodecName: "h264", Duration: "120.0"}},
			want:    120,
		},
		{
			name:    "no video stream at all",
			streams: []ffprobeStream{{CodecType: fileTypeAudio, CodecName: "mp3", Duration: "120.0"}},
			want:    0,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			answer := ffprobeAnswer{Streams: test.streams}

			if got := answer.probedFile().Duration; got != test.want {
				t.Errorf("duration = %v, want %v", got, test.want)
			}
		})
	}
}

func TestANumberFFprobeStatesAsAStringReadsBack(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  float64
	}{
		{name: "a decimal", value: "6540.400000", want: 6540.4},
		{name: "no duration at all", value: "", want: 0},
		{name: "a value that is not a number", value: "N/A", want: 0},
		{name: "a negative duration", value: "-1", want: 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := probeFloat(test.value); got != test.want {
				t.Errorf("probeFloat(%q) = %v, want %v", test.value, got, test.want)
			}
		})
	}
}

func TestASecondProbeOfAFileReplacesTheRecordTheLedgerHeld(t *testing.T) {
	ledger := &likenLedger{}

	ledger.noteProbe(probedFile{Path: "a.mkv", Duration: 1})
	ledger.noteProbe(probedFile{Path: "b.mkv", Duration: 2})
	ledger.noteProbe(probedFile{Path: "a.mkv", Duration: 3})

	want := []probedFile{{Path: "a.mkv", Duration: 3}, {Path: "b.mkv", Duration: 2}}
	if !reflect.DeepEqual(ledger.Probes, want) {
		t.Errorf("probes = %+v, want %+v", ledger.Probes, want)
	}
}
