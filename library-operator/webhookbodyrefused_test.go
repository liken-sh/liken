package main

// The tests of a body whose catalog refuses a read, and of the rows a movie
// carries from its set and its franchises. A body that misses a read is not
// sent, because a receiver would take a title with no credits for a title
// that has none.

import (
	"fmt"
	"testing"
)

// A movie with every kind of row a body reads: a video, a credit with its
// person, a set, a trailer, and a member of a franchise in another library.
func seedFullMovie(t *testing.T, catalog *Catalog, agent *sqliteAgent) map[string]string {
	t.Helper()
	if _, err := catalog.UpsertMovies(t.Context(), []movieRow{{
		Id: "movie:tmdb:1002", Library: "house/movies", Kind: libraryKindMovies,
		Path: "Another Survey (2001)", Title: "Another Survey", SetID: "set:tmdb:77",
	}}); err != nil {
		t.Fatal(err)
	}
	seedVideo(t, catalog, "Another Survey (2001)/movie.mkv", 300, "movie:tmdb:1002")
	for _, statement := range []string{
		`INSERT INTO sets (library, id, title) VALUES ('house/movies', 'set:tmdb:77', 'The Survey Films')`,
		`INSERT INTO contributors (library, path, name) VALUES ('house/movies', '.contributors/Ada Example', 'Ada Example')`,
		`INSERT INTO credits (library, item, billing, contributor, name, part) ` +
			`VALUES ('house/movies', 'movie:tmdb:1002', 0, '.contributors/Ada Example', 'Ada Example', 'actor')`,
		`INSERT INTO trailers (library, item, provider, key, site) VALUES ('house/movies', 'movie:tmdb:1002', 'tmdb', 'k1', 'YouTube')`,
		`INSERT INTO aliases (library, alias, item, source) VALUES ('house/movies', 'movie:tmdb:1002', 'movie:tmdb:1002', 'nfo')`,
		`INSERT INTO franchises (library, id, title) VALUES ('house/sagas', 'franchise:surveys', 'The Surveys')`,
		`INSERT INTO franchise_members (library, franchise, position, kind, alias, universes) ` +
			`VALUES ('house/sagas', 'franchise:surveys', 2, 'movie', 'movie:tmdb:1002', '["prime"]')`,
	} {
		if _, err := agent.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return map[string]string{"Another Survey (2001)/movie.mkv": videoAdded}
}

func TestAMovieCarriesItsSetAndItsFranchises(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	changes := seedFullMovie(t, catalog, agent)

	titles, err := walkTitles(t.Context(), catalog, "house/movies", libraryKindMovies, changes)

	if err != nil {
		t.Fatal(err)
	}
	title := titles[0]
	if title.Set["title"] != "The Survey Films" {
		t.Errorf("set = %v, want the movie's set", title.Set)
	}
	if len(title.Franchises) != 1 || title.Franchises[0].Franchise["title"] != "The Surveys" {
		t.Fatalf("franchises = %+v, want the one franchise of the other library", title.Franchises)
	}
	if universes := title.Franchises[0].Member["universes"].([]any); len(universes) != 1 {
		t.Errorf("universes = %v, want the JSON list", universes)
	}
	if len(title.Trailers) != 1 {
		t.Errorf("trailers = %v, want the one trailer", title.Trailers)
	}
}

// How many reads one build of the titles makes, from the count the agent
// takes off queriesLeft as it answers.
func readsOfABuild(t *testing.T, kind string, seed func(*testing.T, *Catalog, *sqliteAgent) map[string]string) int {
	t.Helper()
	catalog, agent := newSQLiteCatalog(t)
	changes := seed(t, catalog, agent)
	const plenty = 1000
	agent.queriesLeft = plenty
	if _, err := walkTitles(t.Context(), catalog, "house/movies", kind, changes); err != nil {
		t.Fatal(err)
	}
	return plenty - agent.queriesLeft
}

// A series of one episode with one video, which the walk added.
func seedOneEpisode(t *testing.T, catalog *Catalog, _ *sqliteAgent) map[string]string {
	t.Helper()
	if _, err := catalog.UpsertSeries(t.Context(), []seriesRow{{
		Id: "series:tvdb:2001", Library: "house/movies", Kind: libraryKindSeries, Title: "The Long Road",
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.UpsertEpisodes(t.Context(), []episodeRow{{
		Id: "episode:tvdb:1", Library: "house/movies", Kind: libraryKindSeries,
		Series: "series:tvdb:2001", Season: 1, Episode: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	seedVideo(t, catalog, "The Long Road/S01E01.mkv", 100, "episode:tvdb:1")
	return map[string]string{"The Long Road/S01E01.mkv": videoAdded}
}

// Every read of a build is one that can be refused, and each refusal ends the
// build with an error, whichever read it is.
func TestABuildWhoseCatalogRefusesAReadReturnsTheError(t *testing.T) {
	builds := []struct {
		name string
		kind string
		seed func(*testing.T, *Catalog, *sqliteAgent) map[string]string
	}{
		{"a movie", libraryKindMovies, seedFullMovie},
		{"a series", libraryKindSeries, seedOneEpisode},
	}
	for _, build := range builds {
		reads := readsOfABuild(t, build.kind, build.seed)
		for step := 1; step <= reads; step++ {
			t.Run(fmt.Sprintf("%s, the read %d of %d", build.name, step, reads), func(t *testing.T) {
				catalog, agent := newSQLiteCatalog(t)
				changes := build.seed(t, catalog, agent)
				agent.queriesLeft = step

				_, err := walkTitles(t.Context(), catalog, "house/movies", build.kind, changes)

				if err == nil {
					t.Error("the build reported no error, want the refused read")
				}
			})
		}
	}
}
