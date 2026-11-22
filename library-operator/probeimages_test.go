package main

// probeimages_test.go proves where the measurement of an image subtitle
// stream goes after the probe: the ledger keeps it under the names that
// plan 97 gives, the walk carries it into the stream rows, the catalog holds
// no measurement as null, and a record written before the probe measured
// image streams opens the probe gap again, so every library gains the
// numbers with no person asking for a refresh.

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var filmImage = &probedImage{CanvasHeight: 1080, LineHeight: 61, InkBottom: 918}

func TestTheLedgerNamesTheMeasurementAsThePlanDoes(t *testing.T) {
	record := probedFile{Path: "a.mkv", Counted: true, Measured: true, Streams: []probedStream{
		{Kind: fileTypeSubtitle, Codec: codecPGS, Image: filmImage},
	}}

	data, err := yaml.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"measured: true", "image:\n", "canvasHeight: 1080", "lineHeight: 61", "inkBottom: 918"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the record has no %q:\n%s", want, data)
		}
	}
	var read probedFile
	if err := yaml.Unmarshal(data, &read); err != nil {
		t.Fatal(err)
	}
	if got := read.Streams[0].Image; got == nil || *got != *filmImage || !read.Measured {
		t.Errorf("read back %+v from\n%s", read, data)
	}
}

// Only a record that holds an image subtitle stream and was written before
// the measurement needs the probe again. A record of a file with no image
// stream holds everything a new probe would write.
func TestARecordWithAnImageStreamAndNoMeasurementIsUnmeasured(t *testing.T) {
	cases := []struct {
		name   string
		record probedFile
		want   bool
	}{
		{name: "a PGS stream", record: probedFile{Streams: []probedStream{{Kind: fileTypeSubtitle, Codec: codecPGS}}}, want: true},
		{name: "a VobSub stream", record: probedFile{Streams: []probedStream{{Kind: fileTypeSubtitle, Codec: codecVobSub}}}, want: true},
		{
			name:   "a measured PGS stream",
			record: probedFile{Measured: true, Streams: []probedStream{{Kind: fileTypeSubtitle, Codec: codecPGS}}},
			want:   false,
		},
		{name: "a text stream", record: probedFile{Streams: []probedStream{{Kind: fileTypeSubtitle, Codec: "subrip"}}}, want: false},
		{name: "a text subtitle file", record: probedFile{Path: "a.en.srt", Counted: true}, want: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := test.record.unmeasured(); got != test.want {
				t.Errorf("unmeasured = %v, want %v", got, test.want)
			}
		})
	}
}

// The episode's probe record, with event counts and a PGS stream, and the
// result of the probe's last attempt.
func (e enrichedEpisode) probedWithAnImageStream(t *testing.T, measured bool, result string) {
	t.Helper()
	writeFactLedger(t, e.season, factProbe, likenLedger{
		Probes: []probedFile{{
			Path: replacedEntry, At: e.made, Modified: e.written.Unix(), Size: int64(len("the first encode")),
			Duration: 1500, Counted: true, Measured: measured,
			Streams: []probedStream{{Kind: fileTypeVideo, Codec: "h264"}, {Kind: fileTypeSubtitle, Codec: codecPGS}},
		}},
		Attempts: []likenAttempt{{Path: replacedEntry, At: e.made, Result: result}},
	})
}

// A found attempt at an unmeasured record no longer closes the probe gap, so
// the next probe run reads the file again. An error at the same record still
// stands for its own window, so a file whose window read fails is tried once
// a day and not on every run.
func TestAnUnmeasuredRecordReopensTheProbeGap(t *testing.T) {
	cases := []struct {
		name     string
		measured bool
		result   string
		want     int
	}{
		{name: "a record from before the measurement", measured: false, result: attemptFound, want: 1},
		{name: "a measured record", measured: true, result: attemptFound, want: 0},
		{name: "a record whose window read failed", measured: false, result: attemptError, want: 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			episode := seedEnrichedEpisode(t)
			episode.probedWithAnImageStream(t, test.measured, test.result)
			catalog, _ := newSQLiteCatalog(t)

			episode.rescan(t, catalog)

			if got := gapsOf(t, catalog)[factProbe]; got != test.want {
				t.Errorf("probe gap = %d, want %d", got, test.want)
			}
		})
	}
}

// The reopened record still fills the row and the streams, so the screens
// keep the codecs and the tracks while the probe reads the file again.
func TestAnUnmeasuredRecordStillFillsTheRow(t *testing.T) {
	episode := seedEnrichedEpisode(t)
	episode.probedWithAnImageStream(t, false, attemptFound)

	result := readFolder(episode.scan(), episode.series)

	row := fileRowAt(t, result, episode.path)
	if row.VideoCodec != "h264" || row.Probed != 0 || len(streamRowsAt(result, episode.path)) != 2 {
		t.Errorf("row = codec %q probed %d, want codec h264, probed 0, and two streams", row.VideoCodec, row.Probed)
	}
}

func TestTheWalkCarriesEachImageStreamsMeasurement(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "The Long Survey (1982)")
	video := filepath.Join(folder, "The Long Survey (1982).mkv")
	writeFile(t, video, "video")
	writeProbeLedger(t, folder, probedFile{
		Path: "The Long Survey (1982).mkv", Modified: modifiedOf(t, video), Counted: true, Measured: true,
		Streams: []probedStream{
			{Kind: fileTypeVideo, Codec: "h264"},
			{Kind: fileTypeSubtitle, Codec: codecPGS, Image: filmImage},
			{Kind: fileTypeSubtitle, Codec: codecPGS},
		},
	})

	result := &walkResult{}
	scanMovieFolder(movieScan(root), folder, result)

	var got []string
	for _, row := range streamRowsAt(result, "The Long Survey (1982)/The Long Survey (1982).mkv") {
		got = append(got, imageColumnsText(row.ImageCanvasHeight, row.ImageLineHeight, row.ImageInkBottom))
	}
	if want := []string{"none none none", "1080 61 918", "none none none"}; !slices.Equal(got, want) {
		t.Errorf("image columns = %q, want %q", got, want)
	}
}

func imageColumnsText(columns ...*int) string {
	var words []string
	for _, column := range columns {
		if column == nil {
			words = append(words, "none")
			continue
		}
		words = append(words, strconv.Itoa(*column))
	}
	return strings.Join(words, " ")
}

// A stream with no measurement holds null in each image column, which a
// reader tells apart from a measurement.
func TestTheCatalogHoldsNoMeasurementAsNull(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	canvas, line, bottom := 1080, 61, 918
	streams := []streamRow{
		{Library: "house/movies", Path: "a.mkv", Ordinal: 0, Kind: fileTypeSubtitle, Present: true,
			ImageCanvasHeight: &canvas, ImageLineHeight: &line, ImageInkBottom: &bottom},
		{Library: "house/movies", Path: "a.mkv", Ordinal: 1, Kind: fileTypeSubtitle, Present: true},
	}
	if err := upsertWalk(t.Context(), catalog, &walkResult{streams: streams}); err != nil {
		t.Fatal(err)
	}

	got := countsIn(t, catalog, `SELECT ifnull(CAST(image_canvas_height AS TEXT), 'none') || ' ' || `+
		`ifnull(CAST(image_line_height AS TEXT), 'none') || ' ' || ifnull(CAST(image_ink_bottom AS TEXT), 'none') `+
		`FROM streams WHERE library = ? ORDER BY ordinal`)
	if want := []string{"1080 61 918", "none none none"}; !slices.Equal(got, want) {
		t.Errorf("image columns = %q, want %q", got, want)
	}
}
