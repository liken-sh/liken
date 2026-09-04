package main

// arrival_test.go proves the walk's read of the arrival ledger beside a
// title: the added column it feeds, the arrived column it writes from the
// ledger alone, the change time it falls back on, and that it writes
// nothing.

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// The reader's context over a movies root.
func movieScan(root string) folderScan {
	return folderScan{root: root, library: "house/movies", kind: libraryKindMovies}
}

// The change time read the way the test proves it, apart from the code under
// test.
func changeTimeOf(t *testing.T, path string) int64 {
	t.Helper()
	var stat syscall.Stat_t
	if err := syscall.Stat(path, &stat); err != nil {
		t.Fatal(err)
	}
	return stat.Ctim.Sec
}

// The ledger file's bytes, or nothing where the file is not there.
func arrivalFile(t *testing.T, folder string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(folder, likenDirectory, arrivalLedgerName))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return data
}

// The one file row of a walk at a given path.
func fileRowAt(t *testing.T, result *walkResult, path string) fileRow {
	t.Helper()
	for _, row := range result.files {
		if row.Path == path {
			return row
		}
	}
	t.Fatalf("no file row at %s in %+v", path, result.files)
	return fileRow{}
}

func TestAMovieWithNoLedgerIsAddedAtItsChangeTime(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "The Matrix (1999)")
	writeFile(t, filepath.Join(folder, "The Matrix (1999).mkv"), "video")

	result := &walkResult{}
	scanMovieFolder(movieScan(root), folder, result)

	changed := changeTimeOf(t, filepath.Join(folder, "The Matrix (1999).mkv"))
	if result.movies[0].Added != changed {
		t.Errorf("added = %d, want the change time %d", result.movies[0].Added, changed)
	}
	if got := fileRowAt(t, result, "The Matrix (1999)/The Matrix (1999).mkv").Arrived; got != 0 {
		t.Errorf("arrived = %d, want 0 where the ledger holds no entry", got)
	}
	if arrivalFile(t, folder) != nil {
		t.Error("the walk wrote arrival.yaml, want the volume left alone")
	}
}

func TestAnArrivalEntryKeepsItsTimeWhenTheChangeTimeMoves(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "The Matrix (1999)")
	writeFile(t, filepath.Join(folder, "The Matrix (1999).mkv"), "video")
	held := "files:\n  - path: The Matrix (1999).mkv\n    at: 2001-02-03T04:05:06Z\n"
	writeFile(t, filepath.Join(folder, likenDirectory, arrivalLedgerName), held)

	result := &walkResult{}
	scanMovieFolder(movieScan(root), folder, result)

	want := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC).Unix()
	if result.movies[0].Added != want {
		t.Errorf("added = %d, want the ledger's %d", result.movies[0].Added, want)
	}
	if got := fileRowAt(t, result, "The Matrix (1999)/The Matrix (1999).mkv").Arrived; got != want {
		t.Errorf("arrived = %d, want the ledger's %d", got, want)
	}
	if got := string(arrivalFile(t, folder)); got != held {
		t.Errorf("arrival.yaml = %q, want it left as %q", got, held)
	}
}

func TestAMovieArrivesWithItsMainFile(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "Long Film (1999)")
	writeFile(t, filepath.Join(folder, "Long Film (1999) - part1.mkv"), "video")
	writeFile(t, filepath.Join(folder, "Long Film (1999) - part2.mkv"), "video")
	writeFile(t, filepath.Join(folder, likenDirectory, arrivalLedgerName),
		"files:\n  - path: Long Film (1999) - part1.mkv\n    at: 2001-01-01T00:00:00Z\n"+
			"  - path: Long Film (1999) - part2.mkv\n    at: 2000-01-01T00:00:00Z\n")

	result := &walkResult{}
	scanMovieFolder(movieScan(root), folder, result)

	want := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	if result.movies[0].Added != want {
		t.Errorf("added = %d, want the first video's arrival %d", result.movies[0].Added, want)
	}
}

// A series root with one season folder and one loose video, the two folders
// that hold an episode's ledger.
func seriesWithASeason(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	series := filepath.Join(root, "A Show (2005)")
	writeFile(t, filepath.Join(series, "tvshow.nfo"), "<tvshow><title>A Show</title></tvshow>")
	writeFile(t, filepath.Join(series, "Season 01", "A Show S01E01.mkv"), "video")
	writeFile(t, filepath.Join(series, "Season 01", "A Show S01E02.mkv"), "video")
	writeFile(t, filepath.Join(series, "A Show S02E01.mkv"), "video")
	return root, series
}

