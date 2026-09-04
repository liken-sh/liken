package main

// arrival_test.go proves the arrival ledger the walk keeps beside a title,
// the added column it feeds, and the pass over a volume that refuses the
// write.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A recorder over the test's own log, so a test reads the one line a refused
// write leaves.
func ledgerRecorder(t *testing.T) (*arrivalRecorder, *bytes.Buffer) {
	t.Helper()
	log := &bytes.Buffer{}
	logf := func(format string, args ...any) {
		fmt.Fprintf(log, format+"\n", args...)
	}
	return newArrivalRecorder(newVolumeWriter("scan-1"), logf), log
}

// The reader's context over a movies root that keeps the ledger.
func movieScan(root string, arrivals *arrivalRecorder) folderScan {
	return folderScan{root: root, library: "house/movies", kind: libraryKindMovies, arrivals: arrivals}
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

func TestTheWalkWritesAnArrivalLedgerBesideAMovie(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "The Matrix (1999)")
	writeFile(t, filepath.Join(folder, "The Matrix (1999).mkv"), "video")
	recorder, log := ledgerRecorder(t)

	result := &walkResult{}
	scanMovieFolder(movieScan(root, recorder), folder, result)

	changed := changeTimeOf(t, filepath.Join(folder, "The Matrix (1999).mkv"))
	if result.movies[0].Added != changed {
		t.Errorf("added = %d, want the change time %d", result.movies[0].Added, changed)
	}
	ledger := string(arrivalFile(t, folder))
	want := "at: " + time.Unix(changed, 0).UTC().Format(time.RFC3339)
	if !strings.Contains(ledger, "path: The Matrix (1999).mkv") || !strings.Contains(ledger, want) {
		t.Errorf("arrival.yaml = %q, want the file's path and %q", ledger, want)
	}
	if log.Len() != 0 {
		t.Errorf("log = %q, want nothing from a write that landed", log.String())
	}
}

func TestASecondWalkLeavesTheArrivalLedgerAsItIs(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "The Matrix (1999)")
	writeFile(t, filepath.Join(folder, "The Matrix (1999).mkv"), "video")
	recorder, _ := ledgerRecorder(t)

	scanMovieFolder(movieScan(root, recorder), folder, &walkResult{})
	first := arrivalFile(t, folder)
	info, err := os.Stat(filepath.Join(folder, likenDirectory, arrivalLedgerName))
	if err != nil {
		t.Fatal(err)
	}
	scanMovieFolder(movieScan(root, recorder), folder, &walkResult{})

	if second := arrivalFile(t, folder); !bytes.Equal(first, second) {
		t.Errorf("arrival.yaml changed on a re-walk:\n%s\nwas:\n%s", second, first)
	}
	again, err := os.Stat(filepath.Join(folder, likenDirectory, arrivalLedgerName))
	if err != nil {
		t.Fatal(err)
	}
	if !again.ModTime().Equal(info.ModTime()) {
		t.Error("the re-walk wrote arrival.yaml again, want the file left alone")
	}
}

func TestAnArrivalEntryKeepsItsTimeWhenTheChangeTimeMoves(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "The Matrix (1999)")
	writeFile(t, filepath.Join(folder, "The Matrix (1999).mkv"), "video")
	held := "files:\n  - path: The Matrix (1999).mkv\n    at: 2001-02-03T04:05:06Z\n"
	writeFile(t, filepath.Join(folder, likenDirectory, arrivalLedgerName), held)
	recorder, _ := ledgerRecorder(t)

	result := &walkResult{}
	scanMovieFolder(movieScan(root, recorder), folder, result)

	want := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC).Unix()
	if result.movies[0].Added != want {
		t.Errorf("added = %d, want the ledger's %d", result.movies[0].Added, want)
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
	scanMovieFolder(movieScan(root, nil), folder, result)

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

func TestTheWalkWritesAnArrivalLedgerInASeasonFolder(t *testing.T) {
	root, series := seriesWithASeason(t)
	season := filepath.Join(series, "Season 01")
	writeFile(t, filepath.Join(season, likenDirectory, arrivalLedgerName),
		"files:\n  - path: A Show S01E01.mkv\n    at: 2001-02-03T04:05:06Z\n")
	recorder, _ := ledgerRecorder(t)

	result := &walkResult{}
	scanSeriesFolder(folderScan{root: root, library: "house/series", kind: libraryKindSeries, arrivals: recorder}, series, result)

	first := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC).Unix()
	episodes := episodesByID(result)
	cases := []struct {
		id   string
		want int64
	}{
		{"episode:path:a-show-2005:s01e01", first},
		{"episode:path:a-show-2005:s01e02", changeTimeOf(t, filepath.Join(season, "A Show S01E02.mkv"))},
		{"episode:path:a-show-2005:s02e01", changeTimeOf(t, filepath.Join(series, "A Show S02E01.mkv"))},
	}
	for _, testCase := range cases {
		if got := episodes[testCase.id].Added; got != testCase.want {
			t.Errorf("%s added = %d, want %d", testCase.id, got, testCase.want)
		}
	}
	if result.series[0].Added != first {
		t.Errorf("series added = %d, want the earliest episode's %d", result.series[0].Added, first)
	}
	if ledger := string(arrivalFile(t, season)); !strings.Contains(ledger, "path: A Show S01E02.mkv") ||
		!strings.Contains(ledger, "at: 2001-02-03T04:05:06Z") {
		t.Errorf("season arrival.yaml = %q, want the held entry and the new one", ledger)
	}
	if ledger := string(arrivalFile(t, series)); !strings.Contains(ledger, "path: A Show S02E01.mkv") {
		t.Errorf("series arrival.yaml = %q, want the loose episode", ledger)
	}
}

