package main

// markfacesrows_test.go proves that the close container reads the faceless
// end of a film from the catalog, and that a better clue that lands later
// replaces the estimate of the faces tier.

import (
	"reflect"
	"testing"
)

// The films of the movie library these tests seed, each a primary video of
// 6,000 seconds.
const (
	facesLibrary  = "house/movies"
	liveFilm      = "Film [2001]/Film [2001].mkv"
	drawnFilm     = "Drawn [2002]/Drawn [2002].mkv"
	facelessStart = 5600000
)

// A movie library of two films whose detections records end in a faceless
// run from 5,600 seconds. The second is animated.
func seedFacelessFilms(t *testing.T, catalog *Catalog) {
	t.Helper()
	film := func(id, path string) (movieRow, fileRow) {
		return movieRow{Id: id, Library: facesLibrary, Kind: libraryKindMovies, Path: path},
			fileRow{Path: path, Library: facesLibrary, Present: true, Type: fileTypeVideo, Role: fileRolePrimary,
				DurationMs: 6000000, FacelessTailMs: facelessStart, Items: []string{id}}
	}
	live, liveFile := film("movie:tmdb:1", liveFilm)
	drawn, drawnFile := film("movie:tmdb:2", drawnFilm)
	result := &walkResult{
		movies: []movieRow{live, drawn},
		files:  []fileRow{liveFile, drawnFile},
		genres: append(genreRows(facesLibrary, live.Id, []string{"Thriller"}),
			genreRows(facesLibrary, drawn.Id, []string{"Family", "Animation"})...),
	}
	if err := upsertWalk(t.Context(), catalog, result); err != nil {
		t.Fatal(err)
	}
}

func facesMarksOf(t *testing.T, catalog *Catalog, path string) []string {
	t.Helper()
	rows, err := catalog.queryStrings(t.Context(),
		`SELECT ordinal || ' ' || IFNULL(start_ms, '') || ' ' || IFNULL(end_ms, '') || ' ' || source `+
			`FROM marks WHERE library = ? AND path = ? AND kind = ? ORDER BY ordinal`,
		[]any{facesLibrary, path, markKindCredits})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestTheFacelessEndOfAFilmIsItsCreditsUntilABetterClueLands(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	seedFacelessFilms(t, catalog)
	derive := func() {
		t.Helper()
		if err := deriveMarkEstimates(t.Context(), catalog, facesLibrary, nil); err != nil {
			t.Fatal(err)
		}
	}

	derive()
	if got, want := facesMarksOf(t, catalog, liveFilm), []string{"-1 5600000  faces"}; !reflect.DeepEqual(got, want) {
		t.Errorf("film = %v, want %v", got, want)
	}
	if got := facesMarksOf(t, catalog, drawnFilm); len(got) != 0 {
		t.Errorf("animated film = %v, want no estimate", got)
	}

	// A probe of the film finds a chapter that names the credits.
	if _, err := agent.db.Exec(`UPDATE files SET credits_chapter_ms = 5650000 WHERE path = ?`, liveFilm); err != nil {
		t.Fatal(err)
	}
	derive()
	if got, want := facesMarksOf(t, catalog, liveFilm), []string{"-1 5650000  estimate"}; !reflect.DeepEqual(got, want) {
		t.Errorf("film = %v, want the chapter in place of the faceless end", got)
	}
}
