package main

// genrenames_test.go proves the one genre vocabulary, and that the walk and
// the enricher's merge both speak it.

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestCanonicalGenres(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"a genre the vocabulary does not name stays as written",
			[]string{"Western", " Drama "}, []string{"Western", "Drama"}},
		{"each science fiction spelling is one genre",
			[]string{"Sci-Fi", "Science-Fiction", "sci-fi"}, []string{"Science Fiction"}},
		{"a combined TMDb genre splits into its two genres",
			[]string{"Sci-Fi & Fantasy", "Action & Adventure", "War & Politics"},
			[]string{"Science Fiction", "Fantasy", "Action", "Adventure", "War", "Politics"}},
		{"a repeat keeps the place of the first, which is the main genre",
			[]string{"Science Fiction", "Action", "Sci-Fi", "Adventure"},
			[]string{"Science Fiction", "Action", "Adventure"}},
		{"a split half already held is not added again",
			[]string{"Fantasy", "Sci-Fi & Fantasy"}, []string{"Fantasy", "Science Fiction"}},
		{"sport and talk take one spelling each",
			[]string{"Sport", "Sports", "Talk", "Talk Show"}, []string{"Sports", "Talk Show"}},
		{"music and musical stay two genres",
			[]string{"Music", "Musical"}, []string{"Music", "Musical"}},
		{"a blank genre is dropped",
			[]string{"", "  ", "Horror"}, []string{"Horror"}},
		{"no genres stay none", nil, nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := canonicalGenres(testCase.in); !slices.Equal(got, testCase.want) {
				t.Errorf("canonicalGenres(%q) = %q, want %q", testCase.in, got, testCase.want)
			}
		})
	}
}

// A .nfo file two providers filled, with each one's spelling of science
// fiction.
func mixedGenreVolume(t *testing.T) string {
	t.Helper()
	movies := t.TempDir()
	writeFile(t, filepath.Join(movies, "Far Orbit (2031)", "Far Orbit (2031).mkv"), "video")
	writeFile(t, filepath.Join(movies, "Far Orbit (2031)", "movie.nfo"),
		`<movie><title>Far Orbit</title><year>2031</year>`+
			`<genre>Sci-Fi &amp; Fantasy</genre><genre>Action</genre>`+
			`<genre>Sci-Fi</genre><genre>Science Fiction</genre></movie>`)
	return movies
}

func TestTheWalkWritesTheCanonicalGenres(t *testing.T) {
	movies := mixedGenreVolume(t)

	result := &walkResult{}
	scanMovieFolder(folderScan{root: movies, library: "house/movies", kind: libraryKindMovies},
		filepath.Join(movies, "Far Orbit (2031)"), result)

	id := result.movies[0].Id
	want := []genreRow{
		{Library: "house/movies", Item: id, Rank: 0, Genre: "Science Fiction"},
		{Library: "house/movies", Item: id, Rank: 1, Genre: "Fantasy"},
		{Library: "house/movies", Item: id, Rank: 2, Genre: "Action"},
	}
	if !slices.Equal(result.genres, want) {
		t.Errorf("genres = %+v, want %+v", result.genres, want)
	}
}

// TMDb and OMDb spell science fiction two ways, so the union of their
// answers held both before the merge spoke one vocabulary.
func TestTheMergeUnitesTheCanonicalGenres(t *testing.T) {
	tmdb := providerAnswer{block: providerBlockTMDb, answer: factAnswer{
		Genres: []string{"Sci-Fi & Fantasy", "Action & Adventure"},
	}}
	omdb := providerAnswer{block: providerBlockOMDb, answer: factAnswer{
		Genres: []string{"Action", "Sci-Fi", "Thriller"},
	}}

	overview, _ := mergeAnswers(factOverview, []providerAnswer{tmdb, omdb})

	want := []string{"Science Fiction", "Fantasy", "Action", "Adventure", "Thriller"}
	if !slices.Equal(overview.Genres, want) {
		t.Errorf("genres = %q, want %q", overview.Genres, want)
	}
}
