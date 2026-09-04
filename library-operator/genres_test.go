package main

// genres_test.go proves the genre rows the walk lifts out of a sidecar, in
// its order, and the two sweeps that take them with the title.

import (
	"path/filepath"
	"strings"
	"testing"
)

// A movie folder and a series folder whose sidecars each list two genres,
// the first one the main genre.
func genreVolume(t *testing.T) (string, string) {
	t.Helper()
	movies := t.TempDir()
	writeFile(t, filepath.Join(movies, "Unforgiven (1992)", "Unforgiven (1992).mkv"), "video")
	writeFile(t, filepath.Join(movies, "Unforgiven (1992)", "movie.nfo"),
		`<movie><title>Unforgiven</title><year>1992</year><genre>Western</genre><genre> Drama </genre></movie>`)
	series := t.TempDir()
	writeFile(t, filepath.Join(series, "Deadwood (2004)", "tvshow.nfo"),
		`<tvshow><title>Deadwood</title><year>2004</year><genre>Drama</genre><genre>Western</genre></tvshow>`)
	return movies, series
}

func TestTheWalkLiftsAGenreRowPerGenreInTheSidecarsOrder(t *testing.T) {
	movies, series := genreVolume(t)

	movieResult := &walkResult{}
	scanMovieFolder(folderScan{root: movies, library: "house/movies", kind: libraryKindMovies},
		filepath.Join(movies, "Unforgiven (1992)"), movieResult)
	seriesResult := &walkResult{}
	scanSeriesFolder(folderScan{root: series, library: "house/series", kind: libraryKindSeries},
		filepath.Join(series, "Deadwood (2004)"), seriesResult)

	cases := []struct {
		name string
		rows []genreRow
		want []genreRow
	}{
		{"a movie", movieResult.genres, []genreRow{
			{Library: "house/movies", Item: movieResult.movies[0].Id, Rank: 0, Genre: "Western"},
			{Library: "house/movies", Item: movieResult.movies[0].Id, Rank: 1, Genre: "Drama"},
		}},
		{"a series", seriesResult.genres, []genreRow{
			{Library: "house/series", Item: seriesResult.series[0].Id, Rank: 0, Genre: "Drama"},
			{Library: "house/series", Item: seriesResult.series[0].Id, Rank: 1, Genre: "Western"},
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if len(testCase.rows) != len(testCase.want) {
				t.Fatalf("genres = %+v, want %+v", testCase.rows, testCase.want)
			}
			for i := range testCase.want {
				if testCase.rows[i] != testCase.want[i] {
					t.Errorf("genres[%d] = %+v, want %+v", i, testCase.rows[i], testCase.want[i])
				}
			}
		})
	}
}

// The genre rows of one title as one line per row, so a test states the
// whole set it expects in one string.
func genreLines(t *testing.T, catalog *Catalog, library string) string {
	t.Helper()
	lines, err := catalog.queryStrings(t.Context(),
		`SELECT item || '|' || rank || '|' || genre FROM genres WHERE library = ? ORDER BY item, rank`,
		[]any{library})
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, ",")
}

// Two movies with two genres each: the rows a walk of a volume of two
// Westerns writes.
func twoTitlesOfTwoGenres(library string) *walkResult {
	return &walkResult{
		movies: []movieRow{
			{Id: "movie:tmdb:1", Library: library, Kind: libraryKindMovies, Path: "One (2001)", Title: "One"},
			{Id: "movie:tmdb:2", Library: library, Kind: libraryKindMovies, Path: "Two (2002)", Title: "Two"},
		},
		genres: []genreRow{
			{Library: library, Item: "movie:tmdb:1", Rank: 0, Genre: "Western"},
			{Library: library, Item: "movie:tmdb:1", Rank: 1, Genre: "Drama"},
			{Library: library, Item: "movie:tmdb:2", Rank: 0, Genre: "Western"},
			{Library: library, Item: "movie:tmdb:2", Rank: 1, Genre: "Comedy"},
		},
	}
}

func TestTheGenresOfATitleLeaveWithItAgainstTheRealSchema(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	ctx := t.Context()
	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}
	held := twoTitlesOfTwoGenres("house/movies")
	if err := upsertWalk(ctx, catalog, held); err != nil {
		t.Fatal(err)
	}

	epoch := int64(1000)
	stayed := &walkResult{movies: held.movies[:1], genres: held.genres[:2]}
	if _, err := catalog.markSeen(ctx, markKeys(stayed), epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := pruneLibrary(ctx, catalog, "house/movies", epoch); err != nil {
		t.Fatal(err)
	}

	want := "movie:tmdb:1|0|Western,movie:tmdb:1|1|Drama"
	if got := genreLines(t, catalog, "house/movies"); got != want {
		t.Errorf("genres = %q, want %q", got, want)
	}
}

func TestARescanTakesTheGenresATitleLost(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	ctx := t.Context()
	if err := catalog.ensureSeen(ctx); err != nil {
		t.Fatal(err)
	}
	held := twoTitlesOfTwoGenres("house/movies")
	if err := upsertWalk(ctx, catalog, held); err != nil {
		t.Fatal(err)
	}

	// The rescan of One (2001) read a sidecar that now lists Western alone.
	epoch := int64(2000)
	reread := &walkResult{movies: held.movies[:1], genres: held.genres[:1]}
	if err := flushWalk(ctx, catalog, reread, epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := pruneScope(ctx, catalog, "house/movies", "One (2001)", epoch); err != nil {
		t.Fatal(err)
	}

	want := "movie:tmdb:1|0|Western,movie:tmdb:2|0|Western,movie:tmdb:2|1|Comedy"
	if got := genreLines(t, catalog, "house/movies"); got != want {
		t.Errorf("genres = %q, want %q", got, want)
	}
}

func TestGenreKeysSplitBackIntoTheirColumns(t *testing.T) {
	keys := genreKeys([]string{"movie:tmdb:1" + linkKeySeparator + "2"})
	if len(keys) != 1 || keys[0] != (genreKey{Item: "movie:tmdb:1", Rank: 2}) {
		t.Errorf("genreKeys = %+v, want the item and the rank", keys)
	}
}
