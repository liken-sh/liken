package main

// probecounts_test.go proves where the subtitle event counts go after the
// probe: the walk carries them into the stream rows and the file rows, the
// catalog holds no count apart from zero, and a record written before the
// probe counted events opens the probe gap again, so every library gains the
// counts with no person asking for a refresh.

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestTheWalkCarriesEachSubtitleTracksEventCount(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "The Long Survey (1982)")
	video := filepath.Join(folder, "The Long Survey (1982).mkv")
	subtitles := filepath.Join(folder, "The Long Survey (1982).en.srt")
	writeFile(t, video, "video")
	writeFile(t, subtitles, "subtitles")
	writeProbeLedger(t, folder,
		probedFile{
			Path: "The Long Survey (1982).mkv", Modified: modifiedOf(t, video), Counted: true,
			Streams: []probedStream{
				{Kind: fileTypeVideo, Codec: "h264"},
				{Kind: fileTypeSubtitle, Codec: "subrip", Events: count(22)},
				{Kind: fileTypeSubtitle, Codec: "hdmv_pgs_subtitle"},
			},
		},
		probedFile{Path: "The Long Survey (1982).en.srt", Modified: modifiedOf(t, subtitles), Counted: true, Events: count(1054)},
	)

	result := &walkResult{}
	scanMovieFolder(movieScan(root), folder, result)

	var got []*int64
	for _, row := range streamRowsAt(result, "The Long Survey (1982)/The Long Survey (1982).mkv") {
		got = append(got, row.Events)
	}
	if want := []*int64{nil, count(22), nil}; !equalCounts(got, want) {
		t.Errorf("stream events = %v, want %v", eventsText(got), eventsText(want))
	}
	file := fileRowAt(t, result, "The Long Survey (1982)/The Long Survey (1982).en.srt")
	if !equalCounts([]*int64{file.Events}, []*int64{count(1054)}) {
		t.Errorf("file events = %v, want 1054", eventsText([]*int64{file.Events}))
	}
}

func equalCounts(a, b []*int64) bool {
	return slices.EqualFunc(a, b, func(x, y *int64) bool { return (x == nil && y == nil) || (x != nil && y != nil && *x == *y) })
}

// The count of each row as the catalog holds it, with no count read as the
// word none.
func countsIn(t *testing.T, catalog *Catalog, sql string) []string {
	t.Helper()
	got, err := catalog.queryStrings(t.Context(), sql, []any{"house/movies"})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestTheCatalogHoldsNoCountApartFromZero(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	streams := []streamRow{
		{Library: "house/movies", Path: "a.mkv", Ordinal: 0, Kind: fileTypeSubtitle, Events: count(0), Present: true},
		{Library: "house/movies", Path: "a.mkv", Ordinal: 1, Kind: fileTypeSubtitle, Events: count(569), Present: true},
		{Library: "house/movies", Path: "a.mkv", Ordinal: 2, Kind: fileTypeSubtitle, Present: true},
	}
	files := []fileRow{
		{Library: "house/movies", Path: "a.en.srt", Type: fileTypeSubtitle, Events: count(0), Present: true},
		{Library: "house/movies", Path: "a.fr.srt", Type: fileTypeSubtitle, Present: true},
	}
	if err := upsertWalk(t.Context(), catalog, &walkResult{files: files, streams: streams}); err != nil {
		t.Fatal(err)
	}

	gotStreams := countsIn(t, catalog, `SELECT ifnull(CAST(events AS TEXT), 'none') FROM streams WHERE library = ? ORDER BY ordinal`)
	if want := []string{"0", "569", "none"}; !slices.Equal(gotStreams, want) {
		t.Errorf("stream events = %v, want %v", gotStreams, want)
	}
	gotFiles := countsIn(t, catalog, `SELECT ifnull(CAST(events AS TEXT), 'none') FROM files WHERE library = ? ORDER BY path`)
	if want := []string{"0", "none"}; !slices.Equal(gotFiles, want) {
		t.Errorf("file events = %v, want %v", gotFiles, want)
	}
}

// The probe's own write after a run reaches the count of a text subtitle
// file, so the count lands with the probed column and not one walk later.
func TestTheProbesOwnWriteCarriesASubtitleFilesCount(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	file := fileRow{Library: "house/movies", Path: "a.en.srt", Type: fileTypeSubtitle, Present: true}
	if err := upsertWalk(t.Context(), catalog, &walkResult{files: []fileRow{file}}); err != nil {
		t.Fatal(err)
	}

	file.Events = count(412)
	if _, err := catalog.UpdateFileStreams(t.Context(), []fileRow{file}); err != nil {
		t.Fatal(err)
	}

	got := countsIn(t, catalog, `SELECT ifnull(CAST(events AS TEXT), 'none') FROM files WHERE library = ?`)
	if want := []string{"412"}; !slices.Equal(got, want) {
		t.Errorf("file events = %v, want %v", got, want)
	}
}

// Only a record that holds a subtitle track and was written before the probe
// counted events needs the probe again. Every other record holds nothing a
// count would add to.
func TestARecordWithSubtitlesAndNoCountsIsUncounted(t *testing.T) {
	subtitled := []probedStream{{Kind: fileTypeVideo}, {Kind: fileTypeSubtitle}}
	cases := []struct {
		name   string
		record probedFile
		want   bool
	}{
		{name: "a video with a subtitle stream", record: probedFile{Path: "a.mkv", Streams: subtitled}, want: true},
		{name: "the same video counted", record: probedFile{Path: "a.mkv", Streams: subtitled, Counted: true}, want: false},
		{name: "a video with no subtitle stream", record: probedFile{Path: "a.mkv", Streams: subtitled[:1]}, want: false},
		{name: "a text subtitle file", record: probedFile{Path: "a.en.srt"}, want: true},
		{name: "the same file counted", record: probedFile{Path: "a.en.srt", Counted: true, Events: count(3)}, want: false},
		{name: "a picture subtitle file", record: probedFile{Path: "a.en.sup"}, want: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := test.record.uncounted(); got != test.want {
				t.Errorf("uncounted = %v, want %v", got, test.want)
			}
		})
	}
}

