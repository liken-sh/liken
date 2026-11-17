package main

import (
	"strings"
	"testing"
	"time"
)

// A primary video of the titles library, present and probed.
func placingVideo(path, item string) fileRow {
	return fileRow{Path: path, Library: titlesLibrary, Present: true, Type: fileTypeVideo,
		Role: fileRolePrimary, DurationMs: 6000000, VideoCodec: "h264", Items: wholeItems(item)}
}

// The attempt of the titles worker on one video, landed at the time given.
func readingAt(path, result string, at time.Time) attemptRow {
	return attemptRow{Library: titlesLibrary, Item: path, Fact: factTitles, At: at.Unix(), Result: result}
}

// A placement row of one video, as the placing Job writes it.
func placementRow(path string, start int64, confidence float64) markRow {
	return markRow{Library: titlesLibrary, Path: path, Ordinal: titlesPlacementOrdinal,
		Kind: markKindCreditsPlacement, Start: &start, Source: markSourceTitles, Confidence: &confidence}
}

// The count takes each present primary video with a found reading, and the
// rows of the placements alone. A video that is gone, a file that is not the
// primary video, a reading that failed, and a mark of another kind or source
// count for nothing.
func TestTheReadingsCountAgainstTheRealSchema(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	early, late := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	gone := placingVideo("C/c.mkv", "movie:tmdb:3")
	gone.Present = false
	extra := placingVideo("A/a-extra.mkv", "movie:tmdb:1")
	extra.Role = "extra"
	estimate := int64(5000000)
	seed := &walkResult{
		files: []fileRow{placingVideo("A/a.mkv", "movie:tmdb:1"), placingVideo("B/b.mkv", "movie:tmdb:2"), gone,
			extra, placingVideo("D/d.mkv", "movie:tmdb:4")},
		attempts: []attemptRow{readingAt("A/a.mkv", attemptFound, early), readingAt("B/b.mkv", attemptFound, late),
			readingAt("C/c.mkv", attemptFound, late.Add(time.Hour)), readingAt("A/a-extra.mkv", attemptFound, late),
			readingAt("D/d.mkv", attemptError, late.Add(time.Hour))},
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
	rows := []markRow{placementRow("A/a.mkv", 5400000, 0.9), placementRow("B/b.mkv", 5300000, 0.4),
		{Library: titlesLibrary, Path: "A/a.mkv", Ordinal: -1, Kind: markKindCredits, Start: &estimate,
			Source: markSourceEstimate}}
	if _, err := catalog.UpsertMarks(t.Context(), nil, rows); err != nil {
		t.Fatal(err)
	}

	counts, err := catalog.titlesReadings(t.Context(), titlesLibrary)
	if err != nil {
		t.Fatal(err)
	}

	if want := (titlesReadings{Readings: 2, NewestReading: late, Placements: 2}); counts != want {
		t.Errorf("counts = %+v, want %+v", counts, want)
	}
}

func TestWhenAPlacingIsDue(t *testing.T) {
	model := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	newest := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	last := titlesPlaced{At: newest.Add(time.Hour), Result: titlesPlacePlaced, Model: model, Readings: 1000,
		NewestReading: newest, Placements: 990}
	now := titlesReadings{Readings: 1000, NewestReading: newest, Placements: 990}
	with := func(change func(*titlesReadings)) titlesReadings {
		counts := now
		change(&counts)
		return counts
	}
	cases := []struct {
		name    string
		model   time.Time
		counts  titlesReadings
		last    []titlesPlaced
		listed  bool
		reading bool
		cause   string
	}{
		{name: "no model", counts: now},
		{name: "no placing yet", model: model, counts: now, cause: "no placing has run with the model fitted at 2026-11-14T12:00:00Z"},
		{name: "a new model", model: model.Add(time.Hour), counts: now, last: []titlesPlaced{last},
			cause: "no placing has run with the model"},
		{name: "a Job started with the model, whose result never arrived", model: model.Add(time.Hour), counts: now,
			last: []titlesPlaced{last, {Model: model.Add(time.Hour), Readings: 1000, NewestReading: newest}}, listed: true},
		{name: "nothing changed", model: model, counts: now, last: []titlesPlaced{last}},
		{name: "a catalog built again", model: model, counts: with(func(c *titlesReadings) { c.Placements = 0 }),
			last: []titlesPlaced{last}, cause: "the catalog holds 0 placements, and the last placing wrote 990"},
		{name: "fewer placements while the last Job is listed", model: model, listed: true,
			counts: with(func(c *titlesReadings) { c.Placements = 0 }), last: []titlesPlaced{last}},
		{name: "49 readings while a worker reads", model: model, reading: true,
			counts: with(func(c *titlesReadings) { c.Readings = 1049 }), last: []titlesPlaced{last}},
		{name: "50 readings while a worker reads", model: model, reading: true,
			counts: with(func(c *titlesReadings) { c.Readings = 1050 }), last: []titlesPlaced{last},
			cause: "50 readings have landed since the last placing"},
		{name: "one reading and no worker", model: model,
			counts: with(func(c *titlesReadings) { c.Readings = 1001 }), last: []titlesPlaced{last},
			cause: "a reading has landed since the last placing, and no reading worker runs"},
		{name: "a file read again and no worker", model: model,
			counts: with(func(c *titlesReadings) { c.NewestReading = newest.Add(time.Minute) }), last: []titlesPlaced{last},
			cause: "a reading has landed since the last placing"},
		{name: "a Job started on the readings that landed", model: model, listed: true,
			counts: with(func(c *titlesReadings) { c.Readings = 1001 }),
			last:   []titlesPlaced{last, {Model: model, Readings: 1001, NewestReading: newest}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			cause := titlesPlaceCause(test.model, test.counts, test.last, test.listed, test.reading)

			if (cause == "") != (test.cause == "") || !strings.Contains(cause, test.cause) {
				t.Errorf("cause = %q, want %q", cause, test.cause)
			}
		})
	}
}

// The baseline takes the status's last placing, the broker's, and what each
// listed Job was started on, and leaves out a result of another kind and a
// Job with no annotation.
func TestThePlacingBaselineReadsEveryRecord(t *testing.T) {
	model := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	held := &titlesModelStatus{Kind: titlesKindEnd, Placed: titlesPlaced{At: model, Readings: 10}}
	broker := &titlesPlacing{Kind: titlesKindEnd, titlesPlaced: titlesPlaced{At: model.Add(time.Hour), Readings: 20}}
	started := houseJob("movies-titles-place-1", titlesPlaceMode, JobStatus{Active: 1})
	started.Metadata.Annotations = map[string]string{titlesPlacingAnnotation: `{"at":"0001-01-01T00:00:00Z","readings":30}`}
	bare := houseJob("movies-titles-place-0", titlesPlaceMode, JobStatus{})

	runs := titlesPlaceBaseline(held, broker, []Job{started, bare})
	other := titlesPlaceBaseline(nil, &titlesPlacing{Kind: "main-titles"}, nil)

	if len(runs) != 3 || runs[0].Readings != 10 || runs[1].Readings != 20 || runs[2].Readings != 30 {
		t.Errorf("baseline = %+v, want the status's, the broker's, and the Job's", runs)
	}
	if len(other) != 0 {
		t.Errorf("baseline = %+v, want none for a result of another kind", other)
	}
}
