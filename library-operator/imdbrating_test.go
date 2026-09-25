package main

// what these tests read: the nfo container reads the IMDb datasets once for
// the whole rating.imdb gap, writes the rating as OMDb writes it, writes a
// .nfo file only when the one-decimal rating changed, records the time of
// the file it read, and rates episodes, finding an episode's IMDb id in
// title.episode where its .nfo file has none.

import (
	"bytes"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// When the fixture server says IMDb last replaced every file.
var datasetsModified = testNow.Add(-6 * time.Hour)

// An enricher of the given kind on a fresh catalog, whose sources name the
// imdb block on the dataset server.
func imdbEnricher(t *testing.T, kind string) (*enricher, *Catalog, *datasetServer, string) {
	t.Helper()
	work, catalog, _, server, root := imdbEnricherOnAgent(t, kind)
	return work, catalog, server, root
}

// The same enricher, with the catalog's agent, which a test tells to refuse
// a read or a write.
func imdbEnricherOnAgent(t *testing.T, kind string) (*enricher, *Catalog, *sqliteAgent, *datasetServer, string) {
	t.Helper()
	catalog, agent := newSQLiteCatalog(t)
	root := t.TempDir()
	server := newDatasetServer(t, datasetsModified)
	t.Setenv(librarySourcesVariable, providerBlockIMDb)
	t.Setenv(imdbEndpointVariable, server.URL)
	work, _ := testEnricher(t, kind, root, catalog)
	work.ratingScope = ratingGapScope{reopen: datasetsModified.Unix(), episodes: true}
	return work, catalog, agent, server, root
}

// The run of the fact the way the container runs it: the reads start, then
// the fact works through its gap.
func runIMDbRating(t *testing.T, work *enricher) {
	t.Helper()
	work.startDatasetReads(t.Context(), []string{factRatingIMDb})
	if err := work.nfoGap(t.Context(), factRatingIMDb, work.nfoAnswerLine()); err != nil {
		t.Fatal(err)
	}
}

// One movie with an IMDb id in its .nfo file and its alias in the catalog,
// the .nfo file's ratings block, which may be empty, and the nfo facts the
// catalog's row says the .nfo file answers.
func seedIMDbMovie(t *testing.T, catalog *Catalog, root, imdb, ratings, facts string) string {
	t.Helper()
	folder := "Winter Harbour (2011)"
	nfoPath := filepath.Join(root, folder, movieNFOName)
	writeFile(t, nfoPath, `<?xml version="1.0" encoding="utf-8"?>
<movie>
  <title>Winter Harbour</title>
  <uniqueid type="imdb">`+imdb+`</uniqueid>
`+ratings+`</movie>
`)
	id := "movie:imdb:" + imdb
	seed := &walkResult{
		movies: []movieRow{{Id: id, Library: "house/movies", Kind: libraryKindMovies,
			Path: folder, Title: "Winter Harbour", Released: "2011", NFOFacts: facts}},
		aliases: []aliasRow{{Alias: id, Library: "house/movies", Item: id}},
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
	return nfoPath
}

func TestTheDatasetsWriteAMovieRatingWithItsVotes(t *testing.T) {
	work, catalog, server, root := imdbEnricher(t, libraryKindMovies)
	nfoPath := seedIMDbMovie(t, catalog, root, "tt9000001", "", "")

	runIMDbRating(t, work)

	written := readFileString(t, nfoPath)
	if !strings.Contains(written, `<rating name="imdb" max="10">`) || !strings.Contains(written, "<value>7.9</value>") ||
		!strings.Contains(written, "<votes>5678</votes>") {
		t.Errorf(".nfo = %s, want the imdb rating with its votes", written)
	}
	if got := server.log(); !slices.Equal(got, []string{"GET title.ratings 200"}) {
		t.Errorf("requests = %v, want one read of title.ratings", got)
	}
	ledger, err := readLikenLedger(filepath.Dir(nfoPath), factRatingIMDb)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Attempts) != 1 || !ledger.Attempts[0].DatasetModified.Equal(datasetsModified) {
		t.Errorf("attempts = %+v, want one that records the time of title.ratings", ledger.Attempts)
	}
}

// The .nfo file changes only when the one-decimal rating does. A new vote
// count alone leaves every byte as it was.
func TestTheRatingIsWrittenOnlyWhenItChanges(t *testing.T) {
	cases := []struct {
		name    string
		held    string
		written bool
	}{
		{name: "the same rating with other votes", held: "7.9", written: false},
		{name: "a rating that moved", held: "7.8", written: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			work, catalog, _, root := imdbEnricher(t, libraryKindMovies)
			nfoPath := seedIMDbMovie(t, catalog, root, "tt9000001",
				`  <ratings>
    <rating name="imdb" max="10">
      <value>`+one.held+`</value>
      <votes>12</votes>
    </rating>
  </ratings>
`, "")
			before := readFileString(t, nfoPath)

			runIMDbRating(t, work)

			if changed := readFileString(t, nfoPath) != before; changed != one.written {
				t.Errorf("written = %v, want %v", changed, one.written)
			}
		})
	}
}

