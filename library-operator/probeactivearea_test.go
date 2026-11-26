package main

// probeactivearea_test.go proves where the active area of a video stream
// goes after the probe: the ledger keeps it under the names that plan 102
// gives, the walk carries it into the stream rows, the catalog holds no area
// as null, and a record written before the measurement opens the probe gap
// again, so every library gains the area with no person asking for a
// refresh.

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

var scopeArea = &probedActiveArea{Left: 0, Top: 131, Width: 1920, Height: 817}

func TestTheLedgerNamesTheActiveAreaAsThePlanDoes(t *testing.T) {
	record := probedFile{Path: "a.mkv", Counted: true, ActiveAreaMeasurement: activeAreaMeasurement,
		Streams: []probedStream{{Kind: fileTypeVideo, Codec: "hevc", Width: 1920, Height: 1080, ActiveArea: scopeArea}}}

	data, err := yaml.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"activeAreaMeasurement: 1", "activeArea:\n", "left: 0", "top: 131", "width: 1920", "height: 817"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the record has no %q:\n%s", want, data)
		}
	}
	var read probedFile
	if err := yaml.Unmarshal(data, &read); err != nil {
		t.Fatal(err)
	}
	if got := read.Streams[0].ActiveArea; !reflect.DeepEqual(got, scopeArea) || read.ActiveAreaMeasurement != 1 {
		t.Errorf("read back %+v from\n%s", read, data)
	}
}

// Only a record that holds a video stream and was written before the
// measurement needs the probe again. A record of a sound file or of a text
// subtitle file holds everything a new probe would write.
func TestARecordWithAVideoStreamAndNoActiveAreaVersionLacksTheArea(t *testing.T) {
	cases := []struct {
		name   string
		record probedFile
		want   bool
	}{
		{name: "a video stream", record: probedFile{Streams: []probedStream{{Kind: fileTypeVideo}}}, want: true},
		{
			name: "a measured video stream",
			record: probedFile{ActiveAreaMeasurement: activeAreaMeasurement,
				Streams: []probedStream{{Kind: fileTypeVideo, ActiveArea: scopeArea}}},
			want: false,
		},
		{
			name: "a measured video stream whose samples did not agree",
			record: probedFile{ActiveAreaMeasurement: activeAreaMeasurement,
				Streams: []probedStream{{Kind: fileTypeVideo}}},
			want: false,
		},
		{name: "a sound file with a cover",
			record: probedFile{Streams: []probedStream{{Kind: fileTypeAudio}, {Kind: fileTypeImage}}}, want: false},
		{name: "a text subtitle file", record: probedFile{Path: "a.en.srt", Counted: true}, want: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := test.record.lacksActiveArea(); got != test.want {
				t.Errorf("lacksActiveArea = %v, want %v", got, test.want)
			}
		})
	}
}

// The episode's probe record of the current measurements, without the
// active area's version where measured is false, and the result of the
// probe's last attempt.
func (e enrichedEpisode) probedWithAVideoStream(t *testing.T, measured bool, result string) {
	t.Helper()
	record := probedFile{
		Path: replacedEntry, At: e.made, Modified: e.written.Unix(), Size: int64(len("the first encode")),
		Duration: 1500, Counted: true,
		Streams: []probedStream{{Kind: fileTypeVideo, Codec: "h264", Width: 1920, Height: 1080}},
	}
	if measured {
		record.ActiveAreaMeasurement, record.Streams[0].ActiveArea = activeAreaMeasurement, scopeArea
	}
	writeFactLedger(t, e.season, factProbe, likenLedger{
		Probes:   []probedFile{record},
		Attempts: []likenAttempt{{Path: replacedEntry, At: e.made, Result: result}},
	})
}

// A found attempt at a record without the area no longer closes the probe
// gap, so the next probe run reads the file again. An error at the same
// record still stands for its own window, so a file whose samples fail is
// tried once a day and not on every run.
func TestARecordWithoutTheActiveAreaReopensTheProbeGap(t *testing.T) {
	cases := []struct {
		name     string
		measured bool
		result   string
		want     int
	}{
		{name: "a record from before the measurement", measured: false, result: attemptFound, want: 1},
		{name: "a measured record", measured: true, result: attemptFound, want: 0},
		{name: "a record whose samples failed", measured: false, result: attemptError, want: 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			episode := seedEnrichedEpisode(t)
			episode.probedWithAVideoStream(t, test.measured, test.result)
			catalog, _ := newSQLiteCatalog(t)

			episode.rescan(t, catalog)

			if got := gapsOf(t, catalog)[factProbe]; got != test.want {
				t.Errorf("probe gap = %d, want %d", got, test.want)
			}
		})
	}
}

