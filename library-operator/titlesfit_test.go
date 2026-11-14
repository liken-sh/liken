package main

import (
	"testing"
)

// The count takes a video with a reading and a provider's mark of the start
// of its end titles. A film is one group and the episodes of a series are
// one group. A video with no reading, a mark of another kind, a mark with no
// start, and the rows the estimate and this fact derive are no labels.
func TestTheMarkedCountAgainstTheRealSchema(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	video := func(path string, items ...string) fileRow {
		return fileRow{Path: path, Library: titlesLibrary, Present: true, Type: fileTypeVideo,
			Role: fileRolePrimary, DurationMs: 6000000, VideoCodec: "h264", Items: wholeItems(items...)}
	}
	read := func(path string) attemptRow {
		return attemptRow{Library: titlesLibrary, Item: path, Fact: factTitles, At: ledgerTime.Unix(), Result: attemptFound}
	}
	start := int64(5400000)
	mark := func(path, kind, source string, start *int64) markRow {
		return markRow{Library: titlesLibrary, Path: path, Kind: kind, Start: start, Source: source}
	}
	first, second := episodeID("series:tvdb:9", 1, 1), episodeID("series:tvdb:9", 1, 2)
	seed := &walkResult{
		files: []fileRow{video("A/a.mkv", "movie:tmdb:1"), video("B/b.mkv", "movie:tmdb:2"),
			video("C/c.mkv", "movie:tmdb:3"), video("D/d.mkv", "movie:tmdb:4"), video("E/e.mkv", "movie:tmdb:5"),
			video("S/Season 01/s01e01.mkv", first), video("S/Season 01/s01e02.mkv", second)},
		episodes: []episodeRow{
			{Id: first, Library: titlesLibrary, Kind: libraryKindSeries, Path: "S/Season 01/s01e01.mkv",
				Series: "series:tvdb:9", Season: 1, Episode: 1},
			{Id: second, Library: titlesLibrary, Kind: libraryKindSeries, Path: "S/Season 01/s01e02.mkv",
				Series: "series:tvdb:9", Season: 1, Episode: 2}},
		attempts: []attemptRow{read("A/a.mkv"), read("C/c.mkv"), read("D/d.mkv"), read("E/e.mkv"),
			read("S/Season 01/s01e01.mkv"), read("S/Season 01/s01e02.mkv")},
		marks: []markRow{
			mark("A/a.mkv", markKindCredits, "theintrodb", &start),
			// A mark and no reading.
			mark("B/b.mkv", markKindCredits, "theintrodb", &start),
			// A reading and a mark of the intro alone.
			mark("C/c.mkv", markKindIntro, "theintrodb", &start),
			// A reading and a mark with no start.
			mark("D/d.mkv", markKindCredits, "introdb", nil),
			// A reading and only the rows derived from other clues.
			mark("E/e.mkv", markKindCredits, markSourceEstimate, &start),
			mark("S/Season 01/s01e01.mkv", markKindCredits, "introdb", &start),
			mark("S/Season 01/s01e02.mkv", markKindCredits, "introdb", &start),
		},
	}
	for at := range seed.marks {
		seed.marks[at].Ordinal = 0
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}

	marked, err := catalog.titlesMarked(t.Context(), titlesLibrary)
	if err != nil {
		t.Fatal(err)
	}

	if want := (titlesMarked{Videos: 3, Groups: 2}); marked != want {
		t.Errorf("marked = %+v, want %+v: the film and the two episodes of one series", marked, want)
	}
}

func TestWhenALibraryFitsAModel(t *testing.T) {
	cases := []struct {
		name   string
		marked titlesMarked
		fitted int
		due    bool
	}{
		{name: "too few videos", marked: titlesMarked{Videos: 99, Groups: 60}},
		{name: "one long series", marked: titlesMarked{Videos: 400, Groups: 1}},
		{name: "the first fit", marked: titlesMarked{Videos: 100, Groups: 30}, due: true},
		{name: "a model of nearly as many", marked: titlesMarked{Videos: 149, Groups: 40}, fitted: 100},
		{name: "half as many again", marked: titlesMarked{Videos: 150, Groups: 40}, fitted: 100, due: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if due := titlesFitDue(test.marked, test.fitted); due != test.due {
				t.Errorf("due = %v, want %v", due, test.due)
			}
		})
	}
}

// A report wakes a pass when the count passes the floor of a first fit and
// each time it has grown by a quarter, and not for each video between.
func TestTheMarkedCountWakesAPassAtEachStep(t *testing.T) {
	cases := []struct {
		name          string
		before, after titlesMarked
		acts          bool
	}{
		{name: "under the floor", before: titlesMarked{Videos: 40, Groups: 12}, after: titlesMarked{Videos: 41, Groups: 12}},
		{name: "over the floor", before: titlesMarked{Videos: 99, Groups: 30}, after: titlesMarked{Videos: 100, Groups: 30},
			acts: true},
		{name: "one more video", before: titlesMarked{Videos: 100, Groups: 30}, after: titlesMarked{Videos: 101, Groups: 30}},
		{name: "a quarter more", before: titlesMarked{Videos: 124, Groups: 30}, after: titlesMarked{Videos: 126, Groups: 30},
			acts: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			acts := actsOn(libraryReport{TitlesMarked: test.before}, libraryReport{TitlesMarked: test.after})
			if acts != test.acts {
				t.Errorf("acts = %v, want %v", acts, test.acts)
			}
		})
	}
}