// A run whose gap holds no title sends no request.
func TestAnEmptyGapReadsNoFile(t *testing.T) {
	work, catalog, server, root := imdbEnricher(t, libraryKindMovies)
	seedIMDbMovie(t, catalog, root, "tt9000001",
		`  <ratings>
    <rating name="imdb" max="10"><value>7.9</value></rating>
  </ratings>
`, nfoFactList([]string{factRatingIMDb}))

	runIMDbRating(t, work)

	if got := server.log(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

// One series with the IMDb id the test names, and one episode file under it
// with an .nfo file that names no IMDb id.
func seedIMDbEpisode(t *testing.T, catalog *Catalog, root, seriesIMDb string) (string, string) {
	t.Helper()
	series := "series:imdb:" + seriesIMDb
	file := filepath.Join("Harbour Watch (2019)", "Season 01", "Harbour Watch - S01E02.mkv")
	writeFile(t, filepath.Join(root, file), "video")
	nfoPath := nfoBeside(filepath.Join(root, file))
	writeFile(t, nfoPath, `<?xml version="1.0" encoding="utf-8"?>
<episodedetails>
  <title>The Second Tide</title>
  <season>1</season>
  <episode>2</episode>
</episodedetails>
`)
	id := episodeID(series, 1, 2)
	seed := &walkResult{
		series: []seriesRow{{Id: series, Library: "house/movies", Kind: libraryKindSeries,
			Path: "Harbour Watch (2019)", Title: "Harbour Watch", NFOFacts: nfoFactList([]string{factRatingIMDb})}},
		episodes: []episodeRow{{Id: id, Library: "house/movies", Kind: libraryKindSeries, Path: file,
			Title: "The Second Tide", Series: series, Season: 1, Episode: 2}},
		aliases: []aliasRow{{Alias: series, Library: "house/movies", Item: series}},
	}
	if err := upsertWalk(t.Context(), catalog, seed); err != nil {
		t.Fatal(err)
	}
	return nfoPath, id
}

func TestAnEpisodeTakesItsIDFromTitleEpisodeAndItsRating(t *testing.T) {
	work, catalog, server, root := imdbEnricher(t, libraryKindSeries)
	nfoPath, id := seedIMDbEpisode(t, catalog, root, "tt9000003")

	runIMDbRating(t, work)

	if written := readFileString(t, nfoPath); !strings.Contains(written, "<value>8.3</value>") {
		t.Errorf(".nfo = %s, want the episode's rating", written)
	}
	want := []string{"GET title.episode 200", "GET title.ratings 200"}
	if got := server.log(); !slices.Equal(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	ledger, err := readLikenLedger(filepath.Dir(nfoPath), factRatingIMDb)
	if err != nil {
		t.Fatal(err)
	}
	item, held := ledger.itemAt(filepath.Base(strings.TrimSuffix(nfoPath, ".nfo") + ".mkv"))
	if !held || item.ID[providerBlockIMDb] != "tt9000011" {
		t.Errorf("item = %+v, want the id title.episode gave", item)
	}
	facts := catalogLines(t, catalog, `SELECT nfo_facts FROM episodes WHERE library = ?`)
	if len(facts) != 1 || !strings.Contains(facts[0], factRatingIMDb) {
		t.Errorf("nfo_facts = %v, want the rating on the episode's row", facts)
	}
	attempts := catalogLines(t, catalog,
		`SELECT item || ' ' || result FROM attempts WHERE library = ? AND concern = 'rating.imdb'`)
	if !slices.Equal(attempts, []string{id + " " + attemptFound}) {
		t.Errorf("attempts = %v, want the episode's", attempts)
	}
}

// A later run takes the episode's id from its ledger and reads no
// title.episode.
func TestALaterRunTakesTheEpisodeIDFromItsLedger(t *testing.T) {
	work, catalog, server, root := imdbEnricher(t, libraryKindSeries)
	seedIMDbEpisode(t, catalog, root, "tt9000003")
	runIMDbRating(t, work)
	reopen := []statement{{sql: `DELETE FROM attempts`}, {sql: `UPDATE episodes SET nfo_facts = ''`}}
	if _, err := catalog.apply(t.Context(), reopen); err != nil {
		t.Fatal(err)
	}
	next, _ := testEnricher(t, libraryKindSeries, root, catalog)
	next.ratingScope = work.ratingScope

	runIMDbRating(t, next)

	if got := server.log(); slices.Contains(got[2:], "GET title.episode 200") {
		t.Errorf("requests = %v, want no second read of title.episode", got)
	}
}

// An episode the datasets cannot answer records a miss, and one whose rating
// another writer changed records a fight and keeps its bytes.
func TestAnEpisodeTheDatasetsCannotRateRecordsWhy(t *testing.T) {
	cases := []struct {
		name   string
		series string
		wrote  string
		result string
	}{
		{name: "a series IMDb does not list", series: "tt9000099", result: attemptNothing},
		{name: "a rating another writer changed", series: "tt9000003", wrote: "another hash", result: attemptFight},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			work, catalog, _, root := imdbEnricher(t, libraryKindSeries)
			nfoPath, _ := seedIMDbEpisode(t, catalog, root, one.series)
			file := filepath.Base(strings.TrimSuffix(nfoPath, ".nfo") + ".mkv")
			if one.wrote != "" {
				err := work.writer.updateLikenLedger(filepath.Dir(nfoPath), factRatingIMDb, func(ledger *likenLedger) {
					ledger.noteItem(likenItem{Path: file, Wrote: one.wrote})
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			before := readFileString(t, nfoPath)

			runIMDbRating(t, work)

			ledger, err := readLikenLedger(filepath.Dir(nfoPath), factRatingIMDb)
			if err != nil {
				t.Fatal(err)
			}
			if len(ledger.Attempts) != 1 || ledger.Attempts[0].Result != one.result {
				t.Errorf("attempts = %+v, want one %s", ledger.Attempts, one.result)
			}
			if readFileString(t, nfoPath) != before {
				t.Error("the .nfo file changed, want every byte as it was")
			}
		})
	}
}

// A dataset IMDb will not serve ends the imdb block's work for the run: the
// titles keep their gaps and record no attempt, and the log says why once.
func TestAFileIMDbWillNotServeLeavesTheGap(t *testing.T) {
	work, catalog, server, root := imdbEnricher(t, libraryKindMovies)
	nfoPath := seedIMDbMovie(t, catalog, root, "tt9000001", "", "")
	server.statuses[datasetTitleRatings] = http.StatusInternalServerError
	log := work.log.(*bytes.Buffer)

	runIMDbRating(t, work)

	ledger, err := readLikenLedger(filepath.Dir(nfoPath), factRatingIMDb)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Attempts) != 0 {
		t.Errorf("attempts = %+v, want none", ledger.Attempts)
	}
	if got := strings.Count(log.String(), "IMDb answered 500 for title.ratings"); got != 1 {
		t.Errorf("log = %q, want the answer once", log.String())
	}
}