// The walk carries the area of each video stream into its row, and every
// other stream, and a video stream with no area, has none.
func TestTheWalkCarriesEachVideoStreamsActiveArea(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "The Long Survey (1982)")
	video := filepath.Join(folder, "The Long Survey (1982).mkv")
	writeFile(t, video, "video")
	writeProbeLedger(t, folder, probedFile{
		Path: "The Long Survey (1982).mkv", Modified: modifiedOf(t, video), Counted: true,
		ActiveAreaMeasurement: activeAreaMeasurement,
		Streams: []probedStream{
			{Kind: fileTypeVideo, Codec: "hevc", Width: 1920, Height: 1080, ActiveArea: scopeArea},
			{Kind: fileTypeVideo, Codec: "hevc", Width: 1920, Height: 1080},
			{Kind: fileTypeAudio, Codec: "eac3"},
		},
	})

	result := &walkResult{}
	scanMovieFolder(movieScan(root), folder, result)

	var got []string
	for _, row := range streamRowsAt(result, "The Long Survey (1982)/The Long Survey (1982).mkv") {
		got = append(got, imageColumnsText(row.ActiveLeft, row.ActiveTop, row.ActiveWidth, row.ActiveHeight))
	}
	if want := []string{"0 131 1920 817", "none none none none", "none none none none"}; !slices.Equal(got, want) {
		t.Errorf("active columns = %q, want %q", got, want)
	}
}

// A stream with no area holds null in each of the four columns, which a
// reader tells apart from an area at the corner of the frame.
func TestTheCatalogHoldsNoActiveAreaAsNull(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	left, top, width, height := 0, 131, 1920, 817
	streams := []streamRow{
		{Library: "house/movies", Path: "a.mkv", Ordinal: 0, Kind: fileTypeVideo, Present: true,
			ActiveLeft: &left, ActiveTop: &top, ActiveWidth: &width, ActiveHeight: &height},
		{Library: "house/movies", Path: "a.mkv", Ordinal: 1, Kind: fileTypeVideo, Present: true},
	}
	if err := upsertWalk(t.Context(), catalog, &walkResult{streams: streams}); err != nil {
		t.Fatal(err)
	}

	got := countsIn(t, catalog, `SELECT ifnull(CAST(active_left AS TEXT), 'none') || ' ' || `+
		`ifnull(CAST(active_top AS TEXT), 'none') || ' ' || ifnull(CAST(active_width AS TEXT), 'none') || ' ' || `+
		`ifnull(CAST(active_height AS TEXT), 'none') FROM streams WHERE library = ? ORDER BY ordinal`)
	if want := []string{"0 131 1920 817", "none none none none"}; !slices.Equal(got, want) {
		t.Errorf("active columns = %q, want %q", got, want)
	}
}

// The probe writes the area it measured into the ledger with the version,
// so the walk that reads the folder next does not open the gap again.
func TestTheProbeRecordsTheActiveAreaOfTheFilm(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedProbeGap(t, catalog, root, "The Long Survey (1982)", "The Long Survey (1982).mkv")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	samples := &fakeSamples{outputs: [][]byte{cropdetectFixture(t, "scope-bars.txt")}}
	was := readActiveAreaSample
	t.Cleanup(func() { readActiveAreaSample = was })
	readActiveAreaSample = samples.read

	if err := work.probeGap(t.Context(), answeringProbe(ffprobeOfOneFile)); err != nil {
		t.Fatal(err)
	}

	ledger, err := readLikenLedger(filepath.Join(root, "The Long Survey (1982)"), factProbe)
	if err != nil {
		t.Fatal(err)
	}
	record, held := ledger.probeAt("The Long Survey (1982).mkv")
	if !held || record.ActiveAreaMeasurement != activeAreaMeasurement ||
		!reflect.DeepEqual(record.first(fileTypeVideo).ActiveArea, scopeArea) {
		t.Errorf("record = %+v, want version %d and the area %+v", record, activeAreaMeasurement, scopeArea)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
		t.Errorf("attempts = %+v, want the one attempt that found the streams", ledger.Attempts)
	}
	folder := filepath.Join(root, "The Long Survey (1982)")
	if _, _, _, err := rescanFolder(t.Context(), catalog, movieScan(root), folder); err != nil {
		t.Fatal(err)
	}
	counts, err := catalog.gapCounts(t.Context(), "house/movies", time.Now().UTC(), reopenTimes{})
	if err != nil {
		t.Fatal(err)
	}
	if counts[factProbe] != 0 {
		t.Errorf("probe gap after the walk = %d, want 0", counts[factProbe])
	}
}
