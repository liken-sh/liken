package main

// subtitlecues_test.go proves the read of a text subtitle file into the
// stretches with no dialogue, and the two rules that read a credits start
// and an intro out of those stretches.

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// An episode's subtitles: dialogue, a song, an empty cue, dialogue again 90
// seconds later, and the last line at 40 minutes.
const episodeSRT = "1\n00:00:05,000 --> 00:00:08,000\nWhere were you?\n\n" +
	"2\n00:00:09,000 --> 00:00:30,000\n♪ Theme song plays ♪\n\n" +
	"3\n00:00:31,000 --> 00:00:32,000\n<i></i>\n\n" +
	"4\n00:01:38,500 --> 00:01:40,000\n- At the dock.\n- Again?\n\n" +
	"5\n00:02:00,000 --> 00:02:02,000\n# la la la #\n\n" +
	"6\n00:40:00,000 --> 00:40:02,250\nGoodbye.\n"

func TestASubtitleFileReadsAsItsDialogue(t *testing.T) {
	cases := []struct {
		name string
		file string
		data string
		want subtitleDialogue
	}{
		{
			name: "SubRip",
			file: "Show - S01E01.en.srt",
			data: episodeSRT,
			want: subtitleDialogue{First: 5000, Last: 2402250, Gaps: []timeSpan{
				{Start: 8000, End: 98500}, {Start: 100000, End: 2400000}}},
		},
		{
			name: "WebVTT with no hours",
			file: "Show - S01E01.en.vtt",
			data: "WEBVTT\n\n00:05.000 --> 00:08.000\nWhere were you?\n\n" +
				"01:38.500 --> 01:40.000 align:start\nAt the dock.\n",
			want: subtitleDialogue{First: 5000, Last: 100000, Gaps: []timeSpan{{Start: 8000, End: 98500}}},
		},
		{
			name: "Advanced SubStation",
			file: "Show - S01E01.en.ass",
			data: "[Script Info]\nTitle: x\n\n[Events]\n" +
				"Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
				"Dialogue: 0,0:00:05.00,0:00:08.00,Default,,0,0,0,,Where were you?\n" +
				"Dialogue: 0,0:00:10.00,0:00:30.00,Default,,0,0,0,,{\\i1}♪ Theme ♪{\\i0}\n" +
				"Dialogue: 0,0:01:38.50,0:01:40.00,Default,,0,0,0,,At the dock,\\Nagain.\n",
			want: subtitleDialogue{First: 5000, Last: 100000, Gaps: []timeSpan{{Start: 8000, End: 98500}}},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, held := readSubtitleDialogue(test.file, []byte(test.data))
			if !held || !reflect.DeepEqual(got, test.want) {
				t.Errorf("dialogue = %+v, %v, want %+v", got, held, test.want)
			}
		})
	}
}

// The probe reads a subtitle file beside a film itself, records its dialogue
// in the ledger, and writes the dialogue column, so the close container of
// the same Job reads it. ffprobe is never asked about the subtitle file.
func TestTheProbeRecordsTheDialogueOfASubtitleFile(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	folder := "The Long Survey (1982)"
	seedProbeGap(t, catalog, root, folder, "The Long Survey (1982).mkv")
	subtitle := filepath.Join(folder, "The Long Survey (1982).en.srt")
	writeFile(t, filepath.Join(root, subtitle), episodeSRT)
	seed := &walkResult{files: []fileRow{{
		Path: subtitle, Library: "house/movies", Present: true, Type: fileTypeSubtitle, Role: fileRoleFull,
		Language: "en", Modified: modifiedOf(t, filepath.Join(root, subtitle)), Items: []string{"movie:path:x"},
	}}}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	ffprobeOnlyVideos := func(_ context.Context, path string) ([]byte, error) {
		if isTextSubtitle(path) {
			t.Errorf("ffprobe was asked about %s", path)
		}
		return []byte(ffprobeOfOneFile), nil
	}

	if err := work.probeGap(t.Context(), ffprobeOnlyVideos); err != nil {
		t.Fatal(err)
	}

	columns, err := catalog.queryStrings(t.Context(),
		`SELECT dialogue FROM files WHERE library = ? AND path = ? AND probed = modified`,
		[]any{"house/movies", subtitle})
	if err != nil {
		t.Fatal(err)
	}
	want := subtitleDialogue{First: 5000, Last: 2402250, Gaps: []timeSpan{
		{Start: 8000, End: 98500}, {Start: 100000, End: 2400000}}}
	if len(columns) != 1 || !reflect.DeepEqual(dialogueColumn(columns[0]), &want) {
		t.Errorf("dialogue = %v, want %+v with the probe gap closed", columns, want)
	}
}

// A subtitle file the probe cannot read is an error attempt, like a video
// ffprobe cannot open.
func TestASubtitleFileThatWillNotReadIsAnError(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		path string
	}{
		{name: "a file that is gone", path: filepath.Join(dir, "gone.srt")},
		{name: "a directory with a subtitle's name", path: dir + "/folder.srt"},
	}
	if err := os.Mkdir(cases[1].path, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := readSubtitleRecord(test.path); err == nil {
				t.Error("the read reported no error, want one")
			}
		})
	}
}

