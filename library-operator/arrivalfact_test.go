package main

// arrivalfact_test.go proves the arrival fact: the gap of video files with
// no ledger entry, the ledger it writes beside a folder, the rows that
// follow the write, and what a volume that refuses the write records.

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const arrivalLibrary = "house/movies"

// seedArrivalGap seeds one movie title with one video file that carries no
// arrival, which is the shape of an arrival gap.
func seedArrivalGap(t *testing.T, catalog *Catalog, root, folder, file string) {
	t.Helper()
	writeFile(t, filepath.Join(root, folder, file), "video")
	id := "movie:path:" + folderKey(folder)
	seed := &walkResult{
		movies: []movieRow{{Id: id, Library: arrivalLibrary, Kind: libraryKindMovies, Path: folder, Title: folder}},
		files: []fileRow{{Path: filepath.Join(folder, file), Library: arrivalLibrary, Present: true,
			Type: fileTypeVideo, Items: []string{id}}},
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
}

// One integer column of one row, read straight off the database.
func readColumn(t *testing.T, agent *sqliteAgent, query string, args ...any) int64 {
	t.Helper()
	var value int64
	if err := agent.db.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func arrivedOf(t *testing.T, agent *sqliteAgent, path string) int64 {
	t.Helper()
	return readColumn(t, agent, `SELECT arrived FROM files WHERE library = ? AND path = ?`, arrivalLibrary, path)
}

func addedOf(t *testing.T, agent *sqliteAgent, table, id string) int64 {
	t.Helper()
	return readColumn(t, agent, `SELECT added FROM `+table+` WHERE library = ? AND id = ?`, arrivalLibrary, id)
}

func attemptResultOf(t *testing.T, agent *sqliteAgent, fact, item string) string {
	t.Helper()
	var result string
	err := agent.db.QueryRow(`SELECT result FROM attempts WHERE library = ? AND item = ? AND `+attemptFactColumn+` = ?`,
		arrivalLibrary, item, fact).Scan(&result)
	if err != nil {
		return ""
	}
	return result
}

// A video with no arrival is the gap. A video the ledger holds, a file that
// is not a video, and a video with an attempt inside the window are none of
// them.
func TestTheArrivalGapAgainstTheRealSchema(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seed := &walkResult{
		files: []fileRow{
			{Path: "A/a.mkv", Library: arrivalLibrary, Present: true, Type: fileTypeVideo},
			{Path: "B/b.mkv", Library: arrivalLibrary, Present: true, Type: fileTypeVideo, Arrived: ledgerTime.Unix()},
			{Path: "C/c.srt", Library: arrivalLibrary, Present: true, Type: fileTypeSubtitle},
			{Path: "D/d.mkv", Library: arrivalLibrary, Present: true, Type: fileTypeVideo},
		},
		attempts: []attemptRow{{Library: arrivalLibrary, Item: "D/d.mkv", Fact: factArrival,
			At: ledgerTime.Unix(), Result: attemptError}},
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), catalog)

	gaps, err := work.gaps(t.Context(), factArrival, ledgerTime.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 1 || gaps[0] != "A/a.mkv" {
		t.Errorf("gaps = %v, want the one video with no arrival and no attempt", gaps)
	}
	counts, err := catalog.gapCounts(t.Context(), arrivalLibrary, ledgerTime.Add(2*errorRetryInterval))
	if err != nil {
		t.Fatal(err)
	}
	if counts[factArrival] != 2 {
		t.Errorf("arrival gap = %d, want the two videos once the error window has passed", counts[factArrival])
	}
}

func TestTheArrivalFactStampsEveryVideoOfAFolder(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	root := t.TempDir()
	seedArrivalGap(t, catalog, root, "One (2001)", "One (2001).mkv")
	seedArrivalGap(t, catalog, root, "Two (2002)", "Two (2002).mkv")
	work, log := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.arrivalFact(t.Context()); err != nil {
		t.Fatal(err)
	}

	for _, folder := range []string{"One (2001)", "Two (2002)"} {
		video := folder + ".mkv"
		changed := changeTimeOf(t, filepath.Join(root, folder, video))
		ledger := string(arrivalFile(t, filepath.Join(root, folder)))
		want := "at: " + time.Unix(changed, 0).UTC().Format(time.RFC3339)
		if !strings.Contains(ledger, "path: "+video) || !strings.Contains(ledger, want) {
			t.Errorf("%s arrival.yaml = %q, want the video's path and %q", folder, ledger, want)
		}
		path := filepath.Join(folder, video)
		if got := arrivedOf(t, agent, path); got != changed {
			t.Errorf("%s arrived = %d, want the change time %d", path, got, changed)
		}
		if got := addedOf(t, agent, "movies", "movie:path:"+folderKey(folder)); got != changed {
			t.Errorf("%s added = %d, want the change time %d", folder, got, changed)
		}
		if got := attemptResultOf(t, agent, factArrival, path); got != attemptFound {
			t.Errorf("%s attempt = %q, want %q", path, got, attemptFound)
		}
	}
	if !strings.Contains(log.String(), "stamped 2 of the 2 files") {
		t.Errorf("log = %q, want the count of files stamped", log.String())
	}
}

func TestASecondArrivalRunRewritesNothing(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	root := t.TempDir()
	seedArrivalGap(t, catalog, root, "One (2001)", "One (2001).mkv")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	if err := work.arrivalFact(t.Context()); err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(root, "One (2001)", likenDirectory, arrivalLedgerName)
	first := arrivalFile(t, filepath.Join(root, "One (2001)"))
	before, err := os.Stat(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := work.arrivalFact(t.Context()); err != nil {
		t.Fatal(err)
	}

	if second := arrivalFile(t, filepath.Join(root, "One (2001)")); !bytes.Equal(first, second) {
		t.Errorf("arrival.yaml changed on a second run:\n%s\nwas:\n%s", second, first)
	}
	after, err := os.Stat(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("the second run wrote arrival.yaml again, want the file left alone")
	}
}

func TestTheArrivalFactKeepsAnEntryThatExists(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	root := t.TempDir()
	seedArrivalGap(t, catalog, root, "One (2001)", "One (2001).mkv")
	held := "files:\n  - path: One (2001).mkv\n    at: 2001-02-03T04:05:06Z\n"
	writeFile(t, filepath.Join(root, "One (2001)", likenDirectory, arrivalLedgerName), held)
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.arrivalFact(t.Context()); err != nil {
		t.Fatal(err)
	}

	want := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC).Unix()
	ledger, err := readLikenLedger(filepath.Join(root, "One (2001)"), factArrival)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Files) != 1 || ledger.Files[0].At.Unix() != want {
		t.Errorf("entries = %+v, want the held entry as it was", ledger.Files)
	}
	if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != attemptFound {
		t.Errorf("attempts = %+v, want one found", ledger.Attempts)
	}
	if got := arrivedOf(t, agent, "One (2001)/One (2001).mkv"); got != want {
		t.Errorf("arrived = %d, want the held entry's %d", got, want)
	}
	if got := addedOf(t, agent, "movies", "movie:path:one-2001"); got != want {
		t.Errorf("added = %d, want the held entry's %d", got, want)
	}
}

func TestTheArrivalFactStampsASeasonFolderAndTheSeriesFollows(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	root, series := seriesWithASeason(t)
	seasonVideo := "A Show (2005)/Season 01/A Show S01E01.mkv"
	looseVideo := "A Show (2005)/A Show S02E01.mkv"
	seed := &walkResult{
		series: []seriesRow{{Id: "series:path:a-show-2005", Library: arrivalLibrary, Kind: libraryKindSeries,
			Path: "A Show (2005)", Title: "A Show"}},
		episodes: []episodeRow{
			{Id: "episode:path:a-show-2005:s01e01", Library: arrivalLibrary, Kind: libraryKindSeries,
				Path: seasonVideo, Series: "series:path:a-show-2005", Season: 1, Episode: 1},
			{Id: "episode:path:a-show-2005:s02e01", Library: arrivalLibrary, Kind: libraryKindSeries,
				Path: looseVideo, Series: "series:path:a-show-2005", Season: 2, Episode: 1},
		},
		files: []fileRow{
			{Path: seasonVideo, Library: arrivalLibrary, Present: true, Type: fileTypeVideo},
			{Path: looseVideo, Library: arrivalLibrary, Present: true, Type: fileTypeVideo},
		},
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
	work, _ := testEnricher(t, libraryKindSeries, root, catalog)

	if err := work.arrivalFact(t.Context()); err != nil {
		t.Fatal(err)
	}

	first := changeTimeOf(t, filepath.Join(series, "Season 01", "A Show S01E01.mkv"))
	if got := arrivedOf(t, agent, seasonVideo); got != first {
		t.Errorf("season video arrived = %d, want the change time %d", got, first)
	}
	if got := addedOf(t, agent, "episodes", "episode:path:a-show-2005:s01e01"); got != first {
		t.Errorf("episode added = %d, want the change time %d", got, first)
	}
	if got := addedOf(t, agent, "series", "series:path:a-show-2005"); got == 0 {
		t.Error("series added = 0, want the earliest episode's arrival")
	}
	if ledger := string(arrivalFile(t, filepath.Join(series, "Season 01"))); !strings.Contains(ledger, "path: A Show S01E01.mkv") {
		t.Errorf("season arrival.yaml = %q, want the episode", ledger)
	}
	if ledger := string(arrivalFile(t, series)); !strings.Contains(ledger, "path: A Show S02E01.mkv") {
		t.Errorf("series arrival.yaml = %q, want the loose episode", ledger)
	}
}

func TestAVolumeThatRefusesTheArrivalLedgerRecordsAnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	catalog, agent := newSQLiteCatalog(t)
	root := t.TempDir()
	seedArrivalGap(t, catalog, root, "One (2001)", "One (2001).mkv")
	seedArrivalGap(t, catalog, root, "Two (2002)", "Two (2002).mkv")
	sealed := filepath.Join(root, "One (2001)")
	if err := os.Chmod(sealed, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })
	work, log := testEnricher(t, libraryKindMovies, root, catalog)

	if err := work.arrivalFact(t.Context()); err != nil {
		t.Fatal(err)
	}

	if arrivalFile(t, sealed) != nil {
		t.Error("One (2001) holds an arrival.yaml the volume should have refused")
	}
	if got := attemptResultOf(t, agent, factArrival, "One (2001)/One (2001).mkv"); got != attemptError {
		t.Errorf("attempt = %q, want %q", got, attemptError)
	}
	if got := arrivedOf(t, agent, "One (2001)/One (2001).mkv"); got != 0 {
		t.Errorf("arrived = %d, want 0 where nothing was written", got)
	}
	if got := attemptResultOf(t, agent, factArrival, "Two (2002)/Two (2002).mkv"); got != attemptFound {
		t.Errorf("the run stopped at the refused folder: Two (2002) attempt = %q, want %q", got, attemptFound)
	}
	if !strings.Contains(log.String(), "could not write the arrival ledger") {
		t.Errorf("log = %q, want the refused write", log.String())
	}
}

func TestTheArrivalFactWorksOverTheFolderItsJobNames(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	root := t.TempDir()
	seedArrivalGap(t, catalog, root, "One (2001)", "One (2001).mkv")
	seedArrivalGap(t, catalog, root, "Two (2002)", "Two (2002).mkv")
	work, _ := testEnricher(t, libraryKindMovies, root, catalog)
	work.scope = "Two (2002)"

	if err := work.arrivalFact(t.Context()); err != nil {
		t.Fatal(err)
	}

	if arrivalFile(t, filepath.Join(root, "One (2001)")) != nil {
		t.Error("the fact wrote outside the folder its Job named")
	}
	if got := arrivedOf(t, agent, "Two (2002)/Two (2002).mkv"); got == 0 {
		t.Error("the fact left the folder its Job named unstamped")
	}
}

func TestTheArrivalFactFailsWhereItCannotReadItsGap(t *testing.T) {
	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(),
		NewCatalog("http://127.0.0.1:1", &http.Client{Timeout: time.Second}))

	if err := work.arrivalFact(t.Context()); err == nil {
		t.Error("the fact reported no error, want the unreachable catalog's")
	}
}