func TestTheWalkReadsAnArrivalLedgerInASeasonFolder(t *testing.T) {
	root, series := seriesWithASeason(t)
	season := filepath.Join(series, "Season 01")
	held := "files:\n  - path: A Show S01E01.mkv\n    at: 2001-02-03T04:05:06Z\n"
	writeFile(t, filepath.Join(season, likenDirectory, arrivalLedgerName), held)

	result := &walkResult{}
	scanSeriesFolder(folderScan{root: root, library: "house/series", kind: libraryKindSeries}, series, result)

	first := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC).Unix()
	episodes := episodesByID(result)
	cases := []struct {
		id      string
		path    string
		want    int64
		arrived int64
	}{
		{"episode:path:a-show-2005:s01e01", "A Show (2005)/Season 01/A Show S01E01.mkv", first, first},
		{"episode:path:a-show-2005:s01e02", "A Show (2005)/Season 01/A Show S01E02.mkv",
			changeTimeOf(t, filepath.Join(season, "A Show S01E02.mkv")), 0},
		{"episode:path:a-show-2005:s02e01", "A Show (2005)/A Show S02E01.mkv",
			changeTimeOf(t, filepath.Join(series, "A Show S02E01.mkv")), 0},
	}
	for _, testCase := range cases {
		if got := episodes[testCase.id].Added; got != testCase.want {
			t.Errorf("%s added = %d, want %d", testCase.id, got, testCase.want)
		}
		if got := fileRowAt(t, result, testCase.path).Arrived; got != testCase.arrived {
			t.Errorf("%s arrived = %d, want %d", testCase.path, got, testCase.arrived)
		}
	}
	if result.series[0].Added != first {
		t.Errorf("series added = %d, want the earliest episode's %d", result.series[0].Added, first)
	}
	if got := string(arrivalFile(t, season)); got != held {
		t.Errorf("season arrival.yaml = %q, want it left as %q", got, held)
	}
	if arrivalFile(t, series) != nil {
		t.Error("the walk wrote arrival.yaml beside the loose episode, want the volume left alone")
	}
}

func TestASeriesWithNoEpisodesHasNoArrival(t *testing.T) {
	root := t.TempDir()
	series := filepath.Join(root, "A Show (2005)")
	writeFile(t, filepath.Join(series, "tvshow.nfo"), "<tvshow><title>A Show</title></tvshow>")

	result := &walkResult{}
	scanSeriesFolder(folderScan{root: root, library: "house/series", kind: libraryKindSeries}, series, result)

	if result.series[0].Added != 0 {
		t.Errorf("added = %d, want 0 for a series with no episode", result.series[0].Added)
	}
}

func TestAnUnreadableArrivalLedgerMarksThePassIncomplete(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "The Matrix (1999)")
	writeFile(t, filepath.Join(folder, "The Matrix (1999).mkv"), "video")
	writeFile(t, filepath.Join(folder, likenDirectory, arrivalLedgerName), "files: [not: valid")

	result := &walkResult{}
	scanMovieFolder(movieScan(root), folder, result)

	if !result.readError {
		t.Error("a ledger the walk cannot read left the pass complete, want the incomplete mark")
	}
	changed := changeTimeOf(t, filepath.Join(folder, "The Matrix (1999).mkv"))
	if result.movies[0].Added != changed {
		t.Errorf("added = %d, want the change time %d", result.movies[0].Added, changed)
	}
	if got := string(arrivalFile(t, folder)); got != "files: [not: valid" {
		t.Errorf("arrival.yaml = %q, want the file left as it was", got)
	}
}

func TestAnArrivalEntryAloneIsNoAttempt(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "The Matrix (1999)")
	writeFile(t, filepath.Join(folder, "The Matrix (1999).mkv"), "video")
	writeFile(t, filepath.Join(folder, likenDirectory, arrivalLedgerName),
		"files:\n  - path: The Matrix (1999).mkv\n    at: 2001-02-03T04:05:06Z\n")

	result := &walkResult{}
	scanMovieFolder(movieScan(root), folder, result)

	if result.readError {
		t.Error("the walk marked the pass incomplete over arrival.yaml")
	}
	if len(result.attempts) != 0 {
		t.Errorf("attempts = %+v, want none from an entry with no attempt", result.attempts)
	}
}

func TestTheWalkLiftsTheArrivalFactsAttempts(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "The Matrix (1999)")
	writeFile(t, filepath.Join(folder, "The Matrix (1999).mkv"), "video")
	writeFile(t, filepath.Join(folder, likenDirectory, arrivalLedgerName),
		"files:\n  - path: The Matrix (1999).mkv\n    at: 2001-02-03T04:05:06Z\n"+
			"attempts:\n  - path: The Matrix (1999).mkv\n    at: 2001-02-03T04:05:06Z\n    result: found\n")

	result := &walkResult{}
	scanMovieFolder(movieScan(root), folder, result)

	want := attemptRow{Library: "house/movies", Item: "The Matrix (1999)/The Matrix (1999).mkv",
		Fact: factArrival, At: time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC).Unix(), Result: attemptFound}
	if len(result.attempts) != 1 || result.attempts[0] != want {
		t.Errorf("attempts = %+v, want %+v", result.attempts, want)
	}
}

func TestAFullWalkWritesNoArrivalLedger(t *testing.T) {
	root := titleTree(t, "One (2001)", "Two (2002)")
	scan, _ := fakeScanner(t, root, libraryKindMovies)

	if err := scan.fullWalk(context.Background()); err != nil {
		t.Fatal(err)
	}

	for _, folder := range []string{"One (2001)", "Two (2002)"} {
		if arrivalFile(t, filepath.Join(root, folder)) != nil {
			t.Errorf("the full walk wrote arrival.yaml beside %s", folder)
		}
	}
}
