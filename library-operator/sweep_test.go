package main

// sweep_test.go runs the whole-library sweep against real SQLite,
// beside the prune tests, over every table the sweep reaches, the people
// tables included.

import (
	"bytes"
	"strconv"
	"testing"
)

// The rows one season folder makes in a series library. With
// walkOfOneTitle and walkOfOnePerson it fills every table.
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

// The rows one entry in .contributors/ and one credit on a title make:
// the three people tables a sweep must take with the library.
func walkOfOnePerson(library, item, slug string) *walkResult {
	directory := contributorDirectory(slug)
	return &walkResult{
		contributors: []contributorRow{{Library: library, Path: directory, Name: slug}},
		contributorAliases: []contributorAliasRow{{
			Library: library, Scheme: contributorTMDbScheme, ID: slug, Path: directory,
		}},
		credits: []creditRow{{
			Library: library, Item: item, Contributor: directory, Name: slug,
			Part: creditPartActor, Billing: 0,
		}},
	}
}

// seedTwoLibrariesInEveryTable writes a movie, a series, and a person
// into two libraries at the same ids and paths, so every table the sweep
// reaches holds rows of both.
func seedTwoLibrariesInEveryTable(t *testing.T, catalog *Catalog) {
	t.Helper()
	for _, library := range []string{"house/movies", "house/series"} {
		for _, walk := range []*walkResult{
			walkOfOneTitle(library, "movie:tmdb:1", "One (2001)", "movie:path:one-2001"),
			walkOfOneEpisode(library, "series:tvdb:5", "episode:tvdb:5:1:1", "A Show (2005)"),
			walkOfOnePerson(library, "movie:tmdb:1", "someone-1"),
		} {
			if err := upsertWalk(t.Context(), catalog, walk); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// The tables a whole-library sweep deletes from, the item tables and
// the people tables both.
var everyCatalogTable = []string{"aliases", "movies", "series", "episodes", "file_items", "files", "genres",
	"credits", "contributors", "contributor_aliases"}

// The sweep takes every row of the departing library in every table and
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
		for _, walk := range []*walkResult{
			walkOfOneTitle("house/movies", "movie:tmdb:"+title, "Title "+title, "movie:path:title-"+title),
			walkOfOnePerson("house/movies", "movie:tmdb:"+title, "someone-"+title),
		} {
			if err := upsertWalk(t.Context(), catalog, walk); err != nil {
				t.Fatal(err)
			}
		}
	}
	agent.largestBatch = 0

	removed, err := catalog.SweepLibrary(t.Context(), "house/movies")
	if err != nil {
		t.Fatal(err)
	}

	// Ten titles, each one movie row, one file row, one link, two aliases,
	// a genre, a credit, a contributor, and a contributor alias.
	if removed != 90 {
		t.Errorf("removed = %d, want the 90 rows the ten titles hold", removed)
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
