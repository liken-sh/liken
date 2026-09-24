package main

// What these tests read: how long a marks attempt holds a file out of the
// gap. A work the community is still marking is asked again sooner than one
// whose marks have settled, and an error holds for a day whatever the work's
// age.

import (
	"slices"
	"testing"
	"time"
)

// The file every case seeds, an episode of an identified series.
const marksGapPath = "Severance (2022)/Season 01/Severance - S01E02.mkv"

// One identified series with one episode per release date the test names,
// every episode on the one file, and the file's last marks attempt. An
// empty release date is an episode the catalog holds no date for.
func seedMarksAttempt(t *testing.T, catalog *Catalog, releases []string, result string, at time.Time) {
	t.Helper()
	series := "series:tvdb:371980"
	seed := &walkResult{
		series: []seriesRow{{Id: series, Library: marksLibrary, Kind: libraryKindSeries,
			Path: "Severance (2022)", Title: "Severance"}},
		attempts: []attemptRow{{Library: marksLibrary, Item: marksGapPath, Fact: factMarks,
			Result: result, At: at.Unix()}},
	}
	file := fileRow{Library: marksLibrary, Path: marksGapPath, Type: fileTypeVideo,
		Role: fileRolePrimary, Present: true, DurationMs: 3300000}
	for at, released := range releases {
		id := episodeID(series, 1, 2+at)
		seed.episodes = append(seed.episodes, episodeRow{Id: id, Library: marksLibrary,
			Kind: libraryKindSeries, Path: marksGapPath, Series: series, Season: 1, Episode: 2 + at,
			Released: released})
		file.Items = append(file.Items, id)
	}
	seed.files = []fileRow{file}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
}

// The window a marks attempt holds for follows the release date of the work
// in the file: a day for the first week, a week to the ninetieth day, and
// thirty days after that or without a date. An error holds for a day in
// every case. A file of two episodes reads the later of their dates.
func TestAMarksAttemptHoldsForAWindowThatFollowsTheRelease(t *testing.T) {
	now := time.Now().UTC()
	day := 24 * time.Hour
	released := func(ago time.Duration) string { return now.Add(-ago).Format(time.DateOnly) }
	cases := []struct {
		name     string
		releases []string
		result   string
		ago      time.Duration
		gap      bool
	}{
		{name: "released 3 days ago, attempted 2 days ago", releases: []string{released(3 * day)},
			result: attemptNothing, ago: 2 * day, gap: true},
		{name: "released 3 days ago, attempted 12 hours ago", releases: []string{released(3 * day)},
			result: attemptNothing, ago: 12 * time.Hour},
		{name: "released 3 days ago, found 2 days ago", releases: []string{released(3 * day)},
			result: attemptFound, ago: 2 * day, gap: true},
		{name: "released 30 days ago, attempted 8 days ago", releases: []string{released(30 * day)},
			result: attemptNothing, ago: 8 * day, gap: true},
		{name: "released 30 days ago, attempted 6 days ago", releases: []string{released(30 * day)},
			result: attemptNothing, ago: 6 * day},
		{name: "released 2 years ago, attempted 8 days ago", releases: []string{released(730 * day)},
			result: attemptNothing, ago: 8 * day},
		{name: "released 2 years ago, attempted 31 days ago", releases: []string{released(730 * day)},
			result: attemptFound, ago: 31 * day, gap: true},
		{name: "no release date, attempted 8 days ago", releases: []string{""},
			result: attemptNothing, ago: 8 * day},
		{name: "no release date, attempted 31 days ago", releases: []string{""},
			result: attemptNothing, ago: 31 * day, gap: true},
		{name: "two episodes read the later release", releases: []string{released(730 * day), released(3 * day)},
			result: attemptNothing, ago: 2 * day, gap: true},
		{name: "an error on a new episode, 12 hours ago", releases: []string{released(3 * day)},
			result: attemptError, ago: 12 * time.Hour},
		{name: "an error on a new episode, 2 days ago", releases: []string{released(3 * day)},
			result: attemptError, ago: 2 * day, gap: true},
		{name: "an error on an episode of last month, 12 hours ago", releases: []string{released(30 * day)},
			result: attemptError, ago: 12 * time.Hour},
		{name: "an error on an episode of last month, 2 days ago", releases: []string{released(30 * day)},
			result: attemptError, ago: 2 * day, gap: true},
		{name: "an error on an old episode, 12 hours ago", releases: []string{released(730 * day)},
			result: attemptError, ago: 12 * time.Hour},
		{name: "an error on an old episode, 2 days ago", releases: []string{released(730 * day)},
			result: attemptError, ago: 2 * day, gap: true},
		{name: "an error with no release date, 12 hours ago", releases: []string{""},
			result: attemptError, ago: 12 * time.Hour},
		{name: "an error with no release date, 2 days ago", releases: []string{""},
			result: attemptError, ago: 2 * day, gap: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			seedMarksAttempt(t, catalog, test.releases, test.result, now.Add(-test.ago))

			paths, err := catalog.queryStrings(t.Context(), gapQueries[factMarks],
				gapParams(factMarks, marksLibrary, now, time.Time{}))

			if err != nil {
				t.Fatal(err)
			}
			if got := slices.Contains(paths, marksGapPath); got != test.gap {
				t.Errorf("the gap holds %v, want the file in it: %v", paths, test.gap)
			}
		})
	}
}

// The reporter counts the marks gap with the same query and the same
// parameters, so the count and the container's work list agree.
func TestTheReporterCountsTheMarksGapWithItsWindows(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	now := time.Now().UTC()
	seedMarksAttempt(t, catalog, []string{now.Add(-72 * time.Hour).Format(time.DateOnly)},
		attemptNothing, now.Add(-48*time.Hour))

	counts, err := catalog.gapCounts(t.Context(), marksLibrary, now)

	if err != nil {
		t.Fatal(err)
	}
	if counts[factMarks] != 1 {
		t.Errorf("the marks gap counts %d, want the one new episode", counts[factMarks])
	}
}