// The episode's probe record, given a subtitle stream and the result of the
// probe's last attempt.
func (e enrichedEpisode) probedWith(t *testing.T, counted bool, result string) {
	t.Helper()
	writeFactLedger(t, e.season, factProbe, likenLedger{
		Probes: []probedFile{{
			Path: replacedEntry, At: e.made, Modified: e.written.Unix(), Size: int64(len("the first encode")),
			Duration: 1500, Counted: counted,
			Streams: []probedStream{{Kind: fileTypeVideo, Codec: "h264"}, {Kind: fileTypeSubtitle, Codec: "subrip"}},
		}},
		Attempts: []likenAttempt{{Path: replacedEntry, At: e.made, Result: result}},
	})
}

// A found attempt at an uncounted record no longer closes the probe gap, so
// the next probe run reads the file's headers again. An error at the same
// record still stands for its own window, so a file that will not open is
// tried once a day and not on every run.
func TestAnUncountedRecordReopensTheProbeGap(t *testing.T) {
	cases := []struct {
		name    string
		counted bool
		result  string
		want    int
	}{
		{name: "a record from before the counts", counted: false, result: attemptFound, want: 1},
		{name: "a record with the counts", counted: true, result: attemptFound, want: 0},
		{name: "a record from before the counts that last failed", counted: false, result: attemptError, want: 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			episode := seedEnrichedEpisode(t)
			episode.probedWith(t, test.counted, test.result)
			catalog, _ := newSQLiteCatalog(t)

			episode.rescan(t, catalog)

			if got := gapsOf(t, catalog)[factProbe]; got != test.want {
				t.Errorf("probe gap = %d, want %d", got, test.want)
			}
		})
	}
}

// The reopened record still fills the row, so the screens keep the codecs
// and the streams while the probe reads the file again.
func TestAnUncountedRecordStillFillsTheRow(t *testing.T) {
	episode := seedEnrichedEpisode(t)
	episode.probedWith(t, false, attemptFound)

	result := readFolder(episode.scan(), episode.series)

	row := fileRowAt(t, result, episode.path)
	if row.VideoCodec != "h264" || row.Probed != 0 {
		t.Errorf("row = codec %q probed %d, want codec h264 and probed 0", row.VideoCodec, row.Probed)
	}
	if rows := streamRowsAt(result, episode.path); len(rows) != 2 {
		t.Errorf("streams = %+v, want the record's two", rows)
	}
}