// A cue whose times do not read, and a SubStation line with too few fields,
// are skipped, and the cues around them still count.
func TestACueThatDoesNotReadIsSkipped(t *testing.T) {
	cases := []struct {
		name string
		file string
		data string
	}{
		{name: "a time with four parts", file: "a.srt",
			data: "1\n0:00:00:01,000 --> 0:00:00:02,000\nNo.\n\n2\n00:00:05,000 --> 00:00:08,000\nYes.\n"},
		{name: "a time with one part", file: "a.vtt",
			data: "WEBVTT\n\n5.000 --> 6.000\nNo.\n\n00:05.000 --> 00:08.000\nYes.\n"},
		{name: "minutes that are not a number", file: "a.srt",
			data: "1\n00:aa:01,000 --> 00:00:02,000\nNo.\n\n2\n00:00:05,000 --> 00:00:08,000\nYes.\n"},
		{name: "seconds that are not a number", file: "a.srt",
			data: "1\n00:00:xx --> 00:00:02,000\nNo.\n\n2\n00:00:05,000 --> 00:00:08,000\nYes.\n"},
		{name: "an arrow with no end", file: "a.srt",
			data: "1\n00:00:01,000 -->\nNo.\n\n2\n00:00:05,000 --> 00:00:08,000\nYes.\n"},
		{name: "a SubStation line with too few fields", file: "a.ssa",
			data: "Dialogue: 0,0:00:01.00,0:00:02.00,Default\n" +
				"Dialogue: 0,0:00:05.00,0:00:08.00,Default,,0,0,0,,Yes.\n"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, held := readSubtitleDialogue(test.file, []byte(test.data))
			if want := (subtitleDialogue{First: 5000, Last: 8000}); !held || !reflect.DeepEqual(got, want) {
				t.Errorf("dialogue = %+v, %v, want %+v", got, held, want)
			}
		})
	}
}

// A dialogue column that is empty or does not decode holds no dialogue.
func TestADialogueColumnThatHoldsNothing(t *testing.T) {
	for _, column := range []string{"", "{not json"} {
		if got := dialogueColumn(column); got != nil {
			t.Errorf("dialogueColumn(%q) = %+v, want nil", column, got)
		}
	}
}

// A file with no dialogue at all says nothing about where it is quiet.
func TestASubtitleFileOfSongsAloneHasNoDialogue(t *testing.T) {
	if got, held := readSubtitleDialogue("a.srt", []byte("1\n00:00:05,000 --> 00:00:08,000\n♪ Hum ♪\n")); held {
		t.Errorf("dialogue = %+v, want none", got)
	}
}

func TestTheSubtitleCreditsStartAtTheLastDialogueBeforeALongQuiet(t *testing.T) {
	const duration = 3000000
	cases := []struct {
		name     string
		dialogue subtitleDialogue
		want     int64
		held     bool
	}{
		{
			name:     "a quiet stretch that runs to the end",
			dialogue: subtitleDialogue{First: 5000, Last: 2880000},
			want:     2880000, held: true,
		},
		{
			// The credits run from 2,800 to 2,940 seconds, and a scene
			// with dialogue follows them until the end.
			name: "a scene after the credits",
			dialogue: subtitleDialogue{First: 5000, Last: 2990000, Gaps: []timeSpan{
				{Start: 1200000, End: 1260000}, {Start: 2800000, End: 2940000}}},
			want: 2800000, held: true,
		},
		{
			// The scene after the credits is followed by more credits.
			name: "a scene between two runs of credits",
			dialogue: subtitleDialogue{First: 5000, Last: 2900000, Gaps: []timeSpan{
				{Start: 2700000, End: 2850000}}},
			want: 2700000, held: true,
		},
		{
			// Forty minutes of dialogue after a quiet stretch is the story,
			// not a scene after the credits.
			name: "a long cluster after a quiet stretch",
			dialogue: subtitleDialogue{First: 5000, Last: 2990000, Gaps: []timeSpan{
				{Start: 1600000, End: 1700000}}},
			held: false,
		},
		{
			name:     "a quiet stretch under 45 seconds",
			dialogue: subtitleDialogue{First: 5000, Last: 2960000},
			held:     false,
		},
		{
			name:     "a quiet second half",
			dialogue: subtitleDialogue{First: 5000, Last: 1400000},
			held:     false,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, held := test.dialogue.creditsStart(duration)
			if held != test.held || got != test.want {
				t.Errorf("creditsStart = %d, %v, want %d, %v", got, held, test.want, test.held)
			}
		})
	}
}

func TestTheSubtitleIntroIsTheFirstQuietStretchOfAnIntrosLength(t *testing.T) {
	const duration = 3000000
	cases := []struct {
		name     string
		dialogue subtitleDialogue
		want     timeSpan
		held     bool
	}{
		{
			name: "a quiet stretch after a cold open",
			dialogue: subtitleDialogue{First: 5000, Last: 2900000, Gaps: []timeSpan{
				{Start: 8000, End: 98500}, {Start: 1000000, End: 1100000}}},
			want: timeSpan{Start: 8000, End: 98500}, held: true,
		},
		{
			name:     "a quiet stretch before the first line",
			dialogue: subtitleDialogue{First: 75000, Last: 2900000},
			want:     timeSpan{Start: 0, End: 75000}, held: true,
		},
		{
			name: "a stretch too short, then one that fits",
			dialogue: subtitleDialogue{First: 2000, Last: 2900000, Gaps: []timeSpan{
				{Start: 10000, End: 35000}, {Start: 200000, End: 260000}}},
			want: timeSpan{Start: 200000, End: 260000}, held: true,
		},
		{
			name: "a stretch too long",
			dialogue: subtitleDialogue{First: 2000, Last: 2900000, Gaps: []timeSpan{
				{Start: 10000, End: 190000}}},
			held: false,
		},
		{
			name: "a stretch after ten minutes",
			dialogue: subtitleDialogue{First: 2000, Last: 2900000, Gaps: []timeSpan{
				{Start: 590000, End: 650000}}},
			held: false,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, held := test.dialogue.introSpan(duration)
			if held != test.held || got != test.want {
				t.Errorf("introSpan = %+v, %v, want %+v, %v", got, held, test.want, test.held)
			}
		})
	}
}