func TestASeriesWithNoEpisodesHasNoArrival(t *testing.T) {
	root := t.TempDir()
	series := filepath.Join(root, "A Show (2005)")
	writeFile(t, filepath.Join(series, "tvshow.nfo"), "<tvshow><title>A Show</title></tvshow>")
	recorder, _ := ledgerRecorder(t)

	result := &walkResult{}
	scanSeriesFolder(folderScan{root: root, library: "house/series", kind: libraryKindSeries, arrivals: recorder}, series, result)

	if result.series[0].Added != 0 {
		t.Errorf("added = %d, want 0 for a series with no episode", result.series[0].Added)
	}
	if arrivalFile(t, series) != nil {
		t.Error("the walk wrote arrival.yaml for a folder with no video")
	}
}

func TestAReadOnlyVolumeStillCatalogsWithTheChangeTime(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	root := t.TempDir()
	folders := []string{"One (2001)", "Two (2002)"}
	for _, name := range folders {
		folder := filepath.Join(root, name)
		writeFile(t, filepath.Join(folder, name+".mkv"), "video")
		if err := os.Chmod(folder, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(folder, 0o755) })
	}
	recorder, log := ledgerRecorder(t)

	result := &walkResult{}
	for _, name := range folders {
		scanMovieFolder(movieScan(root, recorder), filepath.Join(root, name), result)
	}

	if result.readError {
		t.Error("a refused ledger write marked the pass incomplete, want the rows to stand")
	}
	for i, name := range folders {
		changed := changeTimeOf(t, filepath.Join(root, name, name+".mkv"))
		if result.movies[i].Added != changed {
			t.Errorf("%s added = %d, want the change time %d", name, result.movies[i].Added, changed)
		}
		if arrivalFile(t, filepath.Join(root, name)) != nil {
			t.Errorf("%s holds an arrival.yaml the volume should have refused", name)
		}
	}
	lines := strings.Count(log.String(), "\n")
	if lines != 1 || !strings.Contains(log.String(), "arrival") {
		t.Errorf("log = %q, want one line about the arrival ledger", log.String())
	}
}

func TestAnUnreadableArrivalLedgerMarksThePassIncomplete(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "The Matrix (1999)")
	writeFile(t, filepath.Join(folder, "The Matrix (1999).mkv"), "video")
	writeFile(t, filepath.Join(folder, likenDirectory, arrivalLedgerName), "files: [not: valid")
	recorder, _ := ledgerRecorder(t)

	result := &walkResult{}
	scanMovieFolder(movieScan(root, recorder), folder, result)

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

func TestTheArrivalLedgerIsNeverAnAttempt(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "The Matrix (1999)")
	writeFile(t, filepath.Join(folder, "The Matrix (1999).mkv"), "video")
	writeFile(t, filepath.Join(folder, likenDirectory, arrivalLedgerName),
		"files:\n  - path: The Matrix (1999).mkv\n    at: 2001-02-03T04:05:06Z\n")

	result := &walkResult{}
	scanMovieFolder(movieScan(root, nil), folder, result)

	if result.readError {
		t.Error("the walk marked the pass incomplete over arrival.yaml")
	}
	if len(result.attempts) != 0 {
		t.Errorf("attempts = %+v, want none from arrival.yaml", result.attempts)
	}
}

func TestAFullWalkAndARescanKeepTheArrivalLedger(t *testing.T) {
	root := titleTree(t, "One (2001)", "Two (2002)")
	scan, _ := fakeScanner(t, root, libraryKindMovies)
	scan.arrivals = newArrivalRecorder(newVolumeWriter("scan-1"), scan.logf)

	if err := scan.fullWalk(context.Background()); err != nil {
		t.Fatal(err)
	}
	if arrivalFile(t, filepath.Join(root, "One (2001)")) == nil {
		t.Error("the full walk wrote no arrival.yaml beside One (2001)")
	}

	writeFile(t, filepath.Join(root, "Three (2003)", "movie.mkv"), "video")
	if err := scan.rescan(context.Background(), filepath.Join(root, "Three (2003)")); err != nil {
		t.Fatal(err)
	}
	if arrivalFile(t, filepath.Join(root, "Three (2003)")) == nil {
		t.Error("the rescan wrote no arrival.yaml beside Three (2003)")
	}
}

func TestTheScannerKeepsTheLedgerUnderItsJobName(t *testing.T) {
	t.Setenv(busAddressVariable, "tcp://127.0.0.1:1")
	t.Setenv(jobNameVariable, "movies-scan-7")
	scan, err := newScanner(time.Now().UTC(), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if scan.arrivals == nil || scan.arrivals.writer == nil || scan.arrivals.writer.job != "movies-scan-7" {
		t.Errorf("arrivals = %+v, want a recorder that writes under the Job's name", scan.arrivals)
	}
}
