package main

// what these tests run: the whole-library sweep against real SQLite,
// beside the prune tests; six tables.

import (
	"bytes"
	"strconv"
	"testing"
)

// the rows one season folder makes in a series library; with
// walkOfOneTitle it fills all six tables.
func walkOfOneEpisode(library, series, episode, path string) *walkResult {
	file := path + "/s01e01.mkv"
	return &walkResult{
		series: []seriesRow{{
			Id: series, Library: library, Kind: libraryKindSeries, Path: path, Title: series,
		}},
		episodes: []episodeRow{{
			Id: episode, Library: library, Kind: libraryKindSeries, Path: file, Title: episode,
			Series: series, Season: 1, Episode: 1,
		}},
		files: []fileRow{{
			Path: file, Library: library, Present: true, Type: fileTypeVideo,
			Role: fileRolePrimary, Items: []string{episode},
		}},
		aliases: []aliasRow{
			{Alias: series, Library: library, Item: series, Source: aliasSourceProvider},
		},
		titles: 1,
	}
}

// writes a movie and a series into two libraries at the same ids and
// paths, filling all six tables.
func seedTwoLibrariesInEveryTable(t *testing.T, catalog *Catalog) {
	t.Helper()
	for _, library := range []string{"house/movies", "house/series"} {
		for _, walk := range []*walkResult{
			walkOfOneTitle(library, "movie:tmdb:1", "One (2001)", "movie:path:one-2001"),
			walkOfOneEpisode(library, "series:tvdb:5", "episode:tvdb:5:1:1", "A Show (2005)"),
		} {
			if err := upsertWalk(t.Context(), catalog, walk); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// the six tables a whole-library sweep deletes from, the same list the
// library-keys read is built from.
var everyCatalogTable = []string{"aliases", "movies", "series", "episodes", "file_items", "files"}

// the sweep takes every row of the departing library in all six tables and
// leaves the survivor whole.
func TestSweepLibraryAgainstTheRealSchema(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	seedTwoLibrariesInEveryTable(t, catalog)

	before := map[string]int{}
	for _, table := range everyCatalogTable {
		before[table] = agent.rowsFor(t, table, "house/series")
		if before[table] == 0 {
			t.Fatalf("the seed left %s empty, so the sweep would prove nothing", table)
		}
	}

	removed, err := catalog.SweepLibrary(t.Context(), "house/movies")
	if err != nil {
		t.Fatal(err)
	}

	if removed == 0 {
		t.Error("removed = 0, want the departing library's rows")
	}
	for _, table := range everyCatalogTable {
		if got := agent.rowsFor(t, table, "house/movies"); got != 0 {
			t.Errorf("%s holds %d rows of the departed library, want none", table, got)
		}
		if got := agent.rowsFor(t, table, "house/series"); got != before[table] {
			t.Errorf("%s holds %d rows of the surviving library, want %d", table, got, before[table])
		}
	}
}

// a sweep with nothing to delete changes no row and reports no failure, so
// the pod can re-issue it.
func TestSweepLibraryRepeatsWithNothingToDelete(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	seedTwoLibrariesInEveryTable(t, catalog)

	if _, err := catalog.SweepLibrary(t.Context(), "house/movies"); err != nil {
		t.Fatal(err)
	}
	removed, err := catalog.SweepLibrary(t.Context(), "house/movies")
	if err != nil {
		t.Fatal(err)
	}

	if removed != 0 {
		t.Errorf("removed = %d on the second sweep, want none", removed)
	}
	for _, table := range everyCatalogTable {
		if got := agent.rowsFor(t, table, "house/series"); got == 0 {
			t.Errorf("%s lost the surviving library's rows to a repeated sweep", table)
		}
	}
}

// the sweeper is the whole cleanup role, so one run proves it deletes what
// the operator named.
func TestTheSweeperDeletesTheLibraryItWasNamed(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	seedTwoLibrariesInEveryTable(t, catalog)
	sweep := &sweeper{library: "house/movies", catalog: catalog, log: &bytes.Buffer{}}

	sweep.sweep(t.Context())

	if got := agent.rowsFor(t, "movies", "house/movies"); got != 0 {
		t.Errorf("the departed library holds %d movie rows, want none", got)
	}
	if got := agent.rowsFor(t, "movies", "house/series"); got == 0 {
		t.Error("the surviving library lost its movie rows")
	}
}

// no one transaction carries a whole library, because a huge delete wedged
// a peer on the harness; see librarysweep.go.
func TestSweepLibraryChunksALargeDelete(t *testing.T) {
	batchWas := pruneBatch
	t.Cleanup(func() { pruneBatch = batchWas })
	pruneBatch = 3

	catalog, agent := newSQLiteCatalog(t)
	for number := range 10 {
		title := strconv.Itoa(number)
		walk := walkOfOneTitle("house/movies", "movie:tmdb:"+title, "Title "+title, "movie:path:title-"+title)
		if err := upsertWalk(t.Context(), catalog, walk); err != nil {
			t.Fatal(err)
		}
	}
	agent.largestBatch = 0

	removed, err := catalog.SweepLibrary(t.Context(), "house/movies")
	if err != nil {
		t.Fatal(err)
	}

	// Ten titles, each one movie row, one file row, one link, and two aliases.
	if removed != 50 {
		t.Errorf("removed = %d, want the 50 rows the ten titles hold", removed)
	}
	if agent.largestBatch > pruneBatch {
		t.Errorf("one transaction carried %d statements, want no more than %d", agent.largestBatch, pruneBatch)
	}
	for _, table := range everyCatalogTable {
		if got := agent.rowsFor(t, table, "house/movies"); got != 0 {
			t.Errorf("%s holds %d rows after the sweep, want none", table, got)
		}
	}
}
