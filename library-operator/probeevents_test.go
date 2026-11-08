package main

// probeevents_test.go proves the event count the probe records for each
// subtitle track: the statistics tag of an embedded stream, the timed cues of
// a text subtitle file, and the difference between no count and a count of
// zero, which media-operator reads to tell an unknown track from an empty one.

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

// A capture assembled from the 1080p capture's own streams: a forced track
// whose mkvmerge statistics carry the plain tag names, a full track muxed by
// an older mkvmerge that suffixes every tag with -eng, and a picture track
// with no statistics at all.
var ffprobeOfSubtitleStatistics = ffprobeCapture("video-subtitle-statistics.json")

func count(events int64) *int64 { return &events }

func TestASubtitleStreamCountsTheEventsItsStatisticsTagNames(t *testing.T) {
	record := ffprobeAnswerOf(t, ffprobeOfSubtitleStatistics).probedFile()

	var got []*int64
	for _, stream := range record.Streams {
		got = append(got, stream.Events)
	}
	want := []*int64{nil, nil, count(22), count(1054), nil}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("events = %v, want %v", eventsText(got), eventsText(want))
	}
	if !record.Counted {
		t.Error("the record does not say that the probe counted its subtitle events")
	}
}

func eventsText(events []*int64) []any {
	var text []any
	for _, one := range events {
		if one == nil {
			text = append(text, "none")
			continue
		}
		text = append(text, *one)
	}
	return text
}

func TestAStatisticsTagThatIsNotACountGivesNoCount(t *testing.T) {
	cases := []struct {
		name   string
		stream ffprobeStream
		want   *int64
	}{
		{name: "the plain tag", stream: subtitleTagged(ffprobeTags{NumberOfFrames: "569"}), want: count(569)},
		{name: "the -eng tag", stream: subtitleTagged(ffprobeTags{NumberOfFramesEnglish: "22"}), want: count(22)},
		{name: "a count of zero", stream: subtitleTagged(ffprobeTags{NumberOfFrames: "0"}), want: count(0)},
		{name: "no tag", stream: subtitleTagged(ffprobeTags{}), want: nil},
		{name: "a value that is not a number", stream: subtitleTagged(ffprobeTags{NumberOfFrames: "N/A"}), want: nil},
		{name: "a negative value", stream: subtitleTagged(ffprobeTags{NumberOfFrames: "-1"}), want: nil},
		{
			name:   "a video stream's frames",
			stream: ffprobeStream{CodecType: fileTypeVideo, Tags: ffprobeTags{NumberOfFrames: "207947"}},
			want:   nil,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := test.stream.probed().Events
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("events = %v, want %v", eventsText([]*int64{got}), eventsText([]*int64{test.want}))
			}
		})
	}
}

func subtitleTagged(tags ffprobeTags) ffprobeStream {
	return ffprobeStream{CodecType: fileTypeSubtitle, CodecName: "subrip", Tags: tags}
}

// The count is every timed cue, so the song, the empty cue, and the lines of
// episodeSRT all count, the way mkvmerge counts every block of a track.
func TestATextSubtitleFileCountsEveryTimedCue(t *testing.T) {
	cases := []struct {
		name string
		file string
		data string
		want int64
	}{
		{name: "an episode's lines, a song, and an empty cue", file: "a.en.srt", data: episodeSRT, want: 6},
		{name: "a song alone", file: "a.en.srt", data: "1\n00:00:05,000 --> 00:00:08,000\n♪ Hum ♪\n", want: 1},
		{name: "no cue", file: "a.en.srt", data: "", want: 0},
		{name: "a cue whose times do not read", file: "a.en.srt", data: "1\nsoon --> later\nHello.\n", want: 0},
		{
			name: "Advanced SubStation",
			file: "a.en.ass",
			data: "[Events]\n" +
				"Dialogue: 0,0:00:05.00,0:00:08.00,Default,,0,0,0,,Where were you?\n" +
				"Dialogue: 0,0:00:10.00,0:00:30.00,Default,,0,0,0,,{\\i1}♪ Theme ♪{\\i0}\n",
			want: 2,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), test.file)
			if err := os.WriteFile(path, []byte(test.data), 0o644); err != nil {
				t.Fatal(err)
			}
			record, err := readSubtitleRecord(path)
			if err != nil {
				t.Fatal(err)
			}
			if record.Events == nil || *record.Events != test.want || !record.Counted {
				t.Errorf("events = %v, counted %v, want %d", eventsText([]*int64{record.Events}), record.Counted, test.want)
			}
		})
	}
}

// The ledger keeps a count of zero, and keeps no count where the probe read
// none, so a rebuilt catalog tells an empty track from an unknown one.
func TestTheLedgerKeepsAZeroCountApartFromNoCount(t *testing.T) {
	written := probedFile{Path: "a.mkv", Counted: true, Streams: []probedStream{
		{Kind: fileTypeSubtitle, Codec: "subrip", Events: count(0)},
		{Kind: fileTypeSubtitle, Codec: "hdmv_pgs_subtitle"},
	}}
	data, err := yaml.Marshal(written)
	if err != nil {
		t.Fatal(err)
	}
	var read probedFile
	if err := yaml.Unmarshal(data, &read); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(read.Streams, written.Streams) || !read.Counted {
		t.Errorf("read back %+v from\n%s", read, data)
	}
}
